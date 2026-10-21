#!/usr/bin/env python3
"""复制显式工程材料，固定身份，并在解包目录离线审计及重算既有轮次。"""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile


def digest(path):
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def tree_digest(files):
    return hashlib.sha256(json.dumps(files, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def read_json(path):
    def unique(items):
        out = {}
        for key, value in items:
            if key in out:
                raise ValueError('duplicate JSON key: ' + key)
            out[key] = value
        return out
    return json.loads(path.read_text(), object_pairs_hook=unique)


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def local(root, name):
    p = PurePosixPath(name)
    if not name or name == '.' or p.is_absolute() or str(p) != name or any(part in ['.', '..'] for part in p.parts) or '\\' in name:
        raise ValueError('expected a normalized relative path: ' + name)
    current = root
    for part in p.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError('symbolic links are not allowed: ' + name)
    return current


def permitted(name):
    return all(part not in {'.git', '.aws', '.agents', '.codex', 'personal', '__pycache__'} and part != '.env' and not part.startswith('.env.') for part in PurePosixPath(name).parts)


def inventory(root):
    files, directories = {}, []
    for current, dirs, names in os.walk(root, followlinks=False):
        for name in sorted(dirs + names):
            path = Path(current) / name
            rel = path.relative_to(root).as_posix()
            info = path.lstat()
            if stat.S_ISDIR(info.st_mode):
                directories.append(rel)
            elif stat.S_ISREG(info.st_mode):
                files[rel] = {'sha256': digest(path), 'size': info.st_size, 'mode': stat.S_IMODE(info.st_mode)}
            else:
                raise ValueError('non-regular bundle member: ' + rel)
    return files, sorted(directories)


def copy_file(source, destination):
    if not source.is_file() or source.is_symlink():
        raise ValueError('expected a regular source file: ' + str(source))
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        raise ValueError('overlapping selection: ' + str(destination))
    before = digest(source)
    shutil.copyfile(source, destination)
    destination.chmod(0o755 if source.stat().st_mode & 0o111 else 0o644)
    if digest(destination) != before or digest(source) != before:
        raise ValueError('source changed while copying: ' + str(source))
    return before


def copy_artifact(repo, bundle, name, excluded, pins):
    if not name.startswith('runs/') or not permitted(name):
        raise ValueError('artifact must be an explicit runs path: ' + name)
    source = local(repo, name)
    if source.is_file():
        pins[name] = copy_file(source, local(bundle / 'source', name))
        return
    if not source.is_dir():
        raise ValueError('artifact is missing: ' + name)
    for current, dirs, files in os.walk(source, followlinks=False):
        relative = Path(current).relative_to(repo).as_posix()
        dirs[:] = sorted(d for d in dirs if d not in excluded and permitted(relative + '/' + d))
        for child in dirs + files:
            if (Path(current) / child).is_symlink():
                raise ValueError('artifact contains a symbolic link: ' + relative + '/' + child)
        local(bundle / 'source', relative).mkdir(parents=True, exist_ok=True)
        for child in sorted(files):
            path = relative + '/' + child
            if not permitted(path):
                raise ValueError('artifact contains an excluded private member: ' + path)
            pins[path] = copy_file(local(repo, path), local(bundle / 'source', path))


def run(args, cwd=None, env=None, log=None):
    result = subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True, timeout=300)
    if log:
        log.write_text(result.stdout + result.stderr)
    if result.returncode:
        raise ValueError('command failed: ' + ' '.join(map(str, args)) + '\n' + result.stderr)
    return result.stdout.strip()


def source_names(repo):
    names = run(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], repo).split('\0')
    roots = {'cmd', 'internal', 'scenarios', 'scripts', 'docs'}
    top = {'README.md', 'go.mod', 'go.sum', 'LICENSE', '.gitignore', '.gitattributes'}
    return sorted(p for p in names if p and permitted(p) and (p.split('/')[0] in roots or p in top) and local(repo, p).is_file())


def bundle_readme(plan, platform):
    rows = '\n'.join(f"| {c['id']} | [Campaign](source/{c['path']}/campaign.json) |" for c in plan['audits'])
    rounds = '\n'.join(f"- [{r['id']} 原汇总](source/{r['report']})" for r in plan['rounds'])
    return f"""# Harness 工程开发版 {plan['id']}

当前支持平台：{platform}。源码按工作区导出，可能包含未提交的实现；确切身份见 manifest.json，不能只用父 Git revision 代表本包。

先核对外部提供的 tar SHA-256，解包后从本目录运行（Python 3.11+）：

```sh
python3 source/scripts/package_delivery.py verify --root . --output ../review-output
```

输出目录必须不存在且位于包外。仅核对全部成员摘要可加 `--integrity-only`，该模式不算执行审计/重算。其他平台可在 source 下自行构建 ./cmd/harness 和 ./scenarios/rpcx/l2/cmd/summarize；本包不声称已在其他平台执行验收。

## 已选定运行

| 材料 | 原记录 |
| --- | --- |
{rows}

## 三份原轮次

{rounds}

原真实实验为 3 Completed / NeedsAttention，S/N=0/3，A=0，错误放行率无定义，81 次准入、1,060,890 已知 Token。两份用户实际复核和一份助手补充开发校准分列；交付依据 fail 不等于业务迁移错误。M3 A6/A7、实际留出及 M4 正式冻结仍未满足，M5 未执行。

原报告/签署/规范/Request 字节保留。复核器生成新 Request 指向本包，输出 audit 与 round，不运行候选、Docker、模型或原归档中的脚本。原审核报告六个断链保留为旧缺陷；本入口定位选定材料，不修复原报告，也不保证全部历史文档的 runs 引用都纳入。

本包只核对已保存记录及重算已有计数，不能证明外部作者身份、全部历史事件或服务账单，也不复演原模型生成。摘要不是第三方签名。构建缓存和 Git 元数据排除，所选必要输入/依赖/命令证据保留；材料清单见 manifest.json 的 plan。历史 sources 索引保留当时路径/身份，不表示其全部历史引用均随包提供；本包成员以 manifest.json 为准。
"""


def create(repo, plan_path, output):
    repo = repo.resolve()
    plan = read_json(plan_path)
    if plan['schema_version'] != 1 or not plan['audits'] or not plan['rounds']:
        raise ValueError('unsupported or empty delivery plan')
    output = output.resolve()
    selected = [local(repo, p) for p in plan['artifacts']] + [repo / p for p in ['cmd', 'internal', 'scenarios', 'scripts', 'docs']]
    if any(output == p or p in output.parents or output in p.parents for p in selected):
        raise ValueError('delivery output must not overlap selected inputs')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.mkdir()
    pins = {}
    names = source_names(repo)
    for name in names:
        pins[name] = copy_file(local(repo, name), local(output / 'source', name))
    source_pins = dict(pins)
    for name in plan['artifacts']:
        copy_artifact(repo, output, name, plan['exclude_artifact_directories'], pins)
    env = dict(os.environ, GOPROXY='off', GOSUMDB='off', GOWORK='off', GOTOOLCHAIN='local', GOFLAGS='-mod=readonly')
    with tempfile.TemporaryDirectory(prefix='harness-delivery-build-') as cache:
        env.update(GOCACHE=cache + '/go', GOMODCACHE=cache + '/modules')
        build = json.loads(run(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION', 'CGO_ENABLED'], output / 'source', env))
        (output / 'bin').mkdir()
        build['arguments'] = ['go', 'build', '-trimpath', '-buildvcs=false']
        for binary, package in [('harness', './cmd/harness'), ('summarize', './scenarios/rpcx/l2/cmd/summarize')]:
            run(build['arguments'] + ['-o', str(output / 'bin' / binary), package], output / 'source', env)
    if names != source_names(repo) or any(digest(local(repo, p)) != value for p, value in pins.items()):
        raise ValueError('selected inputs changed during packaging')
    platform = build['GOOS'] + '/' + build['GOARCH']
    (output / 'README.md').write_text(bundle_readme(plan, platform))
    files, directories = inventory(output)
    manifest = {'schema_version': 1, 'id': plan['id'], 'parent_revision': run(['git', 'rev-parse', 'HEAD'], repo), 'working_tree_snapshot': True,
                'source_sha256': tree_digest(source_pins), 'source_files': sorted(source_pins), 'build': build, 'files': files, 'directories': directories,
                'original_repository': str(repo), 'plan': plan, 'scope': 'selected engineering evidence; not formal evaluation or provider authorization'}
    write_json(output / 'manifest.json', manifest)
    archive = output.with_name(output.name + '.tar.gz')
    with archive.open('xb') as raw:
        with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode='w') as tar:
                for path in [output] + sorted(output.rglob('*')):
                    info = tar.gettarinfo(str(path), arcname=(Path(output.name) / path.relative_to(output)).as_posix())
                    info.uid = info.gid = info.mtime = 0
                    info.uname = info.gname = ''
                    if path.is_file():
                        with path.open('rb') as source:
                            tar.addfile(info, source)
                    else:
                        tar.addfile(info)
    archive.with_name(archive.name + '.sha256').write_text(digest(archive) + '  ' + archive.name + '\n')
    return {'id': plan['id'], 'files': len(files), 'bytes': sum(f['size'] for f in files.values()), 'source_sha256': manifest['source_sha256'], 'archive': str(archive), 'archive_sha256': digest(archive)}


def check_integrity(root):
    if root.is_symlink():
        raise ValueError('bundle root must not be a symbolic link')
    manifest = read_json(local(root, 'manifest.json'))
    if manifest['schema_version'] != 1:
        raise ValueError('unsupported bundle manifest')
    files, directories = inventory(root)
    files.pop('manifest.json', None)
    if files != manifest['files'] or directories != manifest['directories']:
        raise ValueError('bundle members, content or permissions differ from manifest')
    source_pins = {p: files['source/' + p]['sha256'] for p in manifest['source_files']}
    if tree_digest(source_pins) != manifest['source_sha256']:
        raise ValueError('source identity mismatch')
    return manifest


def remap_request(original, old_root, bundle, destination):
    def mapped(value):
        path = Path(value)
        if not path.is_absolute():
            raise ValueError('original request path must be absolute')
        try:
            relative = path.relative_to(old_root).as_posix()
        except ValueError as err:
            raise ValueError('request input is outside the selected repository') from err
        target = local(bundle / 'source', relative)
        if not target.exists():
            raise ValueError('request input was not included: ' + relative)
        return str(target)
    request = {'manifest_path': mapped(original['manifest_path']), 'campaign_dir': mapped(original['campaign_dir']), 'evidence_dir': str(destination), 'references': []}
    for ref in original['references']:
        value = {'target_id': ref['target_id']}
        for kind in ['report', 'review']:
            if kind in ref:
                value[kind] = {'path': mapped(ref[kind]['path']), 'sha256': ref[kind]['sha256']}
        request['references'].append(value)
    return request


def verify(root, output, integrity_only=False):
    if root.is_symlink():
        raise ValueError('bundle root must not be a symbolic link')
    root = root.resolve()
    manifest = check_integrity(root)
    if integrity_only:
        return {'integrity': 'pass', 'execution_checked': False, 'id': manifest['id']}
    output = output.resolve()
    if output == root or root in output.parents or output in root.parents:
        raise ValueError('verification output must be outside the bundle')
    output.parent.mkdir(parents=True, exist_ok=True)
    output.mkdir()
    results = {'integrity': 'pass', 'id': manifest['id'], 'audits': [], 'rounds': []}
    for case in manifest['plan']['audits']:
        target = local(root / 'source', case['path'])
        destination = local(output, 'audit-' + case['id'])
        run([str(root / 'bin/harness'), 'audit', '--campaign', str(target), '--output', str(destination)], log=output / ('audit-' + case['id'] + '.log'))
        report = read_json(destination / 'audit.json')
        if any(report.get(k) != v for k, v in case['expected'].items()):
            raise ValueError('audit result changed: ' + case['id'])
        results['audits'].append({'id': case['id'], 'consistency': report['consistency'], 'execution_state': report['execution_state'], 'recovery_execution_state': report.get('recovery_execution_state')})
    for case in manifest['plan']['rounds']:
        original = read_json(local(root / 'source', case['request']))
        destination = local(output, 'round-' + case['id'])
        request = remap_request(original, Path(manifest['original_repository']), root, destination)
        request_path = output / ('request-' + case['id'] + '.json')
        write_json(request_path, request)
        run([str(root / 'bin/summarize'), '-config', str(request_path)], log=output / ('round-' + case['id'] + '.log'))
        report = read_json(destination / 'round.json')
        expected = read_json(local(root / 'source', case['report']))
        for key in ['valid', 'execution_complete', 'counts', 'metrics', 'cost', 'targets']:
            if report[key] != expected[key]:
                raise ValueError('recomputed round differs: ' + case['id'] + '/' + key)
        results['rounds'].append({'id': case['id'], 'counts': report['counts'], 'metrics': report['metrics']})
    check_integrity(root)
    results['bundle_unchanged'] = True
    write_json(output / 'verification.json', results)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    build = commands.add_parser('create')
    build.add_argument('--repo', type=Path, default=Path(__file__).resolve().parent.parent)
    build.add_argument('--plan', type=Path, required=True)
    build.add_argument('--output', type=Path, required=True)
    check = commands.add_parser('verify')
    check.add_argument('--root', type=Path, required=True)
    check.add_argument('--output', type=Path)
    check.add_argument('--integrity-only', action='store_true')
    args = parser.parse_args()
    if args.command == 'create':
        result = create(args.repo, args.plan, args.output)
    else:
        if not args.integrity_only and args.output is None:
            parser.error('--output is required for execution verification')
        result = verify(args.root, args.output, args.integrity_only)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, subprocess.SubprocessError) as err:
        print(str(err), file=sys.stderr)
        sys.exit(1)

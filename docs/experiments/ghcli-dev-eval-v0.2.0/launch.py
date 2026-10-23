#!/usr/bin/env python3
"""按本版协议检查材料；执行模式须先取得本次用户授权。"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import time
from zoneinfo import ZoneInfo

BUNDLE = Path(__file__).resolve().parent
ROOT = BUNDLE.parents[2]
ATTEMPT = ROOT / "runs/ghcli-dev-eval-v0.2.0-launch"


def digest(path):
    with path.open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def command(args):
    return subprocess.check_output(args, text=True, timeout=35).strip()


def read_key(path):
    key = None
    for line in path.read_text().splitlines():
        line = line.strip()
        if line.startswith("export "):
            line = line[7:].lstrip()
        name, sep, value = line.partition("=")
        if sep and name.strip() == "DEEPSEEK_API_KEY":
            words = shlex.split(value, comments=True, posix=True)
            if key is not None or len(words) != 1 or not words[0] or "\n" in words[0] or "\r" in words[0]:
                raise ValueError("invalid credential setup; values omitted")
            key = words[0]
    if not key:
        raise ValueError("credential not configured; values omitted")
    return key


def verify(expected_sha, execute):
    assert digest(BUNDLE / "freeze.json") == expected_sha, "protocol identity changed"
    freeze = json.loads((BUNDLE / "freeze.json").read_text())
    assert set(freeze["files"]) == {p.name for p in BUNDLE.iterdir() if p.is_file() and p.name != "freeze.json"}
    assert all(digest(BUNDLE / name) == sha for name, sha in freeze["files"].items()), "protocol material changed"
    implementation = json.loads((BUNDLE / "implementation.json").read_text())
    files = json.loads((BUNDLE / "source-files.json").read_text())
    assert len(files) == implementation["source_file_count"]
    assert hashlib.sha256(json.dumps(files, sort_keys=True, separators=(",", ":")).encode()).hexdigest() == implementation["source_sha256"]
    snapshot = ROOT / implementation["snapshot"]
    assert all(digest(ROOT / name) == sha and digest(snapshot / name) == sha for name, sha in files.items()), "source changed"
    assert all(digest(ROOT / b["path"]) == b["sha256"] for b in implementation["binaries"].values()), "binary changed"
    assert command(["go", "version"]) == implementation["build"]["go_version"], "Go toolchain changed"
    if execute:
        assert command(["git", "-C", str(ROOT), "status", "--porcelain"]) == "", "working tree must be clean"
    config = json.loads((BUNDLE / "batch.json").read_text())
    environment = json.loads((BUNDLE / "environment.json").read_text())
    output = (BUNDLE / config["directory"]).resolve()
    assert not output.exists() and not ATTEMPT.exists(), "existing output or launch record; no retry"
    image = json.loads(command(["docker", "image", "inspect", config["execution"]["image"], "--format", "{{json .}}"] ))
    assert image["Id"] == environment["execution"]["image"] and image["Os"] == "linux" and image["Architecture"] == environment["docker"]["image_architecture"]
    assert not image["Config"].get("Volumes"), "image volumes changed"
    context = command(["docker", "context", "show"])
    assert context == environment["resources"]["context"], "Docker context changed"
    engine = json.loads(command(["docker", "--context", context, "info", "--format", "{{json .}}"] ))
    assert engine["ID"] == environment["resources"]["engine_id"], "Docker engine changed"
    binary = ROOT / implementation["binaries"]["harness"]["path"]
    args = [str(binary), "batch", "--config", str(BUNDLE / "batch.json")]
    check = subprocess.run(args + ["--check"], capture_output=True, text=True, timeout=35)
    assert check.returncode == 0, "offline configuration rejected"
    return args, output, implementation


def execute(args, output, implementation, protocol_sha):
    ATTEMPT.mkdir(mode=0o700)
    record = {
        "protocol": "ghcli-dev-eval-v0_2_0", "protocol_sha256": protocol_sha,
        "source_sha256": implementation["source_sha256"],
        "binary_sha256": implementation["binaries"]["harness"]["sha256"],
        "command": args, "batch_directory": str(output), "status": "prepared",
        "generation_window_seconds": 720, "cleanup_grace_seconds": 60,
    }

    def now():
        return datetime.datetime.now(ZoneInfo("Asia/Taipei")).isoformat()

    def save():
        temp = ATTEMPT / "launch.tmp"
        with temp.open("w") as file:
            json.dump(record, file, ensure_ascii=False, indent=2)
            file.write("\n")
            file.flush()
            os.fsync(file.fileno())
        os.replace(temp, ATTEMPT / "launch.json")

    save()
    try:
        key = read_key(ROOT / ".env")
    except Exception:
        record.update(status="credential_setup_failed", finished_at=now())
        save()
        raise SystemExit("credential setup failed; values omitted")
    allowed = ["PATH", "HOME", "TMPDIR", "USER", "LOGNAME", "LANG", "LC_ALL", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "XDG_CONFIG_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR"]
    environment = {name: os.environ[name] for name in allowed if name in os.environ}
    environment["DEEPSEEK_API_KEY"] = key
    record["environment_names"] = sorted(environment)
    with (ATTEMPT / "stdout.log").open("xb") as stdout, (ATTEMPT / "stderr.log").open("xb") as stderr:
        started = time.monotonic()
        child = subprocess.Popen(args, cwd=ROOT, env=environment, stdout=stdout, stderr=stderr, start_new_session=True)
        record.update(status="running", started_at=now(), pid=child.pid)
        save()
        try:
            code = child.wait(timeout=720)
        except subprocess.TimeoutExpired:
            record["sigint_at"] = now()
            os.killpg(child.pid, signal.SIGINT)
            save()
            try:
                code = child.wait(timeout=60)
            except subprocess.TimeoutExpired:
                record["sigkill_at"] = now()
                os.killpg(child.pid, signal.SIGKILL)
                code = child.wait()
        record.update(status="finished", finished_at=now(), exit_code=code, duration_seconds=round(time.monotonic() - started, 3))
        save()
    print(json.dumps({name: record[name] for name in ["status", "exit_code", "duration_seconds", "batch_directory"]}))


def main():
    if sys.flags.optimize:
        raise SystemExit("optimized Python mode is unsupported")
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true", help="verify local pins without credentials, provider calls or container creation")
    mode.add_argument("--execute", action="store_true", help="run the sole attempt after obtaining this experiment's authorization")
    parser.add_argument("--freeze-sha256", required=True)
    options = parser.parse_args()
    args, output, implementation = verify(options.freeze_sha256, options.execute)
    if options.check:
        print(json.dumps({"valid": True, "targets": 1, "model_queried": False, "credentials_read": False, "batch_created": False, "source_sha256": implementation["source_sha256"]}))
        return
    execute(args, output, implementation, options.freeze_sha256)


if __name__ == "__main__":
    main()

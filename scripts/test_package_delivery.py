"""验证交付边界，测试不运行 Go、Docker 或模型。"""

import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('delivery', Path(__file__).with_name('package_delivery.py'))
delivery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(delivery)


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'bundle'
        self.root.mkdir()

    def fixture(self):
        p = self.root / 'source' / 'go.mod'
        p.parent.mkdir()
        p.write_text('module example.com/test\n')
        files, directories = delivery.inventory(self.root)
        manifest = {'schema_version': 1, 'id': 'fixture', 'source_files': ['go.mod'],
                    'source_sha256': delivery.tree_digest({'go.mod': delivery.digest(p)}),
                    'files': files, 'directories': directories}
        delivery.write_json(self.root / 'manifest.json', manifest)
        return p

    def test_integrity_only_requires_no_external_tools(self):
        self.fixture()
        with patch.object(delivery, 'run', side_effect=AssertionError('execution attempted')):
            self.assertEqual(delivery.verify(self.root, None, True), {'integrity': 'pass', 'execution_checked': False, 'id': 'fixture'})

    def test_corrupt_missing_extra_mode_and_link_are_rejected_before_execution(self):
        for mode in ['corrupt', 'missing', 'extra', 'mode', 'link']:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temp:
                self.root = Path(temp)
                p = self.fixture()
                if mode == 'corrupt': p.write_text('changed')
                elif mode == 'missing': p.unlink()
                elif mode == 'extra': (self.root / 'unexpected').write_text('extra')
                elif mode == 'mode': p.chmod(0o755)
                else:
                    target = self.root / 'manifest.json'
                    p.unlink()
                    p.symlink_to(target)
                with patch.object(delivery, 'run', side_effect=AssertionError('execution attempted')):
                    with self.assertRaises(ValueError): delivery.verify(self.root, self.root.parent / 'review')

    def test_output_cannot_modify_the_bundle(self):
        self.fixture()
        with self.assertRaises(ValueError): delivery.verify(self.root, self.root / 'output')
        self.assertFalse((self.root / 'output').exists())

    def test_paths_cannot_escape_or_follow_links(self):
        for name in ['.', '../outside', '/outside', 'a/../outside', 'a//b', 'a\\b']:
            with self.subTest(path=name), self.assertRaises(ValueError): delivery.local(self.root, name)
        (self.root / 'link').symlink_to(self.root.parent, target_is_directory=True)
        with self.assertRaises(ValueError): delivery.local(self.root, 'link/file')

    def test_private_names_are_not_selected(self):
        for p in ['.env', 'docs/.env.local', 'docs/personal/note.md', '.aws/credentials', '.git/config', 'scripts/__pycache__/a.pyc']:
            self.assertFalse(delivery.permitted(p), p)
        self.assertTrue(delivery.permitted('docs/delivery/engineering-v0.1.0.json'))

    def test_original_request_is_preserved_and_only_selected_inputs_are_remapped(self):
        source = self.root / 'source/runs/task'
        source.mkdir(parents=True)
        for name in ['manifest.json', 'reference.json', 'review.json']:
            (source / name).write_text('{}')
        original = {'manifest_path': '/old/repo/runs/task/manifest.json', 'campaign_dir': '/old/repo/runs/task', 'evidence_dir': '/old/repo/runs/output',
                    'references': [{'target_id': 'a', 'report': {'path': '/old/repo/runs/task/reference.json', 'sha256': 'a' * 64}, 'review': {'path': '/old/repo/runs/task/review.json', 'sha256': 'b' * 64}}]}
        raw = json.dumps(original)
        result = delivery.remap_request(original, Path('/old/repo'), self.root, self.root.parent / 'new-round')
        self.assertEqual(raw, json.dumps(original))
        self.assertEqual(result['references'][0]['review']['path'], str(source / 'review.json'))
        original['references'][0]['report']['path'] = '/elsewhere/reference.json'
        with self.assertRaises(ValueError): delivery.remap_request(original, Path('/old/repo'), self.root, self.root.parent / 'out')
        original['references'][0]['report']['path'] = '/old/repo/runs/task/missing.json'
        with self.assertRaises(ValueError): delivery.remap_request(original, Path('/old/repo'), self.root, self.root.parent / 'out')

    def test_duplicate_json_fields_are_rejected(self):
        p = self.root / 'input.json'
        p.write_text('{"id":1,"id":2}')
        with self.assertRaises(ValueError): delivery.read_json(p)

    def test_artifact_metadata_and_cache_are_not_read_or_copied(self):
        repo = self.root / 'repo'
        selected = repo / 'runs/task'
        for directory in ['.git', 'cache', 'evidence']:
            p = selected / directory
            p.mkdir(parents=True)
            (p / 'data').write_text(directory)
        pins = {}
        destination = self.root / 'output'
        delivery.copy_artifact(repo, destination, 'runs/task', ['cache'], pins)
        self.assertEqual(list(pins), ['runs/task/evidence/data'])
        self.assertFalse((destination / 'source/runs/task/.git').exists())
        self.assertFalse((destination / 'source/runs/task/cache').exists())

    def test_create_rejects_overlapping_inputs_before_copy_or_build(self):
        repo = self.root / 'repo'
        (repo / 'runs/task').mkdir(parents=True)
        plan = self.root / 'plan.json'
        delivery.write_json(plan, {'schema_version': 1, 'audits': [{}], 'rounds': [{}], 'artifacts': ['runs/task']})
        for output in [repo / 'docs/bundle', repo / 'runs/task/bundle']:
            with patch.object(delivery, 'run', side_effect=AssertionError('external execution attempted')):
                with self.assertRaises(ValueError): delivery.create(repo, plan, output)
            self.assertFalse(output.exists())


if __name__ == '__main__':
    unittest.main()

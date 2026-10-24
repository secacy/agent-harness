"""仅校准启动器的本地预检与拒绝分支，禁止读取凭证或启动子进程。"""
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import shutil
import sys
import tempfile
import types
import unittest
from unittest import mock

BUNDLE = Path(__file__).resolve().parent
ROOT = BUNDLE.parents[2]
spec = importlib.util.spec_from_file_location('observation_launch', BUNDLE / 'launch.py')
launch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launch)


class LaunchChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=ROOT / 'runs/translation-handoff-dev-v0.1.0-preparation')
        self.addCleanup(self.temp.cleanup)
        self.bundle = Path(self.temp.name)
        for file in BUNDLE.iterdir():
            if file.is_file():
                shutil.copyfile(file, self.bundle / file.name)
        self.freeze = hashlib.sha256((self.bundle / 'freeze.json').read_bytes()).hexdigest()
        self.env = json.loads((self.bundle / 'environment.json').read_text())
        self.impl = json.loads((self.bundle / 'implementation.json').read_text())
        self.commands = []
        self.engine = self.env['resources']['engine_id']
        self.extra_source = False

    def command(self, args):
        self.commands.append(args)
        if args[:4] == ['git', '-C', str(ROOT), 'ls-files']:
            names = list(json.loads((self.bundle / 'source-files.json').read_text()))
            return '\n'.join(names + (['internal/unregistered.go'] if self.extra_source else []))
        if args == ['go', 'version']:
            return self.impl['build']['go_version']
        if args[:3] == ['docker', 'image', 'inspect']:
            return json.dumps({'Id': self.env['execution']['image'], 'Os': 'linux', 'Architecture': self.env['docker']['image_architecture'], 'Config': {}})
        if args == ['docker', 'context', 'show']:
            return self.env['resources']['context']
        if args[:2] == ['docker', '--context']:
            return json.dumps({'ID': self.engine})
        raise AssertionError('unexpected external command')

    def verify(self):
        with mock.patch.object(launch, 'BUNDLE', self.bundle), mock.patch.object(launch, 'command', self.command), mock.patch.object(launch, 'read_key', side_effect=AssertionError('credential read')), mock.patch.object(launch.subprocess, 'Popen', side_effect=AssertionError('child launch')), mock.patch.object(launch.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0)) as check:
            result = launch.verify(self.freeze, False)
            self.assertEqual(check.call_args.args[0][-1], '--check')
            return result

    def test_check_has_one_target_and_no_launch(self):
        with mock.patch.object(launch, 'BUNDLE', self.bundle), mock.patch.object(launch, 'command', self.command), mock.patch.object(launch, 'read_key', side_effect=AssertionError('credential read')), mock.patch.object(launch.subprocess, 'Popen', side_effect=AssertionError('child launch')), mock.patch.object(launch.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0)), mock.patch.object(sys, 'argv', ['launch.py', '--check', '--freeze-sha256', self.freeze]), contextlib.redirect_stdout(io.StringIO()) as output:
            launch.main()
        result = json.loads(output.getvalue())
        self.assertEqual(result['targets'], 1)
        self.assertFalse(result['model_queried'])
        self.assertFalse(result['credentials_read'])
        self.assertFalse(result['batch_created'])

    def test_protocol_tamper_stops_before_commands(self):
        with (self.bundle / 'README.md').open('a') as f:
            f.write('changed')
        with self.assertRaisesRegex(AssertionError, 'protocol material changed'):
            self.verify()
        self.assertEqual(self.commands, [])

    def test_wrong_freeze_identity(self):
        self.freeze = '0' * 64
        with self.assertRaisesRegex(AssertionError, 'protocol identity changed'):
            self.verify()
        self.assertEqual(self.commands, [])

    def test_engine_drift(self):
        self.engine = 'changed-engine'
        with self.assertRaisesRegex(AssertionError, 'Docker engine changed'):
            self.verify()

    def test_already_used_launch(self):
        original = Path.exists
        with mock.patch.object(Path, 'exists', lambda p: True if p == launch.ATTEMPT else original(p)):
            with self.assertRaisesRegex(AssertionError, 'no retry'):
                self.verify()
        self.assertEqual(self.commands[-1], ['go', 'version'])
        self.assertEqual(len(self.commands), 2)

    def test_source_selection_drift(self):
        self.extra_source = True
        with self.assertRaisesRegex(AssertionError, 'source selection changed'):
            self.verify()

    def test_fixed_stop_window(self):
        source = (BUNDLE / 'launch.py').read_text()
        self.assertIn('"generation_window_seconds": 720', source)
        self.assertIn('child.wait(timeout=720)', source)
        self.assertNotIn('2400', source)
        self.assertIn('child.wait(timeout=60)', source)


unittest.main()

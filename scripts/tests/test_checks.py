import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('checks', Path(__file__).parents[1] / 'check.py')
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class DevelopmentChecksTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.addCleanup(os.chdir, Path.cwd())
        self.root = Path(temporary.name)
        self.web = self.root / 'web'
        self.web.mkdir()
        # The frontend bundles the workflow SDK's declarations.
        self.declarations = self.root / 'sdk/dist/workflow.d.ts'
        self.declarations.parent.mkdir(parents=True)
        self.declarations.write_text('export {};')
        self.patch = patch.object(checks, 'ROOT', self.root)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        manifest = {'dependencies': {'example': '1.0.0'}}
        self.lock = {'packages': {'': manifest, 'node_modules/example': {'version': '1.0.0'},
                                  'node_modules/other-platform': {'version': '1.0.0', 'optional': True}}}
        (self.web / 'package.json').write_text(json.dumps(manifest))
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        (self.web / 'node_modules/example').mkdir(parents=True)
        (self.web / 'node_modules/.package-lock.json').write_text(json.dumps({'packages': {
            'node_modules/example': self.lock['packages']['node_modules/example']}}))

    def test_installed_platform_subset_matches_but_changed_lock_and_missing_package_fail(self):
        checks.require_web_dependencies()
        self.lock['packages']['node_modules/example']['version'] = '2.0.0'
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.require_web_dependencies()
        self.lock['packages']['node_modules/example']['version'] = '1.0.0'
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        (self.web / 'node_modules/example').rmdir()
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.require_web_dependencies()

    def test_missing_required_dependency_and_manifest_drift_fail(self):
        self.lock['packages']['node_modules/required'] = {'version': '1.0.0'}
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.require_web_dependencies()
        (self.web / 'package.json').write_text('{"dependencies": {"example": "3.0.0"}}')
        with self.assertRaisesRegex(ValueError, 'update the lockfile'):
            checks.require_web_dependencies()

    def test_frontend_reuses_only_matching_sources_and_outputs(self):
        source = self.web / 'index.html'
        source.write_text('first')

        def build(*command):
            self.assertEqual(command, ('npm', '--prefix', 'web', 'run', 'build'))
            (self.web / 'dist').mkdir(exist_ok=True)
            (self.web / 'dist/index.html').write_text(source.read_text())

        with patch.object(checks, 'run', side_effect=build) as run:
            checks.prepare_frontend()
            checks.prepare_frontend()
            self.assertEqual(run.call_count, 1)
            source.write_text('second')
            checks.prepare_frontend()
            self.assertEqual(run.call_count, 2)
            self.declarations.write_text('export declare const changed: true;')
            checks.prepare_frontend()
            self.assertEqual(run.call_count, 3)
            (self.web / 'dist/index.html').write_text('tampered')
            checks.prepare_frontend()
            self.assertEqual(run.call_count, 4)
            (self.web / 'dist/index.html').unlink()
            checks.prepare_frontend()
            self.assertEqual(run.call_count, 5)
            checks.prepare_frontend(force=True)
            self.assertEqual(run.call_count, 6)

    def test_default_and_scoped_checks_are_cached_and_skip_full_validation(self):
        for args, packages in [([], ['./...']), (['--packages', './internal/dms', '--run', '^TestExample$'], ['./internal/dms'])]:
            with self.subTest(args=args), patch('sys.argv', ['check.py', *args]), \
                    patch.object(checks, 'prepare_go'), patch.object(checks, 'prepare_frontend'), \
                    patch.object(checks, 'check_formatting'), patch.object(checks, 'check_generated') as generate, \
                    patch.object(checks, 'check_web') as web, patch.object(checks, 'run') as run:
                checks.main()
                generate.assert_not_called()
                web.assert_not_called()
                self.assertEqual(run.call_count, 1)
                command = run.call_args.args
                self.assertIn('scripts/go-test.py', command)
                self.assertNotIn('-count=1', command)
                self.assertEqual(list(command[-len(packages):]), packages)

    def test_postgres_mode_runs_database_suites_on_the_configured_server(self):
        dsn = 'postgres://user:secret@localhost:5432/papergo'
        for args, packages in [(['--postgres'], checks.POSTGRES_PACKAGES), (['--postgres', '--packages', './internal/dms'], ['./internal/dms'])]:
            with self.subTest(args=args), patch('sys.argv', ['check.py', *args]), \
                    patch.dict('os.environ', {'PAPERGO_TEST_POSTGRES_DSN': dsn}, clear=True), \
                    patch.object(checks, 'prepare_go'), patch.object(checks, 'prepare_frontend'), \
                    patch.object(checks, 'check_formatting'), patch.object(checks, 'run') as run, \
                    patch.object(checks.subprocess, 'run') as start:
                checks.main()
                start.assert_not_called()
                self.assertEqual(os.environ['TEST_POSTGRES_DSN'], dsn)
                self.assertEqual(list(run.call_args.args[-len(packages):]), packages)

    def test_postgres_mode_rejects_other_modes(self):
        with patch('sys.argv', ['check.py', '--postgres', '--web']), self.assertRaises(SystemExit):
            checks.main()

    def test_native_mode_requires_assets_before_preparing_or_running_anything(self):
        with patch('sys.argv', ['check.py', '--native']), patch.dict('os.environ', {}, clear=True), \
                patch.object(checks, 'prepare_go') as prepare:
            with self.assertRaisesRegex(ValueError, 'DECISION_MODELS_PATH'):
                checks.main()
            prepare.assert_not_called()

    def test_native_sdk_check_does_not_install_missing_sdks(self):
        script = Path(__file__).parents[1] / 'native.py'
        directory = self.root / 'uninstalled-sdk'
        result = subprocess.run([sys.executable, str(script), '--check', '--directory', str(directory)],
                                cwd=self.root, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 2)
        self.assertIn('missing or stale', result.stderr)
        self.assertFalse(directory.exists())

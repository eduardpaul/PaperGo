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
        checks.node_dependencies('web')
        self.lock['packages']['node_modules/example']['version'] = '2.0.0'
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.node_dependencies('web')
        self.lock['packages']['node_modules/example']['version'] = '1.0.0'
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        (self.web / 'node_modules/example').rmdir()
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.node_dependencies('web')

    def test_missing_required_dependency_and_manifest_drift_fail(self):
        self.lock['packages']['node_modules/required'] = {'version': '1.0.0'}
        (self.web / 'package-lock.json').write_text(json.dumps(self.lock))
        with self.assertRaisesRegex(ValueError, 'stale'):
            checks.node_dependencies('web')
        (self.web / 'package.json').write_text('{"dependencies": {"example": "3.0.0"}}')
        with self.assertRaisesRegex(ValueError, 'update the lockfile'):
            checks.node_dependencies('web')

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

    def test_stale_dependencies_install_only_with_setup(self):
        (self.web / 'node_modules/example').rmdir()
        with patch.object(checks, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'stale'):
                checks.node_dependencies('web')
            run.assert_not_called()
            checks.node_dependencies('web', install=True)
        run.assert_called_once_with('npm', 'ci', '--prefix', 'web', '--no-audit', '--no-fund')

    def stage(self, *argv, environ=None):
        """Run a stage with every preparation and command recorded, not run."""
        with patch.dict('os.environ', environ or {}, clear=True), \
                patch.object(checks, 'prepare_go'), patch.object(checks, 'prepare_frontend'), \
                patch.object(checks, 'check_formatting'), patch.object(checks, 'node_dependencies'), \
                patch.object(checks, 'run') as run, patch.object(checks, 'go_tests') as tests, \
                patch.object(checks.subprocess, 'run') as start:
            checks.main(list(argv))
            return run, tests, start

    def test_quick_check_is_cached_and_scoped(self):
        for argv, packages in [([], ['./...']), (['--packages', './internal/dms', '--run', '^TestExample$'], ['./internal/dms'])]:
            with self.subTest(argv=argv):
                _, tests, _ = self.stage(*argv)
                name, flags, selected = tests.call_args.args[:3]
                self.assertEqual(name, 'quick')
                self.assertNotIn('-count=1', flags)
                self.assertEqual(selected, packages)
                if '--run' in argv:
                    self.assertEqual(flags[-2:], ['-run', '^TestExample$'])

    def test_test_stage_runs_go_postgres_and_web_on_the_configured_server(self):
        dsn = 'postgres://user:secret@localhost:5432/papergo'
        with patch.object(checks, 'postgres_packages', return_value=['./internal/dal/x', *checks.POSTGRES_PACKAGES]):
            run, tests, start = self.stage('test', environ={'PAPERGO_TEST_POSTGRES_DSN': dsn})
        start.assert_not_called()
        (go, postgres) = [call.args for call in tests.call_args_list]
        self.assertEqual((go[0], go[2]), ('test', ['./...']))
        self.assertIn('-count=1', go[1])
        self.assertEqual(tests.call_args_list[0].args[4]['PAPERGO_REQUIRE_OCR'], '1')
        self.assertEqual((postgres[0], postgres[2]), ('postgres', ['./internal/dal/x', *checks.POSTGRES_PACKAGES]))
        run.assert_any_call('npm', '--prefix', 'web', 'test', '--', '--maxWorkers=2')

    def test_test_parts_and_shards_select_one_job(self):
        _, tests, _ = self.stage('test', '--only', 'go', '--shard', '2/3')
        self.assertEqual(tests.call_count, 1)
        self.assertEqual(tests.call_args.args[0], 'test-2')
        self.assertEqual(tests.call_args.args[3], (2, 3))
        run, tests, _ = self.stage('test', '--only', 'web')
        tests.assert_not_called()
        _, tests, _ = self.stage('test', '--only', 'postgres', '--packages', './internal/dms',
                                 environ={'PAPERGO_TEST_POSTGRES_DSN': 'postgres://localhost/papergo'})
        self.assertEqual(tests.call_args.args[2], ['./internal/dms'])

    def test_postgres_packages_keep_portable_suites_without_sqlite_only_repeats(self):
        portable = ['papergo/internal/dal', 'papergo/internal/dal/catalog', 'papergo/internal/dal/resources']
        sqlite_only = ['papergo/internal/dal/database', 'papergo/internal/dal/migrations', 'papergo/internal/dal/releaseplan']
        native = ['papergo/internal/dal/search/sqlitestore', 'papergo/internal/dal/search/zvecstore']
        with patch.object(checks.subprocess, 'run') as run:
            run.return_value.stdout = '\n'.join([*portable, *sqlite_only, *native])
            self.assertEqual(checks.postgres_packages(), [*portable, *checks.POSTGRES_PACKAGES])
        self.assertNotIn('./internal/backup', checks.POSTGRES_PACKAGES)

    def test_invalid_combinations_are_rejected(self):
        for argv in (['--run', 'X'], ['lint', '--packages', './x'], ['race', '--only', 'go'], ['test', '--shard', '1/2'],
                     ['race', '--shard', '3/2'], ['race', '--shard', '1/2', '--packages', './x'], ['nope']):
            with self.subTest(argv=argv), patch('sys.stderr'), self.assertRaises(SystemExit):
                checks.main(argv)

    def test_native_libraries_go_first_on_the_current_search_path(self):
        native = self.root / 'native'
        native.mkdir()
        loader = 'PATH' if os.name == 'nt' else 'LD_LIBRARY_PATH'
        (native / 'native.env').write_text(f'CGO_ENABLED=1\n{loader}=/stale\n')
        with patch.dict('os.environ', {'PAPERGO_NATIVE_DIR': str(native), loader: '/added/later'}, clear=True), \
                patch.object(checks, 'run'):
            checks.prepare_go()
            self.assertEqual(os.environ['CGO_ENABLED'], '1')
            self.assertEqual(os.environ[loader], os.pathsep.join([str(native / 'lib'), '/added/later']))
            self.assertEqual(os.environ['OCR_MODELS'], str(self.root / 'models/paddleocr'))

    def test_native_stage_requires_models_before_preparing_anything(self):
        with patch.dict('os.environ', {}, clear=True), patch.object(checks, 'prepare_go') as prepare:
            with self.assertRaisesRegex(ValueError, 'DECISION_MODELS_PATH'):
                checks.main(['native'])
            prepare.assert_not_called()

    def test_native_sdk_check_does_not_install_missing_sdks(self):
        script = Path(__file__).parents[1] / 'native.py'
        directory = self.root / 'uninstalled-sdk'
        result = subprocess.run([sys.executable, str(script), '--check', '--directory', str(directory)],
                                cwd=self.root, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 2)
        self.assertIn('missing or stale', result.stderr)
        self.assertFalse(directory.exists())

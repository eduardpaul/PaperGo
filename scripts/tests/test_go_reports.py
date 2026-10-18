import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).parents[1] / 'go-test.py'
spec = importlib.util.spec_from_file_location('go_reports', SCRIPT)
reports = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reports)


class GoReportsTests(unittest.TestCase):
    def test_parent_timings_do_not_double_count_subtests_or_conflate_packages(self):
        result = reports.summarize([
            {'Action': 'pass', 'Package': 'a', 'Test': 'TestSame/sub', 'Elapsed': 3},
            {'Action': 'pass', 'Package': 'a', 'Test': 'TestSame', 'Elapsed': 4},
            {'Action': 'pass', 'Package': 'b', 'Test': 'TestSame', 'Elapsed': 2},
            {'Action': 'output', 'Package': 'a', 'Output': 'ok a (cached)'},
            {'Action': 'pass', 'Package': 'a', 'Elapsed': 5},
            {'Action': 'skip', 'Package': 'b'},
        ], 7, 1, 0)
        self.assertEqual([row['Elapsed'] for row in result['tests']], [4, 2])
        self.assertTrue(result['packages'][0]['Cached'])
        self.assertEqual(len(result['packages']), 2)
        self.assertIn('cached', reports.markdown(result))

    def test_real_go_failure_and_compile_failure_preserve_exit_code_and_reports(self):
        sources = [
            ('package sample\nimport "testing"\nfunc TestFailure(t *testing.T) { t.Fatal("expected failure detail") }\n', 'expected failure detail'),
            ('package sample\nthis does not compile\n', 'expected declaration'),
        ]
        for source, expected in sources:
            with self.subTest(source=source), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'go.mod').write_text('module sample\n\ngo 1.26\n')
                (root / 'sample_test.go').write_text(source)
                stem = root / 'reports/tests'
                summary = root / 'summary.md'
                env = dict(os.environ, GOTOOLCHAIN='local', GITHUB_STEP_SUMMARY=str(summary))
                result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(stem), '--', './...'],
                                        cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                self.assertIn(expected, result.stdout + result.stderr)
                if 'TestFailure' in source:
                    self.assertRegex(result.stderr, r'FAILED sample/TestFailure \(\d+\.\d+s\)')
                else:
                    self.assertIn('BUILD FAILED sample', result.stderr)
                report = json.loads(stem.with_suffix('.json').read_text())
                self.assertEqual(report['exit_code'], 1)
                self.assertGreater(report['wall_seconds'], 0)
                self.assertTrue(stem.with_suffix('.jsonl').is_file())
                self.assertIn('Go test timings', summary.read_text())

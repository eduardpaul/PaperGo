import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
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

    def test_long_failure_output_is_cut_and_kept_in_the_report(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'go.mod').write_text('module sample\n\ngo 1.26\n')
            (root / 'sample_test.go').write_text('package sample\nimport (\n"strings"\n"testing"\n)\n'
                                                 'func TestHuge(t *testing.T) { t.Fatal(strings.Repeat("x", 4<<20)) }\n')
            stem = root / 'reports/tests'
            env = {key: value for key, value in os.environ.items() if key != 'GITHUB_STEP_SUMMARY'}
            result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(stem), '--', './...'],
                                    cwd=root, env=dict(env, GOTOOLCHAIN='local'), capture_output=True, text=True, timeout=120)
            self.assertEqual(result.returncode, 1)
            self.assertLess(max(map(len, (result.stdout + result.stderr).splitlines())), reports.MAX_LINE + 100)
            self.assertIn('more characters in the .jsonl report', result.stderr)
            events = map(json.loads, stem.with_suffix('.jsonl').read_text().splitlines())
            self.assertIn('x' * (4 << 20), ''.join(event.get('Output', '') for event in events))

    def test_deadline_stops_a_hung_test_and_names_it(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'go.mod').write_text('module sample\n\ngo 1.26\n')
            (root / 'sample_test.go').write_text('package sample\nimport (\n"testing"\n"time"\n)\n'
                                                 'func TestHangs(t *testing.T) { time.Sleep(time.Hour) }\n')
            stem = root / 'reports/tests'
            env = {key: value for key, value in os.environ.items() if key != 'GITHUB_STEP_SUMMARY'}
            started = time.monotonic()
            result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(stem), '--deadline-minutes', '0.1', '--', './...'],
                                    cwd=root, env=dict(env, GOTOOLCHAIN='local'), capture_output=True, text=True, timeout=120)
            self.assertLess(time.monotonic() - started, 60)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('0.1 minute deadline', result.stdout)
            self.assertRegex(result.stdout, r'sample TestHangs: \d+\.\d min')
            self.assertNotEqual(json.loads(stem.with_suffix('.json').read_text())['exit_code'], 0)

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

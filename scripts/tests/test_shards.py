import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('checks', Path(__file__).parents[1] / 'check.py')
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class ShardTests(unittest.TestCase):
    listing = '\n'.join([
        'TestAlpha', 'TestBeta', 'TestBeta', 'TestGamma', 'TestDelta',
        'FuzzOrientation', 'Example', 'ExampleService_Get',
        'ok  papergo/internal/dms  0.010s', '?  papergo/cmd/api  [no test files]',
    ])

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        patcher = patch.object(checks, 'ROOT', self.root)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_all_tests_fuzz_seeds_and_examples_run_on_exactly_one_shard(self):
        expected = {
            'TestAlpha', 'TestBeta', 'TestGamma', 'TestDelta',
            'FuzzOrientation', 'Example', 'ExampleService_Get',
        }
        shards = [checks.shard_names(self.listing, i, 3) for i in range(1, 4)]
        flattened = [name for shard in shards for name in shard]
        self.assertEqual(set(flattened), expected)
        self.assertEqual(len(flattened), len(expected))
        self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)
        for shard in shards:
            pattern = re.compile(checks.run_pattern(shard))
            self.assertEqual({name for name in expected if pattern.search(name)}, set(shard))
            self.assertIsNone(pattern.search('TestAlphaExtra'))

    def test_listing_order_does_not_change_shards(self):
        reversed_listing = '\n'.join(reversed(self.listing.splitlines()))
        for shard in range(1, 4):
            self.assertEqual(checks.shard_names(self.listing, shard, 3), checks.shard_names(reversed_listing, shard, 3))

    def test_empty_shard_matches_no_tests(self):
        names = checks.shard_names('TestAlpha\n', 2, 3)
        self.assertEqual(names, [])
        self.assertIsNone(re.search(checks.run_pattern(names), 'TestAlpha'))

    def test_invalid_shards_and_empty_listing_fail(self):
        for shard, shards in [(0, 3), (4, 3), (1, 0)]:
            with self.subTest(shard=shard, shards=shards), self.assertRaises(ValueError):
                checks.shard_names(self.listing, shard, shards)
        with self.assertRaises(ValueError):
            checks.shard_names('? papergo/cmd/api [no test files]\n', 1, 3)

    def test_shard_lists_then_runs_its_names_with_the_same_flags(self):
        with patch.object(checks.subprocess, 'run') as run:
            run.return_value.stdout = self.listing
            run.return_value.returncode = 0
            checks.go_tests('race-1', ['-race', '-count=1', '-timeout=15m'], ['./...'], (1, 3))
        listing, execution = [call.args[0] for call in run.call_args_list]
        self.assertEqual(listing[:5], ['go', 'test', '-race', '-count=1', '-timeout=15m'])
        self.assertIn('-list', listing)
        self.assertTrue(run.call_args_list[0].kwargs['check'])
        flags = execution[execution.index('--') + 1:]
        self.assertEqual(flags[:3], ['-race', '-count=1', '-timeout=15m'])
        self.assertEqual(flags[-1], './...')
        self.assertTrue(execution[1].endswith('go-test.py'))
        self.assertIn('--preparation-seconds', execution)
        self.assertEqual(flags[flags.index('-run') + 1], checks.run_pattern(checks.shard_names(self.listing, 1, 3)))
        self.assertFalse(run.call_args_list[1].kwargs['check'])

    def test_test_failure_propagates(self):
        with patch.object(checks.subprocess, 'run') as run:
            run.side_effect = [subprocess.CompletedProcess([], 0, stdout=self.listing), subprocess.CompletedProcess([], 1)]
            with self.assertRaises(subprocess.CalledProcessError) as failure:
                checks.go_tests('race-1', ['-race'], ['./...'], (1, 3))
        self.assertEqual(failure.exception.returncode, 1)

    def test_discovery_failure_keeps_diagnostics_without_running_tests(self):
        error = subprocess.CalledProcessError(1, ['go', 'test'], output='compile failure detail')
        with patch.object(checks.subprocess, 'run', side_effect=error) as run, self.assertRaises(subprocess.CalledProcessError):
            checks.go_tests('race-2', ['-race'], ['./...'], (2, 3))
        self.assertEqual(run.call_count, 1)
        self.assertEqual((self.root / '.cache/checks/race-2.discovery.log').read_text(), 'compile failure detail')


if __name__ == '__main__':
    unittest.main()

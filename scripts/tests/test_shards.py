import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('checks', Path(__file__).parents[1] / 'check.py')
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class ShardTests(unittest.TestCase):
    weights = {'papergo/a': 9, 'papergo/b': 5, 'papergo/c': 4, 'papergo/d': 3, 'papergo/e': 1, 'papergo/f': 1}

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        patcher = patch.object(checks, 'ROOT', self.root)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_every_package_runs_on_exactly_one_balanced_shard(self):
        shards = [checks.shard_packages(self.weights, i, 3) for i in range(1, 4)]
        flattened = [package for shard in shards for package in shard]
        self.assertCountEqual(flattened, self.weights)
        loads = [sum(self.weights[package] for package in shard) for shard in shards]
        self.assertEqual(sorted(loads), [7, 7, 9])
        self.assertEqual(shards[0], ['papergo/a'])

    def test_order_does_not_change_shards(self):
        reversed_weights = dict(reversed(list(self.weights.items())))
        for shard in range(1, 4):
            self.assertEqual(checks.shard_packages(self.weights, shard, 3), checks.shard_packages(reversed_weights, shard, 3))

    def test_invalid_shards_fail(self):
        for shard, shards in [(0, 3), (4, 3), (1, 0)]:
            with self.subTest(shard=shard, shards=shards), self.assertRaises(ValueError):
                checks.shard_packages(self.weights, shard, shards)

    def test_weights_count_tests_fuzz_targets_and_examples(self):
        package = self.root / 'a'
        package.mkdir()
        (package / 'a_test.go').write_text('package a\n\nfunc TestOne(t *testing.T) {}\nfunc FuzzTwo(f *testing.F) {}\n'
                                           'func helper() {}\n// func TestCommented(t *testing.T) {}\n')
        (package / 'x_test.go').write_text('package a_test\n\nfunc ExampleThree() {}\n')
        (self.root / 'b').mkdir()
        listing = f'papergo/a|{package}|a_test.go,x_test.go\npapergo/b|{self.root / "b"}|,\n'
        with patch.object(checks.subprocess, 'run') as run:
            run.return_value.stdout = listing
            self.assertEqual(checks.package_weights(['./...']), {'papergo/a': 4, 'papergo/b': 1})
        self.assertEqual(run.call_args.args[0][:3], ['go', 'list', '-f'])

    def test_shard_runs_its_packages_with_the_same_flags(self):
        with patch.object(checks, 'package_weights', return_value=self.weights) as weights, \
                patch.object(checks.subprocess, 'run') as run:
            run.return_value.returncode = 0
            checks.go_tests('race-1', ['-race', '-count=1', '-timeout=15m'], ['./...'], (1, 3))
        weights.assert_called_once_with(['./...'])
        execution = run.call_args.args[0]
        self.assertTrue(execution[1].endswith('go-test.py'))
        self.assertIn('--preparation-seconds', execution)
        flags = execution[execution.index('--') + 1:]
        self.assertEqual(flags, ['-race', '-count=1', '-timeout=15m', *checks.shard_packages(self.weights, 1, 3)])
        self.assertFalse(run.call_args.kwargs['check'])

    def test_test_failure_propagates(self):
        with patch.object(checks, 'package_weights', return_value=self.weights), \
                patch.object(checks.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1)), \
                self.assertRaises(subprocess.CalledProcessError) as failure:
            checks.go_tests('race-1', ['-race'], ['./...'], (1, 3))
        self.assertEqual(failure.exception.returncode, 1)


if __name__ == '__main__':
    unittest.main()

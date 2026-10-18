import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("race", Path(__file__).parents[1] / "test-race.py")
race = importlib.util.module_from_spec(spec)
spec.loader.exec_module(race)


class RaceShardTests(unittest.TestCase):
    listing = "\n".join([
        "TestAlpha", "TestBeta", "TestBeta", "TestGamma", "TestDelta",
        "FuzzOrientation", "Example", "ExampleService_Get",
        "ok  papergo/internal/dms  0.010s", "?  papergo/cmd/api  [no test files]",
    ])

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.addCleanup(os.chdir, Path.cwd())
        os.chdir(temporary.name)

    def test_all_tests_fuzz_seeds_and_examples_run_on_exactly_one_shard(self):
        expected = {
            "TestAlpha", "TestBeta", "TestGamma", "TestDelta",
            "FuzzOrientation", "Example", "ExampleService_Get",
        }
        shards = [race.shard_names(self.listing, i, 3) for i in range(1, 4)]
        flattened = [name for shard in shards for name in shard]
        self.assertEqual(set(flattened), expected)
        self.assertEqual(len(flattened), len(expected))
        self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)
        for shard in shards:
            pattern = re.compile(race.run_pattern(shard))
            self.assertEqual({name for name in expected if pattern.search(name)}, set(shard))
            self.assertIsNone(pattern.search("TestAlphaExtra"))

    def test_listing_order_does_not_change_shards(self):
        reversed_listing = "\n".join(reversed(self.listing.splitlines()))
        for shard in range(1, 4):
            self.assertEqual(
                race.shard_names(self.listing, shard, 3),
                race.shard_names(reversed_listing, shard, 3),
            )

    def test_empty_shard_matches_no_tests(self):
        names = race.shard_names("TestAlpha\n", 2, 3)
        self.assertEqual(names, [])
        self.assertIsNone(re.search(race.run_pattern(names), "TestAlpha"))

    def test_invalid_shards_and_empty_listing_fail(self):
        for shard, shards in [(0, 3), (4, 3), (1, 0)]:
            with self.subTest(shard=shard, shards=shards), self.assertRaises(ValueError):
                race.shard_names(self.listing, shard, shards)
        with self.assertRaises(ValueError):
            race.shard_names("? papergo/cmd/api [no test files]\n", 1, 3)

    def test_commands_keep_race_detection_timeout_and_subtests(self):
        with patch.object(race.subprocess, "run") as run, \
                patch("sys.argv", ["test-race.py", "--shard", "1", "--shards", "3"]):
            run.return_value.stdout = self.listing
            run.return_value.returncode = 0
            race.main()
        listing, execution = [call.args[0] for call in run.call_args_list]
        self.assertEqual(listing[:5], ["go", "test", "-race", "-count=1", "-timeout=15m"])
        flags = execution[execution.index("--") + 1:]
        self.assertEqual(flags[:3], ["-race", "-count=1", "-timeout=15m"])
        self.assertEqual(flags[-1], "./...")
        self.assertTrue(execution[1].endswith("go-test.py"))
        self.assertIn("--output", execution)
        self.assertIn("--preparation-seconds", execution)
        self.assertIn("-list", listing)
        self.assertNotIn("/", execution[execution.index("-run") + 1])
        self.assertTrue(run.call_args_list[0].kwargs["check"])
        self.assertFalse(run.call_args_list[1].kwargs["check"])

    def test_test_failure_propagates_without_dumping_the_regex_command(self):
        with patch.object(race.subprocess, "run") as run, \
                patch("sys.argv", ["test-race.py", "--shard", "1", "--shards", "3"]):
            run.side_effect = [
                subprocess.CompletedProcess([], 0, stdout=self.listing),
                subprocess.CompletedProcess([], 1),
            ]
            self.assertEqual(race.main(), 1)
        self.assertFalse(run.call_args_list[1].kwargs["check"])

    def test_discovery_failure_keeps_diagnostics_without_running_tests(self):
        error = subprocess.CalledProcessError(1, ['go', 'test'], output='compile failure detail')
        with patch.object(race.subprocess, 'run', side_effect=error) as run, \
                patch('sys.argv', ['test-race.py', '--shard', '2', '--shards', '3']):
            with self.assertRaises(subprocess.CalledProcessError):
                race.main()
        self.assertEqual(run.call_count, 1)
        self.assertEqual(Path('.cache/checks/race-2.discovery.log').read_text(), 'compile failure detail')
        self.assertIn('"exit_code": 1', Path('.cache/checks/race-2.discovery.json').read_text())

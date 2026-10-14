import importlib.util
from pathlib import Path
import re
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
            race.main()
        listing, execution = [call.args[0] for call in run.call_args_list]
        for command in [listing, execution]:
            self.assertEqual(command[:5], ["go", "test", "-race", "-count=1", "-timeout=15m"])
            self.assertEqual(command[-1], "./...")
        self.assertIn("-list", listing)
        self.assertIn("-v", execution)
        self.assertNotIn("/", execution[execution.index("-run") + 1])
        self.assertTrue(all(call.kwargs["check"] for call in run.call_args_list))

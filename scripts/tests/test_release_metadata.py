import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("metadata", Path(__file__).parents[1] / "release-metadata.py")
metadata = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metadata)


def releases(*tags, draft=()):
    return [{"tagName": tag, "isDraft": tag in draft} for tag in tags]


class ReleaseMetadataTests(unittest.TestCase):
    def test_first_release(self):
        self.assertEqual(metadata.previous_release("v0.1.0", []), "")

    def test_newest_published_version_skips_drafts_and_model_bundles(self):
        history = releases("v0.9.0", "v0.10.0", "v0.11.0", "models-laya-b7fb100758f4", draft=("v0.11.0",))
        self.assertEqual(metadata.previous_release("v1.0.0", history), "v0.10.0")
        self.assertEqual(metadata.previous_release("v0.1.0", releases("models-laya-b7fb100758f4")), "")

    def test_release_candidates_share_the_history(self):
        self.assertEqual(metadata.previous_release("v1.0.0", releases("v1.0.0-rc.2")), "v1.0.0-rc.2")
        with self.assertRaises(ValueError):
            metadata.previous_release("v1.0.0-rc.3", releases("v1.0.0"))

    def test_rejects_old_invalid_and_released_tags(self):
        for tag in ["v0.9.0", "v01.2.3", "v1.0.1;exit 0", "v1.0.0"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                metadata.previous_release(tag, releases("v1.0.0"))

    def test_asset_contract_includes_shared_zip_and_enabled_platforms(self):
        self.assertEqual(metadata.expected_assets("v0.2.0"), {
            "papergo_v0.2.0_linux_amd64.zip", "papergo_v0.2.0_linux_arm64.zip",
            "papergo_v0.2.0_windows_amd64.zip", "papergo_v0.2.0_assets.zip", "SHA256SUMS",
        })

    def test_publish_attaches_every_asset_with_notes_from_the_previous_release(self):
        with tempfile.TemporaryDirectory() as directory:
            for name in metadata.expected_assets("v0.2.0"):
                Path(directory, name).write_bytes(b"artifact")
            with patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.2.0", "v0.1.0", directory)
            command = run.call_args.args[0]
            self.assertEqual(command[:3], ["gh", "release", "create"])
            self.assertEqual(sum(argument.startswith(directory) for argument in command), 5)
            self.assertIn("--latest", command)
            self.assertEqual(command[command.index("--notes-start-tag") + 1], "v0.1.0")
            with patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.2.0", "", directory)
            self.assertNotIn("--notes-start-tag", run.call_args.args[0])

    def test_release_candidates_are_prereleases(self):
        with tempfile.TemporaryDirectory() as directory:
            for name in metadata.expected_assets("v0.2.0-rc.1"):
                Path(directory, name).write_bytes(b"artifact")
            with patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.2.0-rc.1", "", directory)
            command = run.call_args.args[0]
            self.assertIn("--prerelease", command)
            self.assertIn("--latest=false", command)

    def test_missing_or_empty_assets_prevent_publication(self):
        with tempfile.TemporaryDirectory() as directory:
            for name in metadata.expected_assets("v0.2.0") - {"SHA256SUMS"}:
                Path(directory, name).write_bytes(b"artifact")
            with patch.object(metadata.subprocess, "run") as run, self.assertRaises(ValueError):
                metadata.publish("v0.2.0", "", directory)
            Path(directory, "SHA256SUMS").write_bytes(b"")
            with self.assertRaises(ValueError):
                metadata.publish("v0.2.0", "", directory)
            run.assert_not_called()

    def test_prepare_requires_a_verified_commit(self):
        responses = {"git": "abc123", "release": "[]", "run": "[]"}

        def fake(command, text=True):
            return responses["git" if command[0] == "git" else command[1]]
        with tempfile.NamedTemporaryFile("r") as out, patch.dict(os.environ, {"GITHUB_OUTPUT": out.name}), \
                patch.object(metadata.subprocess, "check_output", side_effect=fake):
            with self.assertRaisesRegex(ValueError, "no successful verify run"):
                metadata.prepare("v0.1.0")
            responses["run"] = '[{"databaseId": 1}]'
            metadata.prepare("v0.1.0")
            self.assertIn("commit=abc123\n", out.read())


if __name__ == "__main__":
    unittest.main()

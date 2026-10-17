import contextlib
import importlib.util
import io
import json
import os
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("metadata", Path(__file__).parents[1] / "release-metadata.py")
metadata = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metadata)


class ReleaseMetadataTests(unittest.TestCase):
    def test_first_release(self):
        self.assertEqual(metadata.select_previous("v0.1.0", []), "")

    def test_published_semver_and_drafts(self):
        releases = [{"tag_name": "v0.9.0"}, {"tag_name": "v0.10.0"}, {"tag_name": "v1.0.0", "draft": True}]
        self.assertEqual(metadata.select_previous("v1.0.0", releases), "v0.10.0")

    def test_prerelease_history(self):
        self.assertEqual(metadata.select_previous("v1.0.0", [{"tag_name": "v1.0.0-rc.2"}]), "v1.0.0-rc.2")
        with self.assertRaises(ValueError):
            metadata.select_previous("v1.0.0-rc.3", [{"tag_name": "v1.0.0"}])

    def test_reject_out_of_order_and_invalid_tags(self):
        for tag in ["v0.9.0", "v01.2.3", "v1.0.1;exit 0"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                metadata.select_previous(tag, [{"tag_name": "v1.0.0"}])


    def test_existing_release_is_excluded_from_its_own_parent(self):
        self.assertEqual(metadata.select_previous("v0.1.0", [{"tag_name": "v0.1.0"}]), "")
        self.assertEqual(metadata.select_previous("v0.2.0", [
            {"tag_name": "v0.1.0"}, {"tag_name": "v0.2.0"},
        ]), "v0.1.0")

    def test_asset_contract_includes_shared_zip_and_enabled_platforms(self):
        self.assertEqual(metadata.expected_assets("v0.2.0"), {
            "papergo_v0.2.0_linux_amd64.zip", "papergo_v0.2.0_linux_arm64.zip",
            "papergo_v0.2.0_windows_amd64.zip", "papergo_v0.2.0_assets.zip",
            "release-database.json", "SHA256SUMS",
        })

    def test_release_states(self):
        tag = "v0.1.0"
        self.assertEqual(metadata.release_state(tag, []), "new")
        self.assertEqual(metadata.release_state(tag, [{"tag_name": tag, "draft": True}]), "draft")
        self.assertEqual(metadata.release_state(tag, [{"tag_name": tag, "immutable": False, "assets": []}]), "empty")
        assets = [{"name": name, "size": 10, "state": "uploaded"} for name in metadata.expected_assets(tag)]
        self.assertEqual(metadata.release_state(tag, [{"tag_name": tag, "immutable": True, "assets": assets}]), "complete")

    def test_reject_immutable_empty_and_partial_published_releases(self):
        tag = "v0.1.0"
        for release in [
            {"tag_name": tag, "immutable": True, "assets": []},
            {"tag_name": tag, "assets": [{"name": "release-database.json", "size": 10, "state": "uploaded"}]},
        ]:
            with self.subTest(release=release), self.assertRaises(ValueError):
                metadata.release_state(tag, [release])

    def prepare_assets(self, directory, tag):
        for name in metadata.expected_assets(tag):
            (Path(directory) / name).write_bytes(b"prepared artifact")

    def test_populate_precreated_published_release_without_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            self.prepare_assets(directory, "v0.1.0")
            with patch.dict(os.environ, {"PREVIOUS_TAG": ""}), patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.1.0", "", "empty", directory)
        self.assertEqual(run.call_count, 1)
        args = run.call_args.args[0]
        self.assertEqual(args[:4], ["gh", "release", "upload", "v0.1.0"])
        self.assertNotIn("--clobber", args)
        self.assertTrue(args[-1].endswith("SHA256SUMS"))

    def test_new_release_attaches_assets_before_publishing(self):
        with tempfile.TemporaryDirectory() as directory:
            self.prepare_assets(directory, "v0.2.0")
            with patch.dict(os.environ, {"PREVIOUS_TAG": "v0.1.0"}), patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.2.0", "v0.1.0", "new", directory)
        calls = [call.args[0] for call in run.call_args_list]
        self.assertEqual([args[2] for args in calls], ["create", "upload", "edit"])
        self.assertIn("--draft", calls[0])
        self.assertIn("--notes-start-tag", calls[0])
        self.assertIn("--draft=false", calls[2])

    def test_draft_retry_replaces_only_draft_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            self.prepare_assets(directory, "v0.1.0-rc.1")
            with patch.dict(os.environ, {"PREVIOUS_TAG": ""}), patch.object(metadata.subprocess, "run") as run:
                metadata.publish("v0.1.0-rc.1", "", "draft", directory)
        self.assertIn("--clobber", run.call_args_list[0].args[0])
        self.assertIn("--prerelease", run.call_args_list[1].args[0])
        self.assertIn("--latest=false", run.call_args_list[1].args[0])

    def test_completed_release_rerun_does_not_mutate_github(self):
        with patch.object(metadata.subprocess, "run") as run:
            metadata.publish("v0.1.0", "", "complete", "unused")
        run.assert_not_called()

    def test_changed_parent_or_missing_assets_prevents_any_publication(self):
        with tempfile.TemporaryDirectory() as directory:
            for parent in ["v0.1.0", ""]:
                with patch.dict(os.environ, {"PREVIOUS_TAG": parent}), patch.object(metadata.subprocess, "run") as run:
                    with self.assertRaises(ValueError):
                        metadata.publish("v0.2.0", "", "empty", directory)
                    run.assert_not_called()

    def test_metadata_accepts_actual_precreated_release_case(self):
        releases = [[{"tag_name": "v0.1.0", "draft": False, "immutable": False, "assets": []}]]
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "outputs"
            env = {"RELEASE_TAG": "v0.1.0", "GITHUB_REPOSITORY": "eduardpaul/PaperGo", "GITHUB_OUTPUT": str(output)}
            with patch.dict(os.environ, env), patch.object(metadata.sys, "argv", ["release-metadata.py"]), \
                    patch.object(metadata.subprocess, "check_output", side_effect=[json.dumps(releases), "commit\n", "commit\n", "2026-10-13T12:00:00Z\n"]), \
                    patch.object(metadata.subprocess, "run"), contextlib.redirect_stdout(io.StringIO()):
                metadata.main()
            values = dict(line.split("=", 1) for line in output.read_text().splitlines())
        self.assertEqual(values["state"], "empty")
        self.assertEqual(values["previous"], "")
        self.assertEqual(values["commit"], "commit")

import importlib.util
from pathlib import Path
import unittest

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

    def test_reject_existing_out_of_order_and_invalid_tags(self):
        for tag in ["v1.0.0", "v0.9.0", "v01.2.3", "v1.0.1;exit 0"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                metadata.select_previous(tag, [{"tag_name": "v1.0.0"}])

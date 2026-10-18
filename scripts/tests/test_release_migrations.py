import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("release_migrations", Path(__file__).parents[1] / "release-migrations.py")
history = importlib.util.module_from_spec(spec)
spec.loader.exec_module(history)


class ReleaseMigrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.remote = root / "remote.git"
        self.repo = root / "source"
        subprocess.run(["git", "init", "--bare", "-q", str(self.remote)], check=True)
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        self.previous_cwd = Path.cwd()
        os.chdir(self.repo)
        self.addCleanup(os.chdir, self.previous_cwd)
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.com")
        self.git("remote", "add", "origin", str(self.remote))
        (self.repo / "source.txt").write_text("source\n")
        self.git("add", "source.txt")
        self.git("commit", "-qm", "Source")
        self.commit = self.git("rev-parse", "HEAD").strip()
        self.git("tag", "v1.0.0")
        self.bundle = root / "bundle.json"
        self.bundle.write_text(json.dumps({"format": 1, "releases": [{"version": "v1.0.0"}]}))

    def git(self, *args):
        return subprocess.check_output(["git", *args], text=True, stderr=subprocess.PIPE)

    def test_records_before_build_and_verifies_retries_without_touching_source(self):
        (self.repo / "untracked.txt").write_text("leave alone")
        before = self.git("status", "--porcelain")
        recorded = history.persist(self.bundle, "v1.0.0", self.commit)
        self.assertEqual(before, self.git("status", "--porcelain"))
        self.assertEqual(self.commit, self.git("rev-parse", "HEAD").strip())
        self.assertEqual(self.bundle.read_bytes(), history.git("show", f"{recorded}:migrations/releases/v1.0.0/release-database.json"))
        self.assertEqual(recorded, history.persist(self.bundle, "v1.0.0", self.commit))
        self.assertEqual(recorded, history.persist(self.bundle, "v1.0.0", self.commit, check=True))
        self.bundle.write_text(self.bundle.read_text() + "\n")
        with self.assertRaisesRegex(ValueError, "immutable"):
            history.persist(self.bundle, "v1.0.0", self.commit)

    def test_new_release_retains_old_bundle(self):
        first = history.persist(self.bundle, "v1.0.0", self.commit)
        old_bytes = self.bundle.read_bytes()
        self.git("tag", "v1.1.0")
        self.bundle.write_text(json.dumps({"format": 1, "releases": [{"version": "v1.0.0"}, {"version": "v1.1.0"}]}))
        second = history.persist(self.bundle, "v1.1.0", self.commit)
        self.assertEqual(first, self.git("rev-parse", f"{second}^").strip())
        self.assertEqual(old_bytes, history.git("show", f"{second}:migrations/releases/v1.0.0/release-database.json"))

    def test_published_bundle_cannot_create_missing_history(self):
        with self.assertRaisesRegex(ValueError, "no committed"):
            history.persist(self.bundle, "v1.0.0", self.commit, check=True)

    def test_rejects_wrong_source_and_wrong_bundle(self):
        with self.assertRaisesRegex(ValueError, "immutable tag"):
            history.persist(self.bundle, "v1.0.0", "0" * 40)
        self.bundle.write_text('{"format":1,"releases":[{"version":"v2.0.0"}]}')
        with self.assertRaisesRegex(ValueError, "end at"):
            history.persist(self.bundle, "v1.0.0", self.commit)

    def test_remote_failure_does_not_create_a_new_history(self):
        self.git("remote", "set-url", "origin", str(self.remote.parent / "missing.git"))
        with self.assertRaisesRegex(RuntimeError, "Cannot inspect"):
            history.persist(self.bundle, "v1.0.0", self.commit)

    def test_new_release_cannot_rewrite_a_recorded_parent(self):
        history.persist(self.bundle, "v1.0.0", self.commit)
        self.git("tag", "v1.1.0")
        self.bundle.write_text(json.dumps({"format": 1, "releases": [{"version": "v1.0.0", "upgrade": ["changed"]}, {"version": "v1.1.0"}]}))
        with self.assertRaisesRegex(ValueError, "committed parent"):
            history.persist(self.bundle, "v1.1.0", self.commit)

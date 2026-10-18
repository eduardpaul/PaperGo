#!/usr/bin/env python3
"""Persist exact release SQL bundles in Git before compilation, without moving tags."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

HISTORY_REF = "refs/heads/release-migrations"
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?")


def git(*args, data=None, env=None):
    return subprocess.check_output(["git", *args], input=data, env=env)


def persist(bundle_path, tag, source_commit, check=False):
    if not TAG.fullmatch(tag):
        raise ValueError("Invalid release tag")
    source = git("rev-parse", f"refs/tags/{tag}^{{commit}}").decode().strip()
    if source != source_commit:
        raise ValueError("Release source does not match its immutable tag")
    raw = Path(bundle_path).read_bytes()
    bundle = json.loads(raw)
    if bundle["format"] != 1 or bundle["releases"][-1]["version"] != tag:
        raise ValueError("Database bundle does not end at the release tag")
    remote = subprocess.run(["git", "ls-remote", "--exit-code", "origin", HISTORY_REF],
                            capture_output=True, check=False)
    if remote.returncode not in (0, 2):
        raise RuntimeError(f"Cannot inspect release history: {remote.stderr.decode()}")
    parent = None
    prefix = f"migrations/releases/{tag}"
    if remote.returncode == 0:
        git("fetch", "--no-tags", "origin", HISTORY_REF)
        parent = git("rev-parse", "FETCH_HEAD").decode().strip()
        files = git("ls-tree", "--name-only", parent, f"{prefix}/").decode().splitlines()
        if files:
            recorded = git("show", f"{parent}:{prefix}/release-database.json")
            recorded_source = git("show", f"{parent}:{prefix}/source.txt").decode().strip()
            if recorded != raw or recorded_source != source:
                raise ValueError("Release migration history is immutable; generated bundle or source differs")
            print(f"Verified committed database history for {tag} at {parent}")
            return parent
    # The cumulative bundle must extend exactly the already committed history.
    # A matching GitHub asset alone cannot replace a recorded release step.
    for i, release in enumerate(bundle["releases"][:-1]):
        previous_tag = release["version"]
        if not TAG.fullmatch(previous_tag) or parent is None:
            raise ValueError("Missing committed parent migration history")
        recorded = json.loads(git("show", f"{parent}:migrations/releases/{previous_tag}/release-database.json"))
        if recorded != dict(format=bundle["format"], releases=bundle["releases"][:i + 1]):
            raise ValueError("Cumulative bundle differs from committed parent migration history")
    if check:
        raise ValueError("Published release has no committed database history")
    # An isolated index builds an artifact-only tree without checking out the
    # history branch or staging/modifying anything in the source checkout.
    with tempfile.TemporaryDirectory(prefix="papergo-release-history-") as directory:
        env = dict(os.environ, GIT_INDEX_FILE=str(Path(directory) / "index"),
                   GIT_AUTHOR_NAME="github-actions[bot]",
                   GIT_AUTHOR_EMAIL="41898282+github-actions[bot]@users.noreply.github.com",
                   GIT_COMMITTER_NAME="github-actions[bot]",
                   GIT_COMMITTER_EMAIL="41898282+github-actions[bot]@users.noreply.github.com")
        git("read-tree", parent if parent else "--empty", env=env)
        for name, content in [("release-database.json", raw), ("source.txt", (source + "\n").encode())]:
            blob = git("hash-object", "-w", "--stdin", data=content).decode().strip()
            git("update-index", "--add", "--cacheinfo", f"100644,{blob},{prefix}/{name}", env=env)
        tree = git("write-tree", env=env).decode().strip()
        parents = ["-p", parent] if parent else []
        commit = git("commit-tree", tree, *parents, data=f"Record {tag} database migrations\n".encode(), env=env).decode().strip()
        # A racing update fails with a non-fast-forward rejection, never a force
        # push. Release publication is serialized by the workflow as well.
        git("push", "origin", f"{commit}:{HISTORY_REF}")
    print(f"Committed database history for {tag} at {commit}")
    return commit


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("bundle", type=Path)
    parser.add_argument("--check", action="store_true", help="Verify an already published bundle without writing")
    args = parser.parse_args()
    persist(args.bundle, os.environ["RELEASE_TAG"], os.environ["RELEASE_COMMIT"], args.check)


if __name__ == "__main__":
    main()

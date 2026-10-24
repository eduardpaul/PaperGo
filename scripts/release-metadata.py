#!/usr/bin/env python3
"""Check a release tag before building it, and publish its assets.

Without arguments it checks that the tag is a version newer than every
published release, has no release yet, and names a commit whose verify run
on main succeeded, then writes tag, commit, date and previous to
$GITHUB_OUTPUT. With --publish DIRECTORY it creates the GitHub release with
the prepared assets.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

VERSION = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.(0|[1-9][0-9]*))?$")
PLATFORMS = [("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64")]  # ("darwin", "arm64")


def version_key(tag):
    match = VERSION.fullmatch(tag)
    if not match:
        raise ValueError(f"Invalid release tag: {tag}; use vX.Y.Z or vX.Y.Z-rc.N")
    major, minor, patch, rc = match.groups()
    return (int(major), int(minor), int(patch), rc is None, int(rc or 0))


def previous_release(tag, releases):
    """The newest published version release, which tag must follow. Other
    releases, such as the model bundles, do not count."""
    current = version_key(tag)
    if any(release["tagName"] == tag for release in releases):
        raise ValueError(f"{tag} already has a release; tag a new version")
    published = [release["tagName"] for release in releases
                 if not release.get("isDraft") and VERSION.fullmatch(release["tagName"])]
    if not published:
        return ""
    previous = max(published, key=version_key)
    if current <= version_key(previous):
        raise ValueError(f"{tag} must be newer than the latest published release {previous}")
    return previous


def expected_assets(tag):
    return {f"papergo_{tag}_{system}_{arch}.zip" for system, arch in PLATFORMS} | {f"papergo_{tag}_assets.zip", "SHA256SUMS"}


def output(command):
    return subprocess.check_output(command, text=True).strip()


def prepare(tag):
    commit = output(["git", "rev-parse", f"refs/tags/{tag}^{{commit}}"])
    releases = json.loads(output(["gh", "release", "list", "--limit", "1000", "--json", "tagName,isDraft"]))
    previous = previous_release(tag, releases)
    runs = json.loads(output(["gh", "run", "list", "--workflow", "ci.yml", "--branch", "main", "--commit", commit,
                              "--status", "success", "--json", "databaseId"]))
    if not runs:
        raise ValueError(f"{tag} names {commit}, which has no successful verify run on main; tag a commit main has verified")
    values = {"tag": tag, "commit": commit, "previous": previous,
              "date": output(["git", "show", "-s", "--format=%cI", commit])}
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as out:
        for key, value in values.items():
            out.write(f"{key}={value}\n")
    print(f"Release {tag} at {commit}; previous published release: {previous or '(first release)'}")


def publish(tag, previous, directory):
    paths = sorted(Path(directory).iterdir())
    if {path.name for path in paths} != expected_assets(tag) or any(path.stat().st_size == 0 for path in paths):
        raise ValueError("Publication requires all three binary ZIPs, the shared assets ZIP and SHA256SUMS")
    command = ["gh", "release", "create", tag, *map(str, paths), "--verify-tag", "--title", f"PaperGo {tag}", "--generate-notes"]
    if previous:
        command += ["--notes-start-tag", previous]
    command += ["--prerelease", "--latest=false"] if "-rc." in tag else ["--latest"]
    subprocess.run(command, check=True)
    print(f"Published {tag}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--publish", metavar="DIRECTORY", help="create the release with the prepared assets")
    args = parser.parse_args()
    tag = os.environ["RELEASE_TAG"]
    version_key(tag)
    if args.publish:
        publish(tag, os.environ.get("PREVIOUS_TAG", ""), args.publish)
    else:
        prepare(tag)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

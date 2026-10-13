#!/usr/bin/env python3
"""Validate a linear published release history; never base upgrades on draft tags."""
import json
import os
import re
import subprocess
import sys

VERSION = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.(0|[1-9][0-9]*))?$")


def version_key(tag):
    match = VERSION.fullmatch(tag)
    if not match:
        raise ValueError(f"Invalid release tag: {tag}; use vX.Y.Z or vX.Y.Z-rc.N")
    major, minor, patch, rc = match.groups()
    return (int(major), int(minor), int(patch), rc is None, int(rc or 0))


def select_previous(tag, releases):
    current = version_key(tag)
    published = [r for r in releases if not r.get("draft", False)]
    if any(r["tag_name"] == tag for r in published):
        raise ValueError(f"{tag} is already published; published assets cannot be replaced")
    # Prereleases participate in the same forward-only database history.
    candidates = [(version_key(r["tag_name"]), r["tag_name"]) for r in published]
    if not candidates:
        return ""
    key, previous = max(candidates)
    if current <= key:
        raise ValueError(f"{tag} must be newer than the previous published release {previous}")
    return previous


def main():
    tag = os.environ["RELEASE_TAG"]
    raw = subprocess.check_output([
        "gh", "api", "--paginate", "--slurp",
        f"repos/{os.environ['GITHUB_REPOSITORY']}/releases?per_page=100",
    ], text=True)
    releases = [release for page in json.loads(raw) for release in page]
    previous = select_previous(tag, releases)
    subprocess.run(["git", "show-ref", "--verify", f"refs/tags/{tag}"], check=True)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tagged = subprocess.check_output(["git", "rev-parse", f"{tag}^{{commit}}"], text=True).strip()
    if head != tagged:
        raise ValueError("Release tag moved or checkout does not match the tag")
    if previous:
        subprocess.run(["git", "merge-base", "--is-ancestor", previous, tag], check=True)
    values = {
        "tag": tag,
        "previous": previous,
        "prerelease": str("-rc." in tag).lower(),
        "commit": head,
        "date": subprocess.check_output(["git", "show", "-s", "--format=%cI", "HEAD"], text=True).strip(),
    }
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in values.items():
            output.write(f"{key}={value}\n")
    print(f"Release {tag}; previous published release: {previous or '(first release)'}")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

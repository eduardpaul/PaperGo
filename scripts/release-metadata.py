#!/usr/bin/env python3
"""Validate a release tag against the published release history and publish its assets."""
import argparse
import json
import os
from pathlib import Path
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
    published = [r for r in releases if not r.get("draft", False) and r["tag_name"] != tag]
    # Prereleases count: release notes start at the latest published tag.
    candidates = [(version_key(r["tag_name"]), r["tag_name"]) for r in published]
    if not candidates:
        return ""
    key, previous = max(candidates)
    if current <= key:
        raise ValueError(f"{tag} must be newer than the previous published release {previous}")
    return previous


def expected_assets(tag):
    return {
        f"papergo_{tag}_{platform}_{arch}.zip"
        for platform, arch in [
            ("linux", "amd64"), ("linux", "arm64"),
            ("windows", "amd64"),
            # ("darwin", "arm64"),
        ]
    } | {f"papergo_{tag}_assets.zip", "SHA256SUMS"}


def release_state(tag, releases):
    release = next((r for r in releases if r["tag_name"] == tag), None)
    if release is None:
        return "new"
    if release.get("draft", False):
        return "draft"
    assets = release.get("assets", [])
    uploaded = {a["name"] for a in assets if a.get("size", 0) > 0 and a.get("state") == "uploaded"}
    if expected_assets(tag) <= uploaded:
        return "complete"
    if release.get("immutable", False):
        raise ValueError(f"{tag} is immutable and missing release assets; publish a new version")
    if assets:
        raise ValueError(
            f"{tag} has an incomplete set of published assets; they cannot be replaced. "
            "Publish a new version or complete the release with the original build artifacts."
        )
    return "empty"


def publish(tag, previous, state, directory):
    if state == "complete":
        print(f"{tag} already has every release asset; nothing to publish")
        return
    if os.environ.get("PREVIOUS_TAG") != previous:
        raise ValueError("Published parent changed; rerun release preparation")
    directory = Path(directory)
    paths = {p.name: p for p in directory.iterdir() if p.is_file()}
    if set(paths) != expected_assets(tag) or any(p.stat().st_size == 0 for p in paths.values()):
        raise ValueError("Publication requires all three binary ZIPs, the shared assets ZIP and SHA256SUMS")
    if state == "new":
        args = ["gh", "release", "create", tag, "--draft", "--verify-tag",
                "--title", f"PaperGo {tag}", "--generate-notes"]
        if previous:
            args += ["--notes-start-tag", previous]
        if "-rc." in tag:
            args += ["--prerelease"]
        subprocess.run(args, check=True)
    # Only draft uploads may replace files. A pre-created published release
    # must be empty and mutable; gh also refuses duplicate names without clobber.
    args = ["gh", "release", "upload", tag]
    args += [str(paths[name]) for name in sorted(paths) if name != "SHA256SUMS"]
    args += [str(paths["SHA256SUMS"])]
    if state in {"new", "draft"}:
        args += ["--clobber"]
    subprocess.run(args, check=True)
    if state in {"new", "draft"}:
        args = ["gh", "release", "edit", tag, "--draft=false"]
        if "-rc." in tag:
            args += ["--prerelease", "--latest=false"]
        else:
            args += ["--prerelease=false", "--latest"]
        subprocess.run(args, check=True)
    print(f"Attached every release asset to {tag}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publish", metavar="DIRECTORY", help="Publish prepared release assets")
    args = parser.parse_args()
    tag = os.environ["RELEASE_TAG"]
    raw = subprocess.check_output([
        "gh", "api", "--paginate", "--slurp",
        f"repos/{os.environ['GITHUB_REPOSITORY']}/releases?per_page=100",
    ], text=True)
    releases = [release for page in json.loads(raw) for release in page]
    version_key(tag)
    state = release_state(tag, releases)
    previous = "" if state == "complete" else select_previous(tag, releases)
    subprocess.run(["git", "show-ref", "--verify", f"refs/tags/{tag}"], check=True)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tagged = subprocess.check_output(["git", "rev-parse", f"{tag}^{{commit}}"], text=True).strip()
    if head != tagged:
        raise ValueError("Release tag moved or checkout does not match the tag")
    if previous:
        subprocess.run(["git", "merge-base", "--is-ancestor", previous, tag], check=True)
    values = {
        "tag": tag,
        "state": state,
        "previous": previous,
        "commit": head,
        "date": subprocess.check_output(["git", "show", "-s", "--format=%cI", "HEAD"], text=True).strip(),
    }
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in values.items():
            output.write(f"{key}={value}\n")
    print(f"Release {tag}: {state}; previous published release: {previous or '(first release)'}")
    if args.publish:
        publish(tag, previous, state, args.publish)
    elif state == "complete":
        print("Binary release assets are already published; container publication is checked separately")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

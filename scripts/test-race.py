#!/usr/bin/env python3
"""Run one shard of the complete Go race suite, including fuzz seeds and examples."""

import argparse
import json
import re
import subprocess
import sys
import time
from pathlib import Path


def shard_names(listing, shard, shards):
    if shards < 1 or not 1 <= shard <= shards:
        raise ValueError("shard must be between 1 and shards")
    # A name shared by several packages must stay on the same shard because
    # Go's -run expression applies to every package. Subtests run with the parent.
    names = sorted(set(re.findall(r"^(?:Test|Fuzz|Example)\w*$", listing, re.MULTILINE)))
    if not names:
        raise ValueError("Go did not list any tests")
    return names[shard - 1::shards]


def run_pattern(names):
    # An empty pattern would run every test, so empty shards must match nothing.
    return "^(" + "|".join(re.escape(name) for name in names) + ")$" if names else "^$"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--shard", type=int, required=True)
    parser.add_argument("--shards", type=int, required=True)
    args = parser.parse_args()
    if args.shards < 1 or not 1 <= args.shard <= args.shards:
        parser.error("shard must be between 1 and shards")
    flags = ["-race", "-count=1", "-timeout=15m"]
    started = time.monotonic()
    report = Path(f".cache/checks/race-{args.shard}")
    report.parent.mkdir(parents=True, exist_ok=True)
    try:
        listing = subprocess.run(
            ["go", "test", *flags, "-list", "^(Test|Fuzz|Example)", "./..."],
            check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
    except subprocess.CalledProcessError as error:
        report.with_suffix(".discovery.log").write_text(error.stdout or "", encoding="utf-8")
        report.with_suffix(".discovery.json").write_text(json.dumps({
            "wall_seconds": time.monotonic() - started, "exit_code": error.returncode,
        }), encoding="utf-8")
        print(error.stdout or "", file=sys.stderr)
        raise
    report.with_suffix(".discovery.log").write_text(listing.stdout, encoding="utf-8")
    names = shard_names(listing.stdout, args.shard, args.shards)
    print(f"Race shard {args.shard}/{args.shards}: {len(names)} test names", flush=True)
    subprocess.run(
        [sys.executable, str(Path(__file__).with_name("go-test.py")),
         "--output", str(report),
         "--preparation-seconds", str(time.monotonic() - started), "--",
         *flags, "-run", run_pattern(names), "./..."],
        check=True,
    )


if __name__ == "__main__":
    main()

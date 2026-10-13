#!/usr/bin/env python3
"""Write release checksums, or verify one downloaded history artifact."""
import argparse
import hashlib
from pathlib import Path


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("--verify", metavar="FILE")
    args = parser.parse_args()
    sums = args.directory / "SHA256SUMS"
    if args.verify:
        expected = {}
        for line in sums.read_text().splitlines():
            value, name = line.split("  ", 1)
            if name in expected:
                raise ValueError(f"Duplicate checksum entry: {name}")
            expected[name] = value
        if expected.get(args.verify) != digest(args.directory / args.verify):
            raise ValueError(f"Checksum mismatch: {args.verify}")
    else:
        paths = sorted(p for p in args.directory.iterdir() if p.is_file() and p.name != "SHA256SUMS")
        sums.write_text("".join(f"{digest(p)}  {p.name}\n" for p in paths))


if __name__ == "__main__":
    main()

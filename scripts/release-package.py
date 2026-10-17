#!/usr/bin/env python3
"""Package both executables with the release database artifact and operator guide."""
import argparse
import json
from pathlib import Path
import tarfile
import zipfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--os", required=True, choices=["linux", "darwin", "windows"])
    parser.add_argument("--arch", required=True, choices=["amd64", "arm64"])
    parser.add_argument("--bin-dir", type=Path, help="Directory containing papergo and papergo-admin")
    parser.add_argument("--release-database", type=Path, help="release-database.json to embed")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    dist = root / "dist"
    dist.mkdir(exist_ok=True)
    suffix = ".exe" if args.os == "windows" else ""
    bin_dir = args.bin_dir or (root / "bin")
    release_database = args.release_database or (root / "migrations" / "release.json")
    inputs = {
        f"papergo{suffix}": bin_dir / f"papergo{suffix}",
        f"papergo-admin{suffix}": bin_dir / f"papergo-admin{suffix}",
        "release-database.json": release_database,
        "RELEASES.md": root / "docs" / "releases.md",
        "config.yaml": root / "deploy" / "config.yaml",
        "README.md": root / "deploy" / "README.md",
    }
    bundle = json.loads(inputs["release-database.json"].read_text())
    if bundle["releases"][-1]["version"] != args.version:
        raise ValueError("Packaged bundle does not match release tag")
    name = f"papergo_{args.version}_{args.os}_{args.arch}"
    if args.os == "windows":
        with zipfile.ZipFile(dist / f"{name}.zip", "w", zipfile.ZIP_DEFLATED) as archive:
            for name, source in inputs.items():
                archive.write(source, name)
    else:
        with tarfile.open(dist / f"{name}.tar.gz", "w:gz") as archive:
            for name, source in inputs.items():
                archive.add(source, arcname=name)


if __name__ == "__main__":
    main()

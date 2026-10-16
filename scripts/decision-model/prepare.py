#!/usr/bin/env python3
"""Install the pinned Laya multilingual checkpoint and official exporter."""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import shutil
import urllib.request


def checksum(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def install(root, name, entry):
    path = root / name
    if path.exists() and checksum(path) == entry["sha256"]:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    partial = path.with_name(f"{path.name}.{os.getpid()}.partial")
    try:
        print(f"Downloading {name}", flush=True)
        with urllib.request.urlopen(entry["url"], timeout=120) as source, partial.open("wb") as target:
            shutil.copyfileobj(source, target, 1024 * 1024)
        if checksum(partial) != entry["sha256"]:
            raise ValueError(f"Checksum mismatch: {name}")
        partial.replace(path)
    finally:
        partial.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=Path(".tools/decision-build"))
    args = parser.parse_args()
    manifest = json.loads(Path(__file__).with_name("artifacts.json").read_text())
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        list(pool.map(lambda item: install(args.directory, *item), manifest["files"].items()))
    # Agent normalizes tokenizer metadata in place. Give it a working copy of
    # that small config, retaining the verified original download unchanged.
    runtime = args.directory / "runtime"
    for source in (args.directory / "model").rglob("*"):
        if not source.is_file():
            continue
        relative = source.relative_to(args.directory / "model")
        destination = runtime / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        if relative.as_posix() == "tokenizer/tokenizer_config.json":
            if destination.is_symlink():
                raise ValueError(f"Expected a working config file: {destination}")
            shutil.copyfile(source, destination)
        elif destination.is_symlink():
            if destination.resolve() != source.resolve():
                raise ValueError(f"Unexpected model link: {destination}")
        elif not destination.exists():
            destination.symlink_to(source.resolve())
        else:
            raise ValueError(f"Expected a model link: {destination}")
    print(args.directory.resolve())


if __name__ == "__main__":
    main()

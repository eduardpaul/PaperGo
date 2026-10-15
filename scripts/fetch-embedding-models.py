#!/usr/bin/env python3
"""Install pinned text-only Q4 exports. No Python dependencies or auth required."""
import argparse
import hashlib
import os
from pathlib import Path
import sys
import urllib.request

MODELS = {
    "embeddinggemma-2": {
        "repo": "onnx-community/embeddinggemma-2-ONNX",
        "revision": "daa72c51243991dfcaf9f9137d2c573d8f7790c0",
        "files": {
            "tokenizer.json": "4d777ef5bdc1aa36227abdfb77c3e49e7b9c892d16e1b6bda41c393504828be4",
            "onnx/model_q4.onnx": "f9eeba97acddf139b8ee2ddf04bc30dceafa88de93fadf74d7644e0d61a477a9",
            "onnx/model_q4.onnx_data": "c3975f2d1ab7a1878ae31a7d7a9b7804a827aff3800b60dfceafce21cac3df49",
        },
    },
    "harrier-oss-v1-0.6b": {
        "repo": "onnx-community/harrier-oss-v1-0.6b-ONNX",
        "revision": "e4daffa011e666dcd2ff2a3c6c05084090ac314d",
        "files": {
            "tokenizer.json": "2320ebaed2032c36f5c539cb8531d0642a10024e164f1d81ebbe163b259e3ba5",
            "onnx/model_q4.onnx": "4a41218d088c2e31fd6a3953d838d08536a27e98cf987796104a9511f9d9a671",
            "onnx/model_q4.onnx_data": "5dee6d2aecd2657464eb80e833ce79593a94430c3abca3d0c1f5424f0713e59e",
        },
    },
}


def checksum(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def install(name, root):
    spec = MODELS[name]
    target = root / name
    for filename, expected in spec["files"].items():
        path = target / filename
        if path.exists() and checksum(path) == expected:
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        partial = path.with_name(path.name + f".{os.getpid()}.partial")
        try:
            url = f'https://huggingface.co/{spec["repo"]}/resolve/{spec["revision"]}/{filename}'
            print(f"Downloading {name}/{filename}", file=sys.stderr)
            with urllib.request.urlopen(url, timeout=120) as source, partial.open("wb") as out:
                while block := source.read(1024 * 1024):
                    out.write(block)
            if checksum(partial) != expected:
                raise ValueError(f"Checksum mismatch: {filename}")
            partial.replace(path)
        finally:
            partial.unlink(missing_ok=True)
    return target.resolve()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("model", choices=[*MODELS, "all"])
    parser.add_argument("--directory", type=Path, default=Path(".tools/embedding/models"))
    args = parser.parse_args()
    for name in MODELS if args.model == "all" else [args.model]:
        print(install(name, args.directory))

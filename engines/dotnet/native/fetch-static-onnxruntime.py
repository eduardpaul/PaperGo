#!/usr/bin/env python3
"""Downloads a static ONNX Runtime 1.23.2 for linux-x64 (experimental).

The archive is the CPU build the Rust `ort` crate (pyke, ort-sys
2.0.0-rc.11, `download-binaries`) links: one libonnxruntime.a. Its URL and
SHA-256 come from ort-sys 2.0.0-rc.11's dist.txt. Publish with

  dotnet publish src/PaperGo.Engine -c Release -r linux-x64 -p:CppCompilerAndLinker=gcc \\
    -p:OnnxRuntimeStatic=DIR/libonnxruntime.a -o out-static

Usage: python3 -I fetch-static-onnxruntime.py DIR
"""

import hashlib
import io
import lzma
import os
import sys
import tarfile
import urllib.request

URL = "https://cdn.pyke.io/0/pyke:ort-rs/ms@1.23.2/x86_64-unknown-linux-gnu.tar.lzma2"
SHA256 = "8c57d059aaaee407812a5698d6706c79e090ad69e1a14204309e802dcbbaa35f"


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    out = os.path.abspath(sys.argv[1])
    os.makedirs(out, exist_ok=True)
    req = urllib.request.Request(URL, headers={"User-Agent": "papergo-engine"})
    data = urllib.request.urlopen(req, timeout=300).read()
    if hashlib.sha256(data).hexdigest() != SHA256:
        sys.exit("checksum mismatch")
    # A raw LZMA2 stream, without an xz container.
    tar = lzma.decompress(data, format=lzma.FORMAT_RAW, filters=[{"id": lzma.FILTER_LZMA2, "dict_size": 1 << 26}])
    with tarfile.open(fileobj=io.BytesIO(tar)) as archive:
        member = archive.getmember("libonnxruntime.a")
        with archive.extractfile(member) as src, open(os.path.join(out, "libonnxruntime.a"), "wb") as dst:
            dst.write(src.read())
    print(os.path.join(out, "libonnxruntime.a"))


if __name__ == "__main__":
    main()

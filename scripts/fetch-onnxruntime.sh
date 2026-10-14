#!/usr/bin/env bash
# Downloads the pinned ONNX Runtime release the PaddleOCR engine loads, checks
# its SHA-256 and prints the shared library's path, for ONNXRUNTIME_LIB.
# Usage: scripts/fetch-onnxruntime.sh [directory]   (default: .tools/onnxruntime)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

version=1.23.2
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) arch=x64 sha256=1fa4dcaef22f6f7d5cd81b28c2800414350c10116f5fdd46a2160082551c5f9b ;;
  Linux-aarch64 | Linux-arm64) arch=aarch64 sha256=7c63c73560ed76b1fac6cff8204ffe34fe180e70d6582b5332ec094810241e5c ;;
  *) echo "no pinned ONNX Runtime for $(uname -s)-$(uname -m); install $version and set ONNXRUNTIME_LIB" >&2; exit 1 ;;
esac
name="onnxruntime-linux-$arch-$version"
dir="${1:-.tools/onnxruntime}"
library="$dir/$name/lib/libonnxruntime.so"
if [[ ! -f "$library" ]]; then
  mkdir -p "$dir"
  archive="$dir/$name.tgz"
  curl -fsSL --retry 3 -o "$archive" "https://github.com/microsoft/onnxruntime/releases/download/v$version/$name.tgz"
  echo "$sha256  $archive" | sha256sum -c --quiet -
  tar -xzf "$archive" -C "$dir"
  rm "$archive"
fi
echo "$(cd "$(dirname "$library")" && pwd)/libonnxruntime.so"

#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

python3 scripts/native.py
source .tools/native/env.sh

npm ci --prefix web
npm --prefix web run build
mkdir -p bin
python3 scripts/build-binaries.py
python3 scripts/build-assets.py

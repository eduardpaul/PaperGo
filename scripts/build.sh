#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

npm ci --prefix web
npm --prefix web run build
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/papergo ./cmd/api

#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ ! -f .cache/development.env ]]; then
    printf 'Run bash scripts/setup-dev.sh first.\n' >&2
    exit 1
fi
set -a
source .cache/development.env
set +a
npm --prefix web run build
mkdir -p bin
go build -o bin/papergo ./cmd/api
printf 'Development bearer token: %s\n' "$DEV_TOKEN"
exec ./bin/papergo

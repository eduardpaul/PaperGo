#!/usr/bin/env bash
# Build and run the API for development on data/papergo.db, or with
# --postgres on the papergo_dev database of scripts/postgres.sh, whose files,
# search index and integration key live in data/postgres. Start over on
# PostgreSQL with bash scripts/postgres.sh drop papergo_dev and removing
# data/postgres.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

postgres=false
case "${1:-}" in
"") ;;
--postgres) postgres=true ;;
*)
    printf 'usage: dev.sh [--postgres]\n' >&2
    exit 2
    ;;
esac

python3 scripts/native.py
source "${PAPERGO_NATIVE_DIR:-.tools/native}/env.sh"

if [[ ! -f .cache/development.env ]]; then
    printf 'Run bash scripts/setup-dev.sh first.\n' >&2
    exit 1
fi
set -a
source .cache/development.env
set +a
if [[ "$postgres" == true ]]; then
    DATABASE_URL=$(bash scripts/postgres.sh url papergo_dev)
    mkdir -p data/postgres/blobs
    export DATABASE_URL DATABASE_PATH=data/postgres/papergo.db BLOB_PATH=data/postgres/blobs
    printf 'Database: %s\n' "${DATABASE_URL#*@}"
fi
npm --prefix web run build
mkdir -p bin
go build -o bin/papergo ./cmd/api
printf 'Development bearer token: %s\n' "$DEV_TOKEN"
exec ./bin/papergo

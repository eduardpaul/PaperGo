#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

python3 scripts/native.py
source "${PAPERGO_NATIVE_DIR:-.tools/native}/env.sh"

go mod download
npm ci --prefix web
npm --prefix web run build
mkdir -p data/blobs .cache
go run ./cmd/admin initialize --database data/papergo.db

# Keep the same private, random token across container rebuilds and API restarts.
if [[ ! -e .cache/development.env ]]; then
    (umask 077; python3 - <<'PY' > .cache/development.env
import secrets
print("APP_ENV=development")
print("AUTH_MODE=development")
print("DEV_SUBJECT=local-admin")
print(f"DEV_TOKEN={secrets.token_hex(32)}")
PY
    )
fi
printf '\nReady. Run bash scripts/dev.sh and npm --prefix web run dev -- --host 0.0.0.0 --strictPort in separate terminals.\n'

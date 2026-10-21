#!/usr/bin/env bash
# Regenerate both drivers' Bob models from the DAL schema masters after a
# schema change. PostgreSQL models are read from a temporary database that
# holds internal/dal/schema/postgres.sql, on the server PAPERGO_TEST_POSTGRES_DSN
# names or else the one of scripts/postgres.sh; psql must be installed, as it
# is in the toolchain image. Delete models of removed tables in the same
# change.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

go tool bobgen-sql -c internal/dal/driver/sqlite/bobgen.yaml

admin=${PAPERGO_TEST_POSTGRES_DSN:-$(bash scripts/postgres.sh dsn)}
database=papergo_bobgen_$$_$RANDOM
psql "$admin" -v ON_ERROR_STOP=1 -q -c "CREATE DATABASE $database"
trap 'psql "$admin" -q -c "DROP DATABASE IF EXISTS $database WITH (FORCE)"' EXIT
dsn=$(python3 -c 'import sys, urllib.parse as u; p = u.urlsplit(sys.argv[1]); print(u.urlunsplit(p._replace(path="/" + sys.argv[2])))' "$admin" "$database")
psql "$dsn" -v ON_ERROR_STOP=1 -q -f internal/dal/schema/postgres.sql
PSQL_DSN=$dsn go tool bobgen-psql -c internal/dal/driver/postgres/bobgen.yaml

if git rev-parse --git-dir >/dev/null 2>&1; then
    git status --short -- internal/dal/driver/sqlite/models internal/dal/driver/postgres/models
fi

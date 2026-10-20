#!/usr/bin/env bash
# Regenerate both drivers' Bob models from the DAL schema masters after a
# schema change. PostgreSQL models are generated in a temporary container,
# so Docker must be running. Delete models of removed tables in the same
# change.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

export TESTCONTAINERS_RYUK_DISABLED=true
go tool bobgen-sql -c internal/dal/driver/sqlite/bobgen.yaml
go tool bobgen-sql -c internal/dal/driver/postgres/bobgen.yaml
git status --short -- internal/dal/driver/sqlite/models internal/dal/driver/postgres/models

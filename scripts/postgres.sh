#!/usr/bin/env bash
# A disposable PostgreSQL 17 server in Docker for running PaperGo and its
# test suites on PostgreSQL. Its data lives in the container; `remove`
# deletes it.
#
#   start           start the server (created on first use) and wait for it
#   stop            stop the server, keeping its data
#   remove          delete the server and its data
#   status          print whether the server runs
#   dsn             print the URL of the server's administrative database,
#                   which test suites create their databases from
#   url [DATABASE]  create DATABASE (default papergo_dev) when missing and
#                   print its URL, for DATABASE_URL
#   drop DATABASE   delete a database, for a fresh start of the API
#   psql [DATABASE] open psql on a database (default papergo_dev)
#   clean           drop test databases interrupted runs left behind; run it
#                   while no tests use the server
#
# PAPERGO_POSTGRES_CONTAINER and PAPERGO_POSTGRES_PORT choose the container
# name (papergo-postgres) and the local port (55432).
set -euo pipefail

name=${PAPERGO_POSTGRES_CONTAINER:-papergo-postgres}
port=${PAPERGO_POSTGRES_PORT:-55432}
user=papergo
password=papergo-dev
image=postgres:17-alpine

url() { printf 'postgres://%s:%s@localhost:%s/%s?sslmode=disable\n' "$user" "$password" "$port" "$1"; }
sql() { docker exec -i "$name" psql -v ON_ERROR_STOP=1 -U "$user" -d papergo -Atq "$@"; }
running() { [[ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" == true ]]; }
valid_name() {
    if [[ ! "$1" =~ ^[a-z_][a-z0-9_]{0,62}$ ]]; then
        printf 'Database names use lowercase letters, digits and underscores: %s\n' "$1" >&2
        exit 2
    fi
}

start() {
    if ! running; then
        if docker inspect "$name" >/dev/null 2>&1; then
            docker start "$name" >/dev/null
        else
            docker run -d --name "$name" -p "127.0.0.1:$port:5432" \
                -e POSTGRES_USER="$user" -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=papergo \
                "$image" >/dev/null
        fi
    fi
    for _ in $(seq 60); do
        if docker exec "$name" pg_isready -q -U "$user" -d papergo 2>/dev/null && sql -c 'SELECT 1' >/dev/null 2>&1; then
            return
        fi
        sleep 1
    done
    printf 'PostgreSQL in container %s did not become ready\n' "$name" >&2
    exit 1
}

command=${1:-status}
case "$command" in
start)
    start
    printf 'PostgreSQL 17 is ready: %s\n' "$(url papergo)"
    ;;
stop) docker stop "$name" >/dev/null ;;
remove) docker rm -f "$name" >/dev/null ;;
status)
    if running; then printf 'running: %s\n' "$(url papergo)"; else printf 'stopped\n'; fi
    ;;
dsn)
    start
    url papergo
    ;;
url)
    database=${2:-papergo_dev}
    valid_name "$database"
    start
    if [[ -z "$(sql -c "SELECT 1 FROM pg_database WHERE datname = '$database'")" ]]; then
        sql -c "CREATE DATABASE $database"
    fi
    url "$database"
    ;;
drop)
    database=${2:?usage: postgres.sh drop DATABASE}
    valid_name "$database"
    start
    sql -c "DROP DATABASE IF EXISTS $database WITH (FORCE)"
    ;;
psql)
    database=${2:-papergo_dev}
    valid_name "$database"
    start
    exec docker exec -it "$name" psql -U "$user" -d "$database"
    ;;
clean)
    start
    count=0
    while read -r database; do
        [[ -n "$database" ]] || continue
        sql -c "DROP DATABASE IF EXISTS \"$database\" WITH (FORCE)"
        count=$((count + 1))
    done < <(sql -c "SELECT datname FROM pg_database WHERE starts_with(datname, 'papergo_fixture_')")
    printf 'Dropped %d test databases\n' "$count"
    ;;
*)
    sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 2
    ;;
esac

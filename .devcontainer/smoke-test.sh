#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
source .tools/native/env.sh

printf 'Checking non-root execution, cache permissions and development tools...\n'
[[ $(id -u) != 0 ]]
for directory in "$GOMODCACHE" "$GOCACHE" web/node_modules; do
    if [[ ! -w $directory ]]; then
        printf 'Missing or unwritable development directory: %s\n' "$directory" >&2
        exit 1
    fi
done
test -s web/dist/index.html
test "$(stat -c %a .cache/development.env)" = 600
go version
node --version
atlas version
gopls version
dlv version
staticcheck -version
govulncheck -version
gcc --version | head -n 1

smoke_dir=$(mktemp -d)
api_pid=
vite_pid=
cleanup() {
    local status=$?
    trap - EXIT
    if [[ $status != 0 ]]; then
        cat "$smoke_dir"/*.log >&2 || true
    fi
    if [[ -n $vite_pid ]]; then kill "$vite_pid" 2>/dev/null || true; fi
    if [[ -n $api_pid ]]; then kill "$api_pid" 2>/dev/null || true; fi
    wait "$vite_pid" "$api_pid" 2>/dev/null || true
    rm -rf "$smoke_dir"
    exit "$status"
}
trap cleanup EXIT

atlas migrate validate --env local
atlas migrate apply --env local --var "database_url=sqlite://$smoke_dir/papergo.db?_fk=1" > "$smoke_dir/atlas.log"
go build -o "$smoke_dir/papergo" ./cmd/api
smoke_token=$(python3 -c 'import secrets; print(secrets.token_hex(32))')

# Running the real API under Delve also exercises the container's ptrace settings.
APP_ENV=development AUTH_MODE=development DEV_TOKEN="$smoke_token" \
    HTTP_ADDR=127.0.0.1:18080 DATABASE_PATH="$smoke_dir/papergo.db" BLOB_PATH="$smoke_dir/blobs" \
    dlv exec "$smoke_dir/papergo" --headless --listen=127.0.0.1:14040 \
        --api-version=2 --accept-multiclient --continue > "$smoke_dir/api.log" 2>&1 &
api_pid=$!
PAPERGO_API=http://127.0.0.1:18080 \
    node web/node_modules/vite/bin/vite.js --config web/vite.config.ts \
        --host 127.0.0.1 --port 15173 --strictPort web > "$smoke_dir/vite.log" 2>&1 &
vite_pid=$!

wait_for_url() {
    for ((attempt=0; attempt<60; attempt++)); do
        if curl --fail --silent "$1" > /dev/null; then return; fi
        sleep 1
    done
    printf 'Timed out waiting for %s\n' "$1" >&2
    return 1
}
wait_for_url http://127.0.0.1:18080/health/ready
wait_for_url http://127.0.0.1:15173/health/ready
curl --fail --silent http://127.0.0.1:18080/ > "$smoke_dir/embedded.html"
grep -q '<div id="root"' "$smoke_dir/embedded.html"
curl --fail --silent http://127.0.0.1:15173/ > "$smoke_dir/vite.html"
grep -q '/@vite/client' "$smoke_dir/vite.html"
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:15173/v1/workspaces)" = 401
curl --fail --silent http://127.0.0.1:15173/auth/config > "$smoke_dir/auth.json"
python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["mode"] == "bearer"' "$smoke_dir/auth.json"
curl --fail --silent -H "Authorization: Bearer $smoke_token" -H 'Content-Type: application/json' \
    --data '{"name":"Devcontainer smoke"}' http://127.0.0.1:15173/v1/workspaces > "$smoke_dir/workspace.json"
python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); assert r["id"] and r["name"] == "Devcontainer smoke"' "$smoke_dir/workspace.json"
curl --fail --silent -H "Authorization: Bearer $smoke_token" \
    http://127.0.0.1:15173/v1/workspaces > "$smoke_dir/workspaces.json"
python3 -c 'import json,sys; assert len(json.load(open(sys.argv[1]))["value"]) == 1' "$smoke_dir/workspaces.json"
go test -race -count=1 -timeout=5m ./internal/database ./web
printf '\nDevcontainer smoke test passed: tools, permissions, SQLite, debugger, embedded UI and Vite proxy.\n'

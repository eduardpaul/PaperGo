#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

python3 scripts/native.py
source .tools/native/env.sh
task_dir=$(mktemp -d)
container_id=

cleanup() {
  task_status=$?
  if [[ -n "$container_id" ]]; then
    if (( task_status != 0 )); then
      docker logs "$container_id" >&2 || true
    fi
    docker rm --force "$container_id" >/dev/null || true
  fi
  rm -rf "$task_dir"
  exit "$task_status"
}
trap cleanup EXIT

docker build --tag papergo:ci-smoke .
# Check both executables in the final image before database or port setup.
docker run --rm papergo:ci-smoke --version
docker run --rm --entrypoint /papergo-admin papergo:ci-smoke --version
if ! go run ./cmd/admin initialize --database "$task_dir/papergo.db" --schema migrations > "$task_dir/migrations.log" 2>&1; then
  cat "$task_dir/migrations.log" >&2
  exit 1
fi

# Use the image's default user and filesystem. Development authentication keeps
# this packaging check independent of an external OIDC provider.
task_token=papergo-ci-smoke-development-token
container_id=$(docker create \
  --publish 127.0.0.1::8080 \
  --env APP_ENV=test \
  --env AUTH_MODE=development \
  --env "DEV_TOKEN=$task_token" \
  papergo:ci-smoke)
# Preserve ownership so the image's UID 65532 can write the migrated database.
tar --owner=65532 --group=65532 -C "$task_dir" -cf - papergo.db \
  | docker cp --archive - "$container_id:/data"
docker start "$container_id" >/dev/null
task_address="http://$(docker port "$container_id" 8080/tcp)"

task_ready=false
for (( attempt=0; attempt<120; attempt++ )); do
  if curl --fail --silent --max-time 2 "$task_address/health/ready" > "$task_dir/ready.json"; then
    task_ready=true
    break
  fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "$container_id")" != true ]]; then
    break
  fi
  sleep 1
done
if [[ "$task_ready" != true ]]; then
  echo 'Container did not become ready.' >&2
  exit 1
fi

curl --fail --silent --show-error --max-time 5 "$task_address/health/live" > "$task_dir/live.json"
curl --fail --silent --show-error --max-time 5 "$task_address/" > "$task_dir/index.html"
curl --fail --silent --show-error --max-time 5 "$task_address/admin/users" > "$task_dir/route.html"
cmp "$task_dir/index.html" "$task_dir/route.html"
task_asset=$(sed -n 's/.*src="\(\/assets\/[^" ]*\.js\)".*/\1/p' "$task_dir/index.html" | head -n 1)
if [[ -z "$task_asset" ]]; then
  echo 'Embedded frontend did not include an entry chunk.' >&2
  exit 1
fi
curl --fail --silent --show-error --max-time 5 "$task_address$task_asset" > "$task_dir/app.js"
task_api_status=$(curl --silent --show-error --max-time 5 --output "$task_dir/unauthorized.json" --write-out '%{http_code}' "$task_address/v1/workspaces")
if [[ "$task_api_status" != 401 ]]; then
  echo "Expected unauthenticated API to return 401, got $task_api_status." >&2
  exit 1
fi
task_status=$(curl --silent --show-error --max-time 10 \
  --output "$task_dir/workspace.json" --write-out '%{http_code}' \
  --header "Authorization: Bearer $task_token" \
  --header 'Content-Type: application/json' \
  --data '{"name":"Docker smoke"}' \
  "$task_address/v1/workspaces")
if [[ "$task_status" != 201 ]]; then
  cat "$task_dir/workspace.json" >&2
  echo "Expected workspace creation to return 201, got $task_status." >&2
  exit 1
fi

# OCR: an image upload runs the image optimization built-in, whose text
# detection loads the image's ONNX Runtime and models.
json() { python3 -c 'import json, sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
api() { curl --fail --silent --show-error --max-time 30 --header "Authorization: Bearer $task_token" "$@"; }
workspace_id=$(json id < "$task_dir/workspace.json")
library_id=$(api --header 'Content-Type: application/json' --data '{"kind":"library","name":"Scans"}' \
  "$task_address/v1/resources/$workspace_id/children" | json id)
api --request PUT --header 'Content-Type: application/json' --data "{\"collection_id\":\"$library_id\",\"enabled\":true}" \
  "$task_address/v1/workspaces/$workspace_id/workflow-builtins/documents.optimize" > /dev/null
python3 - "$task_dir/page.png" <<'PNG'
import struct, sys, zlib
w, h = 800, 600
rows = b"".join(b"\x00" + b"\xff" * w for _ in range(h))
def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 0, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")
open(sys.argv[1], "wb").write(png)
PNG
api --form "file=@$task_dir/page.png;type=image/png" "$task_address/v1/resources/$library_id/documents" > /dev/null
run_status=
for (( attempt=0; attempt<60; attempt++ )); do
  api "$task_address/v1/workspaces/$workspace_id/workflow-runs" > "$task_dir/runs.json"
  run_status=$(python3 -c 'import json, sys; runs = json.load(open(sys.argv[1])).get("value") or [{}]; print(runs[0].get("status", ""))' "$task_dir/runs.json")
  if [[ "$run_status" == completed || "$run_status" == failed ]]; then
    break
  fi
  sleep 1
done
if [[ "$run_status" != completed ]]; then
  cat "$task_dir/runs.json" >&2
  echo "Expected the optimization run on a blank page to complete (OCR finds no text), got '$run_status'." >&2
  exit 1
fi
echo 'Docker embedded frontend, readiness, liveness, API authentication, database write and OCR passed.'

# Load and write through zvec in the final runtime image as well. Reuse the
# initialized main database; the native index itself must be built at startup.
docker rm --force "$container_id" >/dev/null
container_id=$(docker create --memory=512m --publish 127.0.0.1::8080 --env APP_ENV=test --env AUTH_MODE=development \
  --env "DEV_TOKEN=$task_token" --env SEARCH_STORE=zvec papergo:ci-smoke)
tar --owner=65532 --group=65532 -C "$task_dir" -cf - papergo.db | docker cp --archive - "$container_id:/data"
docker start "$container_id" >/dev/null
task_address="http://$(docker port "$container_id" 8080/tcp)"
task_ready=false
for (( attempt=0; attempt<120; attempt++ )); do
  if curl --fail --silent --max-time 2 "$task_address/health/ready" > /dev/null; then task_ready=true; break; fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "$container_id")" != true ]]; then break; fi
  sleep 1
done
if [[ "$task_ready" != true ]]; then echo 'zvec container did not become ready.' >&2; exit 1; fi
api "$task_address/v1/search/status" > "$task_dir/zvec-status.json"
python3 - "$task_dir/zvec-status.json" <<'PY'
import json, sys
status = json.load(open(sys.argv[1]))
assert status['store'] == 'zvec', status
PY
native_workspace=$(api --header 'Content-Type: application/json' --data '{"name":"Native search"}' "$task_address/v1/workspaces" | json id)
native_list=$(api --header 'Content-Type: application/json' --data '{"kind":"list","name":"Native items"}' "$task_address/v1/resources/$native_workspace/children" | json id)
api --header 'Content-Type: application/json' --data '{"kind":"item","name":"Copper native invoice"}' "$task_address/v1/resources/$native_list/children" > /dev/null
native_found=false
for (( attempt=0; attempt<120; attempt++ )); do
  api "$task_address/v1/search?q=copper" > "$task_dir/zvec-search.json"
  if python3 - "$task_dir/zvec-search.json" <<'PY'
import json, sys
hits = json.load(open(sys.argv[1])).get('value') or []
sys.exit(0 if any(hit['name'] == 'Copper native invoice' for hit in hits) else 1)
PY
  then native_found=true; break; fi
  sleep 1
done
if [[ "$native_found" != true ]]; then cat "$task_dir/zvec-search.json" >&2; exit 1; fi
echo 'Docker zvec runtime loading, indexing and full-text query passed under a 512 MiB memory limit.'

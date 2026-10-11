#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
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
if ! atlas migrate apply --dir file://migrations --url "sqlite://$task_dir/papergo.db?_fk=1" > "$task_dir/migrations.log" 2>&1; then
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
for (( attempt=0; attempt<30; attempt++ )); do
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
echo 'Docker readiness, liveness, and authenticated database write passed.'

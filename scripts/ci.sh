#!/usr/bin/env bash
# Run checks exactly as CI does: in the pinned toolchain image, on a clean
# copy of this checkout (tracked and untracked files, without ignored ones),
# with PostgreSQL 17 beside it. Needs only Docker, Python 3 and Git.
#
#   scripts/ci.sh                    the pull request gate: lint, then test
#   scripts/ci.sh STAGE [OPTIONS]    one stage of scripts/check.py, such as
#                                    test --only go --shard 1/2 or race
#   scripts/ci.sh image              print the toolchain image, building it
#                                    when missing
#   scripts/ci.sh tag                print the toolchain image's tag
#   scripts/ci.sh clean              remove the database, network and caches
#
# The image is tagged with a hash of its inputs, the same tag CI publishes
# to GHCR; it is pulled from there when available, otherwise built. Go, npm
# and frontend outputs persist in Docker volumes between runs. The native,
# release and docker stages first prepare the model presets in .tools on
# this machine, which needs Docker; docker runs on the host.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

registry=ghcr.io/eduardpaul/papergo-ci
network=papergo-ci
database=papergo-ci-postgres
dsn="postgres://papergo:papergo@$database:5432/papergo?sslmode=disable"
volumes=(papergo-ci-go-mod:/home/vscode/go/pkg/mod papergo-ci-cache:/home/vscode/.cache
         papergo-ci-web-modules:/work/web/node_modules papergo-ci-sdk-modules:/work/sdk/node_modules
         papergo-ci-web-dist:/work/web/dist papergo-ci-checks:/work/.cache/checks)

# The tag CI computes the same way: a hash of every toolchain image input.
tag() {
    find Dockerfile scripts/native.py scripts/native.json scripts/native-notices scripts/sqlite3def.py -type f -print0 \
        | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | cut -c1-16
}

image() {
    local local_tag
    local_tag=papergo-ci:$(tag)
    if ! docker image inspect "$local_tag" >/dev/null 2>&1; then
        if docker pull --quiet "$registry:$(tag)" >/dev/null 2>&1; then
            docker tag "$registry:$(tag)" "$local_tag"
        else
            printf 'Building the toolchain image %s...\n' "$local_tag" >&2
            docker build --target development --tag "$local_tag" . >&2
        fi
    fi
    printf '%s\n' "$local_tag"
}

start_database() {
    docker network inspect "$network" >/dev/null 2>&1 || docker network create "$network" >/dev/null
    if [[ $(docker inspect --format '{{.State.Running}}' "$database" 2>/dev/null) != true ]]; then
        docker rm --force "$database" >/dev/null 2>&1 || true
        # Disposable: test databases live in memory.
        docker run --detach --name "$database" --network "$network" --tmpfs /var/lib/postgresql/data \
            --env POSTGRES_USER=papergo --env POSTGRES_PASSWORD=papergo --env POSTGRES_DB=papergo \
            postgres:17-alpine >/dev/null
    fi
    for _ in $(seq 60); do
        docker exec "$database" pg_isready --quiet --username papergo --dbname papergo && return
        sleep 1
    done
    printf 'PostgreSQL did not become ready\n' >&2
    exit 1
}

# Runs check.py with each argument list in turn, in one container.
check() {
    local image_tag mounts=() arguments=()
    image_tag=$(image)
    start_database
    for volume in "${volumes[@]}"; do mounts+=(--volume "$volume"); done
    for directory in .tools/decision/model .tools/embedding/models; do
        if [[ -d $directory ]]; then mounts+=(--volume "$PWD/$directory:/work/$directory:ro"); fi
    done
    for run in "$@"; do arguments+=("python3 scripts/check.py $run --setup"); done
    git ls-files -z --cached --others --exclude-standard | tar --create --null --files-from - \
        | docker run --rm --interactive --network "$network" --env PAPERGO_TEST_POSTGRES_DSN="$dsn" \
            "${mounts[@]}" --workdir /work "$image_tag" \
            bash -c "set -euo pipefail; tar --extract --directory /work; $(IFS=';'; printf '%s' "${arguments[*]}")"
}

stage=${1:-}
case "$stage" in
"")
    check lint test
    ;;
image)
    image
    ;;
tag)
    tag
    ;;
clean)
    docker rm --force "$database" >/dev/null 2>&1 || true
    docker network rm "$network" >/dev/null 2>&1 || true
    for volume in "${volumes[@]}"; do docker volume rm "${volume%%:*}" >/dev/null 2>&1 || true; done
    ;;
native | release | docker)
    python3 scripts/prepare-preset-models.py
    if [[ $stage == docker ]]; then
        exec python3 scripts/check.py "$@"
    fi
    check "$(printf '%q ' "$@")"
    ;;
*)
    check "$(printf '%q ' "$@")"
    ;;
esac

#!/usr/bin/env bash
# Rehearse a release on this machine as CI does: generate the embedded
# database history, build and package the executables and shared assets,
# and run the packaged executables. --image also builds the container image
# and runs its smoke test. Needs the model presets in .tools, sqlite3def
# (scripts/sqlite3def.sh) and, for --image, Docker.
#
# Packages land in dist/ and executables in bin/. The release history is a
# build input only: the source placeholder is restored afterwards.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

image=false
version=v0.0.0-rc.1
for argument in "$@"; do
    case "$argument" in
    --image) image=true ;;
    --version=*) version=${argument#--version=} ;;
    *)
        printf 'usage: release-rehearsal.sh [--image] [--version=vX.Y.Z-rc.N]\n' >&2
        exit 2
        ;;
    esac
done

python3 scripts/native.py
source .tools/native/env.sh
if [[ -x .tools/bin/sqlite3def ]]; then
    export SQLITE3DEF=${SQLITE3DEF:-$PWD/.tools/bin/sqlite3def}
fi
# Extracted packages and assets take several GiB; keep them off a small /tmp.
mkdir -p .cache/tmp
export TMPDIR=${TMPDIR:-$PWD/.cache/tmp}

bundle=internal/dal/migrations/release.json
placeholder=$(cat "$bundle")
trap 'printf "%s\n" "$placeholder" > "$bundle"' EXIT

npm --prefix web run build
go run ./cmd/release --version "$version"
python3 scripts/build-binaries.py --ldflags="-s -w -X papergo/internal/buildinfo.Version=$version"
python3 scripts/build-assets.py --archive "dist/papergo_${version}_assets.zip"
rm -f dist/papergo_*_"$(go env GOOS)"_"$(go env GOARCH)".zip
python3 scripts/release-package.py --version "$version" --os "$(go env GOOS)" --arch "$(go env GOARCH)"
python3 scripts/release-smoke.py --assets "dist/papergo_${version}_assets.zip"

if [[ "$image" == true ]]; then
    rm -rf .cache/container-assets
    python3 scripts/build-assets.py --output-dir .cache/container-assets/assets
    docker build --build-context model-assets=./.cache/container-assets --tag papergo:rehearsal .
    python3 scripts/docker-smoke.py --image papergo:rehearsal
fi

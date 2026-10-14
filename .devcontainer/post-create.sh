#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Volumes are mounted after image creation and can initially belong to root.
for directory in /home/vscode/.cache "$GOMODCACHE" "$PWD/web/node_modules"; do
    if [[ $(stat -c %u "$directory") != "$(id -u)" ]]; then
        sudo chown -R "$(id -u):$(id -g)" "$directory"
    fi
done
mkdir -p "$GOCACHE" "$npm_config_cache"
bash scripts/setup-dev.sh

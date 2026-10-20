#!/usr/bin/env bash
# Install the pinned sqlite3def release planner into .tools/bin, where
# scripts/check.py finds it. The devcontainer already provides it.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

version=3.11.26
case "$(uname -s)/$(uname -m)" in
Linux/x86_64) archive=sqlite3def_linux_amd64.tar.gz checksum=7ef1cadbbaca6f12543c00edd3abe6b607ec69f30239517c47e36f86b580e378 ;;
Linux/aarch64 | Linux/arm64) archive=sqlite3def_linux_arm64.tar.gz checksum=40c75c6ddb38cbf080bd6766f1e39afa9e6b08785a55fbcf20207e6622bb1873 ;;
*)
    printf 'Install sqlite3def %s on PATH or set SQLITE3DEF; this script supports Linux amd64 and arm64.\n' "$version" >&2
    exit 1
    ;;
esac
if [[ -x .tools/bin/sqlite3def ]] && .tools/bin/sqlite3def --version | grep -q "^$version "; then
    printf 'sqlite3def %s is installed in .tools/bin\n' "$version"
    exit 0
fi
download=$(mktemp -d)
trap 'rm -rf "$download"' EXIT
curl -fsSL --retry 3 "https://github.com/sqldef/sqldef/releases/download/v$version/$archive" -o "$download/$archive"
echo "$checksum  $download/$archive" | sha256sum --check --quiet
tar -xzf "$download/$archive" -C "$download" sqlite3def
mkdir -p .tools/bin
install -m 0755 "$download/sqlite3def" .tools/bin/sqlite3def
.tools/bin/sqlite3def --version

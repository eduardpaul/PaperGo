#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

python3 scripts/native.py
source .tools/native/env.sh

npm ci --prefix web
npm --prefix web run build
npm --prefix web run lint
npm --prefix web test -- --maxWorkers=2
python3 -m unittest discover -s scripts/tests -v
python3 -m unittest discover -s samples/npm -p 'test_*.py' -v
python3 -m unittest discover -s samples/authentik -p 'test_*.py' -v
go mod verify
ent_snapshot() {
    python3 - <<'PY'
import hashlib
from pathlib import Path

paths = [Path("go.mod"), Path("go.sum"), *sorted(Path("ent").rglob("*.go"))]
for path in paths:
    print(path, hashlib.sha256(path.read_bytes()).hexdigest())
PY
}
generated_before=$(ent_snapshot)
go generate ./ent
if [[ "$generated_before" != "$(ent_snapshot)" ]]; then
    printf 'Ent generation updated files. Review the generated changes and rerun the checks.\n' >&2
    exit 1
fi
test -z "$(gofmt -l cmd internal ent/schema web/*.go)"
go vet ./...
staticcheck ./...
go test -count=1 -timeout=15m ./...
mkdir -p bin
go build -trimpath -o bin/papergo ./cmd/api

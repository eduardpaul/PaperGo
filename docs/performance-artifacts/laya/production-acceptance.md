# Production decision acceptance

The production bundle is identified by `internal/decision/manifest.json` and the
archive contract in `scripts/decision-model/bundle.json`. The offline archive was
packaged and installed locally with all file checksums verified. The archive
SHA-256 is `b7fb100758f42c1cd1bda05a5cee74df6ca68e210d067bf318af35ef5a3dd530`.
Weights are optional build/deployment artifacts; the repository retains the
manifest and measurements, not the weights.

The production engine replays all 475 retained reference questions with exact
Go token IDs, markers and question types, matching winners, probability tolerance
0.0001, decision-logit tolerance 0.001 and relative action-logit tolerance 0.00001.
[Recomputed threshold sets](production-quality.json) match the retained Python
selections at every fixture threshold (0.5, 0.6, 0.7, 0.8, 0.9).

Eight additional official-Python questions cover ordinal score, noul, mixed
option counts with false mask entries, multilingual structured JSON and
context-plus-document serialization. Batch results are checked against
individual execution. Strict-source/overflow tests, missing/corrupt bundle
loading, active/waiting cancellation, recovery, idle unload/reload and close are
also exercised.

[Resource measurements](production-resources.json) record one/eight/fifty short
rows, a maximum-length 1,024-token row and combined resident
PaddleOCR/EmbeddingGemma-2/Laya execution. One-row short timing includes cold
loading. RSS values are process observations on the recorded host, not a bound
for all deployments.

Workflow tests cover item-free routing; exact JSON numbers and token payloads;
workspace cache isolation and competing writers; threshold-only reuse;
complete taxonomy membership, hierarchy eligibility, description overrides and
snapshot invalidation; revoked access on a cache hit; text/blob ownership;
stale sources; editable approval; rejection/no matches; metadata/file/taxonomy
write guards; and abrupt-process recovery after preparation, cache persistence
and decision commit. Native runner tests complete the supplied review sample
with an actual text PDF and a PNG recognized by PaddleOCR before Laya inference.

The SQL schema is unchanged. Domain-event JSON uses a number-preserving Go type
so persisted launch inputs cannot round before dispatch; Ent code is regenerated
and the existing single migration hash is verified.

Reproduction commands from the repository root:

```bash
source .tools/native/env.sh
export DECISION_MODELS_PATH="$PWD/.tools/decision/installed"
export EMBEDDING_MODELS_ROOT="$PWD/.tools/embedding/models"
export PAPERGO_REQUIRE_DECISION=1 PAPERGO_REQUIRE_EMBEDDING=1 PAPERGO_REQUIRE_OCR=1
go test -race -p 1 -timeout 20m ./internal/onnx ./internal/decision \
  ./internal/embedding ./internal/ocr/paddle ./internal/forms \
  ./internal/workflow ./internal/config ./cmd/decide
go test -race -p 1 -timeout 20m ./internal/dms ./internal/scriptrun ./internal/httpapi
go vet ./...
staticcheck ./...
python3 -m unittest discover -s scripts/tests -v
npm --prefix web test -- --maxWorkers=2
npm --prefix web run lint
npm --prefix web run build
```

Synthetic fixture correctness and real file-format integration do not establish
held-out production classification quality. Representative English, Spanish and
Catalan production documents, hard negatives, realistic descriptions and
dedicated held-out ordinal/context evaluation remain outstanding. The supplied
sample retains editable human approval.

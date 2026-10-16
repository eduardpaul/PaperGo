# Decision measurements

`fixtures.json` is the unchanged shared synthetic 19-document/eight-category fixture. Its contents and SHA-256 are retained for reproducible comparisons. These are experiments, not a production accuracy guarantee.

The retained [Laya references and measurements](../../docs/performance-artifacts/laya/) are replayed directly by `internal/decision` tests through the production engine. `typed-reference.py` adds independent official-Python references for score/noul/choice, false marker-mask entries, multilingual structured JSON and document-plus-context serialization:

```bash
.tools/decision-build/venv/bin/python scripts/decision-benchmark/typed-reference.py
```

Source artifacts must be installed by `scripts/decision-model/prepare.py` first. The script verifies their pinned hashes before loading. Python does not run in the application.

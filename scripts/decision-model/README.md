# Laya FP32 model bundle

The application accepts exactly the [supported manifest](../../internal/decision/manifest.json). It pins checkpoint revision, export provenance, graph/tokenizer hashes, input/output dtypes, temperatures and serialization identities. Server inference uses Go and the shared native ONNX Runtime; Python is only a model-build/reference dependency.

Standalone builds and Docker images include this bundle. `python3 scripts/prepare-preset-models.py` prepares it automatically using the `laya-model` Docker target when no verified local bundle exists. Set `PAPERGO_LAYA_BUNDLE` or pass `--laya-bundle PATH_OR_HTTPS_URL` to prepare from an existing artifact. The complete executable requires no external model files or network access. Exporting from source uses about 5.2 GiB peak RAM; allow at least 6 GiB for the build container, or supply a verified prebuilt bundle.

To export directly from the repository root with the pinned CPU environment:

```bash
python3 scripts/decision-model/prepare.py
uv venv --python 3.13 .tools/decision-build/venv
uv pip install --python .tools/decision-build/venv/bin/python torch==2.8.0 --index-url https://download.pytorch.org/whl/cpu
uv pip install --python .tools/decision-build/venv/bin/python -r scripts/decision-model/requirements.txt
uv pip install --python .tools/decision-build/venv/bin/python --no-deps .tools/decision-build/laya-0.4.8-py3-none-any.whl
.tools/decision-build/venv/bin/python scripts/decision-model/export.py
python3 scripts/decision-model/pack.py .tools/decision-build/onnx
python3 scripts/decision-model/install.py .tools/decision/laya-fp32.tar.gz
```

`artifacts.json` verifies cached source artifacts and downloads. Export verifies the generated FP32 graph against the supported hash, then includes manifest, license and attribution. Packaging creates a deterministic archive and adjacent SHA-256 file. Keep this archive as a build artifact; weights are not committed to Git or downloaded by an activity. The installer accepts a local directory/archive or an operator-supplied HTTPS artifact URL, verifies the pinned archive checksum plus every file and manifest, and stages verified files before replacing installed files. Reinstalling also verifies the supplied cached artifact. First native load and idle reload verify the installed bundle independently.

HTTPS downloads use a verified local archive cache; corrupt cached files are replaced only by a matching download. The installer does not export or substitute models, and activities have no model-download network access. Standalone build preparation exports the pinned graph when its verified cache is absent.

For a prebuilt offline bundle, only the final install command is needed. The standalone build packages its destination automatically. Direct Go runs use `AI_DECISION_MODELS_PATH` to select the installed directory. All model files must be installed before starting inference. The bundle contains about 1.2 GiB of FP32 graph weights plus its tokenizer.

Diagnostic input contains already-rendered low-level options; prefixes must not be added twice:

```bash
source .tools/native/env.sh
printf '%s\n' '{"requests":[{"state":"Invoice for coffee","question":"Which category best describes this document?","options":["Other document","Invoices and bills"],"type":"choice"}]}' |
  go run ./cmd/decide -models .tools/decision/model
```

Run the required native checks with `DECISION_MODELS_PATH` and `PAPERGO_REQUIRE_DECISION=1`. See [workflow decisions](../../docs/decisions.md) for public typed-question rendering, limits, review and deployment configuration.

# PaperGo

Download the platform ZIP and `papergo_vX.Y.Z_assets.zip` from the same release. Extract both into this directory. The shared ZIP creates `assets/` beside the executables and `config.yaml`. Run the executable from this directory, then open http://localhost:8080.

- Linux: `./papergo`
- Windows: `papergo.exe`

Sign in as `admin` with password `admin`. `config.yaml` in this directory sets that account, listens on port 8080, and stores the database, files, and zvec search index under `./data`. The first start creates the database from the current schema, and a newer release upgrades it when it starts. No separate migration program is required.

Change `admin_password` before you expose the process beyond this computer. Set `auth.mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Environment variables override the same settings.

Set `PAPERGO_CONFIG` to read a different YAML file. Non-empty environment variables take precedence over matching YAML settings. Unknown fields, duplicate keys and multiple documents are rejected. Quote passwords; an explicitly empty or null `auth.admin_password` prompts on an interactive first start and fails without a terminal. Existing passwords are unchanged on later starts. In proxy mode, `auth.proxy_trusted_cidrs` is a YAML list of CIDR strings.

Keep the native libraries and `notices/` from the platform ZIP beside the executables. Linux libraries live in `lib/`; Windows DLLs sit beside `papergo.exe`. Linux builds require the target system's glibc. Keep `assets/models/` and `assets/notices/` from the shared ZIP in the same installation. Models are identical across platforms and load directly from disk. macOS release builds are temporarily disabled.

OCR and local decisions use the shared assets automatically. Enable local semantic search with `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` (default) or `harrier-oss-v1-0.6b`. Models never download at runtime. `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` select explicit alternatives.

For the all-inclusive container image, persistent volumes, secrets, health checks and source builds, see the [Docker deployment guide](https://github.com/eduardpaul/PaperGo/blob/main/docs/docker.md).

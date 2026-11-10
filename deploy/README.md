# PaperGo

Download the platform ZIP and `papergo_vX.Y.Z_assets.zip` from the same release. Extract both into this directory. The shared ZIP creates `assets/` beside the executable and `config.yaml`. Run the executable from this directory, then open http://localhost:8080.

- Linux: `./papergo`
- Windows: `papergo.exe`

Sign in as `admin` with password `admin`. `config.yaml` in this directory sets that account, listens on port 8080, and stores the database, files, and search index under `./data`. The first start creates the database from the current schema, and a newer release upgrades it when it starts. No separate migration program is required.

Change `admin_password` before you expose the process beyond this computer. Set `auth.mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Environment variables override the same settings.

Set `PAPERGO_CONFIG` to read a different YAML file. Every setting has a YAML key and an environment variable, and non-empty environment variables take precedence over matching YAML settings; [Configuration](https://github.com/eduardpaul/PaperGo/blob/main/docs/configuration.md) lists them. Unknown fields, duplicate keys and multiple documents are rejected. Quote passwords; an explicitly empty or null `auth.admin_password` prompts on an interactive first start and fails without a terminal. Existing passwords are unchanged on later starts. In proxy mode, `auth.proxy_trusted_cidrs` is an optional YAML list of CIDR strings; without it, the proxy secret alone identifies the proxy.

Keep `LICENSE`, `NOTICE` and `notices/` from the platform ZIP beside the executable, and on Linux ONNX Runtime in `lib/`. PaperGo is licensed under the Apache License 2.0 (`LICENSE`); `notices/` holds the notices of the third-party components it ships or compiles in. Linux builds need glibc 2.39 or newer and the distribution's libstdc++ and libgcc_s (Debian 13, Ubuntu 24.04 or later, which install them by default). On Windows, `papergo.exe` is the whole program: it needs no DLLs and no Visual C++ Redistributable. The Windows build includes Microsoft Visual C++ runtime files under Microsoft's terms in `notices/microsoft-visual-cpp-runtime.txt`; using or redistributing it means accepting them. It extracts its embedded ONNX Runtime to `data/cache/onnxruntime` (beside the database) and checks it at every start; that folder is derived data and safe to delete while PaperGo is stopped. Keep `assets/models/` and `assets/notices/` from the shared ZIP in the same installation. Models are identical across platforms and load directly from disk. macOS release builds are temporarily disabled.

OCR and local decisions use the shared assets automatically. Enable local semantic search with `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` (default) or `harrier-oss-v1-0.6b`. Models never download at runtime. `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` select explicit alternatives.

`papergo admin backup --file backup.tar.gz` archives the database and files where the configuration keeps them, and `papergo admin restore` brings them back; `papergo admin` lists every administrative command.

For the all-inclusive container image, persistent volumes, secrets, health checks and source builds, see the [Docker deployment guide](https://github.com/eduardpaul/PaperGo/blob/main/docs/docker.md).

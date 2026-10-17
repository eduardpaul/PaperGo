# PaperGo

Run the executable in this directory, then open http://localhost:8080.

- Linux and macOS: `./papergo`
- Windows: `papergo.exe`

Sign in as `admin` with password `admin`. `config.yaml` in this directory sets that account, listens on port 8080, and stores the database, files, and zvec search index under `./data`. The first start creates the database from the current schema. No separate migration program is required.

Change `admin_password` before you expose the process beyond this computer. Set `auth.mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Environment variables override the same settings.

Set `PAPERGO_CONFIG` to read a different YAML file. Non-empty environment variables take precedence over matching YAML settings. Unknown fields, duplicate keys and multiple documents are rejected. Quote passwords; an explicitly empty or null `auth.admin_password` prompts on an interactive first start and fails without a terminal. Existing passwords are unchanged on later starts. In proxy mode, `auth.proxy_trusted_cidrs` is a YAML list of CIDR strings.

The executable contains its native libraries, compiler runtimes, PaddleOCR, Laya FP32, EmbeddingGemma 2 and Harrier Q4 models, and license notices. Copying the executable alone is enough to run the app. On first start it extracts the runtime into a private OS user cache under `papergo/runtime`; later starts verify and reuse it. The cache needs about 2 GiB of writable space and executable filesystem permissions. Linux uses `$XDG_CACHE_HOME` or `~/.cache`, macOS uses `~/Library/Caches`, and Windows uses `%LocalAppData%`. Native license notices are in the extracted `notices` directory. Linux builds require the target system's glibc.

OCR and local decisions use bundled models automatically. Enable local semantic search with `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` (default) or `harrier-oss-v1-0.6b`. Models never download at runtime. `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` select explicit alternatives.

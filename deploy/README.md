# PaperGo

Run the executable in this directory, then open http://localhost:8080.

- Linux and macOS: `./papergo`
- Windows: `papergo.exe`

Sign in as `admin` with password `admin`. `config.yaml` in this directory sets that account, listens on port 8080, and stores the database, files, and zvec search index under `./data`. The first start creates the database from the current schema. No separate migration program is required.

Change `admin_password` before you expose the process beyond this computer. Set `auth.mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Environment variables override the same settings.

Set `PAPERGO_CONFIG` to read a different YAML file. Non-empty environment variables take precedence over matching YAML settings. Unknown fields, duplicate keys and multiple documents are rejected. Quote passwords; an explicitly empty or null `auth.admin_password` prompts on an interactive first start and fails without a terminal. Existing passwords are unchanged on later starts. In proxy mode, `auth.proxy_trusted_cidrs` is a YAML list of CIDR strings.

Native libraries ship in `lib/` on Linux and macOS and beside `papergo.exe` on Windows. License notices are in `notices/`. Set `ONNXRUNTIME_LIB` to that ONNX Runtime library to use it. OCR stays off until `models/paddleocr` or `OCR_MODELS` is present, and a missing default model directory does not stop the server.

# PaperGo

Run the executable in this directory, then open http://localhost:8080.

- Linux and macOS: `./papergo`
- Windows: `papergo.exe`

Sign in as `admin` with password `admin`. `config.ini` in this directory sets that account, listens on port 8080, and stores the database, files, and zvec search index under `./data`. The first start creates the database from the current schema. No separate migration program is required.

Change `admin_password` before you expose the process beyond this computer. Set `[auth] mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Environment variables override the same settings.

Native libraries ship in `lib/` on Linux and macOS and beside `papergo.exe` on Windows. License notices are in `notices/`.

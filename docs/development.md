# Development environment

PaperGo's [devcontainer](../.devcontainer/devcontainer.json) provides Go 1.27.2,
Node.js 24.15.0 with npm, Python 3, SQLite's CLI, sqlite3def 3.11.26 and a C
compiler, pkg-config and CMake 3.31.10. Go matches CI and the application Docker build; Node uses the same
major version.
The image also pins gopls 0.23.0, Delve 1.27.2, the CI Staticcheck revision
and govulncheck 1.9.0. It supports Linux amd64 and arm64 through the upstream
multi-platform images. The source checkout stays on the host; dependencies and
Go/npm caches use separate Docker volumes for each checkout.

## Open the container

Install Docker and VS Code's Dev Containers extension. Open the repository root
and select **Dev Containers: Reopen in Container**. Allocate at least two CPUs
and 4 GB RAM to Docker. On Windows, keep the checkout in the WSL 2 filesystem.
For rootless Docker on Linux, use **Dev Containers: Clone Repository in Container
Volume** so the checkout belongs to the container user. Rootless Docker maps
the host user to container root, so UID adjustment alone does not let `vscode`
write a host-owned checkout. Cloning into a volume also avoids host filesystem
overhead on Windows and macOS.

The first build installs development tools. After the checkout is mounted, setup
repairs volume permissions, installs the pinned native SDKs, downloads Go modules, installs the npm lockfile,
builds `web/dist`, validates and applies the schema to `data/papergo.db`, and
creates a random development token in `.cache/development.env` with mode 0600.
The editor waits for setup because Go packages need the native SDK headers and embed `web/dist` at compile time. The Go extension and debugger use `.tools/native/native.env`; direct shell commands require `source .tools/native/env.sh` (PowerShell: `. .tools/native/env.ps1`).
Setup can be rerun with **PaperGo: setup** or `bash scripts/setup-dev.sh`, including
after dependency changes. The shell scripts also work with native Go, Node/npm,
Python 3.11+, a C compiler, CMake 3.26+ and pkg-config installations. Docker
runs the PostgreSQL server and model generation.
`.cache/development.env` is generated development
configuration; it is distinct from the optional `.env.example` file, which the
application does not load.

Tools and application processes run as `vscode`, with the Dev Containers default
UID/GID adjustment on Linux. Setup uses sudo only to repair ownership of the
three dependency/cache volumes. The container has an init process to reap
children and `SYS_PTRACE` for Go debugging. Docker's default seccomp profile is
retained. No database service is needed for SQLite. Run Docker image builds and
the optional Compose samples from the host.

## Run and debug

In one terminal, run:

```sh
bash scripts/dev.sh
```

This builds the embedded frontend and API, prints the development bearer token,
then starts the API. Open forwarded port 8080 for the embedded UI. Paste the
printed token into the development sign-in screen. The token persists across
rebuilds and restarts; delete `.cache/development.env` and rerun setup to replace it.

In a second terminal, run:

```sh
npm --prefix web run dev -- --host 0.0.0.0 --strictPort
```

Open forwarded port 5173 for frontend hot reload. Vite proxies `/v1`, `/auth` and
`/health` to the API at `127.0.0.1:8080` inside the same container. Use the forwarded
URL shown in VS Code's Ports view; if a host port is occupied, VS Code can choose
another. For a Codespaces web URL, start Vite with
`__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS="${CODESPACE_NAME}-5173.${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN}"`
before the npm command so its exact forwarded hostname is accepted. Keep
Codespaces ports private. API source changes require restarting the API command
or debugger; frontend changes are handled by Vite.

The **PaperGo: API** and **PaperGo: Vite** tasks run those commands. To debug,
stop any running API, select **PaperGo API** in Run and Debug, and press F5. The
debug configuration rebuilds the frontend and reads the native build environment plus `.cache/development.env`.
Run Vite alongside it for frontend work. Read the token file to sign in when
starting through the debugger. **PaperGo: API and Vite** starts both.

### PostgreSQL

`scripts/postgres.sh` runs a disposable PostgreSQL 17 server in Docker on
`localhost:55432`; its data lives in the container until `remove`:

```sh
bash scripts/postgres.sh start         # create or start the server and wait for it
bash scripts/postgres.sh psql          # psql on the papergo_dev database
bash scripts/postgres.sh stop          # stop it, keeping its data
bash scripts/postgres.sh remove        # delete it and its data
```

`bash scripts/dev.sh --postgres` (**PaperGo: API on PostgreSQL**) runs the API on
the `papergo_dev` database, which it creates when missing; its files, search
index and integration key live in `data/postgres`. To start over, run
`bash scripts/postgres.sh drop papergo_dev` and delete `data/postgres`. The
**PaperGo API (PostgreSQL)** debug configuration does the same under the
debugger.

## Check changes

Run **PaperGo: check** or:

```sh
bash scripts/check.sh
```

The default checks Go formatting and runs the ordinary Go suite with Go's
test-result cache enabled. For the edit/check loop, select the packages you are
working on, or check the frontend separately:

```sh
bash scripts/check.sh --packages ./internal/dms
bash scripts/check.sh --packages ./internal/dms --run '^TestSeekPagesMatchSortedEligibleRows$'
bash scripts/check.sh --web
```

`--postgres` runs the suites that open databases on PostgreSQL 17 instead of
SQLite: the DAL packages, which test both engines, and the application suites.
It uses the server `PAPERGO_TEST_POSTGRES_DSN` names, or starts the one of
`scripts/postgres.sh`. Combine it with `--packages` to choose the packages.
Tests create and drop their own databases; `bash scripts/postgres.sh clean`
drops the ones an interrupted run left behind.

```sh
bash scripts/check.sh --postgres
bash scripts/check.sh --postgres --packages ./internal/dms --run '^TestReceipts'
```

`bash scripts/release-rehearsal.sh` packages and runs the release executables as
the release workflow does: it generates the embedded database history, builds the
executables, shared assets and platform ZIP into `bin/` and `dist/`, and runs the
packaged executables. `--image` also builds the container image and runs its
smoke test. It needs the model presets in `.tools`, sqlite3def and, for the
image, Docker, and restores the release history placeholder afterwards.

Package selection is explicit: include affected callers when changing shared
code. There is no automatic reverse-dependency selection. An unscoped check runs
all Go packages. `--web` runs typechecking, lint and all frontend tests without
requiring native SDKs; for repeated frontend edits use
`npm --prefix web test -- --watch`.

Checks require installed dependencies and never download SDKs or run `npm ci`.
Run setup after dependency changes, or use `python3 scripts/native.py` and
`npm ci --prefix web` to refresh just those dependencies. Go checks build the
embedded frontend on the first check, then reuse it only while both the source
and generated asset hashes match. Frontend edits or missing/changed assets
trigger a rebuild. Reports live in `.cache/checks`: raw Go JSON events, structured
package/test timings and a Markdown summary.

Use `bash scripts/check.sh --full` for frontend build/lint/tests, Python tests,
module verification, Bob generation/drift check, Go formatting, vet, Staticcheck,
uncached ordinary Go tests and the application build. Staticcheck must be
installed, as it is in the devcontainer. `--race` runs the entire Go suite with
race detection and forced execution. `--native` requires `DECISION_MODELS_PATH`
and `EMBEDDING_MODELS_ROOT`, then runs real decision, embedding, OCR and runner
tests with race detection; missing required assets fail. The PowerShell entry
point accepts the same arguments, for example
`./scripts/check.ps1 --packages ./internal/dms`. `govulncheck ./...` runs a
dependency scan separately.

The verification workflow builds and typechecks the embedded frontend once,
then distributes the real assets to its Go jobs. Docker and devcontainer smoke
checks exercise their own builds. Frontend lint/tests and backend verification
retain their own checks. CI runs the full Go suite with race detection across
three jobs.
Each job discovers the tests, fuzz seeds and examples, then runs a distinct shard
with a 15-minute timeout per package. Raw events and timing reports are uploaded
even if test execution fails; discovery duration is recorded separately from the
execution command. Superseded branch/PR runs are canceled; release validation
retains its serialized publication policy. Run a shard locally with
`python3 scripts/test-race.py --shard 1 --shards 3`, repeating with shards 2 and 3
for the complete suite. Race instrumentation makes the pure-Go SQLite driver
substantially slower, so allow for a longer run on a development machine.

The devcontainer CI job builds the actual configuration and runs
`bash .devcontainer/smoke-test.sh` as the non-root user. The smoke test uses its
own disposable database and ports, verifies the toolchain and writable caches,
starts the real API under Delve, checks the embedded UI, Vite client and all
three proxy routes, verifies authentication and an authenticated write, and runs
SQLite and embedded-frontend tests with race detection.

To validate a host checkout with rootful Docker using the reference CLI:

```sh
npx --yes @devcontainers/cli@0.89.0 up --workspace-folder .
npx --yes @devcontainers/cli@0.89.0 exec --workspace-folder . bash .devcontainer/smoke-test.sh
```

## Schema and generated files

Edit `internal/dal/schema/sqlite.sql` and `internal/dal/schema/postgres.sql`
directly, then regenerate both drivers' Bob models with
`bash scripts/generate-models.sh` (**PaperGo: generate Bob models**); the
PostgreSQL ones are generated in a temporary container, so Docker must be
available.

Release planning uses sqlite3def 3.11.26, installed in the development container.
Outside the container, `bash scripts/sqlite3def.sh` installs the pinned executable
into `.tools/bin`, where checks find it; setup runs it when sqlite3def is missing.
The release planner tests execute the real CLI against the full PaperGo schema.
See [release history and validation](releases.md#release-only-database-history).

Stop the API or debugger before resetting local data. Local databases and blobs
are disposable: delete `data/`, recreate the directory with `mkdir -p data/blobs`,
then run `go run ./cmd/admin initialize --database data/papergo.db`. Do not add migration files or backfills.

Go/npm caches and `web/node_modules` survive container rebuilds. `npm ci` replaces
the installed dependency tree with the lockfile contents. To clear a stale build
cache, run `go clean -cache`; for module downloads use `go clean -modcache`.
Rebuild the container after editing its Dockerfile or configuration. Review Go
and Node updates alongside CI and the application Dockerfile, and update the
pinned tools explicitly.

## Design references

The configuration follows the official [Development Container specification](https://github.com/devcontainers/spec/blob/main/docs/specs/devcontainerjson-reference.md)
for lifecycle readiness, users, init and forwarded ports; VS Code's
[non-root user guidance](https://code.visualstudio.com/remote/advancedcontainers/add-nonroot-user)
for Linux bind-mount ownership; and its [disk performance guidance](https://code.visualstudio.com/remote/advancedcontainers/improve-performance)
for dependency volumes.
The [reference CLI](https://code.visualstudio.com/docs/devcontainers/devcontainer-cli)
and official [devcontainers/ci action](https://github.com/devcontainers/ci)
exercise the environment independently of an interactive editor session.
[Vite's server options](https://vite.dev/config/server-options) describe host
binding and the environment variable for allowing a forwarded hostname.

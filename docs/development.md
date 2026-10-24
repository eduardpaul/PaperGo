# Development environment

PaperGo's [devcontainer](../.devcontainer/devcontainer.json) is the `development`
stage of the repository's one [Dockerfile](../Dockerfile), which is also CI's
toolchain image and the base of the application build. It provides Go, Node.js
with npm, Python 3, SQLite's CLI, the PostgreSQL client, the native
SDKs, a C compiler, pkg-config and CMake, plus gopls, Delve, Staticcheck and
govulncheck. The Dockerfile is the only place their versions are pinned: Go,
Node and CMake as `ARG`s at its top and the native SDKs in
`scripts/native.json`. CI jobs that cannot use
the image, on Windows and Linux arm64, install the same versions with
`.github/actions/toolchain`, which reads them through
`scripts/toolchain-versions.py`. To change a version, edit the Dockerfile. It
supports Linux amd64 and arm64 through the upstream multi-platform images. The source checkout stays on the host; dependencies and
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

`scripts/check.py` holds every check, in stages. Each CI job runs exactly one
stage, so a failing job names the command that reproduces it.

| Stage | What it checks |
| --- | --- |
| `quick` (default) | Go formatting and the Go suite with Go's test-result cache: the edit/check loop |
| `lint` | Formatting, module checksums, generated Bob models and workflow SDK, vet, Staticcheck, frontend typecheck and lint, Python tests |
| `test` | Uncached Go suite on SQLite, the database suites on PostgreSQL 17, frontend tests |
| `race` | The Go suite with race detection |
| `web` | Frontend typecheck, lint and tests |
| `native` | Real decision, embedding and OCR models |
| `zvec` | Native search store conformance, parity, soak and fuzzing |
| `release` | Builds, packages and runs the release executables with the shared assets |
| `docker` | Builds the container image and runs its smoke test |
| `vuln` | Reachable Go dependency vulnerabilities |

### Edit and check

Run **PaperGo: check**, or select the packages you are working on:

```sh
bash scripts/check.sh
bash scripts/check.sh --packages ./internal/dms
bash scripts/check.sh --packages ./internal/dms --run '^TestSeekPagesMatchSortedEligibleRows$'
bash scripts/check.sh web
bash scripts/check.sh test --only postgres --packages ./internal/dms
```

Package selection is explicit: include affected callers when changing shared
code. `test --only go|postgres|web` runs one part of the test stage;
`--shard 1/2` runs half of the Go suite, as CI does. The database suites use the
server `PAPERGO_TEST_POSTGRES_DSN` names, or start the one of `scripts/postgres.sh`;
tests create and drop their own databases, and `bash scripts/postgres.sh clean`
drops the ones an interrupted run left behind. For repeated frontend edits use
`npm --prefix web test -- --watch`. The PowerShell entry point takes the same
arguments: `./scripts/check.ps1 --packages ./internal/dms`.

The Go stage covers SQLite. The PostgreSQL stage selects PostgreSQL for the
shared fixtures and skips explicit SQLite contracts, schema-file tests and
SQLite backups, so they run once per test stage.

Checks use the installed dependencies and never download them; `--setup` first
runs `npm ci` where the installed packages do not match the lockfile, as CI does.
Go checks build the embedded frontend on the first check, then reuse it only
while both the source and generated asset hashes match. Reports live in
`.cache/checks`: raw Go JSON events, structured package/test timings and a
Markdown summary.

### Run checks as CI does

`scripts/ci.sh` runs stages in CI's toolchain image, the devcontainer image,
which pins Go, Node, Python, the native SDKs, Staticcheck, govulncheck
and the PostgreSQL client. It needs only Docker, Python 3 and Git:

```sh
scripts/ci.sh                         # the pull request gate: lint, then test
scripts/ci.sh test --only go --shard 1/2
scripts/ci.sh race
scripts/ci.sh release                 # also: native, zvec, docker, vuln
```

It copies a clean snapshot of the checkout into the container: tracked and new
files, without ignored ones, so local build outputs cannot hide a failure. A
disposable PostgreSQL 17 runs beside it in memory. Go modules, build and npm
caches, `node_modules` and the frontend build persist in Docker volumes, so
repeated runs only recompile what changed. The image is tagged with a hash of
its inputs (the Dockerfile and the native SDK pins), the tag CI
publishes to `ghcr.io/eduardpaul/papergo-ci`; it is pulled from there when you
can read it, otherwise built locally once. `native`, `release` and `docker`
first prepare the model presets in `.tools` with
`scripts/prepare-preset-models.py`, which needs Docker for Laya; `docker` runs
on the host. `scripts/ci.sh clean` removes the database, network and cache
volumes.

### Continuous integration

Pull requests run `lint` and `test`: Go in two shards, PostgreSQL and the
frontend, about ten minutes on warm caches. Pushes to `main`, a weekly
schedule and releases also run `race` in three shards, `native`, `zvec`,
`release` and `vuln` in the toolchain image, `release` on Windows and `zvec` on
Linux arm64 with natively installed SDKs, and `docker`. The weekly schedule and
manual runs also build the devcontainer and run its smoke test. The first job
of each run publishes the toolchain image when its inputs changed. Each kind of
job restores its Go, npm and Staticcheck cache; runs on `main` save a new one
once a week or when a lockfile changes, and pull requests only read them. Go test reports are uploaded even when tests
fail. Superseded branch
and pull request runs are cancelled; release validation keeps its serialized
publication policy.

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
`bash scripts/generate-models.sh` (**PaperGo: generate Bob models**). The
PostgreSQL models are read from a temporary database holding the schema, on the
server `PAPERGO_TEST_POSTGRES_DSN` names or that of `scripts/postgres.sh`; `psql`
must be installed, as it is in the toolchain image. The `lint` stage regenerates
both and fails when that changes a file.

Once a release is published, a schema change also adds a hand-written
migration for each engine to `internal/dal/migrations/`; see
[Database upgrades](releases.md#database-upgrades).

Stop the API or debugger before resetting local data. Local databases and blobs
are disposable: delete `data/`, recreate the directory with `mkdir -p data/blobs`,
then run `go run ./cmd/admin initialize --database data/papergo.db`. Do not add backfills.

Go/npm caches and `web/node_modules` survive container rebuilds. `npm ci` replaces
the installed dependency tree with the lockfile contents. To clear a stale build
cache, run `go clean -cache`; for module downloads use `go clean -modcache`.
Rebuild the container after editing the Dockerfile or its configuration; a
version change there reaches the devcontainer, CI and the application build
together.

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

# Docker deployment

The image contains the web UI, current database schema, native libraries and all four local model presets. It needs no external database, model download, migration command or host Go/Node/Python installation. One container owns the SQLite database and search store; persist `/data` on local storage and run one replica.

## Published image

Releases publish `ghcr.io/eduardpaul/papergo:vX.Y.Z` for Linux amd64 and arm64. Docker selects the matching architecture automatically. Stable releases update `latest`; release candidates use their exact `vX.Y.Z-rc.N` tag and never update `latest`. Pin a release tag or image digest for a deployment.

GitHub initially creates container packages as private. After the first publication, the package owner can set its visibility to Public in the package settings to allow anonymous pulls. A private package requires `docker login ghcr.io` with a personal access token (classic) granting `read:packages`. The release workflow itself uses `GITHUB_TOKEN` with job-scoped package permissions; no registry password needs to be configured in repository secrets. See [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

## Start with Compose

From the repository root, create the initial administrator secret and start the [Compose deployment](../deploy/compose.yaml):

```sh
mkdir -p deploy/secrets
chmod 0700 deploy/secrets
openssl rand -base64 32 > deploy/secrets/admin-password
chmod 0444 deploy/secrets/admin-password
docker compose -f deploy/compose.yaml up -d
```

Open http://localhost:8080 and sign in as `admin` with the password stored in that file. `PAPERGO_VERSION=vX.Y.Z docker compose -f deploy/compose.yaml up -d` selects a release. `PAPERGO_PORT=8081` changes the host port. The Compose file also holds the optional [MCP server](mcp.md) for AI assistants, commented out; uncommented, it listens on `PAPERGO_MCP_PORT` (default 8090).

The secret's host directory is private. Its file is readable by the container's UID 65532 because Compose file-backed secrets retain host permissions. The secret is mounted read-only and is not stored in the image or the container's environment. Production local authentication requires an explicit initial password through `ADMIN_PASSWORD_FILE`, `ADMIN_PASSWORD` or YAML; it never supplies a default password. Use only one of the two environment settings. The password file accepts 1–72 bytes and ignores trailing CR/LF. The initial password applies only to a new administrator; changing the secret does not reset an existing password.

The deployment runs as UID/GID 65532, drops capabilities, forbids privilege escalation, uses a read-only root filesystem and allows temporary files under `/tmp`. The named data volume persists the database, blobs, search index and integration encryption key across container replacement. `docker compose down` retains it; `down --volumes` deletes it. For a host bind mount instead, create the data directory with ownership `65532:65532` and writable permissions before mounting it.

The published port binds to loopback. Use the [Nginx Proxy Manager](../samples/npm/README.md) or [authentik](../samples/authentik/README.md) deployment for HTTPS ingress and external sign-in. The application serves HTTP inside the container. Configure the public HTTPS URL and trusted proxy/identity settings for the chosen ingress.

To supply YAML, mount a readable configuration file at `/config/config.yaml` and set `PAPERGO_CONFIG=/config/config.yaml`. The image's `HTTP_ADDR`, `DATABASE_PATH` and `BLOB_PATH` defaults take precedence over YAML: keep them for `/data` deployment, or explicitly clear/replace those environment settings when YAML should provide the paths and address.

Models load from `/opt/papergo/assets/models`; native libraries live in `/opt/papergo/lib`. Model assets are in a separate cached image layer. They are read directly, so runtime model extraction and a writable code directory are unnecessary. Local decisions and OCR are enabled; semantic search requires `AI_EMBEDDINGS_PROVIDER=local`, with `AI_EMBEDDINGS_MODEL=embeddinggemma-2` or `harrier-oss-v1-0.6b`. The image includes roughly 2 GiB of application/model files. Model inference needs additional RAM; allow at least 4 GiB when using Laya and embeddings together, and size for your workload.

The image includes a readiness health check using `/opt/papergo/papergo --health`. It needs no shell or curl. Logs go to standard output; SIGTERM shuts down the HTTP server and workflow runner. Compose allows 45 seconds for that shutdown.

For maintenance, stop the API before a zvec backup and use the same image and data volume:

```sh
docker compose -f deploy/compose.yaml stop papergo
docker compose -f deploy/compose.yaml run --rm --no-deps \
  --entrypoint /opt/papergo/papergo-admin papergo backup --file /data/backup.tar.gz
docker compose -f deploy/compose.yaml up -d
```

## Build from source

```sh
docker build --pull -t papergo:local .
docker tag papergo:local ghcr.io/eduardpaul/papergo:local
PAPERGO_VERSION=local docker compose -f deploy/compose.yaml up -d
```

BuildKit builds the frontend, native SDKs, models and Go executables. Laya export uses the pinned Linux amd64 CPU environment and about 6 GiB of build memory; ARM hosts need amd64 emulation for that export. Release CI supplies the already verified shared model assets as the `model-assets` named build context, so each platform builds natively without re-exporting weights. A source build also accepts this context when assets have been prepared:

```sh
python3 scripts/build-assets.py --output-dir .cache/container-assets/assets
docker build --build-context model-assets=.cache/container-assets -t papergo:local .
```

Check an image directly with `python3 scripts/docker-smoke.py --image papergo:local`. The test requires only Docker and Python on the test host. It checks production password setup, automatic schema creation, persistent data across replacement, health, graceful shutdown, the UI, zvec, PDF rendering, OCR, Laya and both embedding presets under the image's nonroot user and a read-only root filesystem.

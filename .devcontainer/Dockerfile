# syntax=docker/dockerfile:1
# Keep Go and Node aligned with .github/workflows/ci.yml and the runtime build.
FROM node:24.15.0-bookworm-slim AS node

# PaperGo's schema uses FTS5, which upstream Atlas 1.3.0 images do not enable.
# Build the pinned Community source with the SQLite feature the app requires.
FROM golang:1.27.2-bookworm AS atlas
ADD --checksum=sha256:4d83772262864d35f8490d919eb405aa47c44d5b703496494b3141c691ed8987 \
    https://codeload.github.com/ariga/atlas/tar.gz/refs/tags/v1.3.0 /tmp/atlas.tar.gz
RUN mkdir -p /src/atlas && tar -xzf /tmp/atlas.tar.gz --strip-components=1 -C /src/atlas
WORKDIR /src/atlas/cmd/atlas
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -p 2 -tags sqlite_fts5 \
        -ldflags="-X ariga.io/atlas/cmd/atlas/internal/cmdapi.version=v1.3.0" \
        -o /out/atlas .

FROM golang:1.27.2-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git python3 sqlite3 sudo \
    && rm -rf /var/lib/apt/lists/*

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=atlas /out/atlas /usr/local/bin/atlas
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# Install editor and CI tools in the image; opening a checkout never installs latest.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOBIN=/usr/local/bin go install -p 2 golang.org/x/tools/gopls@v0.23.0 \
    && GOBIN=/usr/local/bin go install -p 2 github.com/go-delve/delve/cmd/dlv@v1.27.2 \
    && GOBIN=/usr/local/bin go install -p 2 honnef.co/go/tools/cmd/staticcheck@v0.7.0-0.dev.0.20261009230814-452d5bb86b45 \
    && GOBIN=/usr/local/bin go install -p 2 golang.org/x/vuln/cmd/govulncheck@v1.9.0

RUN groupadd --gid 1000 vscode \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash vscode \
    && printf 'vscode ALL=(root) NOPASSWD:ALL\n' > /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode \
    && mkdir -p /home/vscode/go/pkg/mod /home/vscode/.cache \
    && chown -R vscode:vscode /home/vscode

ENV GOPATH=/home/vscode/go \
    GOMODCACHE=/home/vscode/go/pkg/mod \
    GOCACHE=/home/vscode/.cache/go-build \
    npm_config_cache=/home/vscode/.cache/npm \
    PATH=/usr/local/go/bin:/home/vscode/go/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
USER vscode

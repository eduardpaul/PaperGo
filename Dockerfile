# syntax=docker/dockerfile:1
# PaperGo's one Dockerfile, and the one place its toolchain is pinned:
#   toolchain    Go, Node, Python, compilers, native SDKs and the
#                PostgreSQL client; the application is built in it
#   development  the toolchain with editor and analysis tools: the
#                devcontainer, CI's container jobs and scripts/ci.sh
#   (default)    the production image
# CI jobs that run outside the image read these versions with
# scripts/toolchain-versions.py.
ARG GO_VERSION=1.27.3
ARG NODE_VERSION=24.15.0
ARG CMAKE_VERSION=3.31.10

FROM node:${NODE_VERSION}-bookworm-slim AS node

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
COPY sdk/dist/workflow.d.ts /src/sdk/dist/
RUN npm run build

# Exports the pinned Laya graph on a fixed CPU platform. Only the models
# workflow builds these two stages; it publishes the result, which builds
# download through scripts/decision-model/bundle.json.
FROM --platform=linux/amd64 python:3.13-slim-bookworm AS laya-export
WORKDIR /src
COPY scripts/decision-model/requirements.txt scripts/decision-model/
RUN pip install --no-cache-dir torch==2.8.0 --index-url https://download.pytorch.org/whl/cpu \
    && pip install --no-cache-dir -r scripts/decision-model/requirements.txt
COPY scripts/decision-model/artifacts.json scripts/decision-model/prepare.py \
    scripts/decision-model/export.py scripts/decision-model/
COPY internal/decision/manifest.json internal/decision/LICENSE internal/decision/NOTICE internal/decision/
RUN python scripts/decision-model/prepare.py \
    && pip install --no-cache-dir --no-deps .tools/decision-build/laya-0.4.8-py3-none-any.whl \
    && python scripts/decision-model/export.py

FROM scratch AS laya-model
COPY --from=laya-export /src/.tools/decision-build/onnx/model.onnx \
    /src/.tools/decision-build/onnx/manifest.json \
    /src/.tools/decision-build/onnx/LICENSE /src/.tools/decision-build/onnx/NOTICE /
# The export directory links to the source tokenizer. Copy its bytes so this
# artifact remains complete after leaving the export container.
COPY --from=laya-export /src/.tools/decision-build/model/tokenizer/tokenizer.json /tokenizer.json

# Keep the large, platform-independent models in their own cached image layer.
# Release CI supplies this stage as a named context containing verified assets.
FROM python:3.12-slim-bookworm AS model-assets
WORKDIR /src
COPY scripts/build-assets.py scripts/prepare-preset-models.py scripts/fetch-embedding-models.py scripts/
COPY scripts/decision-model/install.py scripts/decision-model/bundle.json scripts/decision-model/
COPY scripts/model-notices scripts/model-notices
COPY internal/decision/manifest.json internal/decision/
COPY models/paddleocr models/paddleocr
RUN --mount=type=cache,target=/src/.tools/embedding/models \
    --mount=type=cache,target=/src/.tools/decision \
    python3 scripts/build-assets.py --output-dir /assets

FROM golang:${GO_VERSION}-bookworm AS toolchain
ARG CMAKE_VERSION
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git libicu72 postgresql-client python3 python3-pip pkg-config sqlite3 sudo \
    && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && pip3 install --break-system-packages --no-cache-dir cmake==${CMAKE_VERSION}
# CI jobs run as root on a checkout another user owns; Git, and the VCS
# stamping of go list and go build, would otherwise refuse the repository.
RUN git config --system --add safe.directory '*'
# The pinned cgo SDKs: headers, libraries and license notices. Only WebP is
# compiled; zvec, PDFium, ONNX Runtime and tokenizers use pinned binaries.
# Setup in a checkout rewrites their environment files for that checkout.
COPY scripts/native.py scripts/native.json /opt/papergo/native-src/scripts/
COPY scripts/native-notices /opt/papergo/native-src/scripts/native-notices
RUN python3 /opt/papergo/native-src/scripts/native.py --directory /opt/papergo/native \
    && rm -rf /opt/papergo/native/sdk /opt/papergo/native-src/.tools
ENV PAPERGO_NATIVE_DIR=/opt/papergo/native

FROM toolchain AS development
# Editor and CI tools are part of the image; opening a checkout never
# installs a newer one.
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
    # scripts/ci.sh copies a checkout to /work; its cache volumes start out
    # owned by vscode because these directories are.
    && mkdir -p /work/web/node_modules /work/web/dist /work/sdk/node_modules /work/.cache/checks \
    && chown -R vscode:vscode /home/vscode /work \
    # The devcontainer can renumber vscode to the host user; setup in any
    # checkout rewrites the SDK's environment files for that checkout.
    && chmod -R a+rwX /opt/papergo/native
# Every tool cache, Staticcheck's included, stays under /home/vscode/.cache,
# whichever user runs the container.
ENV GOPATH=/home/vscode/go \
    GOMODCACHE=/home/vscode/go/pkg/mod \
    GOCACHE=/home/vscode/.cache/go-build \
    XDG_CACHE_HOME=/home/vscode/.cache \
    npm_config_cache=/home/vscode/.cache/npm \
    PATH=/usr/local/go/bin:/home/vscode/go/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
USER vscode

FROM toolchain AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
ARG VERSION=development
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# papergo-mcp has its own image, extensions/mcp/Dockerfile.
RUN --mount=type=cache,target=/root/.cache/go-build \
    python3 scripts/build-binaries.py --output-dir /out/bin \
       --ldflags="-s -w -X papergo/internal/buildinfo.Version=$VERSION -X papergo/internal/buildinfo.Commit=$COMMIT -X papergo/internal/buildinfo.Date=$BUILD_DATE" \
    && rm /out/bin/papergo-mcp \
    && mkdir -p /state/blobs

FROM gcr.io/distroless/cc-debian12:nonroot
ARG VERSION=development
ARG COMMIT=unknown
ARG SOURCE_URL=https://github.com/eduardpaul/PaperGo
LABEL org.opencontainers.image.title="PaperGo" \
    org.opencontainers.image.source=$SOURCE_URL \
    org.opencontainers.image.version=$VERSION \
    org.opencontainers.image.revision=$COMMIT
WORKDIR /opt/papergo
COPY --from=model-assets /assets/ /opt/papergo/assets/
COPY --from=build /out/bin/ /opt/papergo/
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD ["/opt/papergo/papergo", "--health"]
ENTRYPOINT ["/opt/papergo/papergo"]

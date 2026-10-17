FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

# Export once on a fixed CPU platform; every target uses this verified graph.
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

# Install the native SDKs once, including headers and license notices. Only
# WebP is compiled here; zvec, PDFium, ONNX and tokenizers use pinned binaries.
FROM python:3.12-slim-bookworm AS native
RUN apt-get update && apt-get install -y --no-install-recommends gcc g++ make pkg-config \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir cmake==3.31.10
WORKDIR /src
COPY scripts/native.py scripts/native.json scripts/
COPY scripts/native-notices scripts/native-notices
RUN --mount=type=cache,target=/src/.tools/native-downloads python3 scripts/native.py

FROM golang:1.27.2-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=native /src/.tools/native /src/.tools/native
COPY --from=laya-model / /src/.tools/decision/model/
COPY --from=frontend /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    . .tools/native/env.sh \
    && python3 scripts/build-standalone.py --output /out/papergo \
    && python3 scripts/build-standalone.py --command admin --output /out/papergo-admin \
    && mkdir -p /state/blobs

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /out/papergo /papergo
COPY --from=build /out/papergo-admin /papergo-admin
COPY migrations /schema
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production \
    XDG_CACHE_HOME=/data/.cache
EXPOSE 8080
ENTRYPOINT ["/papergo"]

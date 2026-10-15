FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

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
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=native /src/.tools/native /src/.tools/native
COPY --from=frontend /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    . .tools/native/env.sh \
    && go build -trimpath -ldflags="-s -w" -o /out/papergo ./cmd/api \
    && go build -trimpath -ldflags="-s -w" -o /out/papergo-admin ./cmd/admin \
    && mkdir -p /state/blobs

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=native /src/.tools/native/lib/libonnxruntime.so /usr/lib/papergo/libonnxruntime.so
COPY --from=native /src/.tools/native/lib/libpdfium.so /usr/lib/papergo/libpdfium.so
COPY --from=native /src/.tools/native/lib/libzvec_c_api.so /usr/lib/papergo/libzvec_c_api.so
COPY --from=native /src/.tools/native/notices /usr/share/doc/papergo
COPY models/paddleocr /usr/share/papergo/models/paddleocr
COPY --from=build /out/papergo /papergo
COPY --from=build /out/papergo-admin /papergo-admin
COPY migrations /schema
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production \
    ONNXRUNTIME_LIB=/usr/lib/papergo/libonnxruntime.so OCR_MODELS=/usr/share/papergo/models/paddleocr
EXPOSE 8080
ENTRYPOINT ["/papergo"]

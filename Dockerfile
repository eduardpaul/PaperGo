FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/papergo ./cmd/api \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/papergo-admin ./cmd/admin \
    && mkdir -p /state/blobs

# OCR: the pinned ONNX Runtime release, checked against its SHA-256.
FROM debian:12-slim AS ocr
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY scripts/fetch-onnxruntime.sh scripts/
RUN library=$(bash scripts/fetch-onnxruntime.sh /opt/ort) \
	&& mkdir -p /out/lib /out/doc \
	&& cp -L "$library" /out/lib/libonnxruntime.so \
	&& cp "$(dirname "$library")/../LICENSE" "$(dirname "$library")/../ThirdPartyNotices.txt" /out/doc/

# ONNX Runtime needs glibc and libstdc++; PaperGo itself loads it through
# purego and is built without CGO.
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=ocr /out/lib/libonnxruntime.so /usr/lib/papergo/libonnxruntime.so
COPY --from=ocr /out/doc /usr/share/doc/onnxruntime
COPY models/paddleocr /usr/share/papergo/models/paddleocr
COPY --from=build /out/papergo /papergo
COPY --from=build /out/papergo-admin /papergo-admin
COPY migrations /schema
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production AUTH_MODE=oidc \
	ONNXRUNTIME_LIB=/usr/lib/papergo/libonnxruntime.so OCR_MODELS=/usr/share/papergo/models/paddleocr
EXPOSE 8080
ENTRYPOINT ["/papergo"]

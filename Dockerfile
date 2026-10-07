FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/papergo ./cmd/api \
    && mkdir -p /state/blobs

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/papergo /papergo
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production AUTH_MODE=oidc
EXPOSE 8080
ENTRYPOINT ["/papergo"]

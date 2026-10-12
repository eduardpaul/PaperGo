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

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/papergo /papergo
COPY --from=build /out/papergo-admin /papergo-admin
COPY migrations /schema
COPY --from=build --chown=65532:65532 /state /data
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 DATABASE_PATH=/data/papergo.db BLOB_PATH=/data/blobs APP_ENV=production AUTH_MODE=oidc
EXPOSE 8080
ENTRYPOINT ["/papergo"]

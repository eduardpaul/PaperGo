# papergo-mcp, PaperGo's optional MCP server. Build from the repository root:
#
#   docker build -f extensions/mcp/Dockerfile -t papergo-mcp .
#
# It is pure Go: no native libraries, models or database.
FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/mcp ./cmd/mcp
COPY extensions/mcp ./extensions/mcp
COPY internal/buildinfo ./internal/buildinfo
COPY internal/dal/refs ./internal/dal/refs
ARG VERSION=development
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# Only the modules papergo-mcp imports are downloaded.
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -trimpath -o /out/papergo-mcp \
       -ldflags="-s -w -X papergo/internal/buildinfo.Version=$VERSION -X papergo/internal/buildinfo.Commit=$COMMIT -X papergo/internal/buildinfo.Date=$BUILD_DATE" \
       ./cmd/mcp

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=development
ARG COMMIT=unknown
ARG SOURCE_URL=https://github.com/eduardpaul/PaperGo
LABEL org.opencontainers.image.title="PaperGo MCP" \
    org.opencontainers.image.source=$SOURCE_URL \
    org.opencontainers.image.version=$VERSION \
    org.opencontainers.image.revision=$COMMIT
COPY --from=build /out/papergo-mcp /opt/papergo/papergo-mcp
USER 65532:65532
# The container listens on every interface; publish it only on a private
# network or behind a TLS reverse proxy.
ENV PAPERGO_MCP_LISTEN=0.0.0.0:8081
EXPOSE 8081
ENTRYPOINT ["/opt/papergo/papergo-mcp"]

# syntax=docker/dockerfile:1
FROM golang:1.24.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-mod=vendor go build -trimpath -o /out/echoservice-server ./cmd/server
RUN CGO_ENABLED=0 GOFLAGS=-mod=vendor go build -trimpath -o /out/echoservice ./cmd/cli

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home echo
WORKDIR /app
RUN mkdir -p /app/data && chown 10001:10001 /app/data
COPY --from=build /out/ /app/bin/
COPY conf/server/config.toml /app/conf/server/config.toml
USER 10001:10001
ENV ECHO_HTTPSERVER_LISTEN=0.0.0.0:9090
ENV ECHO_DB_PATH=/app/data/echo-service.db
EXPOSE 9090
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 CMD ["curl", "--fail", "--silent", "http://127.0.0.1:9090/readyz"]
ENTRYPOINT ["/app/bin/echoservice-server", "--config", "/app/conf/server/config.toml"]

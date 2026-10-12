# syntax=docker/dockerfile:1
# Nodera API (server; also carries the nodera-migrate release job) and Node Agent images. Build one with:
#   docker build -f api/Dockerfile --target server  -t nodera-api api
#   docker build -f api/Dockerfile --target agent   -t nodera-agent api
FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nodera-server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nodera-migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nodera-agent ./cmd/agent

# ---- API server -------------------------------------------------------
FROM alpine:3.20 AS server
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -S nodera && adduser -S -G nodera -u 10001 nodera \
 && mkdir -p /data && chown nodera:nodera /data
COPY --from=build /out/nodera-server /usr/local/bin/nodera-server
# The schema-migration release step ships in the same image (same version, same
# embedded migrations); compose runs it with `entrypoint: [nodera-migrate]`.
COPY --from=build /out/nodera-migrate /usr/local/bin/nodera-migrate
USER nodera
ENV NODERA_HTTP_ADDR=:8080 NODERA_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=5 CMD wget -qO- http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/nodera-server"]

# ---- Node Agent (runs on each managed node, separate from the API) ----
FROM alpine:3.20 AS agent
RUN apk add --no-cache ca-certificates docker-cli \
 && addgroup -S nodera && adduser -S -G nodera -u 10001 nodera \
 && mkdir -p /var/lib/nodera-agent && chown nodera:nodera /var/lib/nodera-agent
COPY --from=build /out/nodera-agent /usr/local/bin/nodera-agent
# The agent needs access to the Docker socket: it is the ONLY Nodera component
# that gets it (the API does not). It runs as the same uid as the API (10001) so
# both can share the host data directory (database dump hand-off). Add the host docker group's GID with
# `group_add` in compose. It never listens on a port.
USER nodera
ENV NODERA_AGENT_STATE=/var/lib/nodera-agent/state.json NODERA_AGENT_DATA=/var/lib/nodera-agent/data
ENTRYPOINT ["/usr/local/bin/nodera-agent"]
CMD ["run"]

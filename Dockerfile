# mono-agent CLI — minimal production image.
#
# This builds the DEFAULT binary (no `-tags social`): Instagram/LinkedIn/X/
# TikTok engagement nodes are NOT compiled in. To build the opt-in social
# variant, add `-tags social` to the go build line below.
#
# Stage 1: build (static, CGO-free → runs on any base image, incl. scratch)
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

# Everything else. NOTE: data/ (migrations, actions, skills) and
# internal/workflow/{schemas,templates} are go:embed-ed — they must stay in
# the build context (see .dockerignore).
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/monoagentcli ./cmd/monoagentcli

# Stage 2: runtime
FROM alpine:3.22

# TLS roots for outbound HTTPS (API nodes, RSS, webhooks, release updates).
RUN apk add --no-cache ca-certificates

COPY --from=build /out/monoagentcli /usr/local/bin/monoagentcli

# All state lives under $HOME/.monoagent (SQLite DB at
# ~/.monoagent/monoagent.db, workflows at ~/.monoagent/workflows, secrets
# vault at ~/.monoagent/vault — resolved via os.UserHomeDir/$HOME in
# cmd/monoagentcli/root.go and internal/vault/vault.go). Pointing HOME at
# /data puts ALL of it on one volume.
ENV HOME=/data
# Non-root runtime user; /data is chowned so the volume is writable by it.
RUN adduser -D -u 10001 monoagent \
    && mkdir -p /data \
    && chown monoagent:monoagent /data
USER monoagent
VOLUME ["/data"]

WORKDIR /app

# Liveness = the CLI answers `version` (exit 0). The daemon is the
# entrypoint, so any successful subcommand invocation proves the process
# tree is alive and the binary still executes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD monoagentcli version

# Webhook trigger port. The engine binds 127.0.0.1:9321 by default
# (internal/workflow/engine.go applyEngineDefaults); set
# MONOAGENT_WEBHOOK_ADDR=0.0.0.0:9321 (see docker-compose.yml) so the
# published port is reachable from outside the container. Secure webhook
# nodes (auth token / HMAC secret) before exposing the port further.
EXPOSE 9321

ENTRYPOINT ["/usr/local/bin/monoagentcli", "daemon"]

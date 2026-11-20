# syntax=docker/dockerfile:1.7-labs
# One image, one process (docs/GO-PORT.md): `palui serve` is the HTTP server,
# the dashboard and the runtime (connectors, turns, schedulers, outbox).
#
#   1. dashboard  Node + pnpm build the static SPA (apps/app/dist)
#   2. server     Go builds one static binary with that dist embedded
#   3. final      distroless static (CA certificates, no shell or package
#                 manager), the binary as /usr/local/bin/palui, user 1000:1000
#
# The context is the repo root, trimmed by .dockerignore: docs, the site and
# the edge worker never enter it (the site and edge only send their
# package.json, which the shared lockfile needs), so a docs or site commit
# changes no layer. A Go-only change doesn't touch stage 1, and a dashboard
# change doesn't rerun `go mod download`.

# --- 1. dashboard ------------------------------------------------------------
FROM node:24-bookworm-slim AS dashboard
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /repo
# Every workspace manifest, so the shared lockfile matches; nothing else yet,
# so this layer stays cached until dependencies change. Only @palui/app's own
# dependencies are installed: the site, the edge worker and the host bridge
# aren't part of the image.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc* ./
COPY --parents apps/*/package.json ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
  pnpm install --frozen-lockfile --filter @palui/app
COPY apps/app ./apps/app
RUN pnpm --filter @palui/app build

# --- 2. server ---------------------------------------------------------------
# The tag follows the `go` directive in apps/server/go.mod.
FROM golang:1.26.11-alpine3.23 AS server
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src/apps/server
COPY apps/server/go.mod apps/server/go.sum ./
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod go mod download
COPY apps/server ./
# The dashboard build replaces the placeholder that internal/web embeds.
COPY --from=dashboard /repo/apps/app/dist ./internal/web/dist
# Last, so a release (a new version in package.json) only reruns the link.
COPY package.json /src/package.json
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
  --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
  version=$(sed -n 's/^  "version": *"\([^"]*\)".*/\1/p' /src/package.json | head -n 1) \
  && echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+' \
  && go build -trimpath \
    -ldflags "-s -w -X github.com/zakiego/palui/apps/server/internal/version.Version=$version" \
    -o /out/palui ./cmd/palui \
  && /out/palui version
# The final stage has no mkdir: /data comes from here, empty.
RUN mkdir -p /out/data

# --- 3. final ----------------------------------------------------------------
# No shell, no busybox, no apk: the binary is static (CGO_ENABLED=0, modernc
# sqlite) and embeds the time zones. The ops scripts run plain file work in a
# pinned alpine helper on the same volume, never inside this image.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
# The production volume was written by the old image's `node` user, 1000:1000.
# Running as the same ids keeps it readable and writable without a chown. A new
# volume copies this empty /data, owner included.
COPY --from=server --chown=1000:1000 /out/data /data
WORKDIR /app
# NODE_ENV=production is the server's prod mode (MASTER_KEY required,
# dev fixtures off). /data is the volume: palui.db (SQLite WAL), wa/ (the
# WhatsApp stores), backups/.
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000 DATA_DIR=/data \
  PALUI_BUILD_INFO=/app/build/build-info.json
# `palui` is the server and the admin CLI: docker compose exec app palui owner invite
COPY --from=server /out/palui /usr/local/bin/palui
# build-info.json names the commit (scripts/pre-deploy.sh writes it before the
# build). Optional: the glob lets a local build without it still copy package.json.
COPY package.json build-info.jso[n] ./build/
USER 1000:1000
EXPOSE 3000
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
  CMD ["palui", "healthcheck"]
CMD ["palui", "serve"]

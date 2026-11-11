# syntax=docker/dockerfile:1.7-labs
# One image, two services (PLAN 3.1): `app` runs the Nitro server in .output/,
# `runtime` runs runtime/dist/index.mjs from the same image (compose.yaml).
# zaki-selfhost Dockerfile.nitro, on bookworm-slim: better-sqlite3 ships glibc builds.
#
# Monorepo: the context is the repo root. Only apps/app, apps/runtime and the
# packages they use are installed and built; the site and the edge worker never
# enter the image (see .dockerignore), so a docs commit doesn't change any layer.
FROM node:24-bookworm-slim AS build
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /repo
# Every workspace manifest, so the shared lockfile matches; nothing else yet,
# so the install layer stays cached until dependencies change.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc* ./
COPY --parents apps/*/package.json packages/*/package.json ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
  pnpm install --frozen-lockfile --filter @palui/app... --filter @palui/runtime...
COPY tsconfig.base.json ./
COPY packages ./packages
COPY apps/app ./apps/app
COPY apps/runtime ./apps/runtime
RUN pnpm --filter @palui/app build && pnpm --filter @palui/runtime build
# The runtime bundle keeps native and heavy packages external (better-sqlite3,
# baileys); `pnpm deploy` gives it a self-contained node_modules for them.
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
  pnpm --filter @palui/runtime deploy --prod --legacy /out/runtime \
  && ARCH=$(node -p "process.arch") \
  # better-sqlite3 carries prebuilds for every platform. Keep this one.
  && for d in apps/app/.output/server/node_modules/better-sqlite3 /out/runtime/node_modules/better-sqlite3; do \
       find "$d/prebuilds" -type f ! -name "linux-$ARCH.node" -delete 2>/dev/null || true; \
     done

FROM node:24-bookworm-slim
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOST=0.0.0.0 DATA_DIR=/data
# /data is the volume: palui.db (SQLite WAL), wa-auth/, backups/.
# `palui` is the admin CLI: docker compose exec app palui owner invite
RUN mkdir -p /data && chown node:node /data \
  && printf '#!/bin/sh\nexec node /app/.output/server/cli.mjs "$@"\n' > /usr/local/bin/palui \
  && chmod 755 /usr/local/bin/palui
USER node
COPY --from=build --chown=node:node /repo/apps/app/.output ./.output
COPY --from=build --chown=node:node /out/runtime/dist ./runtime/dist
COPY --from=build --chown=node:node /out/runtime/node_modules ./runtime/node_modules
COPY --from=build --chown=node:node /out/runtime/package.json ./runtime/package.json
EXPOSE 3000
VOLUME ["/data"]
CMD ["node", ".output/server/index.mjs"]

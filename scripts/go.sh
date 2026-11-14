#!/usr/bin/env bash
# The Go half of the root `pnpm typecheck`, `pnpm test` and `pnpm build`.
# Each root script runs the workspace packages first (pnpm -r), then this, and
# fails if either side fails, so the three commands a contributor runs before
# a PR cover apps/server too.
#
#   scripts/go.sh typecheck   gofmt (lists unformatted files) and go vet
#   scripts/go.sh test        go test ./...
#   scripts/go.sh build       go build ./...
#
# The image build (Dockerfile) links the real binary with the dashboard
# embedded and the version from package.json; this only proves it compiles.
set -euo pipefail

cd "$(dirname "$0")/../apps/server"

if ! command -v go >/dev/null 2>&1; then
  echo "go.sh: Go isn't installed. apps/server/go.mod names the version." >&2
  exit 1
fi

case "${1:-}" in
  typecheck)
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
      echo "go.sh: gofmt would change these files (run gofmt -w in apps/server):" >&2
      echo "$unformatted" >&2
      exit 1
    fi
    go vet ./...
    ;;
  test)
    go test ./...
    ;;
  build)
    go build ./...
    ;;
  *)
    echo "usage: scripts/go.sh typecheck|test|build" >&2
    exit 2
    ;;
esac

#!/usr/bin/env bash
#
# Runtime half of `make dev`: start the Vite frontend (which generates the
# mkcert dev certificates), wait for those certs to appear, then start the Go
# backend in the foreground. Both run together; Ctrl-C tears the whole tree
# down cleanly.
#
# Prerequisites (host check, postgres, settings, migrations, web deps) are
# handled by the Makefile targets that run before this script.

set -euo pipefail

# Run from the repo root regardless of where the script was invoked from.
cd "$(dirname "$0")/.."

CERT="web/.mkcert/cert.pem"
KEY="web/.mkcert/dev.pem"   # vite-plugin-mkcert emits the key as dev.pem
DEV_HOST="local-fleet-lite.dimo.org"
WEB_PORT="3009"
API_PORT="8084"

# Recursively kill a process and all of its descendants (npm -> vite -> esbuild).
# pgrep -P is available on both macOS and Linux.
killtree() {
  local pid="$1"
  local child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    killtree "$child"
  done
  kill "$pid" 2>/dev/null || true
}

WEB_PID=""
cleanup() {
  if [ -n "$WEB_PID" ]; then
    echo
    echo "▶ shutting down frontend…"
    killtree "$WEB_PID"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

echo "▶ starting frontend (Vite + mkcert) on https://$DEV_HOST:$WEB_PORT …"
( cd web && npm run dev ) &
WEB_PID=$!

echo "▶ waiting for dev TLS certs ($CERT)…"
until [ -f "$CERT" ] && [ -f "$KEY" ]; do
  if ! kill -0 "$WEB_PID" 2>/dev/null; then
    echo "✗ frontend exited before generating certs — see its output above." >&2
    exit 1
  fi
  sleep 0.5
done
echo "✓ dev certs present"

echo "▶ starting backend (api on https://$DEV_HOST:$API_PORT)…"
# Foreground: when this exits (or Ctrl-C), the EXIT trap stops the frontend.
( cd api && go run ./cmd/fleet-lite-app )

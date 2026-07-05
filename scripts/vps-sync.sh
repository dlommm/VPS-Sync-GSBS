#!/usr/bin/env bash
# Thin wrapper — all logic lives in the vps-sync Go binary.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${VPS_SYNC_BIN:-$ROOT/bin/vps-sync}"
[ -x "$BIN" ] || BIN="$(command -v vps-sync || true)"
[ -n "$BIN" ] || { echo "vps-sync binary not found; run: go build -o bin/vps-sync ./cmd/vps-sync" >&2; exit 1; }
cd "$ROOT"
exec "$BIN" "$@"

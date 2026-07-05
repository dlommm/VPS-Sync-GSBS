#!/usr/bin/env bash
# One-time VPS dependency install (Debian/Ubuntu).
set -euo pipefail
if [ "$(id -u)" -ne 0 ]; then echo "Run with sudo" >&2; exit 1; fi
apt-get update
apt-get install -y sqlite3 rsync curl ca-certificates git build-essential
if ! command -v go >/dev/null; then
  GO_VER="1.25.0"
  arch="$(uname -m)"
  case "$arch" in x86_64) go_arch=amd64 ;; aarch64) go_arch=arm64 ;; *) exit 1 ;; esac
  curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${go_arch}.tar.gz" | tar -C /usr/local -xz
  echo 'export PATH=$PATH:/usr/local/go/bin' >/etc/profile.d/go-path.sh
fi
echo "Build vps-sync: cd /opt/vps-sync-gsbs && go build -o bin/vps-sync ./cmd/vps-sync"

#!/usr/bin/env bash
# Self-updating runner: pull latest publisher + GSBS library, rebuild, then
# execute vps-sync (default: run). Used by the weekly cron so every scheduled
# publish is on current code.
#
#   ./scripts/update-and-run.sh          # update + weekly publish
#   ./scripts/update-and-run.sh export   # update + any other command
set -euo pipefail

# Everything lives in main() so bash parses the whole file before executing:
# a git pull that rewrites this script mid-run can't corrupt the running copy.
main() {
  local root gsbs_dir
  root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  gsbs_dir="${GSBS_DIR:-$root/../GSBS (Game Sync & Backup Service)}"

  update_repo "$root"
  if [ -d "$gsbs_dir/.git" ]; then
    update_repo "$gsbs_dir"
  else
    echo "[update] note: GSBS checkout not found at $gsbs_dir (set GSBS_DIR to override)" >&2
  fi

  cd "$root"
  echo "[update] building vps-sync"
  # Stage the build so a failure (e.g. GSBS gained a dependency and go.sum
  # hasn't been retidied+pushed yet) keeps the previous working binary and the
  # publish still happens on schedule.
  if go build -o bin/vps-sync.new ./cmd/vps-sync; then
    mv bin/vps-sync.new bin/vps-sync
  else
    rm -f bin/vps-sync.new
    if [ -x bin/vps-sync ]; then
      echo "[update] WARNING: build failed; running previous binary (fix: go mod tidy locally, test, push)" >&2
    else
      echo "[update] ERROR: build failed and no previous binary exists" >&2
      exit 1
    fi
  fi

  exec ./bin/vps-sync "${@:-run}"
}

# Fast-forward only: local edits or a diverged branch abort the update loudly
# instead of being clobbered, and the previous build keeps running.
update_repo() {
  local dir="$1"
  echo "[update] pulling $dir"
  if ! git -C "$dir" fetch origin; then
    echo "[update] WARNING: fetch failed for $dir; continuing with current code" >&2
    return 0
  fi
  if ! git -C "$dir" merge --ff-only FETCH_HEAD; then
    echo "[update] WARNING: $dir has local changes or diverged; not updating it" >&2
  fi
}

main "$@"
exit $?

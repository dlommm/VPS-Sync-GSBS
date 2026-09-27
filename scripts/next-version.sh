#!/usr/bin/env bash
# Print the next release version (X.Y.Z, no "v") from the commits since the
# newest vX.Y.Z tag. With no tag yet, the first release is 1.0.0.
#
# Bump rules, strongest wins:
#   major  a commit subject like "feat!: ..." / "fix(x)!: ...", a body line
#          starting "BREAKING CHANGE", or "[major]" anywhere in the message
#   minor  a subject starting "feat:" / "feat(scope):", or "[minor]"
#   patch  anything else — including no new commits at all (a scheduled
#          rebuild that picks up a new GSBS main or base image)
#
# BUMP=major|minor|patch forces the bump; BUMP=auto (default) applies the rules.
set -euo pipefail

bump="${BUMP:-auto}"
last="$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n1)"

if [[ -z "$last" ]]; then
  echo "1.0.0"
  exit 0
fi

IFS=. read -r major minor patch <<<"${last#v}"

if [[ "$bump" == auto ]]; then
  log="$(git log "$last"..HEAD --format='%s%n%b')"
  if grep -qE '^[a-zA-Z]+(\([^)]*\))?!:|^BREAKING[ -]CHANGE|\[major\]' <<<"$log"; then
    bump=major
  elif grep -qE '^feat(\([^)]*\))?:|\[minor\]' <<<"$log"; then
    bump=minor
  else
    bump=patch
  fi
fi

case "$bump" in
  major) echo "$((major + 1)).0.0" ;;
  minor) echo "$major.$((minor + 1)).0" ;;
  patch) echo "$major.$minor.$((patch + 1))" ;;
  *) echo "unknown BUMP=$bump (want auto, major, minor or patch)" >&2; exit 1 ;;
esac

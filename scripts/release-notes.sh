#!/usr/bin/env bash
# Print the CHANGELOG.md section for a release tag, e.g. `release-notes.sh v0.1.0`
# prints the body under `## [0.1.0]`. Fails when the section is missing or empty, so
# a tag cannot be released before its changelog entry exists.
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag>}"
version="${tag#v}"
changelog="${2:-CHANGELOG.md}"

notes="$(awk -v v="$version" '
  /^## \[/ { if (found) exit; if (index($0, "## [" v "]") == 1) { found = 1; next } }
  found { print }
' "$changelog")"

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "release-notes: no \"## [$version]\" section in $changelog" >&2
  echo "release-notes: move the Unreleased entries under it before tagging $tag" >&2
  exit 1
fi

printf '%s\n' "$notes"

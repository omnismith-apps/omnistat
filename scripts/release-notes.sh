#!/usr/bin/env bash
# Print the CHANGELOG.md section for a release tag, e.g. `release-notes.sh v0.1.0`
# prints the body under `## [0.1.0]`. A pre-release tag (`v0.2.0-rc.1`) uses its own
# section when there is one, else `## [Unreleased]`, so a release candidate can be cut
# before the changelog is finalised. Fails when the section is missing or empty, so a
# release cannot be tagged before its changelog entry exists.
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag>}"
version="${tag#v}"
changelog="${2:-CHANGELOG.md}"

section() {
  awk -v v="$1" '
    /^## \[/ { if (found) exit; if (index($0, "## [" v "]") == 1) { found = 1; next } }
    found { print }
  ' "$changelog"
}

blank() { [ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]; }

notes="$(section "$version")"
if blank "$notes" && [[ "$version" == *-* ]]; then
  notes="$(section "Unreleased")"
  if ! blank "$notes"; then
    notes="$(printf 'Pre-release %s: the changes currently under *Unreleased*.\n%s' "$tag" "$notes")"
  fi
fi

if blank "$notes"; then
  echo "release-notes: no \"## [$version]\" section in $changelog" >&2
  echo "release-notes: move the Unreleased entries under it before tagging $tag" >&2
  exit 1
fi

printf '%s\n' "$notes"

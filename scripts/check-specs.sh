#!/usr/bin/env bash
# Validates the spec-driven layout:
#  - every specs/features/<NNN-slug>/ has a spec.md
#  - spec.md / plan.md / tasks.md carry YAML frontmatter with a status
#  - feature dirs are numbered and kebab-cased
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
err() { echo "specs-check: $*" >&2; fail=1; }

has_frontmatter() {
  head -n1 "$1" | grep -q '^---$' && grep -q '^status:' "$1"
}

shopt -s nullglob
for dir in specs/features/*/; do
  name=$(basename "$dir")
  [[ "$name" =~ ^[0-9]{3}-[a-z0-9]+(-[a-z0-9]+)*$ ]] || err "feature dir '$name' must match NNN-kebab-slug"
  [[ -f "$dir/spec.md" ]] || { err "$dir is missing spec.md"; continue; }
  for f in spec.md plan.md tasks.md; do
    [[ -f "$dir/$f" ]] || continue
    has_frontmatter "$dir/$f" || err "$dir$f lacks frontmatter with a 'status:' field"
  done
done

for adr in specs/decisions/[0-9]*.md; do
  has_frontmatter "$adr" || err "$adr lacks frontmatter with a 'status:' field"
done

[[ -f specs/constitution.md ]] || err "specs/constitution.md is missing"

if [[ $fail -eq 0 ]]; then echo "specs-check: ok"; fi
exit $fail

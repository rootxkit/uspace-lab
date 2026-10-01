#!/usr/bin/env bash
# KT-3: for every system pinned in api/<system>/SOURCE, confirm at that
# commit the skeleton layout the aggregate relies on (docs/PLAN.md §3.3):
# api/openapi.yaml, schemas/, migrations/relational/,
# migrations/timeseries/, deploy/ and web/ exist, and api/openapi.yaml
# parses as OpenAPI 3.1.
#
#   scripts/check-layout.sh [--root DIR]
#
# Online, like check-mirrors.sh: an unreachable repository is
# "unverified" (a failure with REQUIRE_MIRRORS=1), never "ok". The last
# line says how many systems were checked, including zero.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib-mirror.sh
. "$here/scripts/lib-mirror.sh"

root="$here"
if [[ "${1:-}" == "--root" ]]; then root="$2"; fi

# path:kind as git cat-file -t reports it.
required=(api/openapi.yaml:blob schemas:tree migrations/relational:tree migrations/timeseries:tree deploy:tree web:tree)

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
checked=0 unpinned=0 failed=0 unverified=0
shopt -s nullglob
for src in "$root"/api/*/SOURCE; do
  # A SOURCE file, not a directory (a case-insensitive file system lets
  # a directory named source match the glob).
  [[ -f "$src" ]] || continue
  system="$(basename "$(dirname "$src")")"
  repo="$(source_get "$src" repo)"
  commit="$(source_get "$src" commit)"
  if [[ -z "$commit" ]]; then
    echo "unpinned   $system (layout not checked)"
    unpinned=$((unpinned + 1))
    continue
  fi
  work="$tmp/$system"
  mkdir -p "$work"
  if ! fetch_at "$repo" "$commit" "$work" >/dev/null 2>"$tmp/err-$system"; then
    echo "unverified $system: could not fetch ${commit:0:12} ($(tr '\n' ' ' < "$tmp/err-$system" | cut -c1-200))"
    unverified=$((unverified + 1))
    continue
  fi
  ok=1
  for item in "${required[@]}"; do
    path="${item%:*}" want="${item##*:}"
    got="$(git -C "$work" cat-file -t "FETCH_HEAD:$path" 2>/dev/null || echo missing)"
    if [[ "$got" != "$want" ]]; then
      echo "FAIL       $system: $path is $got at ${commit:0:12}, want $want"
      ok=0
    fi
  done
  if [[ "$ok" == 1 ]]; then
    git -C "$work" checkout -q FETCH_HEAD -- api/openapi.yaml
    if ! (cd "$here" && "${GO:-go}" run ./scripts/contracts openapi "$work/api/openapi.yaml" | sed 's/^/    /'); then
      ok=0
    fi
  fi
  if [[ "$ok" == 1 ]]; then
    echo "ok         $system layout at ${commit:0:12}"
    checked=$((checked + 1))
  else
    failed=$((failed + 1))
  fi
done
echo "$checked systems' layout checked, $failed failed, $unverified unverified, $unpinned unpinned"
if [[ "$failed" -gt 0 ]]; then exit 1; fi
if [[ "$unverified" -gt 0 && "${REQUIRE_MIRRORS:-0}" == "1" ]]; then exit 1; fi
exit 0

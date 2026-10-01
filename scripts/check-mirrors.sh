#!/usr/bin/env bash
# Re-fetch every mirror at its pinned commit and fail on any byte
# difference (docs/PLAN.md §3.2). A mirror is schemas/<system>/ or
# api/<system>/ with a SOURCE file; one whose SOURCE names no commit is
# unpinned and holds no copy.
#
#   scripts/check-mirrors.sh [--root DIR]
#
# Online. When a pinned mirror cannot be fetched it is reported as
# "unverified", never as "ok" (LESSONS E-04); REQUIRE_MIRRORS=1 makes that
# a failure (main-branch CI). The last line always says how many mirrors
# were checked, including zero.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib-mirror.sh
. "$here/scripts/lib-mirror.sh"

root="$here"
if [[ "${1:-}" == "--root" ]]; then root="$2"; fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
checked=0 unpinned=0 failed=0 unverified=0
shopt -s nullglob
for src in "$root"/schemas/*/SOURCE "$root"/api/*/SOURCE; do
  # A SOURCE file, not a directory: on a case-insensitive file system
  # schemas/common/source/ (the source/status schema) matches the glob.
  [[ -f "$src" ]] || continue
  dir="$(dirname "$src")"
  name="${dir#"$root"/}"
  repo="$(source_get "$src" repo)"
  commit="$(source_get "$src" commit)"
  path="$(source_get "$src" path)"
  if [[ -z "$commit" ]]; then
    echo "unpinned   $name (no commit in SOURCE; nothing mirrored)"
    unpinned=$((unpinned + 1))
    continue
  fi
  work="$tmp/$checked-$unverified-$failed-${name//\//-}"
  mkdir -p "$work/src" "$work/want" "$work/have"
  if ! fetch_at "$repo" "$commit" "$work/src" >/dev/null 2>"$work/err" \
     || ! checkout_path "$work/src" "$path" 2>>"$work/err"; then
    echo "unverified $name: could not fetch $path at ${commit:0:12} ($(tr '\n' ' ' < "$work/err" | cut -c1-200))"
    unverified=$((unverified + 1))
    continue
  fi
  if [[ -d "$work/src/$path" ]]; then
    cp -R "$work/src/$path/." "$work/want/"
  else
    cp "$work/src/$path" "$work/want/"
  fi
  (cd "$dir" && find . -mindepth 1 -maxdepth 1 ! -name SOURCE ! -name README.md -exec cp -R {} "$work/have/" \;)
  rc=0
  diff -r "$work/want" "$work/have" >"$work/diff" || rc=$?
  case "$rc" in
    0) echo "ok         $name matches $path at ${commit:0:12}"; checked=$((checked + 1)) ;;
    1) echo "MISMATCH   $name differs from $path at ${commit:0:12}:"; sed 's/^/    /' "$work/diff"
       failed=$((failed + 1)) ;;
    *) echo "error      $name: diff failed ($rc)"; failed=$((failed + 1)) ;;
  esac
done
echo "$checked mirrors checked and identical, $failed differ, $unverified unverified, $unpinned unpinned"
if [[ "$failed" -gt 0 ]]; then exit 1; fi
if [[ "$unverified" -gt 0 && "${REQUIRE_MIRRORS:-0}" == "1" ]]; then exit 1; fi
exit 0

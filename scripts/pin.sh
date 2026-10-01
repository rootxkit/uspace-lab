#!/usr/bin/env bash
# Pin a system's mirror: fetch its schemas/ and api/openapi.yaml at a
# commit, replace the copies under schemas/<system>/ and api/<system>/,
# write each SOURCE and print the diff against the previous copy.
#
#   scripts/pin.sh [options] <system> <ref>
#
#   <ref>              a full commit SHA, a tag or a branch; SOURCE always
#                      records the full SHA it resolved to.
#   --out DIR          write under DIR instead of this repository (a dry
#                      run: DIR/schemas/<system>/, DIR/api/<system>/).
#   --repo URL         the owning repository (default: SOURCE's repo, else
#                      https://github.com/rootxkit/uspace-<system>.git).
#   --schemas-path P   path of the schemas in the owning repo (default schemas).
#   --api-path P       path of the OpenAPI file (default api/openapi.yaml).
#
# A pin is its own `build(contracts): pin <system> at <short commit>` commit
# (decision record §4.3), with the regenerated index and clients
# (`make index clients`). A path missing at the commit is an error, never
# an empty mirror.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib-mirror.sh
. "$here/scripts/lib-mirror.sh"

out="$here" repo="" schemas_path="schemas" api_path="api/openapi.yaml"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --repo) repo="$2"; shift 2 ;;
    --schemas-path) schemas_path="$2"; shift 2 ;;
    --api-path) api_path="$2"; shift 2 ;;
    -h|--help) sed -n '2,23p' "$0"; exit 0 ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) break ;;
  esac
done
if [[ $# -ne 2 ]]; then
  echo "usage: scripts/pin.sh [--out DIR] [--repo URL] [--schemas-path P] [--api-path P] <system> <ref>" >&2
  exit 2
fi
system="$1" ref="$2"
if [[ ! "$system" =~ ^[a-z][a-z0-9-]*$ ]]; then
  echo "error: system name $system is not lower-case letters, digits and hyphens" >&2
  exit 2
fi
if [[ "$out" == "$here" && ! -f "$here/api/$system/SOURCE" ]]; then
  echo "error: api/$system/SOURCE does not exist: $system is not a mirrored system (use --out for a dry run)" >&2
  exit 2
fi
if [[ -z "$repo" && -f "$out/api/$system/SOURCE" ]]; then
  repo="$(source_get "$out/api/$system/SOURCE" repo)"
fi
repo="${repo:-https://github.com/rootxkit/uspace-$system.git}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/src"
echo "== fetching $repo at $ref"
commit="$(fetch_at "$repo" "$ref" "$tmp/src")"
fetched_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "   resolved to $commit"

pin_one() {
  local kind="$1" path="$2"
  local dst="$out/$kind/$system"
  checkout_path "$tmp/src" "$path"
  mkdir -p "$dst" "$tmp/old/$kind" "$tmp/new/$kind"
  # The previous copy, without the layout files, for the diff.
  (cd "$dst" && find . -mindepth 1 -maxdepth 1 ! -name SOURCE ! -name README.md -exec cp -R {} "$tmp/old/$kind/" \;)
  if [[ -d "$tmp/src/$path" ]]; then
    cp -R "$tmp/src/$path/." "$tmp/new/$kind/"
  else
    cp "$tmp/src/$path" "$tmp/new/$kind/"
  fi
  find "$dst" -mindepth 1 -maxdepth 1 ! -name SOURCE ! -name README.md -exec rm -rf {} +
  cp -R "$tmp/new/$kind/." "$dst/"
  cat > "$dst/SOURCE" <<EOF
# Read-only mirror. Written by scripts/pin.sh; never edited by hand.
repo = $repo
commit = $commit
path = $path
fetched_at = $fetched_at
EOF
  local n
  n="$(find "$tmp/new/$kind" -type f | wc -l | tr -d ' ')"
  echo "== $kind/$system: $n files from $path at ${commit:0:12}"
  local rc=0
  diff -ru "$tmp/old/$kind" "$tmp/new/$kind" || rc=$?
  case "$rc" in
    0) echo "   no change against the previous copy" ;;
    1) ;;
    *) echo "error: diff failed ($rc)" >&2; return "$rc" ;;
  esac
}

pin_one schemas "$schemas_path"
pin_one api "$api_path"
echo "pinned $system at ${commit:0:12} under $out"

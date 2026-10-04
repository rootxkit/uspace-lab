#!/usr/bin/env bash
# WP-L3: build the self-hosted basemap bundle (decision record M38).
#
#   basemap/build.sh OUT_DIR [BUILD]
#
#   OUT_DIR  the bundle directory, replaced when the build succeeds
#   BUILD    a Protomaps daily build, YYYYMMDD. Default: the newest listed.
#
# The bbox and zoom policy come from basemap/regions.yaml, the pinned
# fonts and sprites from basemap/inputs.yaml, the tool pins from
# basemap/tools.env; nothing of them is written here (INV-03).
#
# Produces, the layout uspace-ui reads under /basemap/ (its PLAN §6.3):
#   OUT_DIR/basemap.pmtiles            one archive: the country to its
#                                      max zoom, the city boxes above it
#   OUT_DIR/fonts/<fontstack>/<range>.pbf  glyphs drawn by font-maker from
#                                      Noto Sans + Noto Sans Georgian
#   OUT_DIR/fonts/OFL.txt              the font licence
#   OUT_DIR/sprites/v4/                the Protomaps icon sheets + licence
#   OUT_DIR/SOURCE.json                where every byte came from, with
#                                      the OSM data date and tool versions
#
# go-pmtiles extract takes one max zoom per run, so the per-region
# policy is several extracts (the country, then one per zoom above it
# over the city boxes reaching that zoom), disjoint by construction,
# joined with `pmtiles merge` and checked with `pmtiles verify`.
#
# Environment: BASEMAP_PROFILE (bundle | storybook; storybook.sh sets
# it), BASEMAP_REGIONS, BASEMAP_INPUTS (default the files beside this
# script), BASEMAP_TOOLS (default basemap/.tools; filled by tools.sh).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/.." && pwd)"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  sed -n '2,32p' "$0" >&2
  exit 2
fi
out="$1"
build="${2:-}"
profile="${BASEMAP_PROFILE:-bundle}"
regions="${BASEMAP_REGIONS:-$here/regions.yaml}"
inputs="${BASEMAP_INPUTS:-$here/inputs.yaml}"
tools="${BASEMAP_TOOLS:-$here/.tools}"
started=$(date +%s)

"$here/tools.sh" "$tools"
# shellcheck source=/dev/null
. "$tools/versions.env"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
(cd "$repo" && go build -buildvcs=false -o "$work/basemap" ./cmd/basemap)
bm="$work/basemap"

# Each step writes a file first: a failure inside a process
# substitution would not stop the script.
"$bm" build-info --inputs "$inputs" --out "$work/build.json" ${build:+"$build"} >"$work/build.tsv"
IFS=$'\t' read -r build url <"$work/build.tsv"
echo "build.sh: profile $profile, Protomaps build $build ($url)"

staging="$work/bundle"
mkdir -p "$staging/fonts"

"$bm" plan --regions "$regions" --profile "$profile" --work "$work" >"$work/plan.tsv"
parts=()
while IFS=$'\t' read -r name minz maxz bbox region; do
  args=(extract "$url" "$work/$name.pmtiles" --minzoom="$minz" --maxzoom="$maxz" --download-threads=8)
  if [[ "$bbox" != "-" ]]; then args+=(--bbox="$bbox"); fi
  if [[ "$region" != "-" ]]; then args+=(--region="$region"); fi
  if [[ "$bbox" != "-" ]]; then what="bbox $bbox"; else what="region $region"; fi
  echo "build.sh: extract $name z$minz-$maxz over $what"
  "$PMTILES" "${args[@]}" 2>"$work/$name.log" || { cat "$work/$name.log" >&2; exit 1; }
  # The log carries a progress bar redrawn with carriage returns; keep
  # the summary lines.
  tr '\r' '\n' <"$work/$name.log" | grep -E "fetching [0-9]+ tiles|transferred" | sed 's/^.*\.go:[0-9]*: /  /' || true
  parts+=("$work/$name.pmtiles")
done <"$work/plan.tsv"
if [[ ${#parts[@]} -eq 0 ]]; then
  echo "build.sh: the plan has no extract" >&2
  exit 1
fi
if [[ ${#parts[@]} -eq 1 ]]; then
  mv "${parts[0]}" "$staging/basemap.pmtiles"
else
  echo "build.sh: merge ${#parts[@]} extracts"
  "$PMTILES" merge "${parts[@]}" "$staging/basemap.pmtiles" 2>"$work/merge.log" || { cat "$work/merge.log" >&2; exit 1; }
fi
"$PMTILES" verify "$staging/basemap.pmtiles" 2>"$work/verify.log" || { cat "$work/verify.log" >&2; exit 1; }
echo "build.sh: pmtiles verify ok"

"$bm" fetch --inputs "$inputs" --dest "$work/inputs" --bundle "$staging"
"$bm" fontstacks --inputs "$inputs" --fetched "$work/inputs" >"$work/fontstacks.tsv"
i=0
while IFS=$'\t' read -r -a stack; do
  i=$((i + 1))
  name="${stack[0]}"
  # font-maker refuses an output directory that exists.
  "$FONT_MAKER" --name "$name" "$work/glyphs-$i" "${stack[@]:1}" >"$work/glyphs-$i.log"
  mv "$work/glyphs-$i/$name" "$staging/fonts/$name"
  echo "build.sh: fontstack \"$name\": $(find "$staging/fonts/$name" -name '*.pbf' | wc -l | tr -d ' ') range files from $((${#stack[@]} - 1)) fonts"
done <"$work/fontstacks.tsv"

"$bm" source --regions "$regions" --inputs "$inputs" --profile "$profile" \
  --build-info "$work/build.json" --fetched "$work/inputs" \
  --archive "$staging/basemap.pmtiles" \
  --tool "$PMTILES_BUILT" --tool "$FONT_MAKER_BUILT" \
  --tool "go=$(go env GOVERSION)" \
  --tool "uspace-lab=$(git -C "$repo" describe --always --dirty --abbrev=40 2>/dev/null || echo unknown)" \
  --out "$staging/SOURCE.json"

mkdir -p "$(dirname "$out")"
rm -rf "$out"
mv "$staging" "$out"
elapsed=$(($(date +%s) - started))
echo "build.sh: done in ${elapsed}s: $(du -sb "$out" | cut -f1) bytes ($(du -sh "$out" | cut -f1)) in $out"

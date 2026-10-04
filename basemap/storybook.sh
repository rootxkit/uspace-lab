#!/usr/bin/env bash
# WP-L3: the Tbilisi-only extract for uspace-ui's stories and browser
# tests (ui WP-3), built by the same script as the bundle, with the same
# fonts and sprites, and verified against the storybook budget.
#
#   basemap/storybook.sh OUT_DIR [BUILD]
#
# The box and max zoom are `storybook` in basemap/regions.yaml. OUT_DIR
# gets the /basemap/ layout (basemap.pmtiles, fonts/, sprites/v4/,
# SOURCE.json); copy it into the kit (today browser/public/basemap/).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  sed -n '2,10p' "$0" >&2
  exit 2
fi
export BASEMAP_PROFILE=storybook
"$here/build.sh" "$@"
"$here/verify.sh" "$1"
echo "storybook.sh: $(du -sb "$1" | cut -f1) bytes ($(du -sh "$1" | cut -f1)) in $1"

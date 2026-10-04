#!/usr/bin/env bash
# WP-L3: pack a verified bundle into the release asset.
#
#   basemap/package.sh OUT_DIR ASSET.tar.gz
#
# Writes ASSET.tar.gz (the bundle's files at the archive root, so that
# `tar -xzf ASSET.tar.gz -C /srv/basemap` gives /srv/basemap/basemap.pmtiles)
# and ASSET.tar.gz.sha256 in `sha256sum -c` format. The archive is
# reproducible for the same files: names sorted, owner and group 0,
# every mtime set to SOURCE.json's fetched_at, gzip without a name or
# time stamp. Needs GNU tar.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  sed -n '2,11p' "$0" >&2
  exit 2
fi
dir="$1"
asset="$2"
[[ -f "$dir/SOURCE.json" && -f "$dir/basemap.pmtiles" ]] || { echo "package.sh: $dir is not a bundle" >&2; exit 1; }
tar --version 2>/dev/null | grep -q "GNU tar" || { echo "package.sh: GNU tar required" >&2; exit 1; }

stamp="$(sed -n 's/^  "fetched_at": "\(.*\)",$/\1/p' "$dir/SOURCE.json")"
[[ -n "$stamp" ]] || { echo "package.sh: no fetched_at in $dir/SOURCE.json" >&2; exit 1; }

mkdir -p "$(dirname "$asset")"
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="$stamp" \
  --format=gnu -C "$dir" -cf - . | gzip -n -6 >"$asset.part"
mv "$asset.part" "$asset"
(cd "$(dirname "$asset")" && sha256sum "$(basename "$asset")" >"$(basename "$asset").sha256")
echo "package.sh: $(wc -c <"$asset" | tr -d ' ') bytes, $(cat "$asset.sha256")"

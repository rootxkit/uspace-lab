#!/usr/bin/env bash
# WP-L3: install the pinned basemap tools into DIR (basemap/tools.env).
#
#   basemap/tools.sh DIR
#
# DIR/pmtiles     go-pmtiles at GO_PMTILES_VERSION (go install; the Go
#                 checksum database verifies the module)
# DIR/font-maker  protomaps/font-maker at FONT_MAKER_COMMIT, compiled
#                 (needs git, cmake, clang, the FreeType and Boost
#                 headers: apt-get install cmake clang libfreetype-dev
#                 libboost-dev)
# DIR/versions.env  the versions actually installed, read back from the
#                 binaries and the checkout, for SOURCE.json (E-05)
#
# Re-running with the same pins reuses what is there.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=basemap/tools.env
. "$here/tools.env"

if [[ $# -ne 1 ]]; then
  sed -n '2,18p' "$0" >&2
  exit 2
fi
mkdir -p "$1"
dir="$(cd "$1" && pwd)"
for tool in go git cmake clang++; do
  command -v "$tool" >/dev/null || { echo "tools.sh: $tool not found" >&2; exit 1; }
done

exe=""
case "$(go env GOOS)" in windows) exe=".exe" ;; esac

# go-pmtiles. `go install` names the binary after the module (go-pmtiles).
pmtiles_version() {
  go version -m "$dir/pmtiles$exe" 2>/dev/null | awk '$1 == "mod" && $2 == "github.com/protomaps/go-pmtiles" { print $3 }'
}
if [[ "$(pmtiles_version)" != "$GO_PMTILES_VERSION" ]]; then
  echo "tools.sh: installing go-pmtiles $GO_PMTILES_VERSION"
  GOBIN="$dir" GOFLAGS='' go install "github.com/protomaps/go-pmtiles@$GO_PMTILES_VERSION"
  mv -f "$dir/go-pmtiles$exe" "$dir/pmtiles$exe"
fi
got_pmtiles="$(pmtiles_version)"
if [[ "$got_pmtiles" != "$GO_PMTILES_VERSION" ]]; then
  echo "tools.sh: go-pmtiles reports ${got_pmtiles:-no version}, pinned $GO_PMTILES_VERSION" >&2
  exit 1
fi

# font-maker.
src="$dir/font-maker-src"
if [[ ! -x "$dir/font-maker" || "$(cat "$dir/font-maker.commit" 2>/dev/null)" != "$FONT_MAKER_COMMIT" ]]; then
  echo "tools.sh: building font-maker $FONT_MAKER_COMMIT"
  rm -rf "$src"
  git init -q "$src"
  git -C "$src" remote add origin "$FONT_MAKER_REPO"
  git -C "$src" fetch -q --depth 1 origin "$FONT_MAKER_COMMIT"
  git -C "$src" checkout -q FETCH_HEAD
  git -C "$src" submodule -q update --init --depth 1
  cmake -S "$src" -B "$src/build" -DCMAKE_BUILD_TYPE=Release >"$src/cmake.log"
  cmake --build "$src/build" -j "$(nproc 2>/dev/null || echo 2)" >"$src/make.log"
  cp "$src/build/font-maker" "$dir/font-maker"
  git -C "$src" rev-parse HEAD >"$dir/font-maker.commit"
fi
got_fm="$(cat "$dir/font-maker.commit")"
if [[ "$got_fm" != "$FONT_MAKER_COMMIT" ]]; then
  echo "tools.sh: font-maker built from $got_fm, pinned $FONT_MAKER_COMMIT" >&2
  exit 1
fi

cat >"$dir/versions.env" <<EOF
PMTILES="$dir/pmtiles$exe"
FONT_MAKER="$dir/font-maker"
PMTILES_BUILT="github.com/protomaps/go-pmtiles=$got_pmtiles"
FONT_MAKER_BUILT="github.com/protomaps/font-maker=$got_fm"
EOF
echo "tools.sh: go-pmtiles $got_pmtiles, font-maker $got_fm in $dir"

#!/usr/bin/env bash
# WP-L3: verify a basemap bundle before it is published or served.
#
#   basemap/verify.sh OUT_DIR
#
# Checks, each printed with what it measured (cmd/basemap verify):
#   - the total size is within the profile's budget in basemap/budget.txt;
#   - every fontstack of basemap/inputs.yaml draws every Georgian code
#     point of Asomtavruli, Mkhedruli, Mtavruli and Nuskhuri, and Basic
#     Latin: the range PBFs are decoded and the code points looked up,
#     the file names are not trusted;
#   - every tile obeys basemap/regions.yaml: inside the bounds, nothing
#     above the country's max zoom outside a city box allowing it, and
#     each city's centre tile present at its max zoom;
#   - SOURCE.json is complete, its hashes match the archive and the
#     shipped files;
#   - a Caddy file_server serving OUT_DIR answers a byte range of
#     basemap.pmtiles with 206 (the deployment serves /basemap/ the same
#     way, M38). Caddy is the `caddy` on PATH, else CADDY_IMAGE from
#     basemap/tools.env in Docker. With neither, the check fails; it is
#     never skipped.
#
# Exits non-zero when any check fails. Environment: BASEMAP_PROFILE
# (bundle | storybook), BASEMAP_REGIONS, BASEMAP_INPUTS, BASEMAP_BUDGET,
# BASEMAP_VERIFY_PORT (default 18780).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/.." && pwd)"
# shellcheck source=basemap/tools.env
. "$here/tools.env"

if [[ $# -ne 1 ]]; then
  sed -n '2,27p' "$0" >&2
  exit 2
fi
[[ -d "$1" ]] || { echo "verify.sh: $1 is not a directory" >&2; exit 2; }
dir="$(cd "$1" && pwd)"
profile="${BASEMAP_PROFILE:-bundle}"
regions="${BASEMAP_REGIONS:-$here/regions.yaml}"
inputs="${BASEMAP_INPUTS:-$here/inputs.yaml}"
budget="${BASEMAP_BUDGET:-$here/budget.txt}"
port="${BASEMAP_VERIFY_PORT:-18780}"

# Paths handed to Go and Docker: native form under Git Bash on Windows.
native() {
  if command -v cygpath >/dev/null; then cygpath -m "$1"; else printf '%s' "$1"; fi
}

pid="" cid=""
# shellcheck disable=SC2317 # run by the EXIT trap
stop_server() {
  if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  if [[ -n "$cid" ]]; then docker rm -f "$cid" >/dev/null 2>&1 || true; fi
}
trap stop_server EXIT

if command -v caddy >/dev/null; then
  server="caddy $(caddy version | cut -d' ' -f1)"
  caddy file-server --root "$dir" --listen "127.0.0.1:$port" >/dev/null 2>&1 &
  pid=$!
elif command -v docker >/dev/null; then
  server="$CADDY_IMAGE"
  cid="$(MSYS_NO_PATHCONV=1 docker run -d --rm -p "127.0.0.1:$port:8080" \
    -v "$(native "$dir"):/srv:ro" "$CADDY_IMAGE" caddy file-server --root /srv --listen :8080)"
else
  echo "verify.sh: neither caddy nor docker found: the range request cannot be made" >&2
  exit 1
fi

base="http://127.0.0.1:$port"
ready=""
for _ in $(seq 1 40); do
  if curl -fsS -o /dev/null "$base/SOURCE.json" 2>/dev/null; then ready=1; break; fi
  sleep 0.25
done
if [[ -z "$ready" ]]; then
  echo "verify.sh: the file server ($server) did not answer on $base" >&2
  if [[ -n "$cid" ]]; then docker logs "$cid" >&2 || true; fi
  exit 1
fi
echo "verify.sh: $dir served by $server on $base"

status=0
(cd "$repo" && go run ./cmd/basemap verify \
  --regions "$(native "$regions")" --inputs "$(native "$inputs")" --budget "$(native "$budget")" \
  --profile "$profile" --range-url "$base/basemap.pmtiles" "$(native "$dir")") || status=$?
exit "$status"

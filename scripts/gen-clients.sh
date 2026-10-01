#!/usr/bin/env bash
# Regenerate the clients of every pinned api/<system>/openapi.yaml:
#   api/clients/go/<system>/client.gen.go  oapi-codegen v2 (the go.mod
#                                          `tool` directive pins it), client
#                                          and types only
#   api/clients/ts/<system>.d.ts           openapi-typescript at
#                                          OPENAPI_TYPESCRIPT_VERSION (until
#                                          uspace-ui ships uspace-ui-gen-api,
#                                          ui Q14)
# Clients of systems no longer pinned are removed. Generated files are
# committed; CI runs this and fails if the tree changes. The last line
# says how many systems were generated, including zero.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib-mirror.sh
. "$here/scripts/lib-mirror.sh"
cd "$here"
OPENAPI_TYPESCRIPT_VERSION="${OPENAPI_TYPESCRIPT_VERSION:-7.13.0}"
GO="${GO:-go}"

generated=0 skipped=0
for src in api/*/SOURCE; do
  # A SOURCE file, not a directory (a case-insensitive file system lets
  # a directory named source match the glob).
  [[ -f "$src" ]] || continue
  system="$(basename "$(dirname "$src")")"
  if [[ -z "$(source_get "$src" commit)" ]]; then
    echo "unpinned   $system: no client"
    rm -rf "api/clients/go/$system" "api/clients/ts/$system.d.ts"
    skipped=$((skipped + 1))
    continue
  fi
  spec="api/$system/openapi.yaml"
  mkdir -p "api/clients/go/$system" api/clients/ts
  "$GO" tool oapi-codegen -generate types,client -package "$system" \
    -o "api/clients/go/$system/client.gen.go" "$spec"
  npx --yes "openapi-typescript@$OPENAPI_TYPESCRIPT_VERSION" "$spec" \
    -o "api/clients/ts/$system.d.ts"
  echo "generated  $system: api/clients/go/$system/client.gen.go, api/clients/ts/$system.d.ts"
  generated=$((generated + 1))
done
echo "$generated systems' clients generated, $skipped unpinned"

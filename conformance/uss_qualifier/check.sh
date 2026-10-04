#!/usr/bin/env bash
# make conformance-qualifier-check: every configuration coverage.yaml
# names is rendered with placeholder values and handed to the pinned
# uss_qualifier image with --exit-before-execution, which validates it
# against the configuration schema of that commit and resolves its $refs
# (E-04: the keys are checked by the tool that reads them, not from
# memory). Also checks that the image embeds SOURCE's commit and that
# compose.yaml uses SOURCE's digest. Needs Docker.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
lab="$(cd "$here/../.." && pwd)"
export MSYS_NO_PATHCONV=1
# A bind-mount source as Docker on this host reads it (Git Bash on
# Windows hands Docker a Windows path).
hostpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s' "$1"; fi; }
src() { sed -n "s/^$1 = //p" "$here/SOURCE"; }
image="$(src image)"
digest="$(src image_digest)"
commit="$(src commit)"
ref="${image%:*}@${digest}"
fails=0

if grep -q "$digest" "$here/compose.yaml"; then
  echo "ok   compose.yaml uses $digest"
else
  echo "FAIL compose.yaml does not use $digest"
  fails=$((fails + 1))
fi
docker pull -q "$ref" >/dev/null
embedded="$(docker image inspect "$ref" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^GIT_COMMIT_HASH=//p')"
if [ "$embedded" != "$commit" ]; then
  echo "FAIL $ref embeds commit '$embedded', SOURCE pins $commit"
  fails=$((fails + 1))
else
  echo "ok   $ref embeds commit $commit"
fi

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
export QUALIFIER_DSS_HOST=dss QUALIFIER_DSS_URL=http://dss:8082 QUALIFIER_PARTICIPANT=check-participant \
  QUALIFIER_MOCK_RIDSP_URL=http://mock-ridsp QUALIFIER_MOCK_RIDDP_URL=http://mock-riddp QUALIFIER_MOCK_SCD_URL=http://mock-scd \
  QUALIFIER_TARGET_URL_REGEX='http://target.*' QUALIFIER_MOCK_URL_REGEX='http://mock-.*' \
  QUALIFIER_TARGET_INTERFACE_URL=http://target:8080/interface
configs="$(sed -n 's/^    config: //p' "$here/coverage.yaml")"
[ -n "$configs" ] || { echo "FAIL coverage.yaml names no configuration"; exit 1; }
for config in $configs; do
  out="$scratch/${config%.yaml}"
  mkdir -p "$out"
  (cd "$lab" && "${GO:-go}" run ./cmd/conformance qualifier-render --config "$config" --out "$(hostpath "$out")") >/dev/null
  rc=0
  log="$(docker run --rm -e AUTH_SPEC='NoAuth()' -v "$(hostpath "$out"):/cfg:ro" -w /app/monitoring/uss_qualifier "$ref" \
    uv run main.py --config "file:///cfg/$config" --exit-before-execution 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ] && grep -q "Exiting because --exit-before-execution specified" <<<"$log"; then
    echo "ok   $config: $(grep -o 'Baseline signature: TB-[0-9a-f]*' <<<"$log")"
  else
    echo "FAIL $config (exit $rc):"
    tail -n 20 <<<"$log" | sed 's/^/  /'
    fails=$((fails + 1))
  fi
done
if [ "$fails" -ne 0 ]; then
  echo "qualifier check: $fails failed"
  exit 1
fi
echo "qualifier check: every configuration validated by uss_qualifier at $commit"

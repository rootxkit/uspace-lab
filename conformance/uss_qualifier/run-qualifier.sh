#!/usr/bin/env bash
# Run one uss_qualifier configuration of conformance/uss_qualifier/ with
# the pinned InterUSS image (SOURCE: by digest, never patched) and write
# its report under OUT:
#
#   conformance/uss_qualifier/run-qualifier.sh <config> <out dir>
#
# The configuration is rendered first (cmd/conformance qualifier-render)
# from the QUALIFIER_* variables below, into OUT/config/. The report is
# OUT/qualifier/report.json; fold it into a run with
#   CONFORMANCE_QUALIFIER_REPORTS="<REQ>=OUT/qualifier/report.json" make conformance TARGET=...
#
# Environment (conformance/uss_qualifier/README.md):
#   QUALIFIER_PARTICIPANT, QUALIFIER_TARGET_INTERFACE_URL,
#   QUALIFIER_TARGET_URL_REGEX   the system under test
#   QUALIFIER_DSS_URL, QUALIFIER_DSS_HOST  the lab DSS as the containers reach it
#   QUALIFIER_MOCK_RIDSP_URL, QUALIFIER_MOCK_RIDDP_URL, QUALIFIER_MOCK_SCD_URL,
#   QUALIFIER_MOCK_URL_REGEX     the mock USS (compose.yaml)
#   QUALIFIER_TOKEN_URL          the lab issuer's token endpoint as the containers reach it
#   LAB_SECRETS_JSON             the issuer's client-secrets.json (lab-01's
#                                secret; default deploy/local/client-secrets.json)
#   QUALIFIER_DOCKER_NETWORK     the network the containers join (default uspace-lab_lab)
#   QUALIFIER_TIMEOUT_S          bound on the run (default 3600)
# The secret reaches the container as the AUTH_SPEC environment
# variable, never in a file or on a command line.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
lab="$(cd "$here/../.." && pwd)"
export MSYS_NO_PATHCONV=1
# A bind-mount source as Docker on this host reads it (Git Bash on
# Windows hands Docker a Windows path).
hostpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s' "$1"; fi; }

config="${1:?usage: run-qualifier.sh <config> <out dir>}"
out="${2:?usage: run-qualifier.sh <config> <out dir>}"
[ -f "$here/$config" ] || { echo "run-qualifier: no configuration $here/$config" >&2; exit 2; }
command -v docker >/dev/null 2>&1 || { echo "run-qualifier: docker is required" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "run-qualifier: jq is required" >&2; exit 2; }

src() { sed -n "s/^$1 = //p" "$here/SOURCE"; }
image="$(src image)"
digest="$(src image_digest)"
commit="$(src commit)"
ref="${image%:*}@${digest}"

secrets="${LAB_SECRETS_JSON:-$lab/deploy/local/client-secrets.json}"
[ -r "$secrets" ] || { echo "run-qualifier: $secrets is not readable (make dss-up writes it)" >&2; exit 2; }
secret="$(jq -r '."lab-01" // empty' "$(hostpath "$secrets")")"
[ -n "$secret" ] || { echo "run-qualifier: $secrets has no lab-01 secret" >&2; exit 2; }
: "${QUALIFIER_TOKEN_URL:?set QUALIFIER_TOKEN_URL (the lab issuer as the containers reach it)}"
AUTH_SPEC="ClientIdClientSecret(token_endpoint=${QUALIFIER_TOKEN_URL},client_id=lab-01,client_secret=${secret},send_request_as_data=true)"
export AUTH_SPEC

mkdir -p "$out/config" "$out/qualifier"
(cd "$lab" && "${GO:-go}" run ./cmd/conformance qualifier-render --config "$config" --out "$(hostpath "$out/config")") >/dev/null

echo "run-qualifier: $config with $ref (commit $commit) -> $out/qualifier"
rc=0
timeout "${QUALIFIER_TIMEOUT_S:-3600}" docker run --rm \
  --network "${QUALIFIER_DOCKER_NETWORK:-uspace-lab_lab}" \
  -e AUTH_SPEC -e PYTHONUNBUFFERED=1 \
  -u "$(id -u):$(id -g)" \
  -v "$(hostpath "$out/config"):/cfg:ro" -v "$(hostpath "$out/qualifier"):/out" \
  -w /app/monitoring/uss_qualifier \
  "$ref" uv run main.py --config "file:///cfg/$config" --output-path /out || rc=$?
if [ ! -f "$out/qualifier/report.json" ]; then
  echo "run-qualifier: uss_qualifier exited $rc and wrote no report.json" >&2
  exit 1
fi
# uss_qualifier exits non-zero when a check fails; the report says which
# and cmd/conformance judges it. Say so rather than hide it.
echo "run-qualifier: uss_qualifier exited $rc; report $out/qualifier/report.json"

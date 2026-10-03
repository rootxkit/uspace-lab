#!/usr/bin/env bash
# make sim-ussp-up (docs/WORKPACKAGES/WP-L5.md): run the peer USSP
# (cmd/sim-ussp, profile sim) against the lab stack's real DSS with a
# sim-ussp-01 token from the lab issuer, and check the DSS took its
# writes. Starts the DSS and the issuer too if they are not up.
#
#   deploy/sim-ussp-up.sh       build, start, wait, check, report
#
# It succeeds only when (LESSONS E-02, E-04: what the DSS answered, not
# what was sent):
#   1. sim-ussp logged "ISA written" and "operational intent reference
#      written" with the id, version and OVN the DSS returned, and is
#      still running (serving its endpoints);
#   2. a search of the DSS over the SIM_USSP_INTENT area, made by the
#      issuer's probe with its own token, finds at least one operational
#      intent reference there, where the dss-up proof found none.
#
# Environment: COMPOSE_PROJECT_NAME (default uspace-lab).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"
export MSYS_NO_PATHCONV=1

command -v docker >/dev/null 2>&1 || { echo "sim-ussp-up: docker is required" >&2; exit 1; }

if [ ! -f .env ]; then
  cp .env.example .env
  echo "sim-ussp-up: created deploy/.env from deploy/.env.example"
fi
# A deploy/.env made before the sim profile existed lacks its variables:
# add each one .env.example has and .env does not, and say which.
while IFS= read -r line; do
  name="${line%%=*}"
  if ! grep -q "^${name}=" .env; then
    printf '%s\n' "$line" >> .env
    echo "sim-ussp-up: added ${name} to deploy/.env from deploy/.env.example"
  fi
done < <(grep -E '^[A-Z][A-Z0-9_]*=' .env.example)

envval() { sed -n "s/^$1=//p" .env | tail -n 1; }
state_dir="$(envval LAB_STATE_DIR)"
state_dir="${state_dir:-./local}"
mkdir -p "$state_dir/public"
LAB_UID="$(id -u)"
LAB_GID="$(id -g)"
export LAB_UID LAB_GID

project="${COMPOSE_PROJECT_NAME:-uspace-lab}"
dc() { docker compose -p "$project" -f compose.yaml --env-file .env --profile dss --profile issuer --profile sim "$@"; }

fail() {
  echo "sim-ussp-up: $*" >&2
  dc logs --no-color --tail 50 sim-ussp >&2 || true
  exit 1
}

# A previous sim-ussp container goes first, so the logs read below are
# this run's and not an earlier run's success.
dc rm --stop --force sim-ussp >/dev/null 2>&1 || true
# sim-ussp exits 1 when the DSS refuses a write, which fails --wait.
dc up -d --build --wait --wait-timeout 600 || fail "the stack did not come up (a refused DSS write stops sim-ussp)"

# 1. Its own report of the DSS's answers.
logs=""
for _ in $(seq 1 60); do
  logs="$(dc logs --no-color sim-ussp 2>&1)"
  if grep -q '"msg":"ISA written"' <<<"$logs" && grep -q '"msg":"operational intent reference written"' <<<"$logs"; then
    break
  fi
  state="$(docker inspect -f '{{.State.Status}}' "$(dc ps -a -q sim-ussp)")"
  [ "$state" = running ] || fail "sim-ussp is $state before both writes were reported"
  sleep 1
done
grep -q '"msg":"operational intent reference written"' <<<"$logs" || fail "no write reported within 60 s"
state="$(docker inspect -f '{{.State.Status}}' "$(dc ps -a -q sim-ussp)")"
[ "$state" = running ] || fail "sim-ussp is $state after its writes; it should be serving"
grep -E '"msg":"(ISA written|operational intent reference written)"' <<<"$logs" | sed 's/^[^{]*/sim-ussp-up: /'

# 2. The DSS's own state, read back by another client.
IFS=, read -r lat lng radius low high <<<"$(envval SIM_USSP_INTENT)"
out="$(dc exec -T \
  -e DSS_PROBE_LAT_DEG="$lat" -e DSS_PROBE_LNG_DEG="$lng" -e DSS_PROBE_RADIUS_M="$radius" \
  -e DSS_PROBE_ALT_LOWER_WGS84_M="$low" -e DSS_PROBE_ALT_UPPER_WGS84_M="$high" \
  lab-issuer /lab-issuer probe)" || { printf '%s\n' "$out" >&2; fail "the read-back probe failed"; }
printf '%s\n' "$out"
n="$(sed -n 's/.*HTTP 200 (want 200), \([0-9][0-9]*\) operational intent references.*/\1/p' <<<"$out" | head -n 1)"
[ -n "$n" ] || fail "the read-back probe printed no count"
[ "$n" -ge 1 ] || fail "the DSS holds $n operational intent references in the SIM_USSP_INTENT area"
echo "sim-ussp-up: the DSS holds $n operational intent reference(s) in the SIM_USSP_INTENT area; sim-ussp is serving at http://sim-ussp:8093 on the lab network"

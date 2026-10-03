#!/usr/bin/env bash
# make dss-up (docs/WORKPACKAGES/WP-L2.md): bring up the DSS and the lab
# issuer, then prove them together. Exits non-zero unless every proof
# answered as expected (LESSONS E-02: the success line is printed only
# after the refusal it is measured against).
#
#   deploy/dss-up.sh            build, start, wait, prove, report
#
# Environment: COMPOSE_PROJECT_NAME (default uspace-lab).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"
# Git Bash on Windows rewrites container paths such as /lab-issuer.
export MSYS_NO_PATHCONV=1

command -v docker >/dev/null 2>&1 || { echo "dss-up: docker is required" >&2; exit 1; }

if [ ! -f .env ]; then
  cp .env.example .env
  echo "dss-up: created deploy/.env from deploy/.env.example"
fi

# The state directory the issuer writes (bind mount). Created here, owned
# by the invoking user, so the container (run as that user) can write it.
state_dir="$(sed -n 's/^LAB_STATE_DIR=//p' .env | tail -n 1)"
state_dir="${state_dir:-./local}"
mkdir -p "$state_dir/public"
LAB_UID="$(id -u)"
LAB_GID="$(id -g)"
export LAB_UID LAB_GID

project="${COMPOSE_PROJECT_NAME:-uspace-lab}"
dc() { docker compose -p "$project" -f compose.yaml --env-file .env --profile dss --profile issuer "$@"; }

start="$(date +%s)"
dc up -d --build --wait --wait-timeout 600
up_s=$(( $(date +%s) - start ))
echo "dss-up: stack healthy after ${up_s}s (build, pull, CockroachDB, migrations, DSS, issuer)"

secrets="$state_dir/client-secrets.json"
echo "dss-up: client secrets in deploy/${secrets#./} (git-ignored; printed once in 'docker compose logs lab-issuer' when generated)"

echo "dss-up: proving the DSS and the issuer together:"
dc exec -T lab-issuer /lab-issuer probe
echo "dss-up: all proofs passed"

echo "dss-up: memory and CPU per container (docker stats, one sample):"
ids="$(dc ps -q)"
# shellcheck disable=SC2086 # one argument per container id
docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' $ids

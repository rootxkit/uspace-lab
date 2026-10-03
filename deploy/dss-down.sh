#!/usr/bin/env bash
# make dss-down (docs/WORKPACKAGES/WP-L2.md): stop the DSS and the lab
# issuer and remove their containers, network and volumes (the DSS's
# datastore). The issuer's state directory (key, client secrets) is kept;
# remove deploy/local/ to start over with new ones.
#
# It reports success only after checking that no container or volume of
# the project is left (LESSONS E-02: a teardown that says it worked must
# have been checked).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"

project="${COMPOSE_PROJECT_NAME:-uspace-lab}"
env_file=.env
[ -f "$env_file" ] || env_file=.env.example
dc() { docker compose -p "$project" -f compose.yaml --env-file "$env_file" --profile dss --profile issuer "$@"; }

dc down --volumes --remove-orphans

left_containers="$(docker ps -a -q --filter "label=com.docker.compose.project=$project")"
left_volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")"
if [ -n "$left_containers" ] || [ -n "$left_volumes" ]; then
  echo "dss-down: still present for project $project:" >&2
  [ -z "$left_containers" ] || docker ps -a --filter "label=com.docker.compose.project=$project" >&2
  [ -z "$left_volumes" ] || echo "volumes: $left_volumes" >&2
  exit 1
fi
echo "dss-down: no container or volume of project $project is left (deploy/local/ kept)"

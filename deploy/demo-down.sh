#!/usr/bin/env bash
# make demo-down (docs/RUNBOOKS/demo.md): stop and remove every container,
# network and volume of the demo project (the DSS, the issuer, the four
# systems), then check that nothing of it is left (LESSONS E-02). Keeps
# DEMO_STATE_DIR (keys, passwords, CA); `--purge` removes it too, so the
# next make demo starts with new ones.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"
export MSYS_NO_PATHCONV=1
project="${COMPOSE_PROJECT_NAME:-uspace-demo}"
env_file=demo.env
[ -f "$env_file" ] || env_file=demo.env.example
val() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
state="$(val DEMO_STATE_DIR)"; state="${state:-./local-demo}"
pw="$state/passwords.env"
# Both files in deploy/, named relative to it: on Windows docker is a
# native program and cannot open the /tmp/... path mktemp gives under
# Git Bash with path conversion off (MSYS_NO_PATHCONV, above).
tmp="$(mktemp ./.demo-down-env.XXXXXX)"; fill="$(mktemp ./.demo-down-env.XXXXXX)"; trap 'rm -f "$tmp" "$fill"' EXIT
# A demo.env older than the example lacks the variables the compose
# files have come to require since, and compose then refuses to read the
# project even to take it down: the example's value fills each one
# demo.env does not set (read first, so demo.env wins).
if [ "$env_file" != demo.env.example ]; then
  while IFS= read -r line; do
    case "$line" in ''|'#'*) continue ;; esac
    grep -q "^${line%%=*}=" "$env_file" || printf '%s\n' "$line"
  done < demo.env.example > "$fill"
fi
args=(-p "$project" -f compose.yaml -f systems/compose.yaml --env-file "$fill" --env-file "$env_file")
# Without the generated passwords the file does not interpolate; any
# value will do to take the project down.
if [ -f "$pw" ]; then args+=(--env-file "$pw"); else
  for n in AUTHORITY_PG_PASSWORD CISP_PG_PASSWORD CISP_PG_API_PASSWORD CISP_PG_DELIVER_PASSWORD \
    USSP_PG_PASSWORD USSP_PG_API_PASSWORD USSP_PG_TSDB_PASSWORD ANSP_PG_PASSWORD CISP_SIGNING_KID; do echo "$n=x"; done > "$tmp"
  args+=(--env-file "$tmp")
fi
docker compose "${args[@]}" --profile dss --profile issuer --profile sim --profile systems down --volumes --remove-orphans

left_containers="$(docker ps -a -q --filter "label=com.docker.compose.project=$project")"
left_volumes="$(docker volume ls -q --filter "label=com.docker.compose.project=$project")"
if [ -n "$left_containers" ] || [ -n "$left_volumes" ]; then
  echo "demo-down: still present for project $project:" >&2
  [ -z "$left_containers" ] || docker ps -a --filter "label=com.docker.compose.project=$project" >&2
  [ -z "$left_volumes" ] || echo "volumes: $left_volumes" >&2
  exit 1
fi
if [ "${1:-}" = "--purge" ]; then
  rm -rf "$state"
  echo "demo-down: removed $state"
fi
echo "demo-down: no container or volume of project $project is left"

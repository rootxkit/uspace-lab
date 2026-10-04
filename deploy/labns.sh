#!/usr/bin/env bash
# Run a command (the scenario runner, curl) as a client of the demo
# stack (docs/RUNBOOKS/demo.md): the lab hosts resolve to 127.0.0.1,
# where the lab Caddy is published, and the lab CA is the trusted root.
# Linux or WSL only.
#
#   deploy/labns.sh [--env deploy/demo.env] <command> [args...]
#
# Nothing on the machine changes: the command runs in its own user and
# mount namespace (unshare, no root needed) where /etc/hosts is a copy
# with the lab hosts added, and SSL_CERT_FILE names the lab CA. The
# processes it starts (the SITL reader and harness) inherit both.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
env_file="$here/demo.env"
if [ "${1:-}" = "--env" ]; then env_file="$2"; shift 2; fi
[ "$#" -gt 0 ] || { echo "usage: labns.sh [--env demo.env] <command> [args...]" >&2; exit 2; }
[ -f "$env_file" ] || { echo "labns: no $env_file (make demo writes it)" >&2; exit 2; }
command -v unshare >/dev/null || { echo "labns: unshare is required (util-linux)" >&2; exit 2; }
val() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }

state="$(val DEMO_STATE_DIR)"; state="${state:-./local-demo}"
case "$state" in /*) ;; *) state="$here/$state" ;; esac
ca="$state/ca/ca.pem"
[ -f "$ca" ] || { echo "labns: no lab CA at $ca (make demo writes it)" >&2; exit 2; }

hosts="$(mktemp)"
trap 'rm -f "$hosts"' EXIT
cat /etc/hosts > "$hosts"
for h in AUTHORITY_HOST CISP_HOST USSP_HOST ANSP_HOST LAB_HOST; do
  v="$(val "$h")"
  [ -n "$v" ] || { echo "labns: $h is not set in $env_file" >&2; exit 2; }
  echo "127.0.0.1 $v" >> "$hosts"
done
chmod 0644 "$hosts"

export SSL_CERT_FILE="$ca"
# The namespace's root is the invoking user mapped; HOME and PATH are
# kept, so ~/ardupilot-venv and the rest resolve as outside.
unshare --user --map-root-user --mount -- bash -c 'mount --bind "$1" /etc/hosts && shift && exec "$@"' labns "$hosts" "$@"

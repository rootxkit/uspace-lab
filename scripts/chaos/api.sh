#!/usr/bin/env bash
# 05 §6 '`api` process (one system)': the system's api is killed, then
# started again. The hot-path processes keep every live service running
# on their projections, with projection age shown; their events wait in
# JetStream.
#
#   scripts/chaos/api.sh inject SYSTEM
#   scripts/chaos/api.sh restore SYSTEM
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=api
chaos_usage="api.sh inject|restore SYSTEM"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  kill_svc "$1-api"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  start_svc "$1-api"
}

chaos_main "$@"

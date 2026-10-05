#!/usr/bin/env bash
# 05 §6 'One ingest adapter instance': the adapter process is killed
# (SIGKILL), then started again. Its clients reconnect; the dedupe
# window absorbs the replays; everyone else is unaffected.
#
#   scripts/chaos/adapter.sh inject SERVICE
#   scripts/chaos/adapter.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=adapter
chaos_usage="adapter.sh inject|restore SERVICE"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  kill_svc "$1"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  start_svc "$1"
}

chaos_main "$@"

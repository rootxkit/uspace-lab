#!/usr/bin/env bash
# 05 §6 'DSS': the DSS server is stopped (5 min in the matrix; its
# datastore keeps running), then started again. Cross-USSP deconfliction
# is unavailable; local intents and all in-flight services continue.
#
#   scripts/chaos/dss.sh inject SERVICE
#   scripts/chaos/dss.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=dss
chaos_usage="dss.sh inject|restore SERVICE"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  stop_svc "$1"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  start_svc "$1"
}

chaos_main "$@"

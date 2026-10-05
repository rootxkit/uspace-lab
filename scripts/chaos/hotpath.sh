#!/usr/bin/env bash
# 05 §6 'One hot-path process (one system)': monitor, rid-sp, detect,
# deliver or manned-feed is killed, then started again. That service is
# shown as down; api and the other processes continue; other systems are
# unaffected.
#
#   scripts/chaos/hotpath.sh inject SERVICE
#   scripts/chaos/hotpath.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=hotpath
chaos_usage="hotpath.sh inject|restore SERVICE"
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

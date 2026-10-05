#!/usr/bin/env bash
# 05 §6 'One source type or instance (disabled or dead)', the dead case:
# the source's adapter is stopped, then started again. Its tracks age
# out as stale, counted; other sources are unaffected; consoles show the
# state.
#
#   scripts/chaos/source.sh inject SERVICE
#   scripts/chaos/source.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=source
chaos_usage="source.sh inject|restore SERVICE"
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

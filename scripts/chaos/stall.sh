#!/usr/bin/env bash
# knowledge/scenarios.md SC-15 'stalled adapter': every process of
# SERVICE is frozen (docker pause), then resumed. A stalled process
# holds its connections open and answers nothing, which a crash does
# not.
#
#   scripts/chaos/stall.sh inject SERVICE
#   scripts/chaos/stall.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=stall
chaos_usage="stall.sh inject|restore SERVICE"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  pause_svc "$1"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  unpause_svc "$1"
}

chaos_main "$@"

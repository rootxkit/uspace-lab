#!/usr/bin/env bash
# 05 §6 'The whole droplet (staging)': every running container of the
# stack is stopped, then exactly those are started again. Flights are
# unaffected because nothing commands them; the systems come back with
# their state.
#
#   scripts/chaos/stack.sh inject
#   scripts/chaos/stack.sh restore
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=stack
chaos_usage="stack.sh inject|restore"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 0 ] || usage "$chaos_usage"
  stop_project
}

do_restore() {
  [ $# -eq 0 ] || usage "$chaos_usage"
  start_project
}

chaos_main "$@"

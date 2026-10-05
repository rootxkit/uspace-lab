#!/usr/bin/env bash
# 05 §6 rows 'CISP' and 'Authority': every running container of the
# system is stopped (processes, then NATS, then the database; 5 min in
# the matrix), then started in the reverse order.
#
#   scripts/chaos/system.sh inject SYSTEM
#   scripts/chaos/system.sh restore SYSTEM
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=system
chaos_usage="system.sh inject|restore SYSTEM"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  stop_system "$1"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  start_system "$1"
}

chaos_main "$@"

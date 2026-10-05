#!/usr/bin/env bash
# Both databases of one system at once: the system's one database
# container (deploy/systems/compose.yaml) is stopped, then started
# again. 05 §6 rows 'TimescaleDB' and 'PostgreSQL' together.
#
#   scripts/chaos/dbhost.sh inject SERVICE
#   scripts/chaos/dbhost.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=dbhost
chaos_usage="dbhost.sh inject|restore SERVICE"
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

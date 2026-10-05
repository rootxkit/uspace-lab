#!/usr/bin/env bash
# 05 §6 'PostgreSQL (one system)': the system's relational database
# refuses every connection and its open ones are terminated (the host
# and the time-series database keep running), then it is opened again.
# The control plane refuses writes; the hot path runs on its projections
# with age shown.
#
#   scripts/chaos/postgres.sh inject PG_SERVICE DATABASE
#   scripts/chaos/postgres.sh restore PG_SERVICE DATABASE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=postgres
chaos_usage="postgres.sh inject|restore PG_SERVICE DATABASE"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 2 ] || usage "$chaos_usage"
  block_db "$1" "$2"
}

do_restore() {
  [ $# -eq 2 ] || usage "$chaos_usage"
  unblock_db "$1" "$2"
}

chaos_main "$@"

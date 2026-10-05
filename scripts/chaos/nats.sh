#!/usr/bin/env bash
# 05 §6 'NATS (one system)': the system's NATS server is stopped (60 s
# in the matrix), then started again. Ingest spills to its local disk
# queue; consumers hold last state; consoles freeze with age shown;
# other systems are unaffected (no cross-system NATS).
#
#   scripts/chaos/nats.sh inject SYSTEM
#   scripts/chaos/nats.sh restore SYSTEM
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=nats
chaos_usage="nats.sh inject|restore SYSTEM"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  stop_svc "$1-nats"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  start_svc "$1-nats"
}

chaos_main "$@"

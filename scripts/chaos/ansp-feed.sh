#!/usr/bin/env bash
# 05 §6 'ANSP feed': the ANSP's manned-traffic stream process is
# stopped, then started again. USSP traffic information marks manned
# traffic unavailable; its own e-conspicuity receiver continues at trust
# broadcast.
#
#   scripts/chaos/ansp-feed.sh inject SERVICE
#   scripts/chaos/ansp-feed.sh restore SERVICE
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=ansp-feed
chaos_usage="ansp-feed.sh inject|restore SERVICE"
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

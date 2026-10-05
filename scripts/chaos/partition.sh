#!/usr/bin/env bash
# A network partition of one system: in each of its containers a packet
# filter drops everything but loopback and the system's own containers
# (they keep their database and NATS), then it is removed. Addresses do
# not change. To everyone else the system is down while its processes
# keep running.
#
#   scripts/chaos/partition.sh inject SYSTEM
#   scripts/chaos/partition.sh restore SYSTEM
#
# Prints what it did and when; exit 0 only when the act took effect
# (scripts/chaos/lib.sh). scripts/chaos checks the fault independently.
chaos_domain=partition
chaos_usage="partition.sh inject|restore SYSTEM"
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"

do_inject() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  cut_system "$1"
}

do_restore() {
  [ $# -eq 1 ] || usage "$chaos_usage"
  heal_system "$1"
}

chaos_main "$@"

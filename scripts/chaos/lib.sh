# shellcheck shell=bash
# The fault primitives of scripts/chaos/<domain>.sh (WP-L9). Sourced, never
# run. Every primitive acts on the containers of one compose project,
# found by their compose labels, checks that what it did took effect,
# and prints one line per act with the UTC time it was done (LESSONS
# E-04: a script that says it injected a fault is not evidence that it
# did; scripts/chaos checks every fault independently, but a primitive
# that did nothing still says so and exits 1).
#
# Environment:
#   CHAOS_PROJECT  the compose project (default: COMPOSE_PROJECT_NAME,
#                  else uspace-demo, deploy/demo-up.sh's default)
#   CHAOS_NETWORK  the network every system shares (default
#                  <project>_lab, deploy/systems/compose.yaml)
#   CHAOS_STATE    where a fault keeps what its restore needs (default
#                  deploy/local-demo/chaos, git-ignored)
#
# Nothing here sends anything towards a vehicle (INV-01): the faults are
# the lab's own containers, networks and databases.

set -euo pipefail
export MSYS_NO_PATHCONV=1

chaos_repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHAOS_PROJECT="${CHAOS_PROJECT:-${COMPOSE_PROJECT_NAME:-uspace-demo}}"
CHAOS_NETWORK="${CHAOS_NETWORK:-${CHAOS_PROJECT}_lab}"
CHAOS_STATE="${CHAOS_STATE:-$chaos_repo/deploy/local-demo/chaos}"
chaos_domain="${chaos_domain:-chaos}"

# Names the scripts accept: compose service names, system prefixes and
# PostgreSQL database names. Checked before any side effect.
chaos_name_re='^[a-z][a-z0-9_-]{0,62}$'

chaos_now() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
say() { printf 'chaos: %s %s %s\n' "$(chaos_now)" "$chaos_domain" "$*"; }
die() { printf 'chaos: %s %s FAILED %s\n' "$(chaos_now)" "$chaos_domain" "$*" >&2; exit 1; }
usage() { printf 'usage: %s\n' "$1" >&2; exit 2; }

valid_name() {
  [[ "$1" =~ $chaos_name_re ]] || die "not a valid name: '$1'"
}

command -v docker >/dev/null 2>&1 || die "docker is required"

# cid SERVICE: the one container of SERVICE in the project, or exit 1.
cid() {
  local svc="$1" ids n
  valid_name "$svc"
  ids="$(docker ps -aq --filter "label=com.docker.compose.project=$CHAOS_PROJECT" \
    --filter "label=com.docker.compose.service=$svc")"
  n="$(printf '%s\n' "$ids" | grep -c . || true)"
  [ "$n" = "1" ] || die "service $svc of project $CHAOS_PROJECT has $n containers, want 1"
  printf '%s\n' "$ids"
}

state_of() { docker inspect -f '{{.State.Status}}' "$1"; }
started_at() { docker inspect -f '{{.State.StartedAt}}' "$1"; }

# system_services SYSTEM: the services of SYSTEM ("<system>-*") whose
# container is running now (one-shot migrations have exited and are left
# alone), one per line, sorted.
system_services() {
  local sys="$1"
  valid_name "$sys"
  docker ps --filter "label=com.docker.compose.project=$CHAOS_PROJECT" --filter status=running \
    --format '{{.Label "com.docker.compose.service"}}' | grep "^$sys-" | sort || true
}

# kill_svc SERVICE: SIGKILL, the crash of 05 §6 (no shutdown path runs).
kill_svc() {
  local id; id="$(cid "$1")"
  [ "$(state_of "$id")" = "running" ] || die "$1 is $(state_of "$id"), not running: nothing to kill"
  local before; before="$(started_at "$id")"
  docker kill -s KILL "$id" >/dev/null
  [ "$(state_of "$id")" != "running" ] || die "$1 still running after SIGKILL"
  say "killed $1 (SIGKILL; it had run since $before)"
}

# stop_svc SERVICE: SIGTERM, then SIGKILL after 10 s.
stop_svc() {
  local id; id="$(cid "$1")"
  [ "$(state_of "$id")" = "running" ] || die "$1 is $(state_of "$id"), not running: nothing to stop"
  docker stop -t 10 "$id" >/dev/null
  [ "$(state_of "$id")" = "exited" ] || die "$1 is $(state_of "$id") after stop"
  say "stopped $1"
}

# start_svc SERVICE: start it again; a new process (StartedAt moves).
start_svc() {
  local id; id="$(cid "$1")"
  local st; st="$(state_of "$id")"
  [ "$st" != "running" ] || die "$1 is already running: the fault was not in place"
  docker start "$id" >/dev/null
  [ "$(state_of "$id")" = "running" ] || die "$1 is $(state_of "$id") after start"
  say "started $1 (running since $(started_at "$id"))"
}

pause_svc() {
  local id; id="$(cid "$1")"
  [ "$(state_of "$id")" = "running" ] || die "$1 is $(state_of "$id"), not running: nothing to pause"
  docker pause "$id" >/dev/null
  [ "$(state_of "$id")" = "paused" ] || die "$1 is $(state_of "$id") after pause"
  say "paused $1 (SIGSTOP of every process: a stalled process, SC-15)"
}

unpause_svc() {
  local id; id="$(cid "$1")"
  [ "$(state_of "$id")" = "paused" ] || die "$1 is $(state_of "$id"), not paused: the fault was not in place"
  docker unpause "$id" >/dev/null
  [ "$(state_of "$id")" = "running" ] || die "$1 is $(state_of "$id") after unpause"
  say "resumed $1"
}

# A system's database host and its databases are named by the matrix
# (scripts/chaos/matrix.yaml), never here.
pg_exec() {
  local id="$1" sql="$2"
  docker exec "$id" psql -X -q -v ON_ERROR_STOP=1 -U postgres -d postgres -tAc "$sql"
}

# block_db PG_SERVICE DATABASE: no new connection to DATABASE (ALTER
# DATABASE ... ALLOW_CONNECTIONS false, which binds superusers too), and
# every open one terminated. The host and the system's other database
# keep running: this is "TimescaleDB of one system" or "PostgreSQL of one
# system" (05 §6) on the lab's one database container per system.
block_db() {
  local svc="$1" db="$2" id n
  valid_name "$db"
  id="$(cid "$svc")"
  [ "$(pg_exec "$id" "SELECT datallowconn FROM pg_database WHERE datname = '$db'")" = "t" ] \
    || die "database $db on $svc does not exist or is already blocked"
  pg_exec "$id" "ALTER DATABASE \"$db\" WITH ALLOW_CONNECTIONS false" >/dev/null
  n="$(pg_exec "$id" "SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE datname = '$db' AND pid <> pg_backend_pid()")"
  say "blocked database $db on $svc (connections refused; $n open connection(s) terminated)"
}

unblock_db() {
  local svc="$1" db="$2" id
  valid_name "$db"
  id="$(cid "$svc")"
  [ "$(pg_exec "$id" "SELECT datallowconn FROM pg_database WHERE datname = '$db'")" = "f" ] \
    || die "database $db on $svc is not blocked: the fault was not in place"
  pg_exec "$id" "ALTER DATABASE \"$db\" WITH ALLOW_CONNECTIONS true" >/dev/null
  say "unblocked database $db on $svc"
}

# stop_system SYSTEM: every running container of SYSTEM, processes first,
# then its NATS, then its database host; the order is kept so that
# start_system starts them the other way round.
stop_system() {
  local sys="$1" f svcs ordered s
  mkdir -p "$CHAOS_STATE"
  f="$CHAOS_STATE/system-$sys.order"
  [ ! -e "$f" ] || die "$f exists: system $sys is already down (restore it first)"
  svcs="$(system_services "$sys")"
  [ -n "$svcs" ] || die "system $sys has no running container in $CHAOS_PROJECT"
  ordered="$(printf '%s\n' "$svcs" | grep -Ev -- '-(nats|postgres|timescaledb)$' || true)
$(printf '%s\n' "$svcs" | grep -E -- '-nats$' || true)
$(printf '%s\n' "$svcs" | grep -E -- '-(postgres|timescaledb)$' || true)"
  ordered="$(printf '%s\n' "$ordered" | grep . )"
  printf '%s\n' "$ordered" > "$f"
  while IFS= read -r s; do stop_svc "$s"; done <<<"$ordered"
  say "system $sys down: $(tr '\n' ' ' <<<"$ordered")"
}

start_system() {
  local sys="$1" f s
  f="$CHAOS_STATE/system-$sys.order"
  [ -s "$f" ] || die "no $f: system $sys was not stopped by stop_system"
  while IFS= read -r s; do start_svc "$s"; done < <(tac "$f")
  rm -f "$f"
  say "system $sys started"
}

# The packet filter a partition runs in a container's network namespace
# (scripts/chaos/net/Dockerfile, Alpine pinned by digest, built here on
# first use; never pushed).
CHAOS_NET_IMAGE="${CHAOS_NET_IMAGE:-uspace-lab/chaos-net:local}"
net_image() {
  docker image inspect "$CHAOS_NET_IMAGE" >/dev/null 2>&1 && return 0
  docker build -q -t "$CHAOS_NET_IMAGE" "$chaos_repo/scripts/chaos/net" >/dev/null
  say "built $CHAOS_NET_IMAGE from scripts/chaos/net"
}
# in_netns ID SCRIPT: SCRIPT (sh) in the network namespace of container ID.
in_netns() {
  docker run --rm --net "container:$1" --cap-add NET_ADMIN "$CHAOS_NET_IMAGE" "$2"
}
lab_ip() {
  docker inspect -f "{{(index .NetworkSettings.Networks \"$CHAOS_NETWORK\").IPAddress}}" "$1"
}

# cut_system SYSTEM: a network partition. In every running container of
# SYSTEM a packet filter drops everything that is not loopback or another
# container of SYSTEM: the system keeps its database and NATS, nobody
# else reaches it and it reaches nobody (05 §6 rows "CISP", "Authority",
# 02 §1: the producer is lost to its consumers while its processes run).
# Addresses do not change, so connections can resume when it heals, as
# after a real partition. Chains CHAOS_IN and CHAOS_OUT, first in INPUT
# and OUTPUT.
cut_system() {
  local sys="$1" f s id ips="" ip rules
  mkdir -p "$CHAOS_STATE"
  f="$CHAOS_STATE/partition-$sys.services"
  [ ! -e "$f" ] || die "$f exists: system $sys is already partitioned (heal it first)"
  local svcs; svcs="$(system_services "$sys")"
  [ -n "$svcs" ] || die "system $sys has no running container in $CHAOS_PROJECT"
  net_image
  while IFS= read -r s; do
    ip="$(lab_ip "$(cid "$s")")"
    [[ "$ip" =~ ^[0-9.]+$ ]] || die "$s has no address on $CHAOS_NETWORK"
    ips="$ips $ip"
  done <<<"$svcs"
  rules="set -e; iptables -N CHAOS_IN; iptables -N CHAOS_OUT;
    iptables -A CHAOS_IN -i lo -j ACCEPT; iptables -A CHAOS_OUT -o lo -j ACCEPT;
    for ip in $ips; do iptables -A CHAOS_IN -s \$ip -j ACCEPT; iptables -A CHAOS_OUT -d \$ip -j ACCEPT; done;
    iptables -A CHAOS_IN -j DROP; iptables -A CHAOS_OUT -j DROP;
    iptables -I INPUT 1 -j CHAOS_IN; iptables -I OUTPUT 1 -j CHAOS_OUT"
  : > "$f"
  while IFS= read -r s; do
    id="$(cid "$s")"
    printf '%s\n' "$s" >> "$f"
    in_netns "$id" "$rules" >/dev/null || die "the packet filter did not go into $s"
  done <<<"$svcs"
  say "partitioned system $sys: $(tr '\n' ' ' <<<"$svcs")drop every packet but their own ($(printf '%s' "$ips" | wc -w) addresses) and loopback"
}

heal_system() {
  local sys="$1" f s heal
  f="$CHAOS_STATE/partition-$sys.services"
  [ -s "$f" ] || die "no $f: system $sys was not partitioned by cut_system"
  net_image
  heal="set -e; iptables -D INPUT -j CHAOS_IN; iptables -D OUTPUT -j CHAOS_OUT;
    iptables -F CHAOS_IN; iptables -F CHAOS_OUT; iptables -X CHAOS_IN; iptables -X CHAOS_OUT"
  while IFS= read -r s; do
    in_netns "$(cid "$s")" "$heal" >/dev/null || die "the packet filter of $s did not come out"
  done < "$f"
  rm -f "$f"
  say "healed the partition of system $sys"
}

# stop_project / start_project: the whole stack (05 §6 last row), every
# running container of the project, kept in a list so that exactly those
# start again (one-shot migrations stay exited).
stop_project() {
  local f
  mkdir -p "$CHAOS_STATE"
  f="$CHAOS_STATE/project.running"
  [ ! -e "$f" ] || die "$f exists: the stack is already down (restore it first)"
  docker ps -q --filter "label=com.docker.compose.project=$CHAOS_PROJECT" > "$f"
  [ -s "$f" ] || { rm -f "$f"; die "project $CHAOS_PROJECT has no running container"; }
  # shellcheck disable=SC2046 # one argument per container id
  docker stop -t 10 $(cat "$f") >/dev/null
  local left; left="$(docker ps -q --filter "label=com.docker.compose.project=$CHAOS_PROJECT" | grep -c . || true)"
  [ "$left" = "0" ] || die "$left container(s) still running after stop"
  say "stopped the whole stack ($(grep -c . "$f") containers)"
}

start_project() {
  local f
  f="$CHAOS_STATE/project.running"
  [ -s "$f" ] || die "no $f: the stack was not stopped by stop_project"
  # shellcheck disable=SC2046 # one argument per container id
  docker start $(cat "$f") >/dev/null
  say "started the whole stack again ($(grep -c . "$f") containers)"
  rm -f "$f"
}

# chaos_main ACTION ARGS...: the entry point of every domain script, which
# defines do_inject and do_restore and sets chaos_domain and chaos_usage
# before sourcing this file.
chaos_main() {
  local action="${1:-}"
  [ $# -ge 1 ] && shift
  case "$action" in
    inject) do_inject "$@" ;;
    restore) do_restore "$@" ;;
    *) usage "${chaos_usage:?the domain script sets chaos_usage}" ;;
  esac
}

#!/usr/bin/env bash
# Stop every SITL and MAVProxy process group started by run_sitl.sh, say
# what was stopped, and verify that nothing is left.
#
# Exit 0 when the teardown worked (including "nothing to stop"), 1 when a
# simulator process survived. The success path is the one that broke in
# the predecessor: `pgrep` exits 1 when nothing matches, which here is the
# success case, and `set -euo pipefail` turned that into a silent exit 1
# before the success line. The failure path ran often and worked; the
# success path had never run to completion (LESSONS E-02). Every pgrep
# below whose "no match" is an answer is guarded, and sim/tests/
# test_stop_sitl.py runs both paths against real process groups.

set -euo pipefail

SIM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${SITL_OUT_DIR:-${SIM_DIR}/out}"
PID_FILE="${OUT_DIR}/sitl.pids"
# What a surviving simulator of THIS fleet looks like on the command line:
# every process run_sitl.sh starts names its instance directory
# (sim_vehicle.py's --add-param-file, arducopter's --defaults, MAVProxy's
# --state-basedir). Scoped to it, so another checkout's fleet is neither
# reported nor touched. Overridable so the tests run both paths with
# their own stand-in processes.
default_pattern="$(printf '%s' "${OUT_DIR}/instance-" | sed 's/[][\.^$*+?(){}|]/\\&/g')"
PROC_PATTERN="${SITL_PROC_PATTERN:-${default_pattern}}"
TERM_WAIT_S="${SITL_TERM_WAIT_S:-10}"

if [[ ! -s "${PID_FILE}" ]]; then
  # Nothing recorded; still verify, because a stale simulator from an
  # earlier run would make the next run fail somewhere unrelated.
  leftover=$(pgrep -f "${PROC_PATTERN}" | wc -l) || true
  if (( leftover > 0 )); then
    echo "stop_sitl: no ${PID_FILE}, but ${leftover} simulator process(es) are running:" >&2
    pgrep -af "${PROC_PATTERN}" >&2 || true
    exit 1
  fi
  echo "stop_sitl: nothing to stop (${PID_FILE} absent or empty); nothing running"
  exit 0
fi

mapfile -t pids < "${PID_FILE}"

# Each recorded pid is a process GROUP leader (run_sitl.sh uses setsid):
# sim_vehicle.py starts arducopter and returns and mavproxy --daemon
# forks, so the recorded pid is gone long before what it started.
# Signalling the pids alone once left 5 arducopter processes running
# while the script reported "signalled 0 process(es)".
group_alive() { kill -0 -- "-$1" 2>/dev/null; }

describe_group() {
  # pgrep exits 1 when the group is empty: an answer, not an error.
  pgrep -a -g "$1" 2>/dev/null | sed 's/^/    /' || true
}

stopped=0
described=""
for (( idx = ${#pids[@]} - 1; idx >= 0; idx-- )); do
  pid="${pids[idx]}"
  [[ "${pid}" =~ ^[0-9]+$ ]] || continue
  if group_alive "${pid}"; then
    described+="  group ${pid}:"$'\n'"$(describe_group "${pid}")"$'\n'
    kill -TERM -- "-${pid}" 2>/dev/null || true
    stopped=$(( stopped + 1 ))
  fi
done

for (( waited = 0; waited < TERM_WAIT_S * 4; waited++ )); do
  remaining=0
  for pid in "${pids[@]}"; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    if group_alive "${pid}"; then
      remaining=$(( remaining + 1 ))
    fi
  done
  (( remaining == 0 )) && break
  sleep 0.25
done

killed=0
for pid in "${pids[@]}"; do
  [[ "${pid}" =~ ^[0-9]+$ ]] || continue
  # An explicit if, not `A && B || C`, which also runs C when B fails.
  if group_alive "${pid}"; then
    kill -KILL -- "-${pid}" 2>/dev/null || true
    killed=$(( killed + 1 ))
  fi
done

# SIGKILL is not synchronous: give the kernel time to reap before the
# check, or a teardown that worked reports false survivors (and a warning
# that is sometimes false is one people learn to scroll past).
if (( killed > 0 )); then
  for _ in $(seq 1 20); do
    pgrep -f "${PROC_PATTERN}" >/dev/null 2>&1 || break
    sleep 0.25
  done
fi

: > "${PID_FILE}"

if (( stopped > 0 )); then
  echo "stop_sitl: stopped ${stopped} process group(s) (${killed} needed SIGKILL):"
  printf '%s' "${described}"
else
  echo "stop_sitl: the recorded process groups had already exited"
fi

# `|| true` is load-bearing: no match is the success case.
leftover=$(pgrep -f "${PROC_PATTERN}" | wc -l) || true
if (( leftover > 0 )); then
  echo "stop_sitl: WARNING - ${leftover} simulator process(es) still running:" >&2
  pgrep -af "${PROC_PATTERN}" >&2 || true
  exit 1
fi

echo "stop_sitl: nothing left running"

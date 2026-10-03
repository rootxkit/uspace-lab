#!/usr/bin/env bash
# Launch N ArduCopter SITL instances, each with its own SYSID and two
# local UDP outputs: one for sim/mav_reader.py (receive only) and one for
# sim/fly.py (the harness, the only thing that commands SITL).
#
# From the predecessor (rootxkit/utm sim/run_sitl.sh), reference only;
# what it learned is kept in the comments below.
#
# Configuration: sim/sitl.env (copy sim/sitl.env.example), or the file
# SITL_ENV_FILE names. SITL_HOME has no default: coordinates are
# configuration (docs/PLAN.md INV-03).
#
# Usage:  sim/run_sitl.sh -n 3        (make sim N=3)
# Writes: sim/out/sitl.pids           process groups for stop_sitl.sh
#         sim/out/instances.tsv       instance, sysid, ports, home
#         sim/out/instance-<i>/       SITL and MAVProxy logs

set -euo pipefail

SIM_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${SITL_OUT_DIR:-${SIM_DIR}/out}"
PID_FILE="${OUT_DIR}/sitl.pids"
INSTANCES_FILE="${OUT_DIR}/instances.tsv"

# Metres per degree of longitude at the equator (2*pi*a/360, WGS84 a),
# scaled by cos(latitude) below. A spherical approximation that runs
# about 0.16 % short at mid latitudes (25.041 m measured for 25 m at
# 41.7 N): good enough to park vehicles apart, never for measuring. The
# scenario runner computes the same homes with the same formula
# (internal/scenario), so the two agree to the bit.
METRES_PER_DEG_LON_EQUATOR=111319.49

die() { echo "run_sitl: $*" >&2; exit 1; }
note() { echo "run_sitl: $*" >&2; }

usage() {
  cat >&2 <<'USAGE'
Usage: run_sitl.sh [-n N]

  -n N   Number of SITL instances to launch (default 1).
  -h     This help.

Configuration is read from sim/sitl.env (or SITL_ENV_FILE). SITL_HOME is
required.
USAGE
}

instance_count=1
while getopts ":n:h" opt; do
  case "${opt}" in
    n) instance_count="${OPTARG}" ;;
    h) usage; exit 0 ;;
    :) die "option -${OPTARG} requires an argument" ;;
    \?) usage; die "unknown option -${OPTARG}" ;;
  esac
done

[[ "${instance_count}" =~ ^[0-9]+$ ]] || die "instance count must be an integer, got '${instance_count}'"
(( instance_count >= 1 )) || die "instance count must be at least 1"

# --- Configuration ----------------------------------------------------------

CONFIG_FILE="${SITL_ENV_FILE:-${SIM_DIR}/sitl.env}"
if [[ -f "${CONFIG_FILE}" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "${CONFIG_FILE}"
  set +a
else
  note "no ${CONFIG_FILE}; falling back to the environment"
  note "create it with: cp sim/sitl.env.example sim/sitl.env"
fi

[[ -n "${SITL_HOME:-}" ]] || die "SITL_HOME is not set. Copy sim/sitl.env.example to sim/sitl.env and set your test area. Coordinates are never defaulted in code."

SITL_SPACING_M="${SITL_SPACING_M:-25}"
SITL_SYSID_BASE="${SITL_SYSID_BASE:-1}"
SITL_OUT_PORT_BASE="${SITL_OUT_PORT_BASE:-14560}"
SITL_FLY_PORT_BASE="${SITL_FLY_PORT_BASE:-14660}"
# `-` and not `:-`: an explicitly empty SITL_QGC_PORT means "no ground
# station", which is what CI sets; `:-` would replace it with a default
# and every vehicle would get an --out nobody listens on.
SITL_QGC_PORT="${SITL_QGC_PORT-}"
SITL_FRAME="${SITL_FRAME:-quad}"
SITL_SPEEDUP="${SITL_SPEEDUP:-1}"
# sim_vehicle.py --instance K puts SITL's MAVLink TCP on 5760 + 10 K. A
# base other than 0 keeps this fleet clear of another checkout's.
SITL_INSTANCE_BASE="${SITL_INSTANCE_BASE:-0}"
SITL_TCP_PORT_BASE="${SITL_TCP_PORT_BASE:-5760}"
SITL_TCP_PORT_STRIDE="${SITL_TCP_PORT_STRIDE:-10}"
SITL_STREAMRATE="${SITL_STREAMRATE:-4}"

IFS=',' read -r home_lat home_lon home_alt_amsl_m home_heading_deg <<< "${SITL_HOME}"
for field in home_lat home_lon home_alt_amsl_m home_heading_deg; do
  [[ -n "${!field:-}" ]] || die "SITL_HOME must be 'lat,lon,alt_amsl_m,heading_deg', got '${SITL_HOME}'"
done
number='^-?[0-9]+(\.[0-9]+)?$'
[[ "${home_lat}" =~ ${number} ]] || die "SITL_HOME latitude is not a number: '${home_lat}'"
[[ "${home_lon}" =~ ${number} ]] || die "SITL_HOME longitude is not a number: '${home_lon}'"
[[ "${home_alt_amsl_m}" =~ ${number} ]] || die "SITL_HOME altitude is not a number: '${home_alt_amsl_m}'"
[[ "${home_heading_deg}" =~ ${number} ]] || die "SITL_HOME heading is not a number: '${home_heading_deg}'"
awk -v lat="${home_lat}" 'BEGIN{ exit (lat >= -89 && lat <= 89) ? 0 : 1 }' \
  || die "SITL_HOME latitude out of range: ${home_lat}"
awk -v lon="${home_lon}" 'BEGIN{ exit (lon >= -180 && lon <= 180) ? 0 : 1 }' \
  || die "SITL_HOME longitude out of range: ${home_lon}"

[[ "${SITL_INSTANCE_BASE}" =~ ^[0-9]+$ ]] || die "SITL_INSTANCE_BASE must be a non-negative integer"
last_sysid=$(( SITL_SYSID_BASE + instance_count - 1 ))
(( SITL_SYSID_BASE >= 1 )) || die "SITL_SYSID_BASE must be at least 1; 0 is reserved"
(( last_sysid <= 254 )) || die "SYSID range ${SITL_SYSID_BASE}..${last_sysid} exceeds 254"

# --- Locate the tools ---------------------------------------------------------

SIM_VEHICLE="${SIM_VEHICLE:-$(command -v sim_vehicle.py || true)}"
[[ -n "${SIM_VEHICLE}" ]] || die "sim_vehicle.py not found. Put ArduPilot's Tools/autotest on PATH or set SIM_VEHICLE in sim/sitl.env."
[[ -f "${SIM_VEHICLE}" ]] || die "SIM_VEHICLE is not a file: ${SIM_VEHICLE}"

# SITL runs headless and the UDP fan-out is an explicit MAVProxy per
# instance: sim_vehicle.py's --out only works when it starts MAVProxy
# itself, with one interactive console per vehicle.
MAVPROXY="${MAVPROXY:-$(command -v mavproxy.py || true)}"
[[ -n "${MAVPROXY}" ]] || die "mavproxy.py not found. Install MAVProxy or set MAVPROXY in sim/sitl.env."

# --- Launch -----------------------------------------------------------------

mkdir -p "${OUT_DIR}"
if [[ -s "${PID_FILE}" ]] && kill -0 -- "-$(head -n1 "${PID_FILE}")" 2>/dev/null; then
  die "SITL already appears to be running. Run sim/stop_sitl.sh first."
fi
: > "${PID_FILE}"
printf 'instance\tsysid\ttcp_port\tout_port\tfly_port\thome\n' > "${INSTANCES_FILE}"

# Is something listening on local TCP port $1? `ss` answers at once; a
# connect to a closed local port is not always refused (under WSL's
# mirrored networking it hangs until a timeout, measured here), so the
# /dev/tcp fallback is bounded.
port_open() {
  if command -v ss >/dev/null 2>&1; then
    ss -Hltn "sport = :$1" 2>/dev/null | grep -q .
  else
    timeout 2 bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2>/dev/null
  fi
}

# Refuse to start over another fleet: a second SITL on the same TCP port
# fails, and that fleet's MAVProxy would attach to ours and fan our
# vehicle out to its consumers (seen here: a predecessor checkout's
# MAVProxy, left running, attached to this fleet's instance 0).
for (( i = 0; i < instance_count; i++ )); do
  port=$(( SITL_TCP_PORT_BASE + (SITL_INSTANCE_BASE + i) * SITL_TCP_PORT_STRIDE ))
  if port_open "${port}"; then
    die "TCP ${port} (SITL instance $(( SITL_INSTANCE_BASE + i ))) is in use, by another SITL fleet? Stop it or set SITL_INSTANCE_BASE in ${CONFIG_FILE}"
  fi
done

# Wait for SITL's MAVLink TCP socket before attaching MAVProxy; attaching
# too early leaves a MAVProxy that never connects.
wait_for_port() {
  local port="$1" deadline=$(( SECONDS + 60 ))
  while (( SECONDS < deadline )); do
    if port_open "${port}"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

printf '%-4s %-6s %-8s %-8s %-8s %-9s %s\n' "INST" "SYSID" "TCP" "OUT" "FLY" "PID" "HOME"

for (( i = 0; i < instance_count; i++ )); do
  sysid=$(( SITL_SYSID_BASE + i ))
  out_port=$(( SITL_OUT_PORT_BASE + i ))
  fly_port=$(( SITL_FLY_PORT_BASE + i ))
  sitl_instance=$(( SITL_INSTANCE_BASE + i ))
  tcp_port=$(( SITL_TCP_PORT_BASE + sitl_instance * SITL_TCP_PORT_STRIDE ))
  inst_dir="${OUT_DIR}/instance-${i}"
  mkdir -p "${inst_dir}"

  inst_lon=$(awk -v lon="${home_lon}" -v lat="${home_lat}" \
                 -v spacing="${SITL_SPACING_M}" -v i="${i}" \
                 -v mpd="${METRES_PER_DEG_LON_EQUATOR}" \
    'BEGIN { printf "%.7f", lon + (i * spacing) / (mpd * cos(lat * 3.141592653589793 / 180)) }')
  inst_home="${home_lat},${inst_lon},${home_alt_amsl_m},${home_heading_deg}"

  # Both parameter names: ArduPilot renamed SYSID_THISMAV to MAV_SYSID in
  # 4.6, and writing only the old one against 4.6 is silently ignored
  # (every vehicle stays SYSID 1). An unknown name in an --add-param-file
  # is ignored, so both is safe on 4.5 and 4.6.
  param_file="${inst_dir}/sysid.parm"
  {
    printf 'SYSID_THISMAV %d\n' "${sysid}"
    printf 'MAV_SYSID %d\n' "${sysid}"
  } > "${param_file}"

  # setsid: each instance is its own process group, so stop_sitl.sh can
  # signal the whole tree (sim_vehicle.py starts arducopter and returns).
  # --wipe-eeprom: SITL persists parameters, and a defaults file is read
  # only when that storage is empty; without it a second run keeps the
  # first run's SYSIDs. Started from the instance directory: SITL keeps
  # eeprom.bin and terrain/ in its working directory, and instances that
  # shared one wiped each other's storage (measured: from the repository
  # root only instance 0 of three came up, and eeprom.bin landed in the
  # checkout).
  ( cd "${inst_dir}" && exec setsid "${SIM_VEHICLE}" \
    --vehicle ArduCopter \
    --frame "${SITL_FRAME}" \
    --instance "${sitl_instance}" \
    --custom-location "${inst_home}" \
    --speedup "${SITL_SPEEDUP}" \
    --add-param-file "${param_file}" \
    --no-rebuild \
    --no-mavproxy \
    --wipe-eeprom \
    > "${inst_dir}/sitl.log" 2>&1 ) &
  sitl_pid=$!
  echo "${sitl_pid}" >> "${PID_FILE}"

  if ! wait_for_port "${tcp_port}"; then
    die "instance ${i}: SITL did not open TCP ${tcp_port} within 60 s; see ${inst_dir}/sitl.log"
  fi

  out_args=(--out "udp:127.0.0.1:${out_port}" --out "udp:127.0.0.1:${fly_port}")
  if [[ -n "${SITL_QGC_PORT}" ]]; then
    out_args+=(--out "udp:127.0.0.1:${SITL_QGC_PORT}")
  fi

  # Its own group too: --daemon forks, so the recorded pid exits almost at
  # once and signalling it alone reaches nothing.
  setsid "${MAVPROXY}" \
    --master "tcp:127.0.0.1:${tcp_port}" \
    "${out_args[@]}" \
    --streamrate "${SITL_STREAMRATE}" \
    --state-basedir "${inst_dir}" \
    --aircraft "sitl-${i}" \
    --daemon \
    > "${inst_dir}/mavproxy.log" 2>&1 &
  mavproxy_pid=$!
  echo "${mavproxy_pid}" >> "${PID_FILE}"

  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "${i}" "${sysid}" "${tcp_port}" "${out_port}" "${fly_port}" "${inst_home}" >> "${INSTANCES_FILE}"
  printf '%-4s %-6s %-8s %-8s %-8s %-9s %s\n' \
    "${i}" "${sysid}" "${tcp_port}" "${out_port}" "${fly_port}" "${sitl_pid}" "${inst_home}"
done

cat >&2 <<EOF

run_sitl: ${instance_count} instance(s) up; logs in ${OUT_DIR}/instance-*/
run_sitl: reader UDP ${SITL_OUT_PORT_BASE}..$(( SITL_OUT_PORT_BASE + instance_count - 1 )), harness UDP ${SITL_FLY_PORT_BASE}..$(( SITL_FLY_PORT_BASE + instance_count - 1 ))${SITL_QGC_PORT:+, ground station on ${SITL_QGC_PORT}}
run_sitl: SYSID ${SITL_SYSID_BASE}..${last_sysid}; instances in ${INSTANCES_FILE}
run_sitl: stop with sim/stop_sitl.sh (make sim-down)
EOF

"""Read one SITL vehicle's MAVLink, receive only, into sim/vehicle/v1 lines.

    python sim/mav_reader.py --sysid 1 --out udp:127.0.0.1:14560
    python sim/mav_reader.py --sysid 1 --out udp:127.0.0.1:14560 \\
        --emit udp:127.0.0.1:15560 --mark-alt-invalid

This is the lab's only MAVLink boundary (docs/PLAN.md D2). It binds the
UDP port that run_sitl.sh's MAVProxy forwards vehicle ``--out`` to, parses
what arrives with pymavlink, and writes one ``sim/vehicle/v1`` NDJSON line
(``sim/schema/vehicle-v1.json``) per ``GLOBAL_POSITION_INT`` of the vehicle
to stdout and, with ``--emit``, as one datagram per line to a local UDP
port. Every Go simulator consumes those lines and never opens MAVLink.

Receive only (INV-01). The socket is bound and read; nothing is ever
written to it. The pymavlink parser is given a file object that refuses
writes, so even a library call that tried to reply would raise instead of
reaching the vehicle; ``sim/tests`` assert it is never called, and CI
refuses socket send calls and pymavlink ``*_send`` writers anywhere in
``sim/`` but the harness ``sim/fly.py``, which nothing here imports.

Field table (LESSONS E-03: derived, never remembered). Every field the
reader uses is in ``FIELD_TABLE`` with the unit it expects; at import the
table is checked against pymavlink's own message classes
(``fieldnames`` and ``fieldunits_by_name``) and the scale is taken from
the unit pymavlink declares, so a dialect change stops the reader instead
of producing plausible numbers. ``sim/tests/test_mav_reader.py`` pins the
table and diffs encoded frames that differ in one field.

Where each member of the line comes from:

- ``ts``: ``GLOBAL_POSITION_INT.time_boot_ms`` put on UTC by the last
  ``SYSTEM_TIME`` (``time_unix_usec - time_boot_ms``); null until the
  vehicle has sent a non-zero UTC (R-16).
- ``lat_deg``, ``lon_deg``: ``GLOBAL_POSITION_INT.lat/lon``.
- ``alt_amsl_m``: ``GLOBAL_POSITION_INT.alt`` (MSL).
- ``alt_hae_m``: ``GPS_RAW_INT.alt_ellipsoid`` with a 3D fix and
  non-zero, else null. SITL reports it equal to ``alt`` (no geoid), so
  sim-receiver broadcasts AMSL + N by default (R-16).
- ``alt_pressure_m``: ``SCALED_PRESSURE.press_abs`` through the ICAO
  standard atmosphere (1013.25 hPa).
- ``height_takeoff_m``: ``GLOBAL_POSITION_INT.relative_alt``.
- ``speed_ms``, ``track_deg``, ``vspeed_ms``, ``vn_ms``, ``ve_ms``:
  ``GLOBAL_POSITION_INT.vx/vy/vz`` (north, east, down); below 0.5 m/s the
  track is the heading ``hdg`` when known, else null.
- ``armed``, ``status``: the autopilot component's ``HEARTBEAT``:
  ``base_mode`` armed flag; ``system_status`` critical or emergency is
  ``emergency``, armed is ``airborne``, else ``ground``.
- ``fix_ok``: ``GPS_RAW_INT.fix_type`` at least a 3D fix.
- ``alt_invalid``: ``--mark-alt-invalid`` (S-36: exercise pressure-altitude
  handling without touching any threshold), or no 3D fix.
"""

from __future__ import annotations

import argparse
import json
import math
import signal
import socket
import sys
import time
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any, NoReturn

from pymavlink.dialects.v20 import ardupilotmega as mavlink

SCHEMA = "sim/vehicle/v1"

# Scale from the unit pymavlink declares to the SI unit of the line.
UNIT_SCALE: dict[str, float] = {
    "degE7": 1e-7,
    "mm": 1e-3,
    "cm/s": 1e-2,
    "cdeg": 1e-2,
    "ms": 1e-3,
    "us": 1e-6,
    "hPa": 1.0,
}

# (message class, field, unit pymavlink must declare). The unit column is
# what this reader was written against; check_field_table refuses to run
# when pymavlink says otherwise.
FIELD_TABLE: tuple[tuple[str, str, str], ...] = (
    ("MAVLink_global_position_int_message", "time_boot_ms", "ms"),
    ("MAVLink_global_position_int_message", "lat", "degE7"),
    ("MAVLink_global_position_int_message", "lon", "degE7"),
    ("MAVLink_global_position_int_message", "alt", "mm"),
    ("MAVLink_global_position_int_message", "relative_alt", "mm"),
    ("MAVLink_global_position_int_message", "vx", "cm/s"),
    ("MAVLink_global_position_int_message", "vy", "cm/s"),
    ("MAVLink_global_position_int_message", "vz", "cm/s"),
    ("MAVLink_global_position_int_message", "hdg", "cdeg"),
    ("MAVLink_gps_raw_int_message", "fix_type", ""),
    ("MAVLink_gps_raw_int_message", "alt_ellipsoid", "mm"),
    ("MAVLink_scaled_pressure_message", "press_abs", "hPa"),
    ("MAVLink_system_time_message", "time_unix_usec", "us"),
    ("MAVLink_system_time_message", "time_boot_ms", "ms"),
    ("MAVLink_heartbeat_message", "type", ""),
    ("MAVLink_heartbeat_message", "base_mode", ""),
    ("MAVLink_heartbeat_message", "system_status", ""),
)

# Enumerations, read from the dialect (never written as numbers here).
MAV_MODE_FLAG_SAFETY_ARMED: int = mavlink.MAV_MODE_FLAG_SAFETY_ARMED
MAV_STATE_CRITICAL: int = mavlink.MAV_STATE_CRITICAL
MAV_STATE_EMERGENCY: int = mavlink.MAV_STATE_EMERGENCY
MAV_TYPE_GCS: int = mavlink.MAV_TYPE_GCS
MAV_COMP_ID_AUTOPILOT1: int = mavlink.MAV_COMP_ID_AUTOPILOT1
GPS_FIX_TYPE_3D_FIX: int = mavlink.GPS_FIX_TYPE_3D_FIX
# GLOBAL_POSITION_INT.hdg "If unknown, set to: UINT16_MAX" (common.xml).
HDG_UNKNOWN = 0xFFFF

# ICAO standard atmosphere (ISO 2533), troposphere.
ISA_P0_HPA = 1013.25
ISA_T0_K = 288.15
ISA_LAPSE_K_PER_M = 0.0065
ISA_EXPONENT = 8.31446 * ISA_LAPSE_K_PER_M / (9.80665 * 0.0289644)

# Below this the velocity says nothing about the direction of travel.
TRACK_MIN_SPEED_MS = 0.5

# Bound on one datagram read (E-10); a MAVLink v2 frame is at most 280 B.
MAX_DATAGRAM_BYTES = 65535


class FieldTableError(RuntimeError):
    """pymavlink does not describe a field the way the reader expects."""


def check_field_table() -> dict[tuple[str, str], float]:
    """Check FIELD_TABLE against pymavlink and return the scales.

    Raises FieldTableError naming every disagreement.
    """
    problems: list[str] = []
    scales: dict[tuple[str, str], float] = {}
    for cls_name, fld, unit in FIELD_TABLE:
        cls = getattr(mavlink, cls_name, None)
        if cls is None:
            problems.append(f"{cls_name}: not in the dialect")
            continue
        if fld not in cls.fieldnames:
            problems.append(f"{cls.msgname}.{fld}: not a field ({cls.fieldnames})")
            continue
        declared = cls.fieldunits_by_name.get(fld, "")
        if declared != unit:
            problems.append(f"{cls.msgname}.{fld}: pymavlink says unit {declared!r}, reader expects {unit!r}")
            continue
        if unit:
            if unit not in UNIT_SCALE:
                problems.append(f"{cls.msgname}.{fld}: no scale for unit {unit!r}")
                continue
            scales[(cls.msgname, fld)] = UNIT_SCALE[unit]
    if problems:
        raise FieldTableError("; ".join(problems))
    return scales


SCALE = check_field_table()


def scale(msgname: str, fld: str) -> float:
    return SCALE[(msgname, fld)]


def pressure_altitude_m(press_abs_hpa: float) -> float | None:
    """ISA pressure altitude of an absolute pressure; None when not usable."""
    if not math.isfinite(press_abs_hpa) or press_abs_hpa <= 0:
        return None
    alt_m: float = (ISA_T0_K / ISA_LAPSE_K_PER_M) * (1.0 - (press_abs_hpa / ISA_P0_HPA) ** ISA_EXPONENT)
    return alt_m


def rfc3339_ms(epoch_s: float) -> str:
    dt = datetime.fromtimestamp(epoch_s, tz=UTC)
    return dt.strftime("%Y-%m-%dT%H:%M:%S.") + f"{dt.microsecond // 1000:03d}Z"


class RefuseWrites:
    """A file object for pymavlink that refuses every write.

    pymavlink's MAVLink object writes to its ``file`` when a ``*_send``
    method is called. The reader never calls one; if anything ever did,
    this raises instead of reaching the vehicle. ``writes`` counts the
    attempts so the tests can assert there were none.
    """

    def __init__(self) -> None:
        self.writes = 0

    def write(self, data: bytes) -> int:
        self.writes += 1
        raise PermissionError("sim/mav_reader.py is receive only (INV-01): refusing to write to the vehicle")


@dataclass
class Counters:
    received: dict[str, int] = field(default_factory=dict)
    other_vehicle: int = 0
    other_component: int = 0
    lines: int = 0
    datagrams: int = 0
    parse_errors: int = 0

    def count(self, msgname: str) -> None:
        self.received[msgname] = self.received.get(msgname, 0) + 1


@dataclass
class VehicleState:
    """What the reader has heard from the vehicle besides positions."""

    utc_offset_s: float | None = None
    armed: bool = False
    system_status: int | None = None
    fix_type: int | None = None
    alt_ellipsoid_m: float | None = None
    alt_pressure_m: float | None = None


class Reader:
    """Turns one vehicle's pymavlink messages into sim/vehicle/v1 lines.

    Pure: no I/O. ``feed`` returns the line for a GLOBAL_POSITION_INT of
    the vehicle, else None.
    """

    def __init__(self, sysid: int, *, mark_alt_invalid: bool = False) -> None:
        if not 1 <= sysid <= 254:
            raise ValueError(f"sysid {sysid} is not 1..254")
        self.sysid = sysid
        self.mark_alt_invalid = mark_alt_invalid
        self.state = VehicleState()
        self.counters = Counters()

    def feed(self, msg: Any) -> dict[str, Any] | None:
        name = msg.get_type()
        if msg.get_srcSystem() != self.sysid:
            self.counters.other_vehicle += 1
            return None
        self.counters.count(name)
        if name == "HEARTBEAT":
            # MAVProxy and a GCS heartbeat too; only the autopilot's says
            # whether the vehicle is armed.
            if msg.get_srcComponent() != MAV_COMP_ID_AUTOPILOT1 or msg.type == MAV_TYPE_GCS:
                self.counters.other_component += 1
                return None
            self.state.armed = bool(msg.base_mode & MAV_MODE_FLAG_SAFETY_ARMED)
            self.state.system_status = int(msg.system_status)
            return None
        if name == "SYSTEM_TIME":
            if msg.time_unix_usec > 0:
                self.state.utc_offset_s = msg.time_unix_usec * scale(name, "time_unix_usec") - msg.time_boot_ms * scale(
                    name, "time_boot_ms"
                )
            return None
        if name == "GPS_RAW_INT":
            self.state.fix_type = int(msg.fix_type)
            alt_e = int(msg.alt_ellipsoid)
            self.state.alt_ellipsoid_m = alt_e * scale(name, "alt_ellipsoid") if alt_e != 0 else None
            return None
        if name == "SCALED_PRESSURE":
            self.state.alt_pressure_m = pressure_altitude_m(float(msg.press_abs) * scale(name, "press_abs"))
            return None
        if name == "GLOBAL_POSITION_INT":
            return self._position(msg)
        return None

    def _position(self, msg: Any) -> dict[str, Any]:
        n = "GLOBAL_POSITION_INT"
        st = self.state
        vn = msg.vx * scale(n, "vx")
        ve = msg.vy * scale(n, "vy")
        vd = msg.vz * scale(n, "vz")
        speed = math.hypot(vn, ve)
        track: float | None
        if speed >= TRACK_MIN_SPEED_MS:
            track = math.degrees(math.atan2(ve, vn)) % 360.0
        elif msg.hdg != HDG_UNKNOWN:
            track = (msg.hdg * scale(n, "hdg")) % 360.0
        else:
            track = None
        if track is not None and track >= 360.0:
            track = 0.0
        fix_ok = st.fix_type is not None and st.fix_type >= GPS_FIX_TYPE_3D_FIX
        ts: str | None = None
        boot_s = msg.time_boot_ms * scale(n, "time_boot_ms")
        if st.utc_offset_s is not None:
            ts = rfc3339_ms(st.utc_offset_s + boot_s)
        if st.system_status in (MAV_STATE_CRITICAL, MAV_STATE_EMERGENCY):
            status = "emergency"
        elif st.armed:
            status = "airborne"
        else:
            status = "ground"
        line: dict[str, Any] = {
            "schema": SCHEMA,
            "sysid": self.sysid,
            "ts": ts,
            "time_boot_ms": int(msg.time_boot_ms),
            "lat_deg": msg.lat * scale(n, "lat"),
            "lon_deg": msg.lon * scale(n, "lon"),
            "alt_amsl_m": msg.alt * scale(n, "alt"),
            "alt_hae_m": st.alt_ellipsoid_m if fix_ok else None,
            "alt_pressure_m": st.alt_pressure_m,
            "height_takeoff_m": msg.relative_alt * scale(n, "relative_alt"),
            "speed_ms": speed,
            "track_deg": track,
            "vspeed_ms": -vd,
            "vn_ms": vn,
            "ve_ms": ve,
            "armed": st.armed,
            "status": status,
            "fix_ok": fix_ok,
            "alt_invalid": self.mark_alt_invalid or not fix_ok,
        }
        self.counters.lines += 1
        return line


def new_parser() -> tuple[Any, RefuseWrites]:
    """A pymavlink parser that can never write."""
    sink = RefuseWrites()
    mav = mavlink.MAVLink(sink, srcSystem=0, srcComponent=0)
    mav.robust_parsing = True
    return mav, sink


def parse_datagram(mav: Any, data: bytes, counters: Counters) -> list[Any]:
    """Every complete message in one datagram; bad bytes are counted."""
    try:
        msgs = mav.parse_buffer(data)
    except mavlink.MAVError:
        counters.parse_errors += 1
        return []
    out = []
    for m in msgs or []:
        if m.get_type() == "BAD_DATA":
            counters.parse_errors += 1
            continue
        out.append(m)
    return out


def parse_endpoint(spec: str) -> tuple[str, int]:
    """'udp:HOST:PORT' -> (HOST, PORT); only local UDP is accepted."""
    parts = spec.split(":")
    if len(parts) != 3 or parts[0] != "udp":
        raise ValueError(f"{spec!r} is not udp:HOST:PORT")
    host, port_s = parts[1], parts[2]
    port = int(port_s)
    if not 1 <= port <= 65535:
        raise ValueError(f"{spec!r}: port out of range")
    return host, port


def encode_line(line: dict[str, Any]) -> bytes:
    return (json.dumps(line, separators=(",", ":"), allow_nan=False) + "\n").encode()


def run(
    sysid: int,
    out: str,
    emit: str | None,
    mark_alt_invalid: bool,
    max_seconds: float | None,
    write: Callable[[bytes], None],
    status: Callable[[str], None],
) -> int:
    reader = Reader(sysid, mark_alt_invalid=mark_alt_invalid)
    mav, sink = new_parser()
    host, port = parse_endpoint(out)
    rx = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    rx.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    rx.bind((host, port))
    rx.settimeout(1.0)
    tx: socket.socket | None = None
    emit_addr: tuple[str, int] | None = None
    if emit:
        emit_addr = parse_endpoint(emit)
        tx = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    started = time.monotonic()
    last_status = started
    status(f"mav_reader: sysid {sysid} listening on {host}:{port} (receive only)")
    try:
        while max_seconds is None or time.monotonic() - started < max_seconds:
            now = time.monotonic()
            if now - last_status >= 10.0:
                status(status_line(reader, sink))
                last_status = now
            try:
                data, _ = rx.recvfrom(MAX_DATAGRAM_BYTES)
            except TimeoutError:
                continue
            reader.counters.datagrams += 1
            for msg in parse_datagram(mav, data, reader.counters):
                line = reader.feed(msg)
                if line is None:
                    continue
                encoded = encode_line(line)
                write(encoded)
                if tx is not None and emit_addr is not None:
                    # To the lab's own consumer on a local port, never to
                    # the vehicle: tx is a separate, unbound socket.
                    tx.sendto(encoded, emit_addr)
    finally:
        rx.close()
        if tx is not None:
            tx.close()
        status(status_line(reader, sink))
    return 0


def status_line(reader: Reader, sink: RefuseWrites) -> str:
    c = reader.counters
    return "mav_reader: " + json.dumps(
        {
            "sysid": reader.sysid,
            "datagrams": c.datagrams,
            "lines": c.lines,
            "received": c.received,
            "other_vehicle": c.other_vehicle,
            "other_component": c.other_component,
            "parse_errors": c.parse_errors,
            "writes_refused": sink.writes,
            "utc_known": reader.state.utc_offset_s is not None,
        },
        sort_keys=True,
    )


def main(argv: Iterable[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    p.add_argument("--sysid", type=int, required=True, help="the vehicle's SYSID")
    p.add_argument("--out", required=True, help="udp:HOST:PORT, the SITL output this reader binds")
    p.add_argument("--emit", help="udp:HOST:PORT, also send each line as a datagram there")
    p.add_argument("--mark-alt-invalid", action="store_true", help="S-36: mark the geodetic altitude invalid")
    p.add_argument("--max-seconds", type=float, help="stop after this many seconds")
    args = p.parse_args(list(argv) if argv is not None else None)

    def write(b: bytes) -> None:
        sys.stdout.buffer.write(b)
        sys.stdout.buffer.flush()

    def status(s: str) -> None:
        print(s, file=sys.stderr, flush=True)

    def stop(_signum: int, _frame: object) -> NoReturn:
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)
    try:
        return run(args.sysid, args.out, args.emit, args.mark_alt_invalid, args.max_seconds, write, status)
    except KeyboardInterrupt:
        return 0
    except BrokenPipeError:
        # The consumer went away; nothing to report to.
        return 0


if __name__ == "__main__":
    sys.exit(main())

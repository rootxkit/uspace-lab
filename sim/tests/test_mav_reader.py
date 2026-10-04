"""The reader's field table against pymavlink, the frame diff, the no-send guard.

E-03: every field and unit the reader uses is pinned against pymavlink's
own message classes, and the wire layout is derived by encoding two
frames that differ in one field and diffing them, never written down.
E-01: every absence (no write, no line for another vehicle, no HAE without
a fix, no ts without UTC) is paired with its presence.
"""

from __future__ import annotations

import io
import json
import socket
import struct
import threading
import time
from pathlib import Path
from typing import Any

import pytest
from pymavlink.dialects.v20 import ardupilotmega as mavlink

import mav_reader

SCHEMA_PATH = Path(__file__).resolve().parent.parent / "schema" / "vehicle-v1.json"

# struct sizes of the MAVLink wire types (the C types pymavlink's
# fieldtypes name); used only to derive offsets from ordered_fieldnames.
TYPE_SIZE = {
    "uint8_t": 1,
    "int8_t": 1,
    "uint16_t": 2,
    "int16_t": 2,
    "uint32_t": 4,
    "int32_t": 4,
    "float": 4,
    "uint64_t": 8,
    "int64_t": 8,
    "double": 8,
    "char": 1,
}


def encoder(sysid: int = 7, compid: int = 1) -> Any:
    """A pymavlink encoder writing into memory: frames for the tests."""
    return mavlink.MAVLink(io.BytesIO(), srcSystem=sysid, srcComponent=compid)


def frame(msg: Any, enc: Any) -> bytes:
    return bytes(msg.pack(enc))


def gpi(**kw: Any) -> Any:
    v: dict[str, Any] = {
        "time_boot_ms": 10_000,
        "lat": 417151000,
        "lon": 448271000,
        "alt": 635_000,
        "relative_alt": 30_000,
        "vx": 0,
        "vy": 0,
        "vz": 0,
        "hdg": 9000,
    }
    v.update(kw)
    return mavlink.MAVLink_global_position_int_message(**v)


def heartbeat(armed: bool, status: int = mavlink.MAV_STATE_ACTIVE, mav_type: int = mavlink.MAV_TYPE_QUADROTOR) -> Any:
    base = mavlink.MAV_MODE_FLAG_SAFETY_ARMED if armed else 0
    return mavlink.MAVLink_heartbeat_message(mav_type, mavlink.MAV_AUTOPILOT_ARDUPILOTMEGA, base, 4, status, 3)


def gps_raw(fix: int, alt_ellipsoid: int) -> Any:
    return mavlink.MAVLink_gps_raw_int_message(
        0, fix, 417151000, 448271000, 635_000, 100, 100, 0, 0, 10, alt_ellipsoid, 0, 0, 0, 0, 0
    )


def parse(data: bytes) -> list[Any]:
    mav, _ = mav_reader.new_parser()
    return mav_reader.parse_datagram(mav, data, mav_reader.Counters())


# --- the field table ------------------------------------------------------------


def test_field_table_matches_pymavlink() -> None:
    for cls_name, fld, unit in mav_reader.FIELD_TABLE:
        cls = getattr(mavlink, cls_name)
        assert fld in cls.fieldnames, f"{cls.msgname}.{fld}"
        assert cls.fieldunits_by_name.get(fld, "") == unit, f"{cls.msgname}.{fld}"
    # The scales the reader uses are the ones the units imply.
    assert mav_reader.scale("GLOBAL_POSITION_INT", "lat") == 1e-7
    assert mav_reader.scale("GLOBAL_POSITION_INT", "alt") == 1e-3
    assert mav_reader.scale("GLOBAL_POSITION_INT", "vx") == 1e-2
    assert mav_reader.scale("SCALED_PRESSURE", "press_abs") == 1.0


def test_field_table_refuses_a_unit_pymavlink_does_not_declare(monkeypatch: pytest.MonkeyPatch) -> None:
    # Presence of the refusal: a table that disagrees with pymavlink stops
    # the reader instead of scaling by a remembered factor.
    bad = (*mav_reader.FIELD_TABLE, ("MAVLink_global_position_int_message", "lat", "deg"))
    monkeypatch.setattr(mav_reader, "FIELD_TABLE", bad)
    with pytest.raises(mav_reader.FieldTableError, match=r"GLOBAL_POSITION_INT\.lat"):
        mav_reader.check_field_table()
    missing = (("MAVLink_global_position_int_message", "altitude", "mm"),)
    monkeypatch.setattr(mav_reader, "FIELD_TABLE", missing)
    with pytest.raises(mav_reader.FieldTableError, match="not a field"):
        mav_reader.check_field_table()


def payload(data: bytes) -> bytes:
    """The payload of one unsigned MAVLink v2 frame (header is 10 bytes)."""
    assert data[0] == 0xFD, "a v2 frame"
    length = data[1]
    return data[10 : 10 + length]


def derived_offset(cls: Any, fld: str) -> int:
    """Offset of fld in the payload from pymavlink's own ordering."""
    types = dict(zip(cls.fieldnames, cls.fieldtypes, strict=True))
    offset = 0
    for name in cls.ordered_fieldnames:
        if name == fld:
            return offset
        n = cls.array_lengths[cls.ordered_fieldnames.index(name)] or 1
        offset += TYPE_SIZE[types[name]] * n
    raise AssertionError(fld)


@pytest.mark.parametrize(
    ("fld", "a", "b", "line_key", "delta"),
    [
        ("lat", 417151000, 417151001, "lat_deg", 1e-7),
        ("lon", 448271000, 448271010, "lon_deg", 10e-7),
        ("alt", 635_000, 636_000, "alt_amsl_m", 1.0),
        ("relative_alt", 30_000, 30_500, "height_takeoff_m", 0.5),
    ],
)
def test_two_frames_differing_in_one_field(fld: str, a: int, b: int, line_key: str, delta: float) -> None:
    enc = encoder()
    fa, fb = frame(gpi(**{fld: a}), enc), frame(gpi(**{fld: b}), encoder())
    pa, pb = payload(fa), payload(fb)
    # The bytes that differ are exactly the field's, where pymavlink's
    # ordering puts it (v2 truncates trailing zeros, so compare the shorter).
    n = min(len(pa), len(pb))
    differing = [i for i in range(n) if pa[i] != pb[i]]
    off = derived_offset(mavlink.MAVLink_global_position_int_message, fld)
    assert differing and all(off <= i < off + 4 for i in differing), (fld, differing, off)
    assert struct.unpack_from("<i", pa.ljust(off + 4, b"\0"), off)[0] == a
    # And through the reader, only that member of the line moves.
    lines = []
    for f in (fa, fb):
        r = mav_reader.Reader(7)
        for m in parse(f):
            line = r.feed(m)
            if line is not None:
                lines.append(line)
    assert len(lines) == 2
    changed = {k for k in lines[0] if lines[0][k] != lines[1][k]}
    assert changed == {line_key}
    assert lines[1][line_key] - lines[0][line_key] == pytest.approx(delta, abs=1e-9)


# --- the line ---------------------------------------------------------------------


def feed_all(r: mav_reader.Reader, msgs: list[Any], sysid: int = 7) -> list[dict[str, Any]]:
    out = []
    for m in msgs:
        for p in parse(frame(m, encoder(sysid))):
            line = r.feed(p)
            if line is not None:
                out.append(line)
    return out


def test_line_from_a_flying_vehicle() -> None:
    r = mav_reader.Reader(7)
    utc_us = 1_790_942_400_000_000
    lines = feed_all(
        r,
        [
            heartbeat(armed=True),
            mavlink.MAVLink_system_time_message(utc_us, 5_000),
            gps_raw(mavlink.GPS_FIX_TYPE_3D_FIX, 650_500),
            mavlink.MAVLink_scaled_pressure_message(5_000, 1013.25, 0.0, 2000, 0),
            gpi(time_boot_ms=6_000, vx=300, vy=400, vz=-100),
        ],
    )
    assert len(lines) == 1
    line = lines[0]
    assert line["ts"] == "2026-10-02T12:00:01.000Z"  # utc - 5 s boot + 6 s
    assert line["armed"] is True and line["status"] == "airborne"
    assert line["fix_ok"] is True and line["alt_invalid"] is False
    assert line["alt_hae_m"] == pytest.approx(650.5)
    assert line["alt_pressure_m"] == pytest.approx(0.0, abs=1e-6)
    assert line["speed_ms"] == pytest.approx(5.0)
    assert line["track_deg"] == pytest.approx(53.130102, abs=1e-5)
    assert line["vspeed_ms"] == pytest.approx(1.0)
    validate_against_schema(line)


def test_unknown_time_until_utc_and_no_hae_without_a_fix() -> None:
    r = mav_reader.Reader(7)
    before = feed_all(r, [heartbeat(armed=False), gps_raw(mavlink.GPS_FIX_TYPE_2D_FIX, 650_500), gpi()])
    assert before[0]["ts"] is None
    assert before[0]["alt_hae_m"] is None
    assert before[0]["fix_ok"] is False and before[0]["alt_invalid"] is True
    assert before[0]["status"] == "ground"
    assert before[0]["track_deg"] == pytest.approx(90.0)  # hover: the heading
    # SYSTEM_TIME with time_unix_usec 0 is "not known" and keeps ts null.
    still = feed_all(r, [mavlink.MAVLink_system_time_message(0, 5_000), gpi()])
    assert still[0]["ts"] is None
    after = feed_all(
        r,
        [mavlink.MAVLink_system_time_message(1_790_942_400_000_000, 5_000), gps_raw(mavlink.GPS_FIX_TYPE_3D_FIX, 650_500), gpi()],
    )
    assert after[0]["ts"] is not None
    assert after[0]["alt_hae_m"] == pytest.approx(650.5)
    for line in before + still + after:
        validate_against_schema(line)


def test_host_clock_puts_the_vehicle_on_the_host_time() -> None:
    # SITL's UTC lags the host by about 1.6 s: with the host clock the
    # line is on the host's time at the SYSTEM_TIME's receipt, whatever
    # the vehicle's UTC says, and the skew is kept for the status line.
    host_at_systime = 1_790_942_401.600
    r = mav_reader.Reader(7, host_clock=lambda: host_at_systime)
    lines = feed_all(
        r,
        [mavlink.MAVLink_system_time_message(1_790_942_400_000_000, 5_000), gpi(time_boot_ms=6_000)],
    )
    assert lines[0]["ts"] == "2026-10-02T12:00:02.600Z"  # host 12:00:01.600 at boot 5 s, + 1 s
    assert r.state.utc_skew_s == pytest.approx(-1.6)
    # Presence beside it: the vehicle clock keeps the vehicle's UTC.
    v = mav_reader.Reader(7)
    lines = feed_all(v, [mavlink.MAVLink_system_time_message(1_790_942_400_000_000, 5_000), gpi(time_boot_ms=6_000)])
    assert lines[0]["ts"] == "2026-10-02T12:00:01.000Z"
    assert v.state.utc_skew_s is None
    # No UTC from the vehicle: no ts on either clock (R-16).
    h = mav_reader.Reader(7, host_clock=lambda: host_at_systime)
    assert feed_all(h, [mavlink.MAVLink_system_time_message(0, 5_000), gpi()])[0]["ts"] is None


def test_unknown_heading_and_emergency() -> None:
    r = mav_reader.Reader(7)
    lines = feed_all(r, [heartbeat(armed=True, status=mavlink.MAV_STATE_EMERGENCY), gpi(hdg=mav_reader.HDG_UNKNOWN)])
    assert lines[0]["track_deg"] is None
    assert lines[0]["status"] == "emergency"


def test_mark_alt_invalid_is_s36() -> None:
    plain = feed_all(mav_reader.Reader(7), [gps_raw(mavlink.GPS_FIX_TYPE_3D_FIX, 650_500), gpi()])
    marked = feed_all(mav_reader.Reader(7, mark_alt_invalid=True), [gps_raw(mavlink.GPS_FIX_TYPE_3D_FIX, 650_500), gpi()])
    assert plain[0]["alt_invalid"] is False
    assert marked[0]["alt_invalid"] is True


def test_other_vehicles_and_ground_station_heartbeats_are_ignored() -> None:
    r = mav_reader.Reader(7)
    # Presence: the vehicle's own heartbeat arms it.
    feed_all(r, [heartbeat(armed=True)])
    assert r.state.armed
    # A GCS heartbeat saying disarmed, and another vehicle's, change nothing.
    feed_all(r, [heartbeat(armed=False, mav_type=mavlink.MAV_TYPE_GCS)])
    for p in parse(frame(heartbeat(armed=False), encoder(sysid=7, compid=190))):
        r.feed(p)
    assert r.state.armed
    assert feed_all(r, [gpi()], sysid=8) == []
    assert r.counters.other_vehicle == 1
    assert r.counters.other_component == 2
    assert feed_all(r, [gpi()], sysid=7) != []


def test_garbage_is_counted_not_fatal() -> None:
    mav, _ = mav_reader.new_parser()
    c = mav_reader.Counters()
    good = frame(gpi(), encoder())
    msgs = mav_reader.parse_datagram(mav, b"\x00\xff\x13garbage" + good, c)
    assert [m.get_type() for m in msgs] == ["GLOBAL_POSITION_INT"]


# --- receive only -----------------------------------------------------------------


def test_the_parser_cannot_write() -> None:
    mav, sink = mav_reader.new_parser()
    # Presence of the refusal: a send through the parser raises.
    with pytest.raises(PermissionError, match="receive only"):
        mav.heartbeat_send(mavlink.MAV_TYPE_GCS, mavlink.MAV_AUTOPILOT_INVALID, 0, 0, 0)
    assert sink.writes == 1


def test_a_full_run_never_writes(monkeypatch: pytest.MonkeyPatch) -> None:
    """Feed real datagrams through run(): lines come out, nothing is written back."""
    sinks: list[mav_reader.RefuseWrites] = []
    real_new_parser = mav_reader.new_parser

    def spy() -> tuple[Any, mav_reader.RefuseWrites]:
        mav, sink = real_new_parser()
        sinks.append(sink)
        return mav, sink

    monkeypatch.setattr(mav_reader, "new_parser", spy)
    sent_back: list[bytes] = []
    real_socket = socket.socket

    class Spy(socket.socket):
        def sendto(self, *a: Any, **kw: Any) -> int:  # the --emit path only
            sent_back.append(a[0])
            return int(super().sendto(*a, **kw))

        def connect(self, *a: Any, **kw: Any) -> None:
            raise AssertionError("the reader never connects a socket")

    monkeypatch.setattr(socket, "socket", Spy)
    port = free_udp_port()
    emit_rx = real_socket(socket.AF_INET, socket.SOCK_DGRAM)
    emit_rx.bind(("127.0.0.1", 0))
    emit_rx.settimeout(5)
    emit_port = emit_rx.getsockname()[1]
    out: list[bytes] = []

    def feed() -> None:
        tx = real_socket(socket.AF_INET, socket.SOCK_DGRAM)
        deadline = time.monotonic() + 5
        enc = encoder()
        while time.monotonic() < deadline and len(out) < 3:
            tx.sendto(frame(heartbeat(armed=True), enc) + frame(gpi(), enc), ("127.0.0.1", port))
            time.sleep(0.05)
        tx.close()

    t = threading.Thread(target=feed)
    t.start()
    rc = mav_reader.run(7, f"udp:127.0.0.1:{port}", f"udp:127.0.0.1:{emit_port}", False, 3.0, out.append, lambda s: None)
    t.join()
    assert rc == 0
    assert len(out) >= 3, "lines came out"
    assert sinks and all(s.writes == 0 for s in sinks), "nothing was written through the parser"
    # Everything sendto carried went to the emit port and is a line.
    got, _ = emit_rx.recvfrom(65535)
    emit_rx.close()
    assert json.loads(got)["schema"] == "sim/vehicle/v1"
    assert all(json.loads(b)["sysid"] == 7 for b in sent_back)


def free_udp_port() -> int:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("127.0.0.1", 0))
    port = int(s.getsockname()[1])
    s.close()
    return port


def test_endpoint_parsing() -> None:
    assert mav_reader.parse_endpoint("udp:127.0.0.1:14560") == ("127.0.0.1", 14560)
    for bad in ("tcp:127.0.0.1:5760", "udp:127.0.0.1", "udp:127.0.0.1:0", "udp:127.0.0.1:x"):
        with pytest.raises(ValueError):
            mav_reader.parse_endpoint(bad)


# --- the schema -----------------------------------------------------------------


def validate_against_schema(line: dict[str, Any]) -> None:
    jsonschema = pytest.importorskip("jsonschema")
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    jsonschema.Draft202012Validator(schema, format_checker=jsonschema.FormatChecker()).validate(line)


def test_schema_refuses_what_the_reader_never_writes() -> None:
    jsonschema = pytest.importorskip("jsonschema")
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    v = jsonschema.Draft202012Validator(schema)
    good = feed_all(mav_reader.Reader(7), [gpi()])[0]
    assert v.is_valid(good)
    for key, value in (("sysid", 0), ("status", "flying"), ("ts", "2026-10-02T12:00:00Z"), ("extra", 1)):
        bad = dict(good)
        bad[key] = value
        assert not v.is_valid(bad), key

"""The harness: plan validation, and every step waits for the vehicle.

The harness is driven against a stand-in link that records what it was
asked to send and replays vehicle messages, so each step's confirmation
rule is exercised both ways (E-01): confirmed when the vehicle reports
it, failed when the vehicle never does.
"""

from __future__ import annotations

import inspect
import io
from collections.abc import Callable
from typing import Any

import pytest
from pymavlink import mavutil
from pymavlink.dialects.v20 import ardupilotmega as mavlink

import fly


def test_plan_validation() -> None:
    ok = {"sysid": 1, "steps": [{"action": "arm"}, {"action": "takeoff", "alt_rel_m": 30}, {"action": "land"}]}
    assert fly.validate_plan(ok) is ok
    bad = [
        {},
        {"sysid": 0, "steps": [{"action": "arm"}]},
        {"sysid": 1, "steps": []},
        {"sysid": 1, "steps": [{"action": "loop"}]},
        {"sysid": 1, "steps": [{"action": "takeoff"}]},
        {"sysid": 1, "steps": [{"action": "goto", "lat_deg": 91, "lon_deg": 0, "alt_rel_m": 10}]},
        {"sysid": 1, "steps": [{"action": "goto", "lat_deg": 1, "lon_deg": 0}]},
        {"sysid": 1, "steps": [{"action": "hold", "for_s": 0}]},
        {"sysid": 1, "steps": [{"action": "arm", "at_s": -1}]},
    ]
    for p in bad:
        with pytest.raises(fly.PlanError):
            fly.validate_plan(p)


def test_writer_keywords_are_pymavlink_field_names() -> None:
    # E-03: the keywords the harness passes are the message's own fields.
    sig = inspect.signature(mavlink.MAVLink.set_position_target_global_int_send)
    fields = mavlink.MAVLink_set_position_target_global_int_message.fieldnames
    assert [p for p in sig.parameters if p not in ("self", "force_mavlink1")] == fields
    assert "MAVLink_command_long_message" in dir(mavlink)
    assert mavlink.MAVLink_command_long_message.fieldnames[:3] == ["target_system", "target_component", "command"]


class FakeVehicle:
    """A link: records sends, answers with vehicle messages from a script."""

    def __init__(self, sysid: int, script: Callable[[FakeVehicle], None] | None = None) -> None:
        self.sysid = sysid
        self.enc = mavlink.MAVLink(io.BytesIO(), srcSystem=sysid, srcComponent=1)
        self.inbox: list[Any] = []
        self.sent: list[str] = []
        self.armed = False
        self.mode = 0
        self.lat, self.lon, self.alt_rel = 41.7151, 44.8271, 0.0
        self.target: tuple[float, float, float] | None = None
        self.script = script
        self.mav = self

    # mavutil-like surface used by the harness
    def mode_mapping(self) -> dict[str, int]:
        # mavutil's table is number -> name; a link answers name -> number.
        return {name: number for number, name in mavutil.mode_mapping_acm.items()}

    def set_mode(self, number: int) -> None:
        self.sent.append(f"set_mode:{number}")
        self.mode = number
        if mavutil.mode_mapping_acm.get(number) == "LAND":
            self.alt_rel = 0.0
            self.armed = False

    def arducopter_arm(self) -> None:
        self.sent.append("arm")
        if self.script is None:
            self.armed = True

    def command_long_send(self, *a: Any) -> None:
        self.sent.append(f"command_long:{a[2]}")
        if a[2] == fly.MAV_CMD_NAV_TAKEOFF and self.armed:
            self.alt_rel = float(a[-1])

    def set_position_target_global_int_send(self, **kw: Any) -> None:
        self.sent.append("position_target")
        self.target = (kw["lat_int"] * 1e-7, kw["lon_int"] * 1e-7, kw["alt"])
        self.lat, self.lon, self.alt_rel = self.target

    def recv_match(self, blocking: bool, timeout: float) -> Any:
        if not self.inbox:
            base = mavlink.MAV_MODE_FLAG_SAFETY_ARMED if self.armed else 0
            self.inbox.append(mavlink.MAVLink_heartbeat_message(2, 3, base, self.mode, 4, 3))
            self.inbox.append(
                mavlink.MAVLink_global_position_int_message(
                    1000, round(self.lat * 1e7), round(self.lon * 1e7), 600_000, round(self.alt_rel * 1000), 0, 0, 0, 0
                )
            )
        m = self.inbox.pop(0)
        m.pack(self.enc)  # gives it a header: source system and component
        return m


def run_plan(vehicle: FakeVehicle, steps: list[dict[str, Any]], timeouts: dict[str, float] | None = None) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    h = fly.Harness(vehicle, vehicle.sysid, out.append, timeouts=timeouts)
    h.run(fly.validate_plan({"sysid": vehicle.sysid, "steps": steps}))
    return out


def test_a_plan_flown_and_each_step_confirmed_by_the_vehicle() -> None:
    v = FakeVehicle(3)
    target = {"lat_deg": 41.7160, "lon_deg": 44.8271, "alt_rel_m": 30.0}
    out = run_plan(
        v,
        [
            {"action": "arm"},
            {"action": "takeoff", "alt_rel_m": 30},
            {"action": "goto", **target, "speed_ms": 10},
            {"action": "hold", "for_s": 0.2},
            {"action": "land"},
        ],
    )
    confirmed = [e for e in out if e["state"] == "confirmed"]
    assert [e["action"] for e in confirmed] == ["arm", "takeoff", "goto", "hold", "land"]
    # Observed values, not commanded ones.
    assert confirmed[0]["observed"]["armed"] is True
    assert confirmed[1]["observed"]["alt_rel_m"] == pytest.approx(30.0)
    assert confirmed[2]["observed"]["lat_deg"] == pytest.approx(41.7160)
    assert confirmed[4]["observed"]["armed"] is False
    assert "position_target" in v.sent


def test_a_vehicle_that_never_arms_fails_the_step() -> None:
    v = FakeVehicle(3, script=lambda _v: None)  # arm requests are ignored
    with pytest.raises(fly.StepFailed, match="arm: not confirmed"):
        run_plan(v, [{"action": "arm"}], timeouts={"arm": 0.5, "link": 1.0})
    assert v.sent.count("arm") >= 1


def test_takeoff_refused_when_the_vehicle_says_disarmed() -> None:
    v = FakeVehicle(3)
    with pytest.raises(fly.StepFailed, match="not armed"):
        run_plan(v, [{"action": "takeoff", "alt_rel_m": 30}])


def test_messages_from_another_vehicle_confirm_nothing() -> None:
    v = FakeVehicle(3)
    h = fly.Harness(v, 4, lambda _e: None, timeouts={"link": 0.3})
    with pytest.raises(fly.StepFailed, match="link"):
        h.arm()


def test_distance() -> None:
    assert fly.distance_m(41.7151, 44.8271, 41.7151, 44.8271) == 0
    # 0.001 deg of latitude is about 111 m.
    assert fly.distance_m(41.7151, 44.8271, 41.7161, 44.8271) == pytest.approx(111.2, abs=0.5)

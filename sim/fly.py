"""The scenario harness: fly one SITL vehicle through a plan, step by step.

    python sim/fly.py --link udpin:127.0.0.1:14660 --plan plan.json
    python sim/fly.py --link udpin:127.0.0.1:14660 --plan -   (plan on stdin)

THE ONLY SEND PATH IN THIS REPOSITORY, AND ONLY TOWARDS SITL (INV-01).

This is the one program in the lab that commands a vehicle, and it exists
only to fly the simulated ones a scenario needs: arm, take off, fly to a
point, hold, land. It connects to the harness port run_sitl.sh gives each
SITL instance (``SITL_FLY_PORT_BASE + i``), never to the reader's port and
never to anything but a local SITL. No bridge, simulator or system
imports it (CI enforces it: ``sim/tests/test_send_guard.py``); the
scenario runner starts it as a separate process in SITL mode only, and it
is part of no image but the lab's own.

Every step is confirmed by the vehicle's own telemetry (LESSONS E-08):
armed by the autopilot's HEARTBEAT, height by GLOBAL_POSITION_INT
``relative_alt``, arrival by its position, landing by the vehicle
disarming. A step that the vehicle does not confirm within its timeout
fails the run (exit 2) with what the vehicle last reported. Progress is
one JSON line per step on stdout: the observed values, not the commanded
ones (E-04).

Plan (written by the scenario runner from a scenario's steps)::

    {"sysid": 1, "t0_unix_s": 1790000000.0,
     "steps": [
       {"at_s": 0, "action": "arm"},
       {"action": "takeoff", "alt_rel_m": 30},
       {"at_s": 20, "action": "goto", "lat_deg": 41.7, "lon_deg": 44.8,
        "alt_rel_m": 30, "speed_ms": 10, "tolerance_m": 3},
       {"action": "hold", "for_s": 30},
       {"action": "land"}]}

``at_s`` delays a step until ``t0_unix_s + at_s`` (the runner gives every
vehicle the same t0); a step without it starts when the previous one is
confirmed.
"""

from __future__ import annotations

import argparse
import json
import math
import sys
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from pymavlink import mavutil
from pymavlink.dialects.v20 import ardupilotmega as mavlink

EARTH_RADIUS_M = 6371008.8

# Read from the dialect, never written as numbers.
MAV_CMD_NAV_TAKEOFF: int = mavlink.MAV_CMD_NAV_TAKEOFF
MAV_CMD_DO_CHANGE_SPEED: int = mavlink.MAV_CMD_DO_CHANGE_SPEED
SPEED_TYPE_GROUNDSPEED: int = mavlink.SPEED_TYPE_GROUNDSPEED
MAV_FRAME_GLOBAL_RELATIVE_ALT_INT: int = mavlink.MAV_FRAME_GLOBAL_RELATIVE_ALT_INT
MAV_MODE_FLAG_SAFETY_ARMED: int = mavlink.MAV_MODE_FLAG_SAFETY_ARMED
MAV_COMP_ID_AUTOPILOT1: int = mavlink.MAV_COMP_ID_AUTOPILOT1
MAV_TYPE_GCS: int = mavlink.MAV_TYPE_GCS
MAV_COMP_ID_MISSIONPLANNER: int = mavlink.MAV_COMP_ID_MISSIONPLANNER
# The conventional ground-station SYSID the harness speaks as.
HARNESS_SYSID = 255
# Position only: every velocity, acceleration and yaw input ignored.
POSITION_ONLY_MASK: int = (
    mavlink.POSITION_TARGET_TYPEMASK_VX_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_VY_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_VZ_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_AX_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_AY_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_AZ_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_YAW_IGNORE
    | mavlink.POSITION_TARGET_TYPEMASK_YAW_RATE_IGNORE
)

DEFAULT_TIMEOUTS_S = {"link": 60.0, "arm": 90.0, "takeoff": 90.0, "goto": 600.0, "land": 180.0}
RESEND_S = 3.0
ACTIONS = ("arm", "takeoff", "goto", "hold", "land")


class PlanError(ValueError):
    """The plan is not one this harness can fly."""


class StepFailed(RuntimeError):
    """The vehicle did not confirm a step."""


@dataclass
class Observed:
    """The vehicle's own last report."""

    armed: bool = False
    custom_mode: int | None = None
    lat_deg: float | None = None
    lon_deg: float | None = None
    alt_rel_m: float | None = None
    time_boot_ms: int | None = None

    def as_dict(self) -> dict[str, Any]:
        return {
            "armed": self.armed,
            "custom_mode": self.custom_mode,
            "lat_deg": self.lat_deg,
            "lon_deg": self.lon_deg,
            "alt_rel_m": self.alt_rel_m,
            "time_boot_ms": self.time_boot_ms,
        }


def distance_m(lat1: float, lon1: float, lat2: float, lon2: float) -> float:
    """Haversine distance: only for confirming arrival within metres."""
    p1, p2 = math.radians(lat1), math.radians(lat2)
    dp, dl = p2 - p1, math.radians(lon2 - lon1)
    a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
    return 2 * EARTH_RADIUS_M * math.asin(math.sqrt(min(1.0, a)))


def validate_plan(plan: Any) -> dict[str, Any]:
    if not isinstance(plan, dict):
        raise PlanError("the plan is not an object")
    sysid = plan.get("sysid")
    if not isinstance(sysid, int) or not 1 <= sysid <= 254:
        raise PlanError("sysid must be 1..254")
    steps = plan.get("steps")
    if not isinstance(steps, list) or not steps or len(steps) > 1000:
        raise PlanError("steps must be a list of 1..1000 steps")
    for i, s in enumerate(steps):
        if not isinstance(s, dict) or s.get("action") not in ACTIONS:
            raise PlanError(f"steps[{i}]: action must be one of {ACTIONS}")
        a = s["action"]
        if "at_s" in s and (not isinstance(s["at_s"], int | float) or s["at_s"] < 0):
            raise PlanError(f"steps[{i}].at_s must be >= 0")
        if a == "takeoff" and not _positive(s.get("alt_rel_m")):
            raise PlanError(f"steps[{i}]: takeoff needs alt_rel_m > 0")
        if a == "goto":
            for k in ("lat_deg", "lon_deg", "alt_rel_m"):
                if not isinstance(s.get(k), int | float) or not math.isfinite(s[k]):
                    raise PlanError(f"steps[{i}]: goto needs a finite {k}")
            if not -90 <= s["lat_deg"] <= 90 or not -180 <= s["lon_deg"] <= 180:
                raise PlanError(f"steps[{i}]: goto position out of range")
            if "speed_ms" in s and not _positive(s["speed_ms"]):
                raise PlanError(f"steps[{i}]: speed_ms must be > 0")
        if a == "hold" and not _positive(s.get("for_s")):
            raise PlanError(f"steps[{i}]: hold needs for_s > 0")
    return plan


def _positive(v: Any) -> bool:
    return isinstance(v, int | float) and math.isfinite(v) and v > 0


class Harness:
    """Flies one vehicle; every step waits for the vehicle's confirmation."""

    def __init__(
        self,
        master: Any,
        sysid: int,
        emit: Callable[[dict[str, Any]], None],
        clock: Callable[[], float] = time.time,
        timeouts: dict[str, float] | None = None,
    ) -> None:
        self.master = master
        self.sysid = sysid
        self.emit = emit
        self.clock = clock
        self.timeouts = dict(DEFAULT_TIMEOUTS_S)
        if timeouts:
            self.timeouts.update(timeouts)
        self.obs = Observed()

    # -- reading ---------------------------------------------------------------

    def pump(self, timeout_s: float = 0.5) -> None:
        """Read what the vehicle sent for up to timeout_s; update obs."""
        deadline = time.monotonic() + timeout_s
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return
            msg = self.master.recv_match(blocking=True, timeout=remaining)
            if msg is None:
                return
            self.observe(msg)

    def observe(self, msg: Any) -> None:
        if msg.get_srcSystem() != self.sysid:
            return
        t = msg.get_type()
        if t == "HEARTBEAT":
            if msg.get_srcComponent() != MAV_COMP_ID_AUTOPILOT1 or msg.type == MAV_TYPE_GCS:
                return
            self.obs.armed = bool(msg.base_mode & MAV_MODE_FLAG_SAFETY_ARMED)
            self.obs.custom_mode = int(msg.custom_mode)
        elif t == "GLOBAL_POSITION_INT":
            self.obs.lat_deg = msg.lat * 1e-7
            self.obs.lon_deg = msg.lon * 1e-7
            self.obs.alt_rel_m = msg.relative_alt * 1e-3
            self.obs.time_boot_ms = int(msg.time_boot_ms)

    def wait_until(self, what: str, ok: Callable[[], bool], timeout_s: float, resend: Callable[[], None] | None) -> None:
        deadline = time.monotonic() + timeout_s
        next_send = time.monotonic()
        while not ok():
            now = time.monotonic()
            if now >= deadline:
                raise StepFailed(f"{what}: not confirmed by the vehicle within {timeout_s:.0f} s")
            if resend is not None and now >= next_send:
                resend()
                next_send = now + RESEND_S
            self.pump(0.5)

    # -- the steps -------------------------------------------------------------

    def mode_number(self, name: str) -> int:
        mapping = self.master.mode_mapping()
        if not mapping or name not in mapping:
            raise StepFailed(f"the vehicle reports no mode {name}")
        return int(mapping[name])

    def set_mode(self, name: str) -> None:
        number = self.mode_number(name)
        self.wait_until(
            f"mode {name}",
            lambda: self.obs.custom_mode == number,
            self.timeouts["arm"],
            lambda: self.master.set_mode(number),
        )

    def arm(self) -> None:
        self.wait_until("link", lambda: self.obs.custom_mode is not None, self.timeouts["link"], None)
        self.set_mode("GUIDED")
        self.wait_until("arm", lambda: self.obs.armed, self.timeouts["arm"], self.master.arducopter_arm)

    def takeoff(self, alt_rel_m: float) -> None:
        if not self.obs.armed:
            raise StepFailed("takeoff: the vehicle reports it is not armed")

        def send() -> None:
            self.master.mav.command_long_send(
                self.sysid, MAV_COMP_ID_AUTOPILOT1, MAV_CMD_NAV_TAKEOFF, 0, 0, 0, 0, 0, 0, 0, alt_rel_m
            )

        self.wait_until(
            f"takeoff to {alt_rel_m} m",
            lambda: self.obs.alt_rel_m is not None and self.obs.alt_rel_m >= alt_rel_m * 0.95,
            self.timeouts["takeoff"],
            send,
        )

    def goto(self, lat_deg: float, lon_deg: float, alt_rel_m: float, speed_ms: float | None, tolerance_m: float) -> None:
        if speed_ms is not None:
            self.master.mav.command_long_send(
                self.sysid, MAV_COMP_ID_AUTOPILOT1, MAV_CMD_DO_CHANGE_SPEED, 0, SPEED_TYPE_GROUNDSPEED, speed_ms, -1, 0, 0, 0, 0
            )

        def send() -> None:
            self.master.mav.set_position_target_global_int_send(
                time_boot_ms=0,
                target_system=self.sysid,
                target_component=MAV_COMP_ID_AUTOPILOT1,
                coordinate_frame=MAV_FRAME_GLOBAL_RELATIVE_ALT_INT,
                type_mask=POSITION_ONLY_MASK,
                lat_int=round(lat_deg * 1e7),
                lon_int=round(lon_deg * 1e7),
                alt=alt_rel_m,
                vx=0,
                vy=0,
                vz=0,
                afx=0,
                afy=0,
                afz=0,
                yaw=0,
                yaw_rate=0,
            )

        def arrived() -> bool:
            o = self.obs
            if o.lat_deg is None or o.lon_deg is None or o.alt_rel_m is None:
                return False
            return distance_m(o.lat_deg, o.lon_deg, lat_deg, lon_deg) <= tolerance_m and abs(o.alt_rel_m - alt_rel_m) <= max(
                1.0, tolerance_m
            )

        self.wait_until(f"goto {lat_deg:.7f},{lon_deg:.7f}", arrived, self.timeouts["goto"], send)

    def hold(self, for_s: float) -> dict[str, float]:
        o = self.obs
        if o.lat_deg is None or o.lon_deg is None:
            raise StepFailed("hold: no position from the vehicle")
        lat0, lon0 = o.lat_deg, o.lon_deg
        end = time.monotonic() + for_s
        drift = 0.0
        while time.monotonic() < end:
            self.pump(min(0.5, max(0.0, end - time.monotonic())))
            if self.obs.lat_deg is not None and self.obs.lon_deg is not None:
                drift = max(drift, distance_m(lat0, lon0, self.obs.lat_deg, self.obs.lon_deg))
        return {"max_drift_m": drift}

    def land(self) -> None:
        number = self.mode_number("LAND")
        self.wait_until(
            "land (vehicle disarmed)",
            lambda: not self.obs.armed,
            self.timeouts["land"],
            lambda: self.master.set_mode(number) if self.obs.custom_mode != number else None,
        )

    def run(self, plan: dict[str, Any]) -> None:
        t0 = float(plan.get("t0_unix_s") or self.clock())
        for i, step in enumerate(plan["steps"]):
            at_s = step.get("at_s")
            if at_s is not None:
                while self.clock() < t0 + float(at_s):
                    self.pump(min(0.5, max(0.01, t0 + float(at_s) - self.clock())))
            action = step["action"]
            self.emit({"sysid": self.sysid, "step": i, "action": action, "state": "started", "at_unix_s": self.clock()})
            extra: dict[str, Any] = {}
            if action == "arm":
                self.arm()
            elif action == "takeoff":
                self.takeoff(float(step["alt_rel_m"]))
            elif action == "goto":
                self.goto(
                    float(step["lat_deg"]),
                    float(step["lon_deg"]),
                    float(step["alt_rel_m"]),
                    float(step["speed_ms"]) if "speed_ms" in step else None,
                    float(step.get("tolerance_m", 3.0)),
                )
            elif action == "hold":
                extra = self.hold(float(step["for_s"]))
            elif action == "land":
                self.land()
            self.emit(
                {
                    "sysid": self.sysid,
                    "step": i,
                    "action": action,
                    "state": "confirmed",
                    "at_unix_s": self.clock(),
                    "observed": self.obs.as_dict(),
                    **extra,
                }
            )


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    p.add_argument("--link", required=True, help="udpin:127.0.0.1:PORT, the vehicle's harness port")
    p.add_argument("--plan", required=True, help="plan file, or - for stdin")
    args = p.parse_args(argv)
    if not args.link.startswith(("udpin:127.0.0.1:", "udpin:localhost:")):
        print("fly: the harness flies a local SITL only (udpin:127.0.0.1:PORT)", file=sys.stderr)
        return 2
    raw = sys.stdin.read() if args.plan == "-" else open(args.plan, encoding="utf-8").read()
    try:
        plan = validate_plan(json.loads(raw))
    except (PlanError, json.JSONDecodeError) as e:
        print(f"fly: {e}", file=sys.stderr)
        return 2

    def emit(d: dict[str, Any]) -> None:
        print(json.dumps(d, separators=(",", ":")), flush=True)

    master = mavutil.mavlink_connection(
        args.link, source_system=HARNESS_SYSID, source_component=MAV_COMP_ID_MISSIONPLANNER, dialect="ardupilotmega"
    )
    harness = Harness(master, plan["sysid"], emit)
    try:
        harness.run(plan)
    except StepFailed as e:
        emit({"sysid": plan["sysid"], "state": "failed", "reason": str(e), "observed": harness.obs.as_dict()})
        return 2
    except KeyboardInterrupt:
        return 130
    finally:
        master.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())

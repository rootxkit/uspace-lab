"""Regenerate knowledge/vectors/*.json from the old Python implementation.

Every expected value in the vector files is computed here by calling the
old code (rootxkit/utm, read-only), never typed by hand. The inputs are
derived from the old unit tests: each case names the test it came from.

Usage (from the uspace-lab repository root):

    PYTHONDONTWRITEBYTECODE=1 <utm>/.venv/Scripts/python.exe \
        knowledge/tools/gen_vectors.py --utm <path to the utm checkout>

`PYTHONDONTWRITEBYTECODE` keeps the old checkout free of new __pycache__
files. The old repository is only imported, never written.

The script is deliberately one file of plain Python: it is a translator
from the old tests into data, and it is thrown away the day the Go
implementations no longer need the vectors regenerated.
"""

from __future__ import annotations

import argparse
import base64
import copy
import dataclasses
import hashlib
import hmac
import json
import os
import subprocess
import sys
from datetime import UTC, datetime, timedelta
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from uuid import UUID

HERE = Path(__file__).resolve().parent
OUT = HERE.parent / "vectors"

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument(
    "--utm",
    default=os.environ.get("UTM_REPO", str(HERE.parents[3] / "utm")),
    help="path to the old rootxkit/utm checkout",
)
ARGS = parser.parse_args()
UTM = Path(ARGS.utm).resolve()
sys.path.insert(0, str(UTM))

# --- the old code ----------------------------------------------------------------

from airspace import ed269  # noqa: E402
from airspace.cpa import (  # noqa: E402
    SeparationPolicy,
    Track,
    advance,
    closest_approach,
    local_offset_m,
)
from airspace.ed269 import Ed269Error, applies, parse, parse_applicability  # noqa: E402
from airspace.geodesy import distance_m as vincenty_m  # noqa: E402
from airspace.monitor import AirspaceMonitor, Severity  # noqa: E402
from airspace.zones import great_circle_m, monitored_zone  # noqa: E402
from common import pgm  # noqa: E402
from common.geoid import GeoidGrid  # noqa: E402
from common.sources import Control, SourceControlState  # noqa: E402
from common.terrain import Elevation, TerrainTile, cell_name  # noqa: E402
from common.uas_identity import (  # noqa: E402
    ClassLabel,
    RegistrationStatus,
    cta2063_problem,
    normalize_serial,
    public_registration_number,
    registration_number_problem,
    serial_problem,
)
from gateway import network_rid, odid  # noqa: E402
from gateway.identification import (  # noqa: E402
    resolve,
    resolve_bound,
    resolve_remote_id,
    serial_conflict,
)
from gateway.registry_projection import (  # noqa: E402
    OperatorFacts,
    RegistrySnapshot,
    UasFacts,
    operator_key,
)
from gateway.remote_id import (  # noqa: E402
    Frame,
    RemoteIdTracker,
    amsl,
    place,
)
from gateway.remote_id_auth import AuthenticationError, ReceiverAuthenticator, sign  # noqa: E402
from gateway.remote_id_match import (  # noqa: E402
    LinkFreshness,
    Registered,
    distance_m as haversine_m,
    judge,
)

import logging  # noqa: E402

# The old code logs every rejection; the generator only wants the values.
logging.disable(logging.CRITICAL)

COMMIT = subprocess.run(
    ["git", "-C", str(UTM), "rev-parse", "HEAD"],
    capture_output=True,
    text=True,
    check=True,
).stdout.strip()

GENERATED = (
    "Expected values computed by knowledge/tools/gen_vectors.py, which runs "
    f"the old Python implementation (rootxkit/utm @ {COMMIT[:12]}) on these "
    "inputs. Regenerate rather than edit."
)


def header(
    description: str,
    source: list[str],
    units: dict[str, str],
    tolerance: dict[str, Any],
    owners: list[str],
    **extra: Any,
) -> dict[str, Any]:
    return {
        "description": description,
        "source": source,
        "units": units,
        "tolerance": tolerance,
        "owners": owners,
        "generated": GENERATED,
        "utm_commit": COMMIT,
        **extra,
    }


def write(name: str, head: dict[str, Any], cases: list[dict[str, Any]]) -> None:
    names = [c["name"] for c in cases]
    duplicates = {n for n in names if names.count(n) > 1}
    assert not duplicates, f"{name}: duplicate case names {duplicates}"
    for case in cases:
        assert set(case) >= {"name", "owner", "input", "expected", "why"}, case
    document = {**head, "cases": cases}
    text = json.dumps(document, indent=1, ensure_ascii=False, allow_nan=False)
    # LF on every platform, so the bytes (and the SHA256SUMS a consuming
    # repository computes) do not depend on where the generator ran.
    (OUT / name).write_text(text + "\n", encoding="utf-8", newline="\n")
    print(f"{name}: {len(cases)} cases")


def case(
    name: str, owner: str | list[str], inp: Any, expected: Any, why: str, **extra: Any
) -> dict[str, Any]:
    return {
        "name": name,
        # Always a list, so a loader has one shape to read.
        "owner": [owner] if isinstance(owner, str) else list(owner),
        "input": inp,
        "expected": expected,
        "why": why,
        **extra,
    }


def iso(moment: datetime) -> str:
    return moment.astimezone(UTC).isoformat()


# --- decisions that supersede the old code ------------------------------------------
#
# A few expected values are deliberately not what utm computes: while
# uspace-core implemented waves 1-3 (PRs #3-#15), the owner and the reviews
# decided otherwise, each time for a lesson, regulation or review finding.
# Such a value is never typed into the JSON. It is set here, through
# decided(), which:
#   - takes the value utm computes (or NOT_IN_UTM when utm has no notion of
#     the input, the policy or the field) and the decided value;
#   - refuses to run when the two are equal: an override that changes
#     nothing is stale and must be deleted;
#   - returns the decision text, which the case carries as "decision", and
#     records it in DECISIONS, printed at the end of the run.
# Everything else in a decided case (inputs, the parts of the expected
# value the decision does not touch) is still computed by utm.

NOT_IN_UTM = object()
DECISIONS: list[tuple[str, str, str]] = []


def decided(file: str, name: str, old: Any, new: Any, decision: str) -> str:
    if old is not NOT_IN_UTM:
        assert old != new, f"{file}#{name}: the decided value equals utm's; delete the override"
    DECISIONS.append((file, name, decision))
    return decision


# =================================================================================
# odid_decode.json
# =================================================================================

ODID_TYPE_NAMES = {
    odid.BasicId: "basic_id",
    odid.Location: "location",
    odid.System: "system",
    odid.OperatorId: "operator_id",
}


def odid_message(message: Any) -> dict[str, Any]:
    return {"type": ODID_TYPE_NAMES[type(message)], **dataclasses.asdict(message)}


def odid_decode(raw: bytes) -> dict[str, Any]:
    try:
        return {"messages": [odid_message(m) for m in odid.decode(raw)]}
    except odid.DecodeError as error:
        return {"error": str(error)}


def gen_odid() -> None:
    vectors = json.loads(
        (UTM / "gateway/tests/data/odid_vectors.json").read_text(encoding="utf-8")
    )
    cases = []
    counters: dict[str, int] = {}
    for vector in vectors:
        kind = vector["type"]
        counters[kind] = counters.get(kind, 0) + 1
        raw = bytes.fromhex(vector["hex"])
        why = (
            "opendroneid-core-c encoded this message from pseudo-random values; "
            "the decode must agree with the library's own decode on every field "
            "(gateway/tests/test_odid.py)."
        )
        if kind == "location" and vector["direction"] == 361:
            why += (
                " This one carries the standard's 'unknown' sentinels (direction "
                "361, speed 255, vspeed 63, altitudes -1000 m, timestamp 0xFFFF): "
                "they must decode to null, never to the sentinel number."
            )
        if kind == "pack":
            why = (
                "A message pack the reference library accepted; the messages it "
                "holds, Self-ID and Auth skipped."
            )
        cases.append(
            case(
                f"reference-{kind}-{counters[kind]:03d}",
                "authority",
                {"hex": vector["hex"]},
                odid_decode(raw),
                why,
            )
        )

    one = {
        kind: bytes.fromhex(next(v["hex"] for v in vectors if v["type"] == kind))
        for kind in ("basic_id", "location", "system", "operator_id", "pack")
    }

    def pack_of(*messages: bytes) -> bytes:
        return bytes([0xF2, odid.MESSAGE_SIZE, len(messages)]) + b"".join(messages)

    pack = one["pack"]
    location = pack[3 + odid.MESSAGE_SIZE : 3 + 2 * odid.MESSAGE_SIZE]
    refusals = [
        ("empty", b"", "empty", "An empty datagram is refused, not decoded as nothing."),
        (
            "pack-message-size-24",
            bytes([0xF2, 24, 1]) + one["basic_id"],
            "message size 24",
            "A pack declaring a message size other than 25 is refused whole.",
        ),
        (
            "pack-of-zero",
            bytes([0xF2, odid.MESSAGE_SIZE, 0]),
            "pack of 0",
            "A pack holds 1 to 9 messages.",
        ),
        (
            "pack-of-ten",
            bytes([0xF2, odid.MESSAGE_SIZE, 10]) + one["basic_id"] * 10,
            "pack of 10",
            "A pack holds 1 to 9 messages.",
        ),
        (
            "pack-inside-a-pack",
            pack_of(one["basic_id"], bytes([0xF2]) + bytes(24)),
            "pack inside a pack",
            "Nesting is refused, as the reference library refuses it.",
        ),
        (
            "three-basic-ids",
            pack_of(one["basic_id"], one["basic_id"], one["basic_id"]),
            "Basic ID",
            "At most two Basic ID messages per pack (ODID_BASIC_ID_MAX_MESSAGES).",
        ),
        (
            "two-locations",
            bytes(pack[:2]) + bytes([4]) + bytes(pack[3:]) + bytes(location),
            "LOCATION",
            "More than one Location in a pack is refused whole: which one is the "
            "aircraft's position would be a guess.",
        ),
        (
            "pack-shorter-than-it-says",
            pack[:-1],
            "shorter",
            "A truncated pack is refused, not decoded from what is there.",
        ),
        (
            "truncated-location",
            one["location"][:24],
            "bytes",
            "A 24-byte message is refused: an offset read past the end would be "
            "a plausible wrong value.",
        ),
        (
            "short-system",
            one["system"][:10],
            "bytes, a message is",
            "A short message after a valid type byte is refused.",
        ),
    ]
    for name, raw, contains, why in refusals:
        expected = odid_decode(raw)
        assert "error" in expected and contains in expected["error"], (name, expected)
        cases.append(
            case(
                f"refuse-{name}",
                "authority",
                {"hex": raw.hex()},
                {**expected, "error_contains": contains},
                why,
            )
        )
    assert "messages" in odid_decode(pack_of(one["basic_id"], one["location"]))
    cases.append(
        case(
            "accept-basic-id-and-location-pack",
            "authority",
            {"hex": pack_of(one["basic_id"], one["location"]).hex()},
            odid_decode(pack_of(one["basic_id"], one["location"])),
            "The presence pair of the pack refusals: the same builder, valid.",
        )
    )
    for name, raw in (
        ("self-id", bytes([0x32]) + bytes(24)),
        ("auth", bytes([0x22]) + bytes(24)),
        ("unknown-type-9", bytes([0x92]) + bytes(24)),
    ):
        cases.append(
            case(
                f"skip-{name}",
                "authority",
                {"hex": raw.hex()},
                odid_decode(raw),
                "Self-ID (free text) and Authentication (needs the maker's keys) "
                "are skipped, not misread as another message; an unknown type "
                "decodes to nothing.",
            )
        )

    base = odid.decode_location(one["location"])
    fields = {name: getattr(base, name) for name in odid.Location.__slots__}
    unknown = odid.encode_location(odid.Location(**{**fields, "lat_deg": None, "lon_deg": None}))
    equator = odid.encode_location(odid.Location(**{**fields, "lat_deg": 0.0, "lon_deg": 10.0}))
    north = odid.encode_location(odid.Location(**{**fields, "direction_deg": 359.6}))
    cases += [
        case(
            "position-zero-zero-is-unknown",
            "authority",
            {"hex": unknown.hex()},
            odid_decode(unknown),
            "Latitude 0 AND longitude 0 is the standard's 'unknown position': "
            "null, never a point in the Gulf of Guinea.",
        ),
        case(
            "latitude-zero-alone-is-a-position",
            "authority",
            {"hex": equator.hex()},
            odid_decode(equator),
            "Only the pair 0,0 is unknown; latitude 0 with longitude 10 is a "
            "real point on the equator.",
        ),
        case(
            "direction-rounding-to-360-is-north",
            ["authority", "lab"],
            {"hex": north.hex(), "encoded_from": {"direction_deg": 359.6}},
            odid_decode(north),
            "An encoder (simulator, lab) must round 359.6 to 360 and wrap it to "
            "0, not write 360 (which encodes as 180 + E/W flag garbage).",
        ),
    ]
    write(
        "odid_decode.json",
        header(
            "Open Drone ID (ASTM F3411 / ASD-STAN EN 4709-002) 25-byte messages "
            "and message packs, decoded. input.hex is the frame as broadcast; "
            "expected.messages is the decode, or expected.error for a refusal "
            "(match on error_contains). Field names are the old decoder's; "
            "values the standard reserves for 'unknown' are null.",
            [
                "utm gateway/tests/data/odid_vectors.json (opendroneid-core-c "
                "tools/odid_vectors/gen_vectors.c)",
                "utm gateway/tests/test_odid.py",
                "utm gateway/odid.py",
            ],
            {
                "lat_deg/lon_deg": "degrees, WGS84 (wire: int32 * 1e-7)",
                "alt_*_m/height_m/area_*_m": "metres (wire: uint16 * 0.5 - 1000)",
                "speed_horizontal_ms": "m/s (wire: 0.25 steps, or 0.75 above 63.75)",
                "speed_vertical_ms": "m/s, up positive (wire: int8 * 0.5)",
                "direction_deg": "degrees true over the ground (wire: 0-179 + E/W bit)",
                "seconds_after_hour": "seconds after the full UTC hour (wire: uint16 tenths)",
                "timestamp_s": "seconds since 2019-01-01T00:00:00Z",
                "accuracy fields": "the standard's enum codes, not metres",
            },
            {
                "lat_deg/lon_deg": 1e-9,
                "other floats": 1e-6,
                "integers and strings": "exact",
            },
            ["authority"],
            byte_order="little-endian multi-byte fields; bit fields LSB first",
        ),
        cases,
    )


# =================================================================================
# rid_time.json
# =================================================================================


def location_with(**fields: Any) -> odid.Location:
    base: dict[str, Any] = dict(
        status=odid.Status.AIRBORNE,
        direction_deg=90.0,
        speed_horizontal_ms=10.0,
        speed_vertical_ms=1.0,
        lat_deg=41.7151,
        lon_deg=44.8271,
        alt_baro_m=None,
        alt_hae_m=520.0,
        height_reference=odid.HeightReference.OVER_TAKEOFF,
        height_m=30.0,
        horiz_accuracy=10,
        vert_accuracy=4,
        baro_accuracy=0,
        speed_accuracy=3,
        ts_accuracy=0,
        seconds_after_hour=0.0,
    )
    base.update(fields)
    return odid.Location(**base)


def gen_rid_time() -> None:
    received = datetime(2026, 10, 1, 12, 34, 56, 700_000, tzinfo=UTC)
    in_hour = 34 * 60 + 56.7
    cases = []

    def placed(
        name: str,
        raw: int,
        rx: datetime,
        why: str,
        *,
        ts_accuracy: int = 0,
        tolerance_s: float = 1.0,
        max_latency_s: float = 5.0,
    ) -> None:
        seconds = None if raw == 0xFFFF else raw / 10
        result = place(
            location_with(seconds_after_hour=seconds, ts_accuracy=ts_accuracy),
            rx,
            tolerance_s=tolerance_s,
            max_latency_s=max_latency_s,
        )
        cases.append(
            case(
                name,
                "authority",
                {
                    "timestamp_tenths": raw,
                    "ts_accuracy_code": ts_accuracy,
                    "received_at": iso(rx),
                    "time_tolerance_s": tolerance_s,
                    "max_latency_s": max_latency_s,
                },
                {
                    "ts": iso(result.ts),
                    "captured_at": iso(result.captured_at),
                    "time_source": result.time_source,
                    "fallback": result.fallback,
                },
                why,
            )
        )

    def tenths(seconds: float) -> int:
        return odid._round((seconds % 3600.0) * 10)

    placed(
        "hour-taken-from-receive-time",
        tenths(in_hour - 0.4),
        received,
        "The broadcast carries only tenths after the hour; the hour is the "
        "receiver's. 0.4 s of latency places it 0.4 s before receipt.",
    )
    placed(
        "rollover-broadcast-before-the-hour-heard-after",
        35999,
        datetime(2026, 10, 1, 13, 0, 0, 200_000, tzinfo=UTC),
        "xx:59:59.9 heard at (xx+1):00:00.2 is in hour xx. Taking the receiver's "
        "hour naively would place the aircraft 59 minutes in the future.",
    )
    placed(
        "rollover-broadcast-after-the-hour-heard-before",
        5,
        datetime(2026, 10, 1, 12, 59, 59, 900_000, tzinfo=UTC),
        "The aircraft's clock is 0.6 s ahead of ours across the hour: within "
        "the 1 s tolerance, so it is the next hour, not 59 min 59 s ago.",
    )
    placed(
        "late-broadcast-placed-at-its-own-time",
        tenths(in_hour - 2.0),
        received,
        "S-27: 2 s of receiver latency. Placing at receipt would put a 15 m/s "
        "aircraft 30 m from where it was, against a 60 m minimum.",
    )
    placed(
        "within-latency-bound",
        tenths(in_hour - 4.9),
        received,
        "4.9 s old with a 5 s bound: still the broadcast's own time.",
    )
    placed(
        "beyond-latency-bound-too-old",
        tenths(in_hour - 5.2),
        received,
        "5.2 s old: more than a receiver can plausibly hold a broadcast; placed "
        "on arrival, `ts` still what the broadcast said, counted as too_old.",
    )
    placed(
        "latency-bound-exactly",
        tenths(in_hour - 5.0),
        received,
        "Exactly at the bound is inside (the comparison is strictly greater).",
    )
    placed(
        "declared-accuracy-widens-the-latency-bound",
        tenths(in_hour - 5.5),
        received,
        "Accuracy code 10 = 1.0 s; 5.5 s late is within 5 s + 1 s.",
        ts_accuracy=10,
    )
    placed(
        "small-declared-accuracy-does-not-widen-enough",
        tenths(in_hour - 5.5),
        received,
        "Accuracy code 1 = 0.1 s; 5.5 s late is beyond 5.1 s: too_old. The "
        "presence pair of the case above.",
        ts_accuracy=1,
    )
    placed(
        "clock-ahead-within-tolerance",
        tenths(in_hour + 0.8),
        received,
        "0.8 s ahead of the receiver with a 1 s tolerance: believed (GPS time "
        "and NTP differ; the field truncates to tenths).",
    )
    placed(
        "clock-ahead-exactly-at-tolerance",
        tenths(in_hour + 1.0),
        received,
        "Exactly 1.0 s ahead is the latest instant allowed: believed.",
    )
    placed(
        "clock-ahead-beyond-tolerance",
        tenths(in_hour + 3.0),
        received,
        "3 s ahead: the hour choice puts it 59:57 in the past. Anything more "
        "than half an hour old is read as 'clock ahead', not as old: placed on "
        "arrival, and `ts` is the time the broadcast claims (an hour later).",
    )
    for label, ahead_s in (
        ("ahead-2s", 2.0),
        ("ahead-29min", 29 * 60.0),
        ("behind-10s", -10.0),
        ("behind-29min", -29 * 60.0),
    ):
        placed(
            f"ahead-and-old-told-apart-{label}",
            tenths(in_hour + ahead_s),
            received,
            "Up to half an hour either side of the receiver is told apart: "
            "ahead is clock_ahead, behind beyond the latency bound is too_old. "
            "A steady clock_ahead count usually means the receiving host's own "
            "clock is behind.",
        )
    placed(
        "unknown-timestamp-0xffff",
        0xFFFF,
        received,
        "0xFFFF is the standard's 'unknown': placed on arrival, counted as unknown.",
    )
    placed(
        "timestamp-past-the-hour-is-invalid",
        36000,
        received,
        "36000 tenths (3600.0 s) fits the field but is not inside any hour.",
    )

    # Network Remote ID (ASTM F3411 Display Provider): the response is the batch.
    rx = datetime(2026, 10, 1, 12, 0, 10, tzinfo=UTC)

    def network(
        name: str,
        state_ts: datetime,
        response_at: datetime | None,
        why: str,
    ) -> None:
        result = network_rid.place(
            SimpleNamespace(timestamp=state_ts),  # type: ignore[arg-type]
            response_at=response_at,
            received_at=rx,
        )
        cases.append(
            case(
                f"network-{name}",
                "ussp",
                {
                    "state_timestamp": iso(state_ts),
                    "response_timestamp": None if response_at is None else iso(response_at),
                    "received_at": iso(rx),
                    "max_age_s": network_rid.DEFAULT_MAX_AGE_S,
                    "time_tolerance_s": 1.0,
                    "max_latency_s": 5.0,
                },
                None
                if result is None
                else {
                    "ts": iso(result.ts),
                    "captured_at": iso(result.captured_at),
                    "time_source": result.time_source,
                    "note": result.note,
                },
                why,
            )
        )

    provider_clock = datetime(2026, 10, 1, 12, 5, 0, tzinfo=UTC)  # 5 min skewed
    network(
        "behind-response-placed-on-our-clock",
        provider_clock - timedelta(seconds=2),
        provider_clock,
        "The provider's clock is 5 minutes off ours. The state is 2 s behind "
        "the response's own timestamp, so it is placed 2 s before our receive "
        "time: the provider's skew cancels inside the response.",
    )
    network(
        "ahead-of-its-response",
        provider_clock + timedelta(seconds=1),
        provider_clock,
        "A state newer than the response that carries it is impossible on one "
        "clock: placed at receipt and noted.",
    )
    network(
        "older-than-max-age-dropped",
        provider_clock - timedelta(seconds=61),
        provider_clock,
        "More than 60 s behind its response: not shown as current at all (null).",
    )
    network(
        "no-response-time-recent",
        rx - timedelta(seconds=3),
        None,
        "Without a response timestamp the state's own time is compared with "
        "ours, with the same tolerance and latency bounds as a broadcast.",
    )
    network(
        "no-response-time-clock-ahead",
        rx + timedelta(seconds=2),
        None,
        "2 s ahead of our clock without a response time: placed at receipt.",
    )
    network(
        "no-response-time-too-old",
        rx - timedelta(seconds=7),
        None,
        "7 s old: shown, but placed at receipt and noted too_old.",
    )
    network(
        "no-response-time-older-than-max-age",
        rx - timedelta(seconds=61),
        None,
        "61 s old: dropped.",
    )
    write(
        "rid_time.json",
        header(
            "Placing a Remote ID position in time (S-27). Direct Remote ID "
            "carries only tenths of a second after the UTC hour; the hour is "
            "reconstructed from the receive time. `captured_at` is where the "
            "aircraft is placed; `ts` is what the broadcast claims. Network "
            "Remote ID cases place an SP flight state using the response "
            "timestamp so the provider's clock skew cancels; expected null "
            "means the state is not shown.",
            [
                "utm gateway/tests/test_remote_id_time.py",
                "utm gateway/remote_id.py place(), broadcast_time()",
                "utm gateway/network_rid.py place()",
            ],
            {
                "timestamp_tenths": "uint16 as on the wire; 65535 = unknown",
                "ts_accuracy_code": "MAV_ODID_TIME_ACC: 0 unknown, k = k/10 s",
                "times": "ISO 8601, UTC",
                "*_s": "seconds",
            },
            {"times": "exact to the microsecond"},
            ["authority", "ussp"],
            rule=(
                "moment = latest instant at seconds_after_hour into some hour that "
                "is not after received_at + tolerance + accuracy; age = received_at "
                "- moment; age > 1800 s -> clock_ahead (ts = moment + 1 h); age > "
                "max_latency + accuracy -> too_old; else broadcast."
            ),
        ),
        cases,
    )


# =================================================================================
# rid_identity.json
# =================================================================================

ADDRESS = "AA:BB:CC:00:00:01"
NOW = datetime(2026, 9, 29, 12, 0, tzinfo=UTC)
LAT, LON = 41.7151, 44.8271


class FlatGeoid:
    def __init__(self, n_m: float = 20.0) -> None:
        self.n_m = n_m

    def undulation_m(self, lat_deg: float, lon_deg: float) -> float:
        return self.n_m


def encode_message(spec: dict[str, Any]) -> bytes:
    kind = spec["type"]
    if kind == "basic_id":
        return odid.encode_basic_id(
            odid.BasicId(
                id_type=spec.get("id_type", odid.IdType.SERIAL_NUMBER),
                ua_type=2,
                ua_id=spec["ua_id"],
            )
        )
    if kind == "operator_id":
        return odid.encode_operator_id(
            odid.OperatorId(operator_id_type=0, operator_id=spec["operator_id"])
        )
    assert kind == "location"
    fields = {k: v for k, v in spec.items() if k != "type"}
    return odid.encode_location(location_with(**fields))


def payload_of(messages: list[dict[str, Any]]) -> bytes:
    encoded = [encode_message(m) for m in messages]
    return encoded[0] if len(encoded) == 1 else odid.encode_pack(encoded)


def summary(seen: dict[str, Any] | None) -> dict[str, Any] | None:
    if seen is None:
        return None
    rid = seen["remote_id"]
    return {
        "drone_id": seen["drone_id"],
        "label": seen["label"],
        "identified": rid["identified"],
        "ua_id": rid["ua_id"],
        "id_type": int(rid["id_type"]),
        "operator_id": rid["operator_id"],
        "lat_deg": seen["lat_deg"],
        "rx_ts": seen["rx_ts"],
    }


def run_tracker(settings: dict[str, float], steps: list[dict[str, Any]]) -> dict[str, Any]:
    tracker = RemoteIdTracker(geoid=FlatGeoid(), **settings)  # type: ignore[arg-type]
    outputs = []
    for step in steps:
        frame = Frame(
            receiver_id=step.get("receiver", "rx-1"),
            transmitter=step.get("transmitter", ADDRESS),
            received_at=NOW + timedelta(seconds=step.get("received_offset_s", step["now_s"])),
            payload=payload_of(step["messages"]),
            rssi_dbm=-71.0,
        )
        outputs.append(summary(tracker.take(frame, now_s=step["now_s"])))
    return {
        "per_step": outputs,
        "counters": {
            "identity_changes": tracker.identity_changes,
            "silences": tracker.silences,
            "unidentified": tracker.unidentified,
            "address_conflicts": tracker.address_conflicts,
        },
    }


def basic(ua_id: str, id_type: int = 1) -> dict[str, Any]:
    return {"type": "basic_id", "id_type": id_type, "ua_id": ua_id}


def loc(**fields: Any) -> dict[str, Any]:
    return {"type": "location", **fields}


def oper(operator_id: str) -> dict[str, Any]:
    return {"type": "operator_id", "operator_id": operator_id}


def gen_rid_identity() -> None:
    old, new = "SN-OLD-0001", "SN-NEW-0002"
    cases = []

    def seq(name: str, settings: dict[str, float], steps: list[dict[str, Any]], why: str, source: str) -> None:
        cases.append(
            case(
                name,
                "authority",
                {"settings": settings, "steps": steps},
                run_tracker(settings, steps),
                why,
                source=source,
            )
        )

    T = "test_remote_id_identity.py::"
    R = "test_remote_id.py::"
    seq(
        "different-basic-id-expires-the-identity",
        {},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 1.0, "messages": [basic(new)]},
            {"now_s": 2.0, "messages": [loc(lat_deg=LAT + 0.001)]},
        ],
        "S-32: a new serial on an address drops everything the address said "
        "before. The Basic ID alone publishes nothing: the old aircraft's "
        "Location is not the new one's.",
        T + "test_a_different_basic_id_expires_the_identity",
    )
    seq(
        "a-change-drops-the-previous-operator",
        {},
        [
            {"now_s": 0.0, "messages": [basic(old), loc(), oper("GEO-OP-OLD")]},
            {"now_s": 1.0, "messages": [basic(new), loc()]},
        ],
        "System and Operator ID go with the identity they came with.",
        T + "test_a_change_drops_the_previous_operator",
    )
    seq(
        "same-basic-id-again-is-not-a-change",
        {},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 1.0, "messages": [basic(old)]},
            {"now_s": 2.0, "messages": [loc()]},
        ],
        "Repeating the same Basic ID refreshes it; it does not count as a change.",
        T + "test_the_same_basic_id_again_is_not_a_change",
    )
    seq(
        "second-id-type-is-another-identity",
        {},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 1.0, "messages": [basic("GEO-REG-1", 2)]},
            {"now_s": 2.0, "messages": [loc()]},
        ],
        "A module may send a serial and a CAA registration; both are kept, "
        "the serial preferred.",
        T + "test_a_second_id_type_is_another_identity_not_a_change",
    )
    seq(
        "silence-longer-than-max-gap-forgets-the-serial",
        {"max_gap_s": 3.0, "identify_within_s": 4.0},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 5.0, "messages": [loc()]},
            {"now_s": 6.0, "messages": [loc()]},
            {"now_s": 7.0, "messages": [loc()]},
            {"now_s": 8.0, "messages": [loc()]},
            {"now_s": 9.0, "messages": [loc()]},
        ],
        "5 s of silence is a reboot or another aircraft. Locations are held "
        "for identify_within_s, then published unidentified; never under the "
        "old serial (the U-16 drop-rate run stored 2 rows under the wrong one).",
        T + "test_after_a_silence_a_location_is_not_attached_to_the_old_serial",
    )
    seq(
        "silence-just-under-max-gap-keeps-the-serial",
        {"max_gap_s": 3.0},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 2.9, "messages": [loc()]},
        ],
        "The presence pair: 2.9 s of silence is within the gap.",
        T + "test_within_the_gap_the_identity_is_kept",
    )
    seq(
        "basic-id-after-silence-identifies-the-held-location",
        {"max_gap_s": 3.0},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 5.0, "messages": [loc()]},
            {"now_s": 6.0, "messages": [basic(new)]},
        ],
        "The held Location is published when its Basic ID arrives, under the "
        "new serial, and placed by the frame that carried the Location (rx_ts "
        "of step 2), not the Basic ID's.",
        T + "test_a_basic_id_after_the_silence_identifies_the_held_location",
    )
    seq(
        "identity-older-than-ttl-is-not-used",
        {"identity_ttl_s": 15.0, "identify_within_s": 4.0, "max_gap_s": 3.0},
        [{"now_s": 0.0, "messages": [basic(old), loc()]}]
        + [{"now_s": float(s), "messages": [loc()]} for s in range(1, 21)],
        "Identity TTL 15 s (five 3 s static periods). From 16 s the identity "
        "is stale; Locations are held 4 s, then published unidentified from 19 s.",
        T + "test_an_identity_older_than_its_ttl_is_not_used",
    )
    seq(
        "identity-ttl-is-configurable",
        {"identity_ttl_s": 60.0, "max_gap_s": 3.0},
        [{"now_s": 0.0, "messages": [basic(old), loc()]}]
        + [{"now_s": float(s), "messages": [loc()]} for s in range(1, 41)],
        "With a 60 s TTL the serial holds for 40 s of Locations.",
        T + "test_the_identity_ttl_is_configurable",
    )
    seq(
        "never-identified-is-published-unidentified",
        {"identify_within_s": 4.0},
        [{"now_s": float(s), "messages": [loc()]} for s in range(0, 4)]
        + [{"now_s": 4.0, "messages": [loc(lat_deg=LAT + 0.001)]}],
        "A transmitter that never sends a Basic ID is shown after 4 s as an "
        "unidentified track: id derived from the address (uuid5 of "
        "'transmitter:<address>'), labelled with the address, empty UAS ID, "
        "ID type 0.",
        T + "test_a_transmitter_never_identified_is_published_as_unidentified",
    )
    seq(
        "unidentified-heard-by-two-receivers-is-one-id",
        {"identify_within_s": 0.0},
        [
            {"now_s": 0.0, "messages": [loc()], "receiver": "rx-1"},
            {"now_s": 0.0, "messages": [loc()], "receiver": "rx-2"},
        ],
        "The unidentified id is per transmitter, not per receiver.",
        T + "test_one_unidentified_transmitter_heard_by_two_receivers_is_one_id",
    )
    steps: list[dict[str, Any]] = []
    for s in range(10):
        steps.append({"now_s": float(s), "messages": [basic(old), loc()], "receiver": "rx-a"})
        steps.append({"now_s": s + 0.5, "messages": [loc()], "receiver": "rx-b"})
    seq(
        "receiver-hearing-only-locations-borrows-a-fresh-identity",
        {"identify_within_s": 4.0},
        steps,
        "Receiver A hears the Basic ID, B only Locations: B's Locations take "
        "A's fresh identity. One identified track, not one beside an "
        "unidentified one.",
        T + "test_a_receiver_hearing_only_locations_takes_anothers_identity",
    )
    seq(
        "receiver-alone-without-identity-is-unidentified",
        {"identify_within_s": 4.0},
        [{"now_s": s + 0.5, "messages": [loc()], "receiver": "rx-b"} for s in range(10)],
        "The presence pair: B alone, same frames, unidentified after 4 s.",
        T + "test_without_the_other_receiver_it_is_unidentified",
    )
    steps = [{"now_s": 0.0, "messages": [basic(old), loc()], "receiver": "rx-a"}]
    for s in range(1, 21):
        steps.append({"now_s": float(s), "messages": [loc()], "receiver": "rx-a"})
        steps.append({"now_s": s + 0.5, "messages": [loc()], "receiver": "rx-b"})
    seq(
        "borrowed-identity-must-be-fresh-too",
        {"identity_ttl_s": 15.0, "identify_within_s": 4.0},
        steps,
        "Another receiver's identity follows the same TTL: borrowing does not "
        "extend it.",
        T + "test_another_receivers_identity_must_be_fresh_too",
    )
    seq(
        "two-serials-alternating-on-one-address-are-an-anomaly",
        {},
        [
            {"now_s": float(s), "messages": [basic(old if s % 2 == 0 else new), loc()]}
            for s in range(6)
        ],
        "One transmitter, two fresh identities of one ID type: two radios on "
        "one address or a spoofer. Counted every time (5), logged once per "
        "interval so a spoofer cannot fill the log.",
        T + "test_two_serials_alternating_on_one_address_are_an_anomaly",
    )
    seq(
        "two-receivers-two-serials-one-address-is-an-anomaly",
        {},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()], "receiver": "rx-a"},
            {"now_s": 1.0, "messages": [basic(new), loc()], "receiver": "rx-b"},
        ],
        "The anomaly is per address across receivers.",
        T + "test_two_receivers_hearing_two_serials_on_one_address_are_an_anomaly",
    )
    seq(
        "serial-after-silence-is-not-an-anomaly",
        {"max_gap_s": 3.0},
        [
            {"now_s": 0.0, "messages": [basic(old), loc()]},
            {"now_s": 5.0, "messages": [basic(new), loc()]},
        ],
        "The absence pair: a restart falls silent first, so it is not two "
        "identities at once.",
        T + "test_a_serial_after_a_silence_is_not_an_anomaly",
    )
    seq(
        "location-waits-for-identity-then-is-published",
        {},
        [
            {"now_s": 0.0, "messages": [loc()]},
            {"now_s": 0.5, "messages": [basic("SN-RID-0001")]},
        ],
        "Bluetooth 4 sends Basic ID and Location separately; the address "
        "joins them. The held Location is published when the identity comes.",
        R + "test_a_location_waits_for_an_identity_then_is_published",
    )
    seq(
        "identity-from-another-transmitter-does-not-complete-it",
        {},
        [
            {"now_s": 0.0, "messages": [loc()], "transmitter": "AA:00:00:00:00:01"},
            {"now_s": 0.5, "messages": [basic("SN-RID-0001")], "transmitter": "AA:00:00:00:00:02"},
        ],
        "Joining is by transmitter address only.",
        R + "test_an_identity_from_another_transmitter_does_not_complete_it",
    )
    seq(
        "repeated-identity-alone-does-not-republish",
        {},
        [
            {"now_s": 0.0, "messages": [basic("SN-RID-0001"), loc()]},
            {"now_s": 0.5, "messages": [basic("SN-RID-0001")]},
            {"now_s": 0.6, "messages": [oper("GEO-OP-1")]},
            {"now_s": 1.0, "messages": [loc(lat_deg=LAT + 0.001)]},
        ],
        "Each Location is published once. A static message (Basic ID, Operator "
        "ID) does not republish a stale position as if new.",
        R + "test_a_repeated_identity_alone_does_not_republish_a_position",
    )
    seq(
        "forgotten-transmitter-does-not-lend-its-position",
        {"max_gap_s": 3.0},
        [
            {"now_s": 0.0, "messages": [loc()]},
            {"now_s": 3.5, "messages": [basic("SN-RID-0001")]},
        ],
        "A Location from before a silence is not completed by a Basic ID after it.",
        R + "test_a_forgotten_transmitter_does_not_lend_its_position",
    )
    seq(
        "forgotten-transmitter-pair-within-gap",
        {"max_gap_s": 3.0},
        [
            {"now_s": 0.0, "messages": [loc()]},
            {"now_s": 2.9, "messages": [basic("SN-RID-0001")]},
        ],
        "The presence pair: 2.9 s later the held Location is completed.",
        R + "test_a_forgotten_transmitter_does_not_lend_its_position",
    )
    seq(
        "serial-wins-over-registration-id",
        {},
        [
            {"now_s": 0.0, "messages": [basic("GEO-OP-77", 2)]},
            {"now_s": 0.5, "messages": [basic("SN-9"), loc()]},
            {"now_s": 1.0, "messages": [basic("GEO-OP-77", 2), loc()]},
        ],
        "The serial is fixed to the airframe; a registration can move between "
        "airframes. With both fresh, the track is the serial.",
        R + "test_the_serial_wins_over_a_registration_id",
    )
    seq(
        "empty-identity-does-not-replace-a-serial",
        {},
        [
            {"now_s": 0.0, "messages": [basic("SN-1")]},
            {"now_s": 0.5, "messages": [basic("", 0), loc()]},
        ],
        "ID type 0 with an empty UAS ID is 'no identity', not a new one.",
        R + "test_an_empty_identity_does_not_replace_a_serial",
    )
    write(
        "rid_identity.json",
        header(
            "Joining Remote ID Basic ID and Location by transmitter address, and "
            "when an identity is fresh enough to use (S-32). Each case is a "
            "sequence of frames fed to one tracker; expected.per_step[i] is the "
            "observation published by step i (null: nothing published), and "
            "expected.counters the tracker's counts after the last step. Default "
            "settings: identity_ttl_s 15, max_gap_s 3, identify_within_s 4. "
            "Location fields not given are the defaults of the generator's "
            "location_with(); only identity matters here. drone_id is uuid5 of "
            "namespace 6f1c7d52-4a0b-5c1e-9d3a-2b8e41f07a65 with '<id_type>:<ua_id>' "
            "(identified) or 'transmitter:<address>' (unidentified); a Go "
            "implementation may choose its own ids but must keep the rule that "
            "the same serial is always the same id, across receivers and restarts.",
            [
                "utm gateway/tests/test_remote_id_identity.py",
                "utm gateway/tests/test_remote_id.py",
                "utm gateway/remote_id.py RemoteIdTracker",
            ],
            {"now_s": "seconds on the tracker's monotonic clock", "lat_deg": "degrees"},
            {"lat_deg": 1e-9, "everything else": "exact"},
            ["authority"],
            defaults={"receiver": "rx-1", "transmitter": ADDRESS},
        ),
        cases,
    )


# =================================================================================
# pressure_altitude.json
# =================================================================================


def gen_pressure() -> None:
    hae, baro = 520.0, 507.5
    cases = []

    def single(
        name: str,
        why: str,
        *,
        alt_hae_m: float | None = hae,
        alt_baro_m: float | None = baro,
        vert_accuracy: int = 4,
        undulation_m: float | None = 20.0,
        min_vertical_accuracy: int = 2,
        hold_pressure: bool = False,
    ) -> None:
        location = location_with(
            alt_hae_m=alt_hae_m, alt_baro_m=alt_baro_m, vert_accuracy=vert_accuracy
        )
        alt, source = amsl(
            location,
            None if undulation_m is None else FlatGeoid(undulation_m),
            min_vertical_accuracy=min_vertical_accuracy,
            hold_pressure=hold_pressure,
        )
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {
                    "alt_hae_m": alt_hae_m,
                    "alt_pressure_m": alt_baro_m,
                    "vert_accuracy_code": vert_accuracy,
                    "geoid_undulation_m": undulation_m,
                    "min_vertical_accuracy": min_vertical_accuracy,
                    "hold_pressure": hold_pressure,
                },
                {"alt_amsl_m": alt, "alt_source": source},
                why,
            )
        )

    single("good-geodetic-is-used", "HAE 520 m minus N 20 m is 500 m AMSL.")
    single(
        "missing-geodetic-falls-back-to-pressure",
        "The broadcast's unknown HAE (-1000 m on the wire) is null; pressure "
        "altitude replaces it, marked alt_source pressure.",
        alt_hae_m=None,
    )
    for code, why in (
        (1, "Code 1 (<150 m) is below the threshold 2 (<45 m): flagged poor, pressure used."),
        (2, "Code 2 (<45 m) meets the threshold: geodetic used."),
        (6, "Code 6 (<1 m): geodetic used."),
        (0, "Code 0 is 'unknown', which is not a flag: the geodetic altitude stays."),
    ):
        single(f"vertical-accuracy-code-{code}", why, vert_accuracy=code)
    single(
        "threshold-is-configurable",
        "With a minimum of 5 (<3 m), code 4 (<10 m) is poor.",
        min_vertical_accuracy=5,
    )
    single("neither-altitude", "No AMSL altitude at all: null, not 0.", alt_hae_m=None, alt_baro_m=None)
    single(
        "flagged-geodetic-without-pressure-is-not-used",
        "A poor geodetic altitude with no pressure altitude gives no AMSL "
        "altitude; it is not used 'because it is all there is'.",
        alt_baro_m=None,
        vert_accuracy=1,
    )
    single(
        "pressure-is-not-a-substitute-for-a-missing-geoid",
        "Without a geoid a good HAE gives no AMSL (16-23 m off over Georgia); "
        "pressure replaces a poor geodetic altitude, never a missing geoid.",
        undulation_m=None,
    )
    single(
        "hold-keeps-pressure-even-when-geodetic-is-good",
        "During the hold the tracker passes hold_pressure and pressure is used.",
        hold_pressure=True,
    )
    single(
        "hold-needs-a-pressure-altitude",
        "Holding with no pressure altitude falls through to geodetic.",
        hold_pressure=True,
        alt_baro_m=None,
    )

    def hold(name: str, hold_s: float, steps: list[tuple[float, int, float | None]], why: str) -> None:
        tracker = RemoteIdTracker(geoid=FlatGeoid(20.0), pressure_hold_s=hold_s)
        out = []
        for now_s, accuracy, alt_baro in steps:
            seen = tracker.take(
                Frame(
                    "rx-1",
                    ADDRESS,
                    NOW,
                    payload_of(
                        [
                            basic("SN-RID-0001"),
                            loc(alt_hae_m=hae, alt_baro_m=alt_baro, vert_accuracy=accuracy),
                        ]
                    ),
                    None,
                ),
                now_s=now_s,
            )
            assert seen is not None
            out.append({"alt_source": seen["alt_source"], "alt_amsl_m": seen["alt_amsl_m"]})
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {
                    "pressure_hold_s": hold_s,
                    "alt_hae_m": hae,
                    "geoid_undulation_m": 20.0,
                    "steps": [
                        {"now_s": s, "vert_accuracy_code": a, "alt_pressure_m": b}
                        for s, a, b in steps
                    ],
                },
                {"per_step": out},
                why,
            )
        )

    flapping = [(float(t), 1 if t % 2 == 0 else 2, baro) for t in range(6)]
    hold(
        "accuracy-at-threshold-does-not-flip-the-source",
        10.0,
        flapping,
        "S-33 hysteresis: accuracy alternating <150 m / <45 m stays on pressure "
        "for 10 s after the last poor fix, so the monitor's alerts do not flip "
        "every message.",
    )
    hold(
        "without-hold-it-flips-every-message",
        0.0,
        flapping,
        "The presence pair: no hold, the source flips each message.",
    )
    hold(
        "hold-ends-after-last-poor-geodetic",
        10.0,
        [(0.0, 1, baro)] + [(float(t), 4, baro) for t in range(1, 10)] + [(9.9, 4, baro), (10.1, 4, baro)],
        "Last poor fix at 0 s; pressure until 10 s, geodetic from 10.1 s.",
    )
    hold(
        "hold-needs-a-pressure-altitude-to-hold",
        10.0,
        [(0.0, 1, baro), (1.0, 4, None)],
        "Inside the hold, but no pressure altitude in this message: geodetic.",
    )
    write(
        "pressure_altitude.json",
        header(
            "Choosing the AMSL altitude of a Remote ID observation (S-33): the "
            "geodetic altitude through the geoid, or the pressure altitude when "
            "the geodetic one is missing or flagged poor. alt_source says which. "
            "A pressure altitude is referenced to 1013.25 hPa, not QNH (about 8 m "
            "per hPa, ~160 m on a 20 hPa day): downstream it is a vertical "
            "position of unknown accuracy (see cpa.json and zones_vertical.json).",
            [
                "utm gateway/tests/test_remote_id_altitude.py",
                "utm gateway/remote_id.py amsl(), geodetic_usable(), RemoteIdTracker",
            ],
            {
                "alt_*_m": "metres",
                "vert_accuracy_code": "MAV_ODID_VER_ACC: 0 unknown, 1 <150 m, 2 <45 m, 3 <25 m, 4 <10 m, 5 <3 m, 6 <1 m",
                "now_s": "seconds, tracker clock",
            },
            {"alt_amsl_m": 1e-9},
            ["authority", "ussp"],
        ),
        cases,
    )


# =================================================================================
# identification_status.json and serials_and_registration.json
# =================================================================================

R = RegistrationStatus
OPS = {
    "ACTIVE_OP": (UUID(int=1), "GEOabcd1234efgh", R.ACTIVE),
    "SUSPENDED_OP": (UUID(int=2), "GEOSUSP00000001", R.SUSPENDED),
    "REVOKED_OP": (UUID(int=3), "GEOREVK00000001", R.REVOKED),
}
GHOST_OP = UUID(int=4)
UAS = [
    (UUID(int=10), "uas-a", "1581F5FKD229400A", R.ACTIVE, "ACTIVE_OP", True),
    (UUID(int=11), "uas-b", "1581F5FKD229400B", R.SUSPENDED, "ACTIVE_OP", True),
    (UUID(int=12), "uas-c", "1581F5FKD229400C", R.REVOKED, "ACTIVE_OP", True),
    (UUID(int=13), "uas-d", "SN-D", R.ACTIVE, "SUSPENDED_OP", True),
    (UUID(int=14), "uas-e", "SN-E", R.ACTIVE, "REVOKED_OP", True),
    (UUID(int=15), "hexa-01", "SN-FLEET", R.ACTIVE, None, True),
    (UUID(int=16), "uas-g", "SN-G", R.ACTIVE, "GHOST_OP", True),
    (UUID(int=17), "orphan", "SN-ORPHAN", R.ACTIVE, None, False),
]


def owner_uuid(key: str | None) -> UUID | None:
    if key is None:
        return None
    if key == "GHOST_OP":
        return GHOST_OP
    return OPS[key][0]


def snapshot() -> RegistrySnapshot:
    return RegistrySnapshot(
        uas=tuple(
            UasFacts(d, label, serial, status, owner_uuid(owner), in_registry=inreg)
            for d, label, serial, status, owner, inreg in UAS
        ),
        operators=tuple(OperatorFacts(u, number, status) for u, number, status in OPS.values()),
    )


REGISTRY_FIXTURE = {
    "operators": [
        {"operator_id": str(u), "registration_number": number, "status": status.value}
        for u, number, status in OPS.values()
    ],
    "uas": [
        {
            "drone_id": str(d),
            "label": label,
            "serial": serial,
            "registration_status": status.value,
            "uas_operator_id": None if owner_uuid(owner) is None else str(owner_uuid(owner)),
            "in_registry": inreg,
        }
        for d, label, serial, status, owner, inreg in UAS
    ],
    "notes": [
        f"uas_operator_id {GHOST_OP} owns uas-g but is not in the operators list "
        "(a projection written out of order, or lost).",
        "orphan is in the telemetry projection but has no relational aircraft "
        "(in_registry false: 'unregistered' in the projection).",
        "hexa-01 is our own fleet: registered, no UAS operator.",
    ],
}


def ident(found: Any) -> dict[str, Any]:
    return {
        **found.as_dict(),
        "drone_id": None if found.drone_id is None else str(found.drone_id),
    }


IDENT_FILE = "identification_status.json"

# Spec 04 section 3.2 renamed two of utm's reason codes (uspace-core PR #9).
REASON_RENAMES = {
    "fleet": (
        "matched",
        "Spec 04 section 3.2 has no 'fleet' reason: our own fleet aircraft is "
        "registered on its serial alone, reason matched (uspace-core PR #9).",
    ),
    "relay_binding": (
        "session_binding",
        "Spec 04 section 3.2 calls an authenticated binding 'session_binding'; "
        "utm's relay binding is one (uspace-core PR #9, PLAN section 11 gap 5).",
    ),
}


def ident_case(name: str, owner: Any, inp: dict[str, Any], found: Any, why: str) -> dict[str, Any]:
    """A case whose expected value is utm's, with spec 04's reason codes."""
    expected = ident(found)
    extra: dict[str, Any] = {}
    if expected["reason"] in REASON_RENAMES:
        new_reason, text = REASON_RENAMES[expected["reason"]]
        extra["decision"] = decided(IDENT_FILE, name, expected["reason"], new_reason, text)
        expected = {**expected, "reason": new_reason}
    return case(name, owner, inp, expected, why, **extra)


def uas_row(n: int, serial: str, status: str = "active", owner: UUID | None = None) -> dict[str, Any]:
    return {
        "drone_id": str(UUID(int=n)),
        "serial": serial,
        "registration_status": status,
        "uas_operator_id": None if owner is None else str(owner),
        "in_registry": True,
    }


def gen_identification() -> None:
    snap = snapshot()
    cases = []
    table = [
        ("registered-matched", "1581F5FKD229400A", "GEOabcd1234efgh", "Serial registered and active, owner active, operator ID is the owner's."),
        ("registered-case-insensitive-and-trimmed", "1581F5FKD229400A", " geoABCD1234EFGH ", "Operator numbers compare case-insensitively, around stray spaces (U-01)."),
        ("suspended-uas", "1581F5FKD229400B", "GEOabcd1234efgh", "The UAS itself is suspended."),
        ("suspended-uas-revoked", "1581F5FKD229400C", "GEOabcd1234efgh", "A revoked UAS is reported as suspended status, reason uas_revoked."),
        ("suspended-operator", "SN-D", "GEOSUSP00000001", "The owner is suspended."),
        ("suspended-operator-revoked", "SN-E", "GEOREVK00000001", "The owner is revoked."),
        ("suspended-outranks-operator-id-mismatch-still-flagged", "1581F5FKD229400B", "GEOOTHER0000001", "Suspension outranks the operator ID: status suspended whatever is claimed; the mismatch is still flagged."),
        ("unknown-operator-absent", "1581F5FKD229400A", None, "A registered serial broadcast without an Operator ID is not registered."),
        ("unknown-operator-blank", "1581F5FKD229400A", "  ", "A blank Operator ID is absent."),
        ("unknown-operator-mismatch-unregistered-number", "1581F5FKD229400A", "GEONOTREGISTERED", "A registered serial with a number that is not its owner's is a mismatch: never registered."),
        ("unknown-operator-mismatch-other-registered-number", "1581F5FKD229400A", "GEOSUSP00000001", "Another registered operator's number is still a mismatch."),
        ("unknown-serial-with-active-operator", "SN-NOBODY", "GEOabcd1234efgh", "An unknown airframe flown by a known operator is unknown_operator: nothing registered is flying."),
        ("unknown-serial-no-operator", "SN-NOBODY", None, "Unknown serial."),
        ("owner-not-in-projection", "SN-G", "GEOabcd1234efgh", "The owner is not in the projection: nothing says it is active."),
        ("in-projection-but-not-registry", "SN-ORPHAN", None, "A projection row with no relational aircraft is never registered (migration 0010)."),
        ("eu-secret-suffix-stripped", "1581F5FKD229400A", "GEOabcd1234efgh-x9z", "An EU operator number may be broadcast with '-' and its three secret characters; only the public part is compared."),
        ("fleet-serial-alone", "SN-FLEET", None, "Our own fleet aircraft (no UAS operator) is registered on its serial alone."),
        ("fleet-ignores-operator-id", "SN-FLEET", "GEOANYTHING00001", "The fleet has no UAS operator to compare with."),
        ("unidentified-no-serial", None, "GEOabcd1234efgh", "No serial at all is unidentified, whatever operator is claimed."),
        ("unidentified-empty-serial", "", None, "An empty serial is no serial."),
        ("serial-case-folded-when-unambiguous", "sn-fleet", None, "An exact serial match wins; otherwise a case-insensitive match when exactly one aircraft has it."),
    ]
    for name, serial, operator, why in table:
        cases.append(
            ident_case(
                name,
                ["authority", "ussp"],
                {"kind": "broadcast", "serial": serial, "operator_reg": operator},
                resolve(snap, serial=serial, operator_reg=operator),
                why,
            )
        )
    ambiguous = RegistrySnapshot(
        uas=(
            UasFacts(UUID(int=20), "x", "ab-1", R.ACTIVE, None),
            UasFacts(UUID(int=21), "y", "AB-1", R.ACTIVE, None),
        )
    )
    amb_fixture = [
        {"drone_id": str(UUID(int=20)), "serial": "ab-1", "registration_status": "active", "uas_operator_id": None, "in_registry": True},
        {"drone_id": str(UUID(int=21)), "serial": "AB-1", "registration_status": "active", "uas_operator_id": None, "in_registry": True},
    ]
    for name, serial, why in (
        ("ambiguous-case-fold-is-unknown", "Ab-1", "Two aircraft differ only by case: a folded match is ambiguous and matches neither."),
        ("ambiguous-exact-match-wins", "ab-1", "The exact spelling still matches its own aircraft."),
    ):
        cases.append(
            ident_case(
                name,
                ["authority", "ussp"],
                {"kind": "broadcast", "serial": serial, "operator_reg": None, "registry_override": {"operators": [], "uas": amb_fixture}},
                resolve(ambiguous, serial=serial, operator_reg=None),
                why,
            )
        )
    cases.append(
        ident_case(
            "empty-registry-knows-nobody",
            ["authority", "ussp"],
            {"kind": "broadcast", "serial": "1581F5FKD229400A", "operator_reg": "GEOabcd1234efgh", "registry_override": {"operators": [], "uas": []}},
            resolve(RegistrySnapshot(), serial="1581F5FKD229400A", operator_reg="GEOabcd1234efgh"),
            "With no registry, a matching-looking broadcast is unknown_operator, not registered.",
        )
    )

    # --- decided after utm (uspace-core PR #9) -----------------------------
    unknown = ident(resolve(snap, serial="SN-NOBODY", operator_reg=None))
    kilo = RegistrySnapshot(uas=(UasFacts(UUID(int=30), "k", "SN-KILO", R.ACTIVE, None),))
    for name, serial, reg, override, why in (
        (
            "serial-lookalike-long-s-does-not-fold",
            "\u017fn-fleet",
            snap,
            None,
            "G-12: U+017F (long s) upper-cases to S under Unicode rules, so a "
            "Unicode fold would let a look-alike spelling claim our fleet "
            "serial SN-FLEET. Only ASCII letters fold.",
        ),
        (
            "serial-lookalike-dotless-i-does-not-fold",
            "sn-k\u0131lo",
            kilo,
            [uas_row(30, "SN-KILO")],
            "G-12: U+0131 (dotless i) upper-cases to I under Unicode rules. "
            "Only ASCII letters fold, so it does not match SN-KILO.",
        ),
    ):
        inp: dict[str, Any] = {"kind": "broadcast", "serial": serial, "operator_reg": None}
        if override is not None:
            inp["registry_override"] = {"operators": [], "uas": override}
        old_found = ident(resolve(reg, serial=serial, operator_reg=None))
        new_found = {**unknown, "serial": serial}
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                inp,
                new_found,
                why,
                decision=decided(
                    IDENT_FILE,
                    name,
                    old_found,
                    new_found,
                    "Serials fold ASCII letters only (LESSONS G-12, uspace-core PR #9 "
                    "review). utm's str.upper() folds look-alikes onto ASCII.",
                ),
            )
        )
    dup = [uas_row(31, "SN-DUP"), uas_row(32, "SN-DUP")]
    dup_snap = RegistrySnapshot(uas=tuple(UasFacts(UUID(int=n), "d", "SN-DUP", R.ACTIVE, None) for n in (31, 32)))
    old_found = ident(resolve(dup_snap, serial="SN-DUP", operator_reg=None))
    new_found = {**unknown, "serial": "SN-DUP"}
    cases.append(
        case(
            "duplicate-serial-is-ambiguous",
            ["authority", "ussp"],
            {"kind": "broadcast", "serial": "SN-DUP", "operator_reg": None, "registry_override": {"operators": [], "uas": dup}},
            new_found,
            "Two aircraft registered with the same serial: the match is "
            "ambiguous and names neither (G-05). utm let the last row win, so "
            "which aircraft a broadcast named depended on load order.",
            decision=decided(
                IDENT_FILE,
                "duplicate-serial-is-ambiguous",
                old_found,
                new_found,
                "A serial two aircraft share exactly is ambiguous, as a folded one "
                "is (G-05, uspace-core PR #9). utm let the last row win.",
            ),
        )
    )
    pending_op = UUID(int=40)
    for name, uas, operators, operator_reg, new_found, why in (
        (
            "unrecognised-uas-status-is-not-in-registry",
            [uas_row(33, "SN-PEND", status="pending")],
            [],
            None,
            {**unknown, "reason": "not_in_registry", "serial": "SN-PEND", "drone_id": str(UUID(int=33))},
            "G-03: a registration status the resolver does not know is never "
            "active and never suspended (suspended raises no identification "
            "incident). The row exists, but nothing says it is a valid "
            "registration: unknown_operator, not_in_registry.",
        ),
        (
            "unrecognised-owner-status-is-owner-unknown",
            [uas_row(34, "SN-PENDOP", owner=pending_op)],
            [{"operator_id": str(pending_op), "registration_number": "GEOPEND00000001", "status": "pending"}],
            "GEOPEND00000001",
            {**unknown, "reason": "owner_unknown", "serial": "SN-PENDOP", "operator_reg": "GEOPEND00000001", "drone_id": str(UUID(int=34))},
            "The owner's status is not one the resolver knows: nothing says "
            "the owner is in good standing, so unknown_operator, owner_unknown.",
        ),
    ):
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {"kind": "broadcast", "serial": uas[0]["serial"], "operator_reg": operator_reg, "registry_override": {"operators": operators, "uas": uas}},
                new_found,
                why,
                decision=decided(
                    IDENT_FILE,
                    name,
                    NOT_IN_UTM,
                    new_found,
                    "An unrecognised registration status fails safe as an "
                    "incident-raising status (uspace-core PR #9, coordinator "
                    "answer). utm's RegistrationStatus cannot hold one.",
                ),
            )
        )
    rid_base = {"identified": True, "ua_id": "1581F5FKD229400A", "id_type": 1, "operator_id": "GEOabcd1234efgh"}
    for name, changes, why in (
        ("remote-id-block-registered", {}, "A direct Remote ID block resolves by its Basic ID serial and Operator ID."),
        ("remote-id-block-no-operator", {"operator_id": None}, "No Operator ID: unknown_operator."),
        ("remote-id-block-unidentified", {"identified": False, "ua_id": "", "id_type": 0}, "An unidentified transmitter (S-32) is unidentified."),
        ("remote-id-block-caa-registration-is-not-a-serial", {"id_type": 2}, "A CAA registration ID equal to a registered serial is not that aircraft: only ID type 1 is looked up as a serial (reason not_a_serial)."),
        ("remote-id-block-session-id-is-not-a-serial", {"id_type": 4, "ua_id": "SESSION-1"}, "A session ID is an identity but not a serial."),
        ("remote-id-block-empty-non-serial-is-unidentified", {"id_type": 2, "ua_id": "  "}, "A blank identity of another type is no identity."),
    ):
        block = {**rid_base, **changes}
        cases.append(
            ident_case(
                name,
                ["authority", "ussp"],
                {"kind": "remote_id_block", "remote_id": block},
                resolve_remote_id(snap, block),
                why,
            )
        )
    for name, drone_id, why in (
        ("relay-fleet", UUID(int=15), "A relay track is identified by its station binding, not by a broadcast claim."),
        ("relay-registered-uas", UUID(int=10), "A bound registered UAS."),
        ("relay-suspended-uas", UUID(int=11), "Bound, but the registry suspends it."),
        ("relay-operator-suspended", UUID(int=13), "Bound, operator suspended."),
        ("relay-not-yet-projected", UUID(int=99), "Bound but not yet in the projection read: the binding is the proof (registered)."),
        ("relay-not-in-registry", UUID(int=17), "Bound, but the registry has no such aircraft."),
    ):
        cases.append(
            ident_case(
                name,
                "ussp",
                {"kind": "bound", "drone_id": str(drone_id)},
                resolve_bound(snap, drone_id),
                why,
            )
        )
    old_found = ident(resolve_bound(snap, UUID(int=16)))
    new_found = {**old_found, "status": "unknown_operator", "reason": "owner_unknown"}
    cases.append(
        case(
            "relay-owner-not-in-projection",
            "ussp",
            {"kind": "bound", "drone_id": str(UUID(int=16))},
            new_found,
            "Bound, but the aircraft's owner is not in the projection: the "
            "binding proves which aircraft it is, not that its owner is in good "
            "standing. As for a broadcast (owner-not-in-projection): "
            "unknown_operator, owner_unknown.",
            decision=decided(
                IDENT_FILE,
                "relay-owner-not-in-projection",
                old_found,
                new_found,
                "A bound aircraft whose owner is missing resolves owner_unknown, "
                "as a broadcast does (uspace-core PR #9 review). utm said registered.",
            ),
        )
    )
    cases.append(
        ident_case(
            "serial-conflict",
            ["authority", "ussp"],
            {"kind": "serial_conflict", "serial": "SN-FLEET", "operator_reg": " GEOX "},
            serial_conflict("SN-FLEET", " GEOX "),
            "S-10: one of our serials heard where our authenticated telemetry "
            "says the aircraft is not: a separate track, unknown_operator with "
            "mismatch (see fleet_match.json for when).",
        )
    )
    write(
        "identification_status.json",
        header(
            "Network identification (U-02): every track resolved against the "
            "registry to registered, suspended, unknown_operator or "
            "unidentified, with a stable reason code and a mismatch flag. "
            "fixtures.registry is the projection every case reads unless the "
            "case gives input.registry_override. input.kind: 'broadcast' "
            "(serial + operator ID), 'remote_id_block' (a direct Remote ID "
            "observation's identity), 'bound' (a track whose aircraft an "
            "authenticated binding names), 'serial_conflict'. An "
            "identification alert is raised for statuses "
            "{unidentified, unknown_operator} inside PROHIBITED or "
            "REQ_AUTHORISATION zones; a mismatch raises "
            "identification_mismatch anywhere.",
            [
                "utm gateway/tests/test_identification.py",
                "utm gateway/identification.py",
                "utm gateway/registry_projection.py RegistrySnapshot.find_uas, operator_key",
                "utm docs/runbooks/u02-identification.md",
            ],
            {},
            {"all fields": "exact"},
            ["authority", "ussp"],
            fixtures={"registry": REGISTRY_FIXTURE},
            incident_statuses=["unidentified", "unknown_operator"],
        ),
        cases,
    )

    # --- serials_and_registration.json ------------------------------------
    import re

    eu = re.compile(r"^[A-Z]{3}[A-Za-z0-9]{8,16}$")
    cases = []
    for serial, why in (
        ("1A2B1X", "Length character 1, one character follows."),
        ("1A2B9123456789", "Length 9."),
        ("1A2BF123456789ABCDEF", "Length F = 15; 20 characters in all, the maximum."),
        ("1581A1234567890", "Length A = 10."),
    ):
        cases.append(case(f"cta-valid-{serial}", "authority", {"kind": "cta2063", "serial": serial}, {"valid": True, "problem": None}, why))
    for serial, contains, why in (
        ("1A2B3AB", "says 3", "Length says 3, 2 follow."),
        ("1A2B2ABC", "says 2", "Length says 2, 3 follow."),
        ("1O2B1X", "not a CTA", "O is excluded (reads as 0)."),
        ("1A2B1I", "not a CTA", "I is excluded (reads as 1)."),
        ("1a2b1x", "not a CTA", "Lower case is not CTA-2063-A."),
        ("1A2B0X", "not a CTA", "Length 0 does not exist."),
        ("1A2BG123456789ABCDEFG", "not a CTA", "G is not a length character."),
        ("", "not a CTA", "Empty."),
    ):
        problem = cta2063_problem(serial)
        assert problem is not None and contains in problem
        cases.append(
            case(
                f"cta-invalid-{serial or 'empty'}",
                "authority",
                {"kind": "cta2063", "serial": serial},
                {"valid": False, "problem": problem, "problem_contains": contains},
                why,
            )
        )
    for label in ("C1", "C2", "C3", "C5", "C6", "C0", "C4", None):
        cls = None if label is None else ClassLabel(label)
        problem = serial_problem("DJI-0042", cls)
        cases.append(
            case(
                f"class-{label or 'none'}-legacy-serial",
                "authority",
                {"kind": "serial_for_class", "serial": "DJI-0042", "class_label": label},
                {"valid": problem is None, "problem": problem},
                "2019/945 requires a CTA-2063-A serial of C1, C2, C3, C5 and C6 "
                "(the classes that must broadcast direct Remote ID); C0, C4 and "
                "unlabelled aircraft keep whatever serial the maker printed.",
            )
        )
    cases.append(
        case(
            "class-none-empty-serial",
            "authority",
            {"kind": "serial_for_class", "serial": "", "class_label": None},
            {"valid": False, "problem": serial_problem("", None)},
            "An empty serial is refused for every class.",
        )
    )
    for number, why in (
        ("FIN87astrdge12k8", "The EU shape: country code then alphanumerics."),
        ("FIN87astrdge12k8-xyz", "Only the public part is registered; the hyphen and secret characters are refused."),
        ("fin87astrdge12k8", "The default pattern wants an upper-case country code."),
        ("FIN87", "Too short for the pattern."),
    ):
        problem = registration_number_problem(number, eu)
        cases.append(
            case(
                f"registration-{number}",
                "authority",
                {"kind": "registration_number", "value": number, "pattern": eu.pattern},
                {"valid": problem is None, "problem": problem},
                why + " The pattern is configuration (UAS_OPERATOR_REGISTRATION_PATTERN): Georgia's exact shape is unconfirmed.",
            )
        )
    # G-04 as decided in uspace-core PR #4 and #9: the secret part (a
    # hyphen and three ASCII letters or digits) is stripped only when what
    # precedes it is a registration number under the configured pattern
    # (as given, or with its ASCII letters upper-cased), and the compare
    # key upper-cases ASCII letters only. utm stripped any three-character
    # alphanumeric tail and upper-cased with str.upper(), which folds
    # U+017F onto S and U+0131 onto I.
    def ascii_upper(value: str) -> str:
        return "".join(chr(ord(c) - 32) if "a" <= c <= "z" else c for c in value)

    def ascii_alnum(value: str) -> bool:
        return all("0" <= c <= "9" or "A" <= c <= "Z" or "a" <= c <= "z" for c in value)

    def public_part(value: str) -> str:
        stripped = value.strip()
        head, hyphen, tail = stripped.rpartition("-")
        if (
            hyphen
            and head
            and len(tail) == 3
            and ascii_alnum(tail)
            and len(head) <= 64
            and (eu.fullmatch(head) or eu.fullmatch(ascii_upper(head)))
        ):
            return head
        return stripped

    def spelled(value: str) -> str:
        """A case name in ASCII: the look-alikes spelt out."""
        return value.replace("ſ", "[long-s]").replace("ı", "[dotless-i]")

    g04 = (
        "Strip the secret part only after a registration number under the "
        "configured pattern, and upper-case ASCII letters only (LESSONS G-04, "
        "G-12; uspace-core PR #4 and #9). utm stripped any three-character "
        "tail and folded with str.upper()."
    )
    for given, why in (
        ("FIN87astrdge12k8-xyz", "The EU number with its three secret characters."),
        (" FIN87astrdge12k8-XY1 ", "Trimmed first, then stripped."),
        ("FIN87astrdge12k8", "No secret tail: unchanged."),
        ("GEO-OP-SITL", "A four-character tail is not a secret: unchanged."),
        ("GEO-OP-ABC", "G-04: the secret part follows a registration number, and GEO-OP is none under the pattern, so nothing is stripped. utm stripped any three-character tail and compared GEO-OP."),
        ("FIN87astrdge12k8-", "An empty tail is not stripped."),
        ("-xyz", "Nothing before the hyphen: unchanged."),
        ("FIN87astrdge12k8-x!z", "A non-alphanumeric tail is not a secret."),
        ("fin87astrdge12k8-xyz", "A number broadcast in lower case still has its secret part stripped: the head is matched with its ASCII letters upper-cased."),
        ("FIN87astrdge12k\u017f", "G-12: U+017F (long s) is not folded onto S: the compare key keeps it, so a look-alike never compares equal to FIN87ASTRDGE12KS."),
        ("f\u0131n87astrdge12k8-xyz", "G-12: U+0131 (dotless i) is not folded onto I, so the head is no registration number under the ASCII pattern and nothing is stripped."),
        ("FIN87astrdge12k8-x\u017fz", "A tail with a non-ASCII letter is not a secret part."),
    ):
        name = f"public-part-{spelled(given.strip()) or 'blank'}"
        old = {"public": public_registration_number(given), "compare_key": operator_key(given)}
        new = {"public": public_part(given), "compare_key": ascii_upper(public_part(given))}
        extra = {} if new == old else {"decision": decided("serials_and_registration.json", name, old, new, g04)}
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {"kind": "public_registration_number", "value": given, "pattern": eu.pattern},
                new,
                why + " compare_key is what identification compares (public part, ASCII letters upper-cased).",
                **extra,
            )
        )
    for given, why in (
        ("sn-fleet", "ASCII letters fold: the key of a lower-case spelling is the upper-case one."),
        (" 1581f5fkd229400a ", "Trimmed, then folded."),
        ("\u017fn-fleet", "G-12: U+017F (long s) is kept, so it never folds onto SN-FLEET."),
        ("sn-k\u0131lo", "G-12: U+0131 (dotless i) is kept, so it never folds onto SN-KILO."),
    ):
        name = f"serial-fold-{spelled(given.strip())}"
        old = {"fold_key": normalize_serial(given).upper()}
        new = {"fold_key": ascii_upper(normalize_serial(given))}
        extra = {}
        if new != old:
            extra["decision"] = decided(
                "serials_and_registration.json",
                name,
                old,
                new,
                "Serials fold ASCII letters only (LESSONS G-12, uspace-core PR #9 "
                "review). utm's str.upper() folds look-alikes onto ASCII.",
            )
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {"kind": "serial_fold", "serial": given},
                new,
                why + " fold_key is what a case-insensitive serial lookup compares (G-05); an exact match still wins.",
                **extra,
            )
        )
    write(
        "serials_and_registration.json",
        header(
            "UAS serial numbers (ANSI/CTA-2063-A) and operator registration "
            "numbers (EU 2019/947 Art. 14), as U-01 validates them on "
            "registration and U-02 compares them on identification. Match "
            "`valid` exactly; `problem` is the old wording, `problem_contains` "
            "the part the old tests pinned. public_registration_number cases "
            "give the registration pattern the strip depends on (G-04): the "
            "secret part is stripped only after a number under it. kind "
            "serial_fold gives the key a "
            "case-insensitive serial lookup compares (G-05, G-12).",
            [
                "utm common/tests/test_uas_identity.py",
                "utm common/uas_identity.py",
                "utm gateway/registry_projection.py operator_key",
            ],
            {},
            {"all fields": "exact"},
            ["authority", "ussp"],
            cta2063_rule="4 chars manufacturer code + 1 length char (1-9, A-F = 10-15) + exactly that many chars; alphabet 0-9 A-Z without O and I; at most 20",
        ),
        cases,
    )


# =================================================================================
# fleet_match.json (S-10 spoofing guard, judge())
# =================================================================================


def gen_fleet_match() -> None:
    ours = Registered(drone_id=UUID(int=15), label="hexa-01")
    near = (41.7151, 44.8271)
    cases = []

    def judged(
        name: str,
        why: str,
        *,
        registered: bool = True,
        relay: list[dict[str, Any]],
        broadcast: tuple[float, float] | None,
        now_s: float,
        live_for_s: float = 5.0,
        spoof_distance_m: float = 300.0,
        problem: str | None = None,
    ) -> None:
        links = LinkFreshness(live_for_s=live_for_s)
        for row in relay:
            body = {
                "drone_id": str(ours.drone_id),
                "lat_deg": row.get("lat_deg"),
                "lon_deg": row.get("lon_deg"),
                "backlog": row.get("backlog", False),
            }
            if "source" in row:
                body["source"] = row["source"]
            if "behind_s" in row:
                rx = NOW
                body["rx_ts"] = iso(rx)
                body["captured_at"] = iso(rx - timedelta(seconds=row["behind_s"]))
            links.on_telemetry(json.dumps(body).encode(), now_s=row["heard_at_s"])
        result = judge(
            ours if registered else None,
            broadcast,
            links,
            now_s=now_s,
            spoof_distance_m=spoof_distance_m,
        )
        expected = {
            "verdict": result.verdict.value,
            "apart_m": result.apart_m,
            "ignored_history_rows": links.ignored_history,
            "problem": None,
        }
        extra: dict[str, Any] = {}
        if problem is not None:
            # A threshold that is not a finite number above zero withholds
            # before any row is read (LESSONS E-15, uspace-core PR #9 review).
            old = expected
            expected = {"verdict": "withhold", "apart_m": None, "ignored_history_rows": 0, "problem": problem}
            extra["decision"] = decided(
                "fleet_match.json",
                name,
                old,
                expected,
                "A live_for_s or spoof_distance_m that is not a finite number above "
                "zero withholds, naming the threshold, and is never as_ours or "
                "conflict (LESSONS E-15, uspace-core PR #9 review). utm judged with it.",
            )
        cases.append(
            case(
                name,
                ["authority", "ussp"],
                {
                    "serial_is_ours": registered,
                    "relay_rows": relay,
                    "broadcast_position": None if broadcast is None else list(broadcast),
                    "now_s": now_s,
                    "live_for_s": live_for_s,
                    "spoof_distance_m": spoof_distance_m,
                },
                expected,
                why,
                **extra,
            )
        )

    def north(m: float) -> tuple[float, float]:
        return (near[0] + m / 111_195.0, near[1])

    judged("stranger", "Not one of our serials: nothing to judge.", registered=False, relay=[], broadcast=north(0), now_s=0.0)
    judged("relay-live-and-agreeing-withhold", "Our relay is live and the broadcast is 50 m from it: the relay track is better; the broadcast is stored, not published.", relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}], broadcast=north(50), now_s=1.0)
    judged("relay-live-far-away-conflict", "500 m from where the authenticated relay places our aircraft: not our aircraft (S-10). A separate unverified track, serial_conflict.", relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}], broadcast=north(500), now_s=1.0)
    judged("relay-quiet-broadcast-speaks-for-ours", "Relay silent for more than 5 s: the broadcast takes over as our aircraft, still marked as a broadcast (P1-15). Nothing can contradict it.", relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}], broadcast=north(500), now_s=5.1)
    judged("relay-live-exactly-at-live-for", "Heard exactly 5 s ago is still live (<=).", relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}], broadcast=north(50), now_s=5.0)
    judged("relay-live-no-position-withhold", "Live relay without a position yet: withheld, never judged a conflict without a distance.", relay=[{"heard_at_s": 0.0}], broadcast=north(500), now_s=1.0)
    judged(
        "relay-backlog-is-history",
        "U-02 review: a relay draining its queue after an outage delivers minutes-old positions. A backlog row neither makes the link live nor moves the position; without this our own current broadcast was split off as a spoof (SITL re-check 2026-10-01, 438 m apart).",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1], "backlog": True}],
        broadcast=north(440),
        now_s=1.0,
    )
    judged(
        "relay-row-captured-long-before-receipt-is-history",
        "A row captured more than live_for_s before the Gateway received it is history too, even if not flagged backlog.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1], "behind_s": 6.0}],
        broadcast=north(440),
        now_s=1.0,
    )
    judged(
        "relay-row-slightly-behind-is-live",
        "4 s behind receipt is within live_for_s: live, and 440 m is a conflict.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1], "behind_s": 4.0}],
        broadcast=north(440),
        now_s=1.0,
    )
    judged(
        "remote-id-rows-do-not-make-the-link-live",
        "Only relay telemetry counts; a broadcast (source remote_id or network_remote_id) never vouches for itself.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1], "source": "remote_id"}],
        broadcast=north(500),
        now_s=1.0,
    )
    judged(
        "zero-live-window-withholds",
        "E-15: with live_for_s 0 every row is history, so utm let the broadcast speak for our aircraft (as_ours) while our relay was live 500 m away. A threshold that disarms the guard withholds and names itself.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}],
        broadcast=north(500),
        now_s=1.0,
        live_for_s=0.0,
        problem="live_for_s",
    )
    judged(
        "zero-spoof-distance-withholds",
        "E-15: with spoof_distance_m 0 any distance is a conflict, so our own broadcast 50 m from the relay was split off as a spoof. A threshold that cannot be used withholds and names itself.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}],
        broadcast=north(50),
        now_s=1.0,
        spoof_distance_m=0.0,
        problem="spoof_distance_m",
    )
    judged(
        "negative-live-window-withholds",
        "E-15: a negative window is no window either.",
        relay=[{"heard_at_s": 0.0, "lat_deg": near[0], "lon_deg": near[1]}],
        broadcast=north(500),
        now_s=1.0,
        live_for_s=-5.0,
        problem="live_for_s",
    )
    write(
        "fleet_match.json",
        header(
            "The spoofing guard (S-10, U-02): a broadcast claiming one of our "
            "fleet's serials, judged against that aircraft's authenticated "
            "telemetry. verdict: stranger (not ours), withhold (ours, live, "
            "agreeing: the authenticated track is better), as_ours (ours, link "
            "quiet: the broadcast speaks for it, still marked broadcast), "
            "conflict (ours, live, more than spoof_distance_m away: a separate "
            "unverified track, unknown_operator, mismatch, reason "
            "serial_conflict). relay_rows are what the bus delivered, in order. "
            "problem names a threshold (live_for_s, spoof_distance_m) that is "
            "not a finite number above zero: the guard then withholds before "
            "reading any row; null otherwise.",
            [
                "utm gateway/remote_id_match.py judge(), LinkFreshness",
                "utm gateway/tests/test_remote_id_match.py",
                "utm docs/runbooks/u02-identification.md",
            ],
            {"*_s": "seconds", "apart_m": "metres, haversine on a 6,371,008.8 m sphere", "positions": "[lat_deg, lon_deg]"},
            {"apart_m": 0.01, "verdict": "exact"},
            ["authority", "ussp"],
        ),
        cases,
    )


# =================================================================================
# cpa.json
# =================================================================================

LAT0, LON0 = 41.7151, 44.8271
N1 = local_offset_m(LAT0, LON0, LAT0 + 0.001, LON0)[0]
E1 = local_offset_m(LAT0, LON0, LAT0, LON0 + 0.001)[1]
POLICY = SeparationPolicy(t_cpa_max_s=60, d_horizontal_min_m=60, d_vertical_min_m=20, neighbour_radius_m=800)
POLICY_JSON = {"t_cpa_max_s": 60, "d_horizontal_min_m": 60, "d_vertical_min_m": 20, "neighbour_radius_m": 800}


def latlon(north_m: float, east_m: float = 0.0) -> tuple[float, float]:
    return LAT0 + 0.001 * north_m / N1, LON0 + 0.001 * east_m / E1


def track_spec(n: int, north_m: float, east_m: float = 0.0, *, vn: float = 0.0, ve: float = 0.0, vd: float = 0.0, alt: float = 550.0, at_s: float = 0.0, vertical_known: bool = True) -> tuple[dict[str, Any], Track]:
    lat, lon = latlon(north_m, east_m)
    spec = {
        "lat_deg": lat,
        "lon_deg": lon,
        "alt_amsl_m": alt,
        "vn_ms": vn,
        "ve_ms": ve,
        "vd_ms": vd,
        "captured_at_s": at_s,
        "vertical_known": vertical_known,
        "described_as": {"north_m": north_m, "east_m": east_m},
    }
    track = Track(UUID(int=n), lat, lon, alt, vn, ve, vd, at_s, vertical_known=vertical_known)
    return spec, track


def loss_of_separation(a: Track, b: Track, policy: SeparationPolicy) -> float | None:
    """When the pair is first inside both minima within the window, or None.

    Decided in uspace-core PR #10 (owner): a conflict is a loss of
    separation at any time in [0, t_cpa_max_s], not only at the horizontal
    t_cpa (LESSONS C-19). The pair is put in utm's frame (the older sample
    advanced, the tangent plane about the mid latitude), then the open
    interval where the horizontal distance is below its minimum (a
    quadratic) is intersected with the open interval where the vertical
    gap is below its minimum (linear); with the vertical unknown the
    horizontal interval decides alone. utm judged only now and at t_cpa.
    """
    if a.captured_at_s < b.captured_at_s:
        a = advance(a, b.captured_at_s - a.captured_at_s)
    elif b.captured_at_s < a.captured_at_s:
        b = advance(b, a.captured_at_s - b.captured_at_s)
    lat0, lon0 = (a.lat_deg + b.lat_deg) / 2, a.lon_deg
    an, ae = local_offset_m(lat0, lon0, a.lat_deg, a.lon_deg)
    bn, be = local_offset_m(lat0, lon0, b.lat_deg, b.lon_deg)
    pn, pe, vn, ve = bn - an, be - ae, b.vn_ms - a.vn_ms, b.ve_ms - a.ve_ms
    inf = float("inf")
    sq = vn * vn + ve * ve
    c = pn * pn + pe * pe - policy.d_horizontal_min_m**2
    if sq < 1e-12:
        start, end = (-inf, inf) if c < 0 else (inf, -inf)
    else:
        half_b = pn * vn + pe * ve
        disc = half_b * half_b - sq * c
        if disc <= 0:
            start, end = inf, -inf
        else:
            root = disc**0.5
            start, end = (-half_b - root) / sq, (-half_b + root) / sq
    if a.vertical_known and b.vertical_known:
        gap, rate, vmin = b.alt_amsl_m - a.alt_amsl_m, a.vd_ms - b.vd_ms, policy.d_vertical_min_m
        if rate == 0:
            v_start, v_end = (-inf, inf) if abs(gap) < vmin else (inf, -inf)
        else:
            t1, t2 = (-vmin - gap) / rate, (vmin - gap) / rate
            v_start, v_end = min(t1, t2), max(t1, t2)
        start, end = max(start, v_start), min(end, v_end)
    if start < end and end > 0 and start < policy.t_cpa_max_s:
        return max(start, 0.0)
    return None


def gen_cpa() -> None:
    cases = []
    max_age_s = 10.0

    def pair(
        name: str,
        a: tuple[dict[str, Any], Track],
        b: tuple[dict[str, Any], Track],
        why: str,
        owner: Any = None,
        policy: dict[str, float] | None = None,
    ) -> None:
        pol = POLICY if policy is None else SeparationPolicy(**policy)
        judged = abs(a[1].captured_at_s - b[1].captured_at_s) <= max_age_s
        expected: dict[str, Any] = {"judged": judged}
        extra: dict[str, Any] = {}
        inp: dict[str, Any] = {"a": a[0], "b": b[0], "neighbour_max_age_s": max_age_s}
        if policy is not None:
            inp["policy"] = policy
        if not judged:
            expected["not_judged"] = "stale_neighbour"
        else:
            approach = closest_approach(a[1], b[1])
            old_conflict = pol.is_conflict(approach)
            los_start_s = loss_of_separation(a[1], b[1], pol)
            conflict = los_start_s is not None
            assert conflict or not old_conflict, f"{name}: the window criterion lost a utm conflict"
            expected.update(
                {
                    "t_cpa_s": approach.t_cpa_s,
                    "d_cpa_horizontal_m": approach.d_cpa_horizontal_m,
                    "d_alt_at_cpa_m": approach.d_alt_at_cpa_m if approach.vertical_known else None,
                    "d_horizontal_now_m": approach.d_horizontal_now_m,
                    "d_alt_now_m": approach.d_alt_now_m if approach.vertical_known else None,
                    "vertical_known": approach.vertical_known,
                    "conflict": conflict,
                    "los_start_s": los_start_s,
                }
            )
            invalid = pol.d_horizontal_min_m <= 0 or pol.d_vertical_min_m <= 0
            if conflict != old_conflict and not invalid:
                extra["decision"] = decided(
                    "cpa.json",
                    name,
                    old_conflict,
                    conflict,
                    "A conflict is a loss of separation at any time in the window, "
                    "not only at the horizontal t_cpa (LESSONS C-19, uspace-core "
                    "PR #10, owner decision). utm judged now and at t_cpa only.",
                )
            if invalid:
                old = {**expected, "conflict": old_conflict, "los_start_s": None}
                expected = {"judged": False, "not_judged": "invalid_policy"}
                extra["decision"] = decided(
                    "cpa.json",
                    name,
                    old,
                    expected,
                    "A zero separation minimum disarms the check, so the pair is not "
                    "judged (invalid_policy) rather than judged clear (LESSONS E-15, "
                    "uspace-core PR #10 review). utm judged with it.",
                )
        cases.append(case(name, owner or ["ussp", "authority"], inp, expected, why, **extra))

    pair("head-on", track_spec(1, 0, vn=10), track_spec(2, 1000, vn=-10), "1 km apart closing at 20 m/s: CPA 0 m in 50 s, inside the 60 s window.")
    pair("crossing-right-angles", track_spec(1, -300, vn=10), track_spec(2, 0, 300, ve=-10), "Both reach the same point in 30 s.")
    pair("crossing-that-misses", track_spec(1, -300, vn=10), track_spec(2, 0, 500, ve=-10), "B 20 s late: they pass 200/sqrt(2) = 141 m apart; no conflict.")
    pair("overtaking", track_spec(1, 200, vn=10), track_spec(2, 0, vn=15), "Same track, 5 m/s faster from 200 m behind: caught up in 40 s.")
    pair("parallel-same-speed", track_spec(1, 0, vn=10), track_spec(2, 0, 45, vn=10), "Zero relative velocity: every time is equally close, t_cpa is 0 (no division by zero), 45 m apart and staying so is a conflict.")
    pair("both-hovering", track_spec(1, 0), track_spec(2, 30, 40), "The same degenerate case at rest: 50 m now.")
    pair("diverging-far", track_spec(1, 0, vn=-10), track_spec(2, 100, vn=10), "Closest approach in the past: t_cpa reported as 0 and distances as now. A negative t_cpa would read as 'already happened, safe'.")
    pair("diverging-but-already-too-close", track_spec(1, 0, vn=-1), track_spec(2, 10, vn=1), "Diverging is not a pass while 10 m apart: inside the minima now is a conflict.")
    pair("vertical-separation-at-cpa", track_spec(1, 0, vn=10, alt=550), track_spec(2, 1000, vn=-10, alt=600), "Head-on horizontally but 50 m apart vertically: no conflict. Horizontal and vertical are separate; a single 3-D CPA would let a climb hide a head-on.")
    pair("climb-closes-vertical-gap", track_spec(1, 0, vn=10, vd=-1.0, alt=550), track_spec(2, 1000, vn=-10, alt=600), "vd is positive DOWN (MAVLink): climbing 1 m/s closes 50 m in 50 s. The vertical gap is evaluated at t_cpa.")
    pair("conflict-beyond-window", track_spec(1, 0, vn=5), track_spec(2, 1000, vn=-5), "Meeting in 100 s is beyond the 60 s window: not yet.")
    pair("order-a-b", track_spec(1, -300, vn=10), track_spec(2, 0, 500, ve=-10), "The result does not depend on which aircraft is first.")
    pair("order-b-a", track_spec(2, 0, 500, ve=-10), track_spec(1, -300, vn=10), "The result does not depend on which aircraft is first.")
    pair(
        "hovering-inside-minima-with-velocity-noise",
        track_spec(1, 0),
        track_spec(2, 30, vn=-0.01),
        "Found in SITL (P5-07, 2026-09-29): 30 m apart, hovering, 1 cm/s GPS noise puts the CPA 3000 s away, outside the window. §6.2's three-part test alone cleared the alert as resolved while they were still 30 m apart. Inside the minima NOW is a conflict whatever t_cpa says.",
    )
    pair("hovering-outside-minima-with-noise", track_spec(1, 0), track_spec(2, 80, vn=-0.01), "The absence pair: 80 m apart with the same noise is not a conflict.")
    for d in (40, 50, 60):
        pair(f"opening-from-{d}m", track_spec(1, 0), track_spec(2, d, vn=10), "A diverging pair stays a conflict until it is actually past the 60 m minimum (60 m is not < 60).")
    pair("stacked-30m-vertically", track_spec(1, 0, alt=500), track_spec(2, 30, alt=530), "Inside horizontally but 30 m apart vertically now: not a conflict.")
    pair(
        "older-sample-advanced-a-stale",
        track_spec(1, -50, vn=10, at_s=0),
        track_spec(2, 1000, vn=-10, at_s=5),
        "S-11: A's sample is 5 s older, taken 50 m back down its track. It is advanced along its velocity to B's time first: the answer is 50 s, as with fresh samples, not 52.5 s. A 5 s old sample at 15 m/s is 75 m wrong against a 60 m minimum.",
    )
    pair("older-sample-advanced-b-first", track_spec(2, 1000, vn=-10, at_s=5), track_spec(1, -50, vn=10, at_s=0), "Same, other order.")
    pair(
        "stale-neighbour-not-judged",
        track_spec(1, 0, vn=10, at_s=11),
        track_spec(2, 500, vn=-10, at_s=0),
        "B's sample is 11 s older than A's, beyond neighbour_max_age_s (10 s): too old to advance along a straight line with meaning. The pair is not judged by this message: neither raised, nor refreshed, nor shown clear (silence is not evidence).",
    )
    pair("neighbour-at-max-age-is-judged", track_spec(1, 0, vn=10, at_s=10), track_spec(2, 600, vn=-10, at_s=0), "Exactly 10 s apart is still judged (<=), after advancing B.")
    pair(
        "pressure-tracks-100m-apart-vertically-still-conflict",
        track_spec(1, 0, vn=10, alt=500, vertical_known=False),
        track_spec(2, 500, vn=-10, alt=600, vertical_known=False),
        "S-33: pressure altitudes are not vertical positions (QNH can move them 160 m). The vertical minimum is treated as not met: judged on the horizontal alone; d_alt is null, never a number.",
    )
    pair("same-pair-geodetic-no-conflict", track_spec(1, 0, vn=10, alt=500), track_spec(2, 500, vn=-10, alt=600), "The presence pair: on geodetic altitudes 100 m apart they do not conflict.")
    pair("one-pressure-track-makes-the-pair-unknown", track_spec(1, 0, vn=10, alt=500), track_spec(2, 500, vn=-10, alt=600, vertical_known=False), "One unknown makes the pair's vertical unknown.")
    pair("horizontally-clear-pressure-tracks", track_spec(1, 0, alt=500, vertical_known=False), track_spec(2, 500, alt=500, vertical_known=False), "Unknown vertically is not a conflict by itself.")
    pair(
        "vertical-gap-under-minimum-before-t-cpa",
        track_spec(1, 0, vn=10, alt=550),
        track_spec(2, 1000, vn=-10, vd=1.0, alt=578.6),
        "C-19: head-on, inside 60 m horizontally from 47 s to 53 s. B descends 1 m/s from 28.6 m above A: the vertical gap is 21.4 m at the horizontal t_cpa (50 s), above the 20 m minimum, but 18.4 m at 47 s. Judged only at t_cpa this pair is clear; it loses separation from 47 s.",
    )
    pair(
        "vertical-gap-closes-after-the-pass",
        track_spec(1, 0, vn=10, alt=550),
        track_spec(2, 1000, vn=-10, vd=1.0, alt=625.0),
        "The absence pair: the vertical gap falls under 20 m only from 55 s, after the horizontal interval (47-53 s) ends. Never inside both minima at once: clear.",
    )
    pair(
        "enters-minima-before-window-end-t-cpa-beyond",
        track_spec(1, 0, vn=5),
        track_spec(2, 640, vn=-5),
        "C-19: closing at 10 m/s from 640 m, t_cpa is 64 s, beyond the 60 s window, but the pair is inside 60 m from 58 s. The window bounds the start of the loss of separation, not t_cpa: a conflict, los_start_s 58.",
    )
    pair(
        "enters-minima-after-window-end",
        track_spec(1, 0, vn=5),
        track_spec(2, 680, vn=-5),
        "The absence pair: from 680 m the pair enters the minima at 62 s, after the window. Not yet a conflict.",
    )
    for which, policy in (
        ("horizontal", {**POLICY_JSON, "d_horizontal_min_m": 0}),
        ("vertical", {**POLICY_JSON, "d_vertical_min_m": 0}),
    ):
        pair(
            f"zero-{which}-minimum-is-invalid-policy",
            track_spec(1, 0, vn=10),
            track_spec(2, 1000, vn=-10),
            f"E-15: a zero {which} minimum makes every pair clear, which silently disarms the check. The policy is refused (invalid_policy) and nothing is judged, never judged clear. The pair is the head-on one, a conflict under the real policy.",
            policy=policy,
        )
    write(
        "cpa.json",
        header(
            "Closest point of approach and the conflict test (ARCHITECTURE §6.2, "
            "P5-07, S-11, S-33). Positions are projected onto a local tangent "
            "plane about the pair's mid latitude with WGS-84 meridional and "
            "prime-vertical radii (local_offset_m); the older sample is advanced "
            "to the newer one's time first. t_cpa from the horizontal motion "
            "only, clamped to >= 0; vertical separation evaluated at t_cpa "
            "(the reported numbers). conflict is a loss of separation at any "
            "time in [0, t_cpa_max_s] (C-19): the open interval where the "
            "horizontal distance is below d_h_min overlaps the open interval "
            "where the vertical gap is below d_v_min (with the vertical "
            "unknown, the horizontal interval alone), and that overlap starts "
            "before t_cpa_max_s. This contains utm's criterion, (d_h_now < "
            "d_h_min AND (vertical unknown OR d_alt_now < d_v_min)) OR (t_cpa "
            "< t_max AND d_cpa_h < d_h_min AND (vertical unknown OR "
            "d_alt_at_cpa < d_v_min)). los_start_s is the start of the "
            "overlap (0 when inside now), null when there is no conflict. A "
            "pair not judged gives not_judged: stale_neighbour, or "
            "invalid_policy for a case whose input.policy (which replaces the "
            "header policy) has a minimum that is not above zero. "
            "described_as is how the case was built (metres from 41.7151N "
            "44.8271E); the inputs are lat/lon.",
            [
                "utm airspace/tests/test_cpa.py",
                "utm airspace/tests/test_monitor.py (neighbour age)",
                "utm airspace/tests/test_pressure_altitude.py",
                "utm airspace/cpa.py",
            ],
            {
                "lat_deg/lon_deg": "degrees WGS84",
                "alt_amsl_m": "metres AMSL (separation is judged in AMSL, never AGL)",
                "vn/ve/vd_ms": "m/s north, east, DOWN (MAVLink GLOBAL_POSITION_INT convention)",
                "captured_at_s": "seconds, one clock for all aircraft",
            },
            {"t_cpa_s": 0.01, "los_start_s": 0.01, "distances_m": 0.01, "booleans": "exact"},
            ["ussp", "authority"],
            policy=POLICY_JSON,
        ),
        cases,
    )


# =================================================================================
# alert_lifecycle.json and zones_vertical.json (the monitor)
# =================================================================================

LETTER = {"A": UUID(int=1), "B": UUID(int=2), "C": UUID(int=3), "D": UUID(int=4)}
BY_UUID = {v: k for k, v in LETTER.items()}
DETAIL_KEYS = (
    "t_cpa_s",
    "d_cpa_horizontal_m",
    "d_alt_at_cpa_m",
    "d_horizontal_now_m",
    "vertical_separation_known",
    "identifier",
    "restriction",
    "vertical_known",
    "within_band",
    "limit_not_judged",
    "not_judged",
    "height_agl_m",
    "alt_hae_m",
    "max_height_agl_m",
    "status",
    "identification_reason",
)


def zone_feature(
    identifier: str = "T1",
    restriction: str = "PROHIBITED",
    *,
    half_deg: float = 0.01,
    lower: tuple[float, str] | None = None,
    upper: tuple[float, str] | None = None,
    uom: str = "M",
    applicability: list[dict[str, Any]] | None = None,
    circle: tuple[float, float, float] | None = None,
) -> dict[str, Any]:
    if circle is not None:
        projection: dict[str, Any] = {"type": "Circle", "center": [circle[1], circle[0]], "radius": circle[2]}
    else:
        h = half_deg
        projection = {
            "type": "Polygon",
            "coordinates": [[[LON0 - h, LAT0 - h], [LON0 + h, LAT0 - h], [LON0 + h, LAT0 + h], [LON0 - h, LAT0 + h], [LON0 - h, LAT0 - h]]],
        }
    volume: dict[str, Any] = {
        "uomDimensions": uom,
        "lowerVerticalReference": "AMSL" if lower is None else lower[1],
        "upperVerticalReference": "AMSL" if upper is None else upper[1],
        "horizontalProjection": projection,
    }
    if lower is not None:
        volume["lowerLimit"] = lower[0]
    if upper is not None:
        volume["upperLimit"] = upper[0]
    return {
        "identifier": identifier,
        "country": "GEO",
        "type": "COMMON",
        "restriction": restriction,
        "applicability": applicability or [{"permanent": "YES"}],
        "zoneAuthority": [],
        "geometry": [volume],
    }


def zone_of(feature: dict[str, Any], n: int) -> Any:
    return monitored_zone(UUID(int=100 + n), ed269.parse_zone(feature))


class FlatGround:
    def __init__(self, ground_m: float | None) -> None:
        self.ground_m = ground_m

    def elevation(self, lat_deg: float, lon_deg: float) -> Elevation | None:
        if self.ground_m is None:
            return None
        return Elevation(elevation_m=self.ground_m, dataset="COP-DEM GLO-30", spacing_m=30.0)


def aircraft_message(spec: dict[str, Any], t: float) -> dict[str, Any]:
    at_s = spec.get("captured_at_s", t)
    rx_s = spec.get("rx_at_s", at_s)
    lat, lon = latlon(spec.get("north_m", 0.0), spec.get("east_m", 0.0))
    source = spec.get("source", "relay")
    body: dict[str, Any] = {
        "drone_id": str(LETTER[spec["id"]]),
        "label": spec["id"],
        # The station's own clock: only orders samples within one source.
        "ts": iso(datetime.fromtimestamp(at_s + spec.get("station_clock_offset_s", 0.0), tz=UTC)),
        "rx_ts": iso(datetime.fromtimestamp(rx_s, tz=UTC)),
        "captured_at": iso(datetime.fromtimestamp(at_s, tz=UTC)),
        "backlog": spec.get("backlog", False),
        "lat_deg": lat,
        "lon_deg": lon,
        "alt_amsl_m": spec.get("alt_amsl_m", 550.0),
        "vx_ms": spec.get("vn", 0.0),
        "vy_ms": spec.get("ve", 0.0),
        "vz_ms": spec.get("vd", 0.0),
    }
    flying = spec.get("flying", True)
    if source == "relay":
        body.update({"station_id": spec.get("station", "gs-1"), "armed": flying})
    else:
        body.update(
            {
                "source": source,
                "station_id": spec.get("station", "rx-1"),
                "armed": None,
                "airborne": flying,
                "remote_id": {
                    "identified": spec.get("identified", True),
                    "transmitter": spec.get("transmitter", f"TX-{spec['id']}"),
                },
            }
        )
    if "alt_source" in spec:
        body["alt_source"] = spec["alt_source"]
    if "identification" in spec:
        body["identification"] = spec["identification"]
    return body


def neutral_input(spec: dict[str, Any], t: float) -> dict[str, Any]:
    lat, lon = latlon(spec.get("north_m", 0.0), spec.get("east_m", 0.0))
    out = dict(spec)
    out["lat_deg"] = lat
    out["lon_deg"] = lon
    out.setdefault("captured_at_s", t)
    return out


def alert_summary(alert: Any) -> dict[str, Any]:
    detail = {k: alert.detail[k] for k in DETAIL_KEYS if k in alert.detail}
    return {
        "kind": alert.kind.value,
        "severity": alert.severity.value,
        "aircraft": [BY_UUID[d] for d in alert.drone_ids],
        "detail": detail,
    }


def run_monitor(config: dict[str, Any], steps: list[dict[str, Any]]) -> dict[str, Any]:
    disabled: set[tuple[str, str | None]] = set()

    def enabled(source_type: str, instance_id: str | None) -> bool:
        return (source_type, None) not in disabled and (source_type, instance_id) not in disabled

    zones = [zone_of(f, i) for i, f in enumerate(config.get("zones", []))]
    monitor = AirspaceMonitor(
        policy=POLICY,
        zones=zones,
        terrain=None if "ground_m" not in config else FlatGround(config["ground_m"]),
        geoid=None if config.get("geoid_undulation_m") is None else FlatGeoid(config["geoid_undulation_m"]),
        max_height_agl_m=config.get("max_height_agl_m"),
        clear_after_s=config.get("clear_after_s", 3.0),
        stale_after_s=config.get("stale_after_s", 15.0),
        neighbour_max_age_s=config.get("neighbour_max_age_s", 10.0),
        live_max_age_s=config.get("live_max_age_s", 10.0),
        pressure_uncertainty_m=config.get("pressure_uncertainty_m", 250.0),
        conditional_severity=Severity(config.get("conditional_severity", "warning")),
        source_enabled=enabled,
    )
    out = []
    for step in steps:
        t = step["t_s"]
        op = step["op"]
        if op == "observe":
            change = monitor.observe(aircraft_message(step["aircraft"], t), now_s=t)
        elif op == "tick":
            change = monitor.tick(now_s=t)
        elif op == "switch_source":
            key = (step["source_type"], step.get("instance_id"))
            if step["enabled"]:
                disabled.discard(key)
            else:
                disabled.add(key)
            change = monitor.apply_sources(now_s=t)
        else:
            raise ValueError(op)
        out.append(
            {
                "raised": [alert_summary(a) for a in change.raised],
                "cleared": [{**alert_summary(c.alert), "reason": c.reason.value} for c in change.cleared],
            }
        )
    return {
        "per_step": out,
        "active_after": sorted(
            (alert_summary(a) for a in monitor.active), key=lambda a: (a["kind"], a["aircraft"])
        ),
        "counters": {
            "rejected_backlog": monitor.rejected_backlog,
            "rejected_late": monitor.rejected_late,
            "rejected_out_of_order": monitor.rejected_out_of_order,
            "rejected_source_disabled": monitor.rejected_source_disabled,
            "zone_checks_not_evaluated": monitor.zone_checks_not_evaluated,
            "zone_limits_not_judged": monitor.zone_limits_not_judged,
        },
    }


def obs(t: float, **spec: Any) -> dict[str, Any]:
    return {"t_s": t, "op": "observe", "aircraft": spec}


def neutral_steps(steps: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = []
    for step in steps:
        if step["op"] == "observe":
            out.append({**step, "aircraft": neutral_input(step["aircraft"], step["t_s"])})
        else:
            out.append(step)
    return out


def gen_lifecycle() -> None:
    cases = []
    file = "alert_lifecycle.json"

    def seq(
        name: str,
        config: dict[str, Any],
        steps: list[dict[str, Any]],
        why: str,
        owner: Any = None,
        source: str = "",
        *,
        refused: set[int] | None = None,
        counters: dict[str, int] | None = None,
        decision: str | None = None,
        not_in_utm: bool = False,
    ) -> None:
        """One sequence. With a decision, the expected value is utm's run
        with the refused steps left out (a refused sample judges nothing,
        so it is as if it never came), an empty result at each refused
        step, and the counters utm does not have."""
        expected = run_monitor(config, steps)
        extra: dict[str, Any] = {}
        if decision is not None:
            refused = refused or set()
            new = run_monitor(config, [st for i, st in enumerate(steps) if i not in refused])
            for i in sorted(refused):
                new["per_step"].insert(i, {"raised": [], "cleared": []})
            new["counters"] = {**new["counters"], **(counters or {})}
            events = ("per_step", "active_after")
            old = NOT_IN_UTM if not_in_utm else {k: expected[k] for k in events}
            extra["decision"] = decided(file, name, old, {k: new[k] for k in events}, decision)
            expected = new
        cases.append(
            case(
                name,
                owner or ["authority", "ussp"],
                {"config": config, "steps": neutral_steps(steps)},
                expected,
                why,
                source=source,
                **extra,
            )
        )

    head_on = [obs(0.0, id="A", north_m=0, vn=10), obs(0.0, id="B", north_m=500, vn=-10)]
    seq("head-on-raises-one-critical", {}, head_on, "A raise names both aircraft once; t_cpa 25 s.", source="test_monitor.py::test_a_head_on_pair_raises_one_critical_conflict_naming_both")
    seq(
        "raised-once-not-every-tick",
        {},
        head_on + [obs(1.0, id="A", north_m=10, vn=10), obs(1.0, id="B", north_m=490, vn=-10)],
        "An alert is raised once per condition and refreshed silently while true.",
        source="test_monitor.py::test_the_same_conflict_is_not_raised_again_every_tick",
    )
    seq(
        "resolved-after-hysteresis",
        {"clear_after_s": 3.0},
        head_on + [obs(1.0, id="B", north_m=500, vn=10), obs(3.0, id="B", north_m=510, vn=10), obs(3.5, id="B", north_m=520, vn=10)],
        "Shown false from 1.0 s; cleared 'resolved' only once shown false for MORE than clear_after_s since last true (0 s): at 3.5 s, not 3.0 s.",
        source="test_monitor.py::test_a_resolved_conflict_clears_only_after_the_hysteresis",
    )
    seq(
        "silence-does-not-clear-before-stale",
        {"clear_after_s": 3.0, "stale_after_s": 15.0},
        head_on + [{"t_s": 10.0, "op": "tick"}],
        "No message shows the pair apart, so the alert stays.",
        source="test_monitor.py::test_silence_does_not_clear_a_conflict_before_the_aircraft_are_stale",
    )
    seq(
        "silent-aircraft-cleared-stale-by-tick",
        {"stale_after_s": 15.0},
        head_on + [{"t_s": 10.0, "op": "tick"}, {"t_s": 16.0, "op": "tick"}],
        "The end of a condition can be the ABSENCE of telemetry: a tick with no message must drop stale aircraft (>15 s) and clear with reason stale, not resolved.",
        source="test_monitor.py::test_an_aircraft_that_goes_silent_is_dropped_and_its_alerts_cleared",
    )
    seq(
        "on-the-ground-raises-nothing",
        {},
        [obs(0.0, id="A", north_m=0, vn=10, flying=False), obs(0.0, id="B", north_m=20, vn=-10, flying=False)],
        "Disarmed aircraft at a base are routinely metres apart; alerting would teach operators to ignore alerts.",
        source="test_monitor.py::test_a_pair_on_the_ground_raises_nothing",
    )
    seq(
        "disarming-clears-as-landed",
        {},
        head_on + [obs(1.0, id="B", north_m=500, flying=False)],
        "C-14: a disarmed aircraft leaves the picture and its conflict clears with reason landed. utm cleared it as stale (S-25), which reads as 'we lost it' when the aircraft is known to be on the ground.",
        source="test_monitor.py::test_disarming_clears_the_conflict",
    )
    landed = cases[-1]
    old_cleared = landed["expected"]["per_step"][2]["cleared"]
    assert [c["reason"] for c in old_cleared] == ["stale"], old_cleared
    new_cleared = [{**c, "reason": "landed"} for c in old_cleared]
    landed["expected"]["per_step"][2]["cleared"] = new_cleared
    landed["decision"] = decided(
        file,
        landed["name"],
        old_cleared,
        new_cleared,
        "A disarm or landing clears as landed (LESSONS C-14, PLAN section 11 gap 4, "
        "uspace-core PR #15, owner decision). utm cleared it as stale.",
    )
    steps = []
    for t in range(30):
        noise = 0.01 if t % 2 else -0.01
        steps += [obs(float(t), id="A", north_m=0, vn=noise), obs(float(t), id="B", north_m=29.9, vn=-noise)]
    seq("hovering-inside-minima-stays-alerted", {"clear_after_s": 3.0}, steps, "SITL 2026-09-29: 29.9 m apart hovering with velocity noise. Raised once and never cleared as resolved while they stay.", source="test_monitor.py::test_a_hovering_pair_inside_the_minimum_stays_alerted")
    steps = []
    for t in range(10):
        steps += [obs(float(t), id="A", north_m=0, vn=0.01), obs(float(t), id="B", north_m=80, vn=-0.01)]
    seq("hovering-outside-minima-raises-nothing", {}, steps, "The absence pair at 80 m.", source="test_monitor.py::test_a_hovering_pair_outside_the_minimum_raises_nothing")
    seq(
        "diverging-clears-resolved-once-past-minimum",
        {"clear_after_s": 3.0},
        [obs(0.0, id="A", north_m=0)] + [obs(float(t), id="B", north_m=40 + 5 * t, vn=5) for t in range(9)],
        "Opening at 5 m/s from 40 m: inside until t=3 (55 m), shown clear from t=4 (60 m), cleared at t=7 (more than 3 s after last true).",
        source="test_monitor.py::test_a_diverging_pair_clears_resolved_only_once_past_the_minimum",
    )
    seq(
        "silent-neighbour-ends-stale-never-resolved",
        {"stale_after_s": 15.0, "clear_after_s": 3.0, "neighbour_max_age_s": 10.0},
        head_on + [obs(float(t), id="A", north_m=10 * t, vn=10) for t in range(1, 17)],
        "B goes silent, A keeps reporting (and A flies through B's last position). Once B's sample is more than 10 s old the pair cannot be judged: neither refreshed nor shown clear. It ends when B is dropped (t=16) with reason stale. Without this rule A's own messages 'resolved' a conflict with an aircraft nobody could see.",
        source="test_monitor.py::test_a_silent_neighbours_conflict_ends_stale_never_resolved",
    )
    steps = list(head_on)
    for t in (1, 2, 3, 4, 5):
        steps += [obs(float(t), id="A", north_m=10 * t, vn=10), obs(float(t), id="B", north_m=500 + 10 * t, vn=10)]
    seq("reporting-neighbour-can-resolve", {"clear_after_s": 3.0}, steps, "The presence pair: B reports, diverging, and it resolves.", source="test_monitor.py::test_a_neighbour_that_keeps_reporting_can_still_resolve")
    seq(
        "evidence-outranks-stale",
        {"stale_after_s": 3.0, "clear_after_s": 3.0},
        head_on + [obs(1.0, id="B", north_m=510, vn=10), obs(3.5, id="B", north_m=540, vn=10)],
        "When an alert is both shown false for longer than the hysteresis and one aircraft just went stale, the reason is resolved: evidence outranks silence.",
        source="test_monitor.py::test_evidence_of_resolution_outranks_going_stale",
    )
    seq(
        "backlog-raises-nothing-then-live-alerts",
        {},
        [
            obs(0.0, id="A", north_m=0, vn=10, backlog=True),
            obs(0.0, id="B", north_m=500, vn=-10, backlog=True),
            obs(1.0, id="A", north_m=10, vn=10),
            obs(1.0, id="B", north_m=490, vn=-10),
        ],
        "Records the ingest flagged as backlog (queued before the session that delivered them, or delivered while draining) are history: counted, recorded, never alerted. Live records alert again.",
        source="test_monitor.py::test_a_relay_backlog_raises_nothing_and_live_records_alert_again",
    )
    seq(
        "late-delivery-not-evaluated",
        {"live_max_age_s": 10.0},
        [obs(20.0, id="A", north_m=0, vn=10, captured_at_s=5.0), obs(20.0, id="B", north_m=500, vn=-10, captured_at_s=5.0)],
        "live_max_age_s applies to wall - rx_ts (ingest-to-monitor leg only): 15 s late is rejected as late, counted, not alerted.",
        source="test_monitor.py::test_the_live_window_applies_to_the_gateway_to_monitor_leg_only",
    )
    seq(
        "station-clock-skew-costs-no-alert",
        {},
        [obs(100.0, id="A", north_m=0, vn=10, station="gs-1", station_clock_offset_s=-3600.0), obs(100.0, id="B", north_m=500, vn=-10, station="gs-2", station_clock_offset_s=86400.0)],
        "A's station clock is an hour behind, B's a day ahead. Placement uses the ingest's clock (captured_at derived from rx_ts), so a station clock wrong by any amount still raises. (The station's own `ts` is used only to order samples within one source.)",
        source="test_monitor.py::test_a_station_clock_off_by_any_amount_still_raises_the_conflict",
    )
    seq(
        "out-of-order-sample-ignored",
        {},
        [obs(1.0, id="A", north_m=0, vn=10), obs(2.0, id="A", north_m=-10, vn=10, captured_at_s=0.5)],
        "Within one source, a sample older on that station's clock than the one held, and not received later, is out of order: counted and ignored. Samples from different sources are never ordered against each other.",
        source="test_monitor.py::test_a_sample_older_than_the_one_held_is_ignored",
    )
    seq(
        "source-disabled-clears-with-its-own-reason",
        {},
        [
            obs(0.0, id="A", north_m=0, vn=10),
            obs(0.0, id="B", north_m=500, vn=-10, source="remote_id", station="rx-1"),
            {"t_s": 1.0, "op": "switch_source", "source_type": "remote_id", "instance_id": None, "enabled": False},
            obs(2.0, id="B", north_m=480, vn=-10, source="remote_id", station="rx-1"),
            {"t_s": 3.0, "op": "switch_source", "source_type": "remote_id", "instance_id": None, "enabled": True},
            obs(3.0, id="B", north_m=470, vn=-10, source="remote_id", station="rx-1"),
        ],
        "U-15: switching Remote ID off drops its aircraft at once and clears with reason source_disabled (neither resolved: nothing showed it false; nor stale: it was deliberately removed). While off its messages are counted, not judged. On again, the next message raises again.",
        source="test_source_control.py",
    )
    seq(
        "one-station-off-drops-only-its-aircraft",
        {},
        [
            obs(0.0, id="A", north_m=0, vn=10, station="gs-1"),
            obs(0.0, id="B", north_m=500, vn=-10, station="gs-2"),
            obs(0.0, id="C", north_m=5000, station="gs-2"),
            {"t_s": 1.0, "op": "switch_source", "source_type": "relay", "instance_id": "gs-1", "enabled": False},
        ],
        "An instance switch touches only that instance's tracks.",
        source="test_source_control.py::test_switching_one_station_off_drops_only_its_aircraft",
    )
    zone = zone_feature("Z1", "REQ_AUTHORISATION")
    seq(
        "zone-enter-and-leave",
        {"zones": [zone], "clear_after_s": 3.0},
        [obs(0.0, id="A", north_m=5000), obs(1.0, id="A", north_m=0), obs(2.0, id="A", north_m=0), obs(3.0, id="A", north_m=5000), obs(5.0, id="A", north_m=5000), obs(6.5, id="A", north_m=5000)],
        "Entering raises once; leaving clears as resolved after the hysteresis (last true 2.0 s, cleared at 6.5 s, not 5.0 s).",
        source="test_monitor.py::test_outside_the_zone_raises_nothing_and_leaving_clears",
    )
    timed = zone_feature("Z2", applicability=[{"permanent": "NO", "startDateTime": "2026-10-01T10:00:00Z", "endDateTime": "2026-10-01T11:00:00Z"}])
    end_s = datetime(2026, 10, 1, 11, 0, tzinfo=UTC).timestamp()
    seq(
        "zone-stops-applying-clears",
        {"zones": [timed], "clear_after_s": 3.0},
        [obs(end_s - 1.0, id="A", north_m=0), obs(end_s + 1.0, id="A", north_m=0), obs(end_s + 5.0, id="A", north_m=0)],
        "Applicability is judged at the placed time in UTC. The window ends while the aircraft sits inside; the alert clears resolved.",
        owner=["authority", "ussp", "cisp"],
        source="test_monitor_geozones.py::test_a_zone_that_stops_applying_clears_its_alert",
    )
    seq(
        "applicability-at-placed-time-not-arrival",
        {"zones": [timed], "live_max_age_s": 10.0},
        [{"t_s": end_s + 4.0, "op": "observe", "aircraft": {"id": "A", "north_m": 0, "captured_at_s": end_s - 1.0}}],
        "Captured inside the window, judged 5 s later after it ended: the aircraft WAS in the zone while it applied.",
        owner=["authority", "ussp", "cisp"],
        source="test_monitor_geozones.py::test_applicability_is_judged_at_the_placed_time_not_the_arrival",
    )
    band = zone_feature("Z3", lower=(400, "AMSL"), upper=(600, "AMSL"))
    seq(
        "severity-change-is-raised-again",
        {"zones": [band]},
        [obs(0.0, id="A", north_m=0, alt_amsl_m=500.0, alt_source="pressure"), obs(1.0, id="A", north_m=0, alt_amsl_m=700.0, alt_source="pressure"), obs(2.0, id="A", north_m=0, alt_amsl_m=500.0, alt_source="pressure")],
        "S-33: on pressure altitude, inside the band is critical, inside only the band widened by 250 m is warning. A severity change is raised again under the same key, never changed silently in place.",
        source="test_pressure_altitude.py::test_a_severity_change_is_raised_again_not_changed_in_place",
    )
    ident_unknown = {"status": "unidentified", "reason": "no_serial", "serial": None, "operator_reg": None, "mismatch": False}
    ident_mismatch = {"status": "unknown_operator", "reason": "operator_mismatch", "serial": "S1", "operator_reg": "GEOX", "mismatch": True, "registered_operator_reg": "GEOY"}
    seq(
        "unidentified-in-prohibited-zone-raises-identification",
        {"zones": [zone_feature("Z4")], "clear_after_s": 3.0},
        [
            obs(0.0, id="A", north_m=0, source="remote_id", identification=ident_unknown),
            obs(1.0, id="A", north_m=5000, source="remote_id", identification=ident_unknown),
            obs(4.5, id="A", north_m=5000, source="remote_id", identification=ident_unknown),
        ],
        "U-02: an unidentified or unknown_operator aircraft inside PROHIBITED or REQ_AUTHORISATION raises 'identification' beside the zone alert: the incident seam. Both clear together.",
        source="airspace/tests/test_identification.py",
    )
    seq(
        "mismatch-raised-on-the-ground-too",
        {},
        [obs(0.0, id="A", north_m=0, source="remote_id", flying=False, identification=ident_mismatch)],
        "identification_mismatch is about who, not where: judged on every live message, on the ground and without an altitude too.",
        source="airspace/tests/test_identification.py::test_a_mismatch_is_judged_without_a_track",
    )
    seq(
        "backlog-raises-no-mismatch",
        {},
        [obs(0.0, id="A", north_m=0, source="remote_id", backlog=True, identification=ident_mismatch)],
        "History raises no identity alert either.",
        source="airspace/tests/test_identification.py::test_a_backlog_message_raises_no_mismatch",
    )
    seq(
        "unidentified-and-serial-of-same-transmitter-never-pair",
        {},
        [
            obs(0.0, id="A", north_m=0, vn=10, source="remote_id", transmitter="TX-1", identified=False),
            obs(0.0, id="B", north_m=5, vn=10, source="remote_id", transmitter="TX-1", identified=True),
        ],
        "S-32: one radio under two ids (unidentified, then its serial) must not conflict with itself.",
        source="test_monitor.py::test_an_unidentified_track_and_the_serial_of_its_transmitter_are_one",
    )
    seq(
        "two-serials-on-one-transmitter-do-pair",
        {},
        [
            obs(0.0, id="A", north_m=0, vn=10, source="remote_id", transmitter="TX-1", identified=True),
            obs(0.0, id="B", north_m=5, vn=10, source="remote_id", transmitter="TX-1", identified=True),
        ],
        "Two identified tracks on one address are two claims (a spoofer on its victim's address among them): judged like any pair.",
        source="test_monitor.py::test_two_serials_on_one_transmitter_address_are_a_conflict",
    )
    # --- decided in uspace-core PR #15 (reviews of the alert state machine) --
    t06 = (
        "A sample placed before the aircraft's latest one, from any source, is "
        "refused (rejected_older_than_held) and judges nothing (LESSONS T-06, "
        "T-13; uspace-core PR #15 review). utm took it from another station and "
        "rewound the track."
    )
    seq(
        "older-placement-from-another-station-is-refused",
        {},
        head_on
        + [
            obs(1.0, id="B", north_m=490, vn=-10, station="gs-2"),
            {"t_s": 2.0, "op": "observe", "aircraft": {"id": "B", "north_m": 5000, "station": "gs-9", "captured_at_s": -5.0, "rx_at_s": 2.0}},
            {"t_s": 14.0, "op": "tick"},
        ],
        "T-13: B, held at 1 s, is heard through another station placed at -5 s, far away. utm took it and rewound B's track to -5 s, so the tick at 14 s dropped B as stale and cleared the conflict, though B was heard at 1 s. Refused and counted; the conflict stands.",
        refused={3},
        counters={"rejected_older_than_held": 1},
        decision=t06,
    )
    seq(
        "placement-ahead-of-tolerance-is-refused",
        {},
        head_on
        + [
            {"t_s": 1.0, "op": "observe", "aircraft": {"id": "B", "north_m": 5000, "captured_at_s": 6.0, "rx_at_s": 1.0, "station": "gs-2"}},
            obs(2.0, id="B", north_m=480, vn=-10, station="gs-2"),
        ],
        "T-13: a clear sample placed 5 s ahead of its own receipt (more than ahead_tolerance_s, 1 s). utm took it: the pair was shown clear at 6 s, more than the hysteresis after the last true reading, and the conflict resolved at once; the real sample at 2 s did not raise it again. Refused and counted; the real sample at 2 s still refreshes the conflict.",
        refused={2},
        counters={"rejected_placed_ahead": 1},
        decision=(
            "A sample placed ahead of its receipt, or received ahead of the wall "
            "clock, by more than ahead_tolerance_s is refused (rejected_placed_ahead) "
            "and buys no hysteresis (LESSONS T-13; uspace-core PR #15 review). utm "
            "took it."
        ),
    )
    cap = (
        "Past max_aircraft only an aircraft without an active alert is evicted, "
        "and a new id is refused (rejected_capacity) when every aircraft held has "
        "one; eviction never clears an alert (LESSONS C-18; uspace-core PR #15 "
        "review). utm held every aircraft it heard."
    )
    seq(
        "eviction-takes-an-aircraft-without-an-alert",
        {"max_aircraft": 3},
        head_on
        + [
            obs(0.0, id="C", north_m=5000, station="gs-3"),
            obs(0.0, id="D", north_m=9000, station="gs-4"),
            obs(1.0, id="A", north_m=10, vn=10),
        ],
        "C-18: at the cap of 3, a new aircraft D evicts C, the one aircraft that holds no alert. The conflict between A and B is neither cleared nor touched, and refreshes at 1 s.",
        counters={"aircraft_evicted": 1},
        decision=cap,
        not_in_utm=True,
    )
    seq(
        "full-of-alert-holders-refuses-new-ids",
        {"max_aircraft": 2},
        head_on
        + [
            obs(0.0, id="C", north_m=5000, station="gs-3"),
            obs(0.5, id="C", north_m=5000, station="gs-3"),
            {"t_s": 1.0, "op": "tick"},
        ],
        "C-18: the cap of 2 is held by A and B, both in conflict. A new id is refused and counted, per source too; nothing is evicted, so a flood of new ids can never clear a real conflict.",
        refused={2, 3},
        counters={"rejected_capacity": 2, "rejected_capacity/relay/gs-3": 2},
        decision=cap,
        not_in_utm=True,
    )
    seq(
        "source-share-refuses-a-flooding-source",
        {"max_aircraft": 4, "max_source_share": 0.5},
        [
            obs(0.0, id="A", north_m=0, vn=10, source="remote_id", station="rx-1"),
            obs(0.0, id="B", north_m=500, vn=-10, source="remote_id", station="rx-1"),
            obs(0.0, id="C", north_m=5000, source="remote_id", station="rx-1"),
            obs(0.0, id="D", north_m=30, station="gs-1"),
        ],
        "C-18: receiver rx-1 already holds 2 aircraft with an alert, its share (0.5 of 4). A new id from it is refused (rejected_source_share), so one receiver flooding alert-raising ids cannot take the whole cap. Another source is still judged: D from a relay raises its conflict with A.",
        refused={2},
        counters={"rejected_source_share": 1, "rejected_source_share/remote_id/rx-1": 1},
        decision=(
            "A new id is refused while its source holds max_source_share of "
            "max_aircraft in alert-holding aircraft (LESSONS C-18; uspace-core "
            "PR #15 review). utm had no cap and no share."
        ),
        not_in_utm=True,
    )
    write(
        "alert_lifecycle.json",
        header(
            "Alert raise/clear sequences through the airspace monitor. Each case "
            "is a list of steps on one monitor: observe (one aircraft message "
            "at wall time t_s), tick (no message), switch_source (U-15). "
            "expected.per_step[i] lists what step i raised and cleared (with "
            "the clear reason: resolved, stale, source_disabled, landed). Aircraft "
            "fields: id, lat_deg/lon_deg (computed; north_m/east_m are how the "
            "case was built), alt_amsl_m (default 550), vn/ve/vd (m/s, vd "
            "down), flying (default true: armed for relay, airborne for Remote "
            "ID), captured_at_s (default t_s; the ingest's placement), rx_at_s "
            "(default captured_at_s), station_clock_offset_s (the station's own "
            "ts = captured_at_s + offset; default 0), backlog, source (relay | remote_id), "
            "station, alt_source, identification, transmitter, identified. "
            "An absent alt_source is geodetic: a vertical position, judged "
            "against the vertical minimum (utm read every altitude but "
            "'pressure' that way). "
            "Policy 60 s / 60 m / 20 m / 800 m; defaults clear_after_s 3, "
            "stale_after_s 15, neighbour_max_age_s 10, live_max_age_s 10, "
            "pressure_uncertainty_m 250, ahead_tolerance_s 1, max_aircraft "
            "50000, max_source_share 0.5. The source key of max_source_share "
            "is (source, station). Zones are ED-269 features. counters lists "
            "the counts the case pins; a refused sample is counted under its "
            "reason (rejected_older_than_held, rejected_placed_ahead, "
            "rejected_capacity, rejected_source_share, the last two also per "
            "source as <counter>/<source>/<station>) and judges nothing.",
            [
                "utm airspace/tests/test_monitor.py",
                "utm airspace/tests/test_monitor_geozones.py",
                "utm airspace/tests/test_source_control.py",
                "utm airspace/tests/test_identification.py",
                "utm airspace/tests/test_pressure_altitude.py",
                "utm airspace/monitor.py",
            ],
            {"t_s": "seconds, epoch, the monitor's wall clock", "alt_amsl_m": "metres", "speeds": "m/s"},
            {"times": "exact", "detail floats": "0.1 (the old monitor rounds detail to 0.1)"},
            ["authority", "ussp"],
            policy=POLICY_JSON,
            hysteresis_rule="cleared resolved when (last time shown false) - (last time shown true) > clear_after_s; a message that could not judge a pair or zone neither refreshes nor shows false",
        ),
        cases,
    )


def gen_zones_vertical() -> None:
    cases = []
    ground, n_m = 500.0, 15.0
    file = "zones_vertical.json"

    def missing(zone: dict[str, Any], ground_m: Any, undulation: float | None, max_height: float | None) -> list[str]:
        """What each limit that needs a height lacks here, in uspace-core's
        reason codes (PR #12): no_terrain, ground_unknown, no_geoid."""
        def ground_reason() -> list[str]:
            if ground_m == "absent":
                return ["no_terrain"]
            return ["ground_unknown"] if ground_m is None else []

        if not zone:
            return ground_reason() if max_height is not None else []
        volume = zone["geometry"][0]
        out: list[str] = []
        for key, ref_key, lower in (("lowerLimit", "lowerVerticalReference", True), ("upperLimit", "upperVerticalReference", False)):
            if key not in volume:
                continue
            ref = volume[ref_key]
            if ref == "AGL" and not (lower and volume[key] <= 0):
                out += ground_reason()
            elif ref == "WGS84" and undulation is None:
                out.append("no_geoid")
        return sorted(set(out), key=["no_terrain", "ground_unknown", "no_geoid"].index)

    def one(
        name: str,
        zone: dict[str, Any],
        alt: float,
        why: str,
        *,
        ground_m: Any = "absent",
        undulation: float | None = None,
        alt_source: str | None = None,
        max_height: float | None = None,
        owner: Any = None,
        source: str = "",
        conditional_severity: str | None = None,
        zone_type: str | None = None,
    ) -> None:
        config: dict[str, Any] = {"zones": [zone] if zone else []}
        if ground_m != "absent":
            config["ground_m"] = ground_m
        if undulation is not None:
            config["geoid_undulation_m"] = undulation
        if max_height is not None:
            config["max_height_agl_m"] = max_height
        if conditional_severity is not None:
            config["conditional_severity"] = conditional_severity
        spec: dict[str, Any] = {"id": "A", "north_m": 0, "alt_amsl_m": alt}
        if alt_source:
            spec["alt_source"] = alt_source
        result = run_monitor(config, [obs(0.0, **spec)])
        raised = result["per_step"][0]["raised"]
        counters = {k: result["counters"][k] for k in ("zone_checks_not_evaluated", "zone_limits_not_judged")}
        unjudged = counters["zone_checks_not_evaluated"] or counters["zone_limits_not_judged"] or (not zone and not raised)
        expected = {"raised": raised, "counters": counters, "reasons": missing(zone, ground_m, undulation, max_height) if unjudged else []}
        inp: dict[str, Any] = {
            "zone": zone or None,
            "aircraft": {"alt_amsl_m": alt, "alt_source": alt_source or "geodetic"},
            "terrain": "none" if ground_m == "absent" else ({"ground_m": ground_m} if ground_m is not None else "unknown here"),
            "geoid_undulation_m": undulation,
            "max_height_agl_m": max_height,
            "pressure_uncertainty_m": 250.0,
            "conditional_severity": conditional_severity or "warning",
        }
        extra: dict[str, Any] = {}
        if zone_type == "USPACE":
            # utm has no U-space zone type. The decided value is utm's raise
            # for the same volume as a CONDITIONAL zone, at info and without
            # an ED-269 restriction.
            inp["zone_type"] = zone_type
            as_conditional = run_monitor({**config, "zones": [{**zone, "restriction": "CONDITIONAL"}]}, [obs(0.0, **spec)])
            new_raised = [
                {**r, "severity": "info", "detail": {k: v for k, v in r["detail"].items() if k != "restriction"}}
                for r in as_conditional["per_step"][0]["raised"]
            ]
            assert new_raised, name
            extra["decision"] = decided(
                file,
                name,
                NOT_IN_UTM,
                new_raised,
                "Being in U-space airspace raises info, the lowest severity, so "
                "that it is visible; whether the flight is authorised is judged "
                "elsewhere (uspace-core PR #12, owner decision). utm had no "
                "U-space zone type.",
            )
            expected["raised"] = new_raised
        elif (
            zone
            and zone["restriction"] in ("PROHIBITED", "REQ_AUTHORISATION")
            and counters["zone_checks_not_evaluated"]
            and "no_geoid" in expected["reasons"]
        ):
            # S-37, owner decision: a WGS84 limit with no geoid is treated as
            # an AGL limit with no DEM (Z-09). The zone warns, flags
            # limit_not_judged and names every reference it could not judge.
            volume = zone["geometry"][0]
            refs = []
            for key, ref_key, lower in (("lowerLimit", "lowerVerticalReference", True), ("upperLimit", "upperVerticalReference", False)):
                if key in volume and missing({**zone, "geometry": [{key: volume[key], ref_key: volume[ref_key]}]}, ground_m, undulation, None):
                    refs.append(volume[ref_key])
            new_raised = [
                {
                    "kind": "zone",
                    "severity": "warning",
                    "aircraft": ["A"],
                    "detail": {
                        "identifier": zone["identifier"],
                        "restriction": zone["restriction"],
                        "vertical_known": False,
                        "limit_not_judged": True,
                        "not_judged": refs,
                    },
                }
            ]
            new_counters = {"zone_checks_not_evaluated": 0, "zone_limits_not_judged": 1}
            extra["decision"] = decided(
                file,
                name,
                {"raised": raised, "counters": counters},
                {"raised": new_raised, "counters": new_counters},
                "S-37, owner decision: a WGS84 limit with no geoid is treated like "
                "an AGL limit with no DEM (LESSONS Z-09). A PROHIBITED or "
                "REQ_AUTHORISATION zone warns with limit_not_judged and reason "
                "no_geoid; a limit that cannot be judged is never silent there. "
                "utm left the zone not evaluated.",
            )
            expected["raised"], expected["counters"] = new_raised, new_counters
        elif conditional_severity == "info" and any(r["severity"] == "warning" and r["detail"].get("within_band") is False for r in raised):
            new_raised = [{**r, "severity": "info"} for r in raised]
            extra["decision"] = decided(
                file,
                name,
                raised,
                new_raised,
                "A pressure altitude inside only the widened band raises "
                "min(warning, the zone's severity): an info zone stays info, "
                "since being possibly inside never raises more than being "
                "definitely inside (uspace-core PR #12, owner decision). utm "
                "raised warning.",
            )
            expected["raised"] = new_raised
        cases.append(case(name, owner or ["authority", "ussp"], inp, expected, why, source=source, **extra))

    G = "test_monitor_geozones.py::"
    for restriction in ("PROHIBITED", "REQ_AUTHORISATION", "CONDITIONAL", "NO_RESTRICTION"):
        one(f"restriction-{restriction}", zone_feature("R1", restriction), 550.0, "PROHIBITED critical, REQ_AUTHORISATION warning, CONDITIONAL warning (policy may say info), NO_RESTRICTION nothing.", source=G + "test_each_restriction_raises_its_severity")
    band = zone_feature("B1", lower=(500, "AMSL"), upper=(700, "AMSL"))
    for alt in (450.0, 600.0, 750.0):
        one(f"amsl-band-500-700-at-{int(alt)}", band, alt, "AMSL limits against the AMSL altitude.", source=G + "test_amsl_limits_are_judged_on_the_amsl_altitude")
    ceiling = zone_feature("A1", lower=(0, "AGL"), upper=(120, "AGL"))
    one("agl-ceiling-100m-above-ground", ceiling, 600.0, "Ground 500 m: 600 m AMSL is 100 m AGL, inside 0-120 AGL. Reading the AGL limit as AMSL would put it far above.", ground_m=ground, source=G + "test_agl_limits_are_judged_above_the_ground_not_above_sea_level")
    one("agl-ceiling-130m-above-ground", ceiling, 630.0, "130 m AGL is above the ceiling.", ground_m=ground)
    floor = zone_feature("A2", lower=(50, "AGL"))
    one("agl-floor-below-it", floor, 520.0, "20 m AGL is below a 50 m AGL floor.", ground_m=ground, source=G + "test_an_agl_floor_leaves_out_an_aircraft_below_it")
    one("agl-floor-above-it", floor, 560.0, "60 m AGL is above it.", ground_m=ground)
    wgs = zone_feature("W1", upper=(600, "WGS84"))
    one("wgs84-ceiling-inside", wgs, 580.0, "WGS84 = height above the ellipsoid: AMSL + N. 580 + 15 = 595 < 600.", undulation=n_m, source=G + "test_wgs84_limits_go_through_the_geoid")
    one("wgs84-ceiling-outside", wgs, 590.0, "590 + 15 = 605 > 600.", undulation=n_m)
    feet = zone_feature("F1", upper=(2000, "AMSL"), uom="FT")
    one("feet-converted-inside", feet, 600.0, "2000 ft = 609.6 m (0.3048 exactly).", source=G + "test_limits_in_feet_are_converted")
    one("feet-converted-outside", feet, 615.0, "615 m is above 609.6 m.")
    one("conditional-agl-no-terrain-not-evaluated", zone_feature("C1", "CONDITIONAL", upper=(120, "AGL")), 550.0, "A CONDITIONAL zone whose AGL limit cannot be judged is NOT evaluated: no alert, counted, an active one neither refreshed nor cleared.", source=G + "test_a_limit_without_its_data_is_not_evaluated_and_counted")
    one("conditional-agl-ground-unknown-not-evaluated", zone_feature("C1", "CONDITIONAL", upper=(120, "AGL")), 550.0, "Terrain configured but unknown here (cell never fetched, nodata, unreadable tile): the same. Unknown ground is never 0.", ground_m=None)
    one("prohibited-wgs84-no-geoid-warns", zone_feature("P1", upper=(600, "WGS84")), 550.0, "S-37: a PROHIBITED zone whose WGS84 limit cannot be judged (no geoid) warns, with vertical_known false, limit_not_judged and reason no_geoid, as for an AGL limit without the DEM. A limit that cannot be judged is never silent here.")
    one("req-authorisation-wgs84-no-geoid-warns", zone_feature("P1", "REQ_AUTHORISATION", upper=(600, "WGS84")), 550.0, "S-37: the same for REQ_AUTHORISATION.")
    one("prohibited-wgs84-with-geoid-own-severity", zone_feature("P1", upper=(600, "WGS84")), 550.0, "The presence pair: with the geoid, 550 + 15 = 565 m HAE is inside the 600 m ceiling: critical, nothing flagged.", undulation=n_m)
    one("prohibited-agl-and-wgs84-missing-warns-naming-both", zone_feature("P6", lower=(50, "AGL"), upper=(600, "WGS84")), 550.0, "S-37 with Z-09: no terrain and no geoid. The zone warns, names both references it could not judge, and reports both reasons.")
    one(
        "conditional-agl-and-wgs84-missing-both-reported",
        zone_feature("C2", "CONDITIONAL", lower=(50, "AGL"), upper=(600, "WGS84")),
        550.0,
        "An AGL limit with no terrain and a WGS84 limit with no geoid: not evaluated, and the reasons name both, so that an operator fixing one is not surprised by the other.",
    )
    one(
        "conditional-agl-ground-unknown-and-wgs84-missing-both-reported",
        zone_feature("C3", "CONDITIONAL", upper=(120, "AGL"), lower=(0, "WGS84")),
        550.0,
        "Ground configured but unknown here, and no geoid: both reasons.",
        ground_m=None,
    )
    for restriction in ("PROHIBITED", "REQ_AUTHORISATION"):
        one(f"{restriction.lower()}-agl-ceiling-no-terrain-warns", zone_feature("P2", restriction, lower=(0, "AGL"), upper=(120, "AGL")), 550.0, "U-03 review: a PROHIBITED/REQ_AUTHORISATION zone whose only unjudged limit is AGL raises a WARNING with vertical_known false and limit_not_judged: a false warning beats a missed critical.", source=G + "test_an_agl_ceiling_without_the_ground_warns_that_it_was_not_judged")
    one("prohibited-agl-with-ground-own-severity", zone_feature("P3", lower=(0, "AGL"), upper=(120, "AGL")), ground + 50, "The presence pair: with the DEM, 50 m AGL is critical, nothing flagged.", ground_m=ground, source=G + "test_the_same_agl_zone_with_the_ground_is_judged_at_its_own_severity")
    one("agl-floor-above-ground-needs-terrain", zone_feature("P4", lower=(50, "AGL")), 550.0, "An AGL floor above 0 needs the ground too.", source=G + "test_an_agl_floor_above_the_ground_also_needs_it")
    one("agl-floor-at-ground-needs-no-terrain", zone_feature("P5", lower=(0, "AGL"), upper=(700, "AMSL")), 650.0, "A lower AGL limit at or below 0 is met by any airborne aircraft: judged without terrain; the AMSL ceiling decides.", source=G + "test_an_agl_floor_at_the_ground_is_met_without_terrain")
    one("agl-floor-at-ground-amsl-ceiling-excludes", zone_feature("P5", lower=(0, "AGL"), upper=(700, "AMSL")), 750.0, "A judged limit that excludes the aircraft decides either way.")
    pz = zone_feature("PP", lower=(0, "AGL"), upper=(120, "AGL"))
    for h, why in ((100.0, "inside as indicated: the zone's own severity, flagged vertical_known false, within_band true"), (300.0, "inside only the band widened by 250 m: a warning, within_band false"), (400.0, "beyond the widened band: nothing")):
        one(f"pressure-agl-zone-at-{int(h)}m-agl", pz, ground + h, f"S-33, pressure altitude {h} m AGL against 0-120 m AGL: {why}.", ground_m=ground, alt_source="pressure", source=G + "test_a_pressure_track_against_an_agl_zone_is_widened_by_the_margin")
    one("geodetic-300m-above-agl-zone", pz, ground + 300, "The same 300 m on a geodetic altitude raises nothing.", ground_m=ground)
    one("pressure-wgs84-zone-through-geoid-and-margin", zone_feature("PW", upper=(600, "WGS84")), 700.0, "700 + 15 = 715 m HAE, 115 m above a 600 m WGS84 ceiling, inside the 250 m margin: warning, within_band false.", undulation=n_m, alt_source="pressure", source=G + "test_a_pressure_track_in_a_wgs84_zone_goes_through_the_geoid_and_the_margin")
    one("pressure-and-unjudged-agl-carry-both-flags", pz, 550.0, "Both: warning, limit_not_judged and vertical_known false.", alt_source="pressure", source=G + "test_a_pressure_track_and_an_unjudged_agl_ceiling_carry_both_flags")
    one("pressure-zone-without-limits-judged-as-for-anyone", zone_feature("PN"), 550.0, "A zone with no altitude limits is judged as for anyone: no margin, no flag.", alt_source="pressure", source="test_pressure_altitude.py::test_a_zone_without_altitude_limits_is_judged_as_for_anyone")
    cz = zone_feature("CI", "CONDITIONAL", lower=(0, "AGL"), upper=(120, "AGL"))
    one(
        "conditional-info-pressure-inside-as-indicated-stays-info",
        cz,
        ground + 100,
        "A CONDITIONAL zone the policy puts at info, pressure altitude 100 m AGL: inside as indicated, the zone's own severity (info), within_band true.",
        ground_m=ground,
        alt_source="pressure",
        conditional_severity="info",
    )
    one(
        "conditional-info-pressure-in-widened-band-stays-info",
        cz,
        ground + 300,
        "The same zone at 300 m AGL, inside only the band widened by 250 m: min(warning, info) is info. Being possibly inside never raises more than being definitely inside would.",
        ground_m=ground,
        alt_source="pressure",
        conditional_severity="info",
    )
    one(
        "uspace-zone-raises-info",
        zone_feature("U1", "NO_RESTRICTION"),
        550.0,
        "U-space airspace (2021/664) is visible at the lowest severity. Whether the flight there is authorised is judged by the authorisation check, not here.",
        zone_type="USPACE",
    )
    one(
        "uspace-zone-pressure-in-widened-band-stays-info",
        zone_feature("U2", "NO_RESTRICTION", lower=(0, "AGL"), upper=(120, "AGL")),
        ground + 300,
        "Pressure altitude inside only the widened band of a U-space zone: still info, within_band false.",
        ground_m=ground,
        alt_source="pressure",
        zone_type="USPACE",
    )
    H = "test_height_limit.py::"
    for alt in (619.0, 620.0, 620.5, 650.0):
        one(f"height-limit-120-at-{alt}", {}, alt, "P5-19: AMSL minus DEM ground (500 m) over 120 m warns; exactly at the limit is allowed (strictly greater).", ground_m=ground, max_height=120.0, source=H + "test_the_limit_itself_is_allowed")
    one("height-limit-ground-unknown-not-evaluated", {}, 5000.0, "Unknown ground: the limit is not evaluated, never against 0.", ground_m=None, max_height=120.0, source=H + "test_unknown_ground_is_not_evaluated_rather_than_taken_as_zero")
    one("height-limit-no-terrain-not-evaluated", {}, 5000.0, "No terrain configured: not evaluated.", max_height=120.0, source=H + "test_without_terrain_or_without_a_limit_nothing_is_evaluated")
    one("height-limit-pressure-judged-as-indicated", {}, 650.0, "On pressure altitude the height limit is judged on the indicated height and flagged vertical_known false.", ground_m=ground, max_height=120.0, alt_source="pressure", source="test_pressure_altitude.py::test_on_pressure_the_height_limit_is_judged_as_indicated")
    write(
        "zones_vertical.json",
        header(
            "One aircraft inside a zone horizontally (or none, for the height "
            "limit), judged vertically. Each limit is compared in its own "
            "reference and never converted with a single number: AMSL against "
            "AMSL altitude; AGL against AMSL minus the DEM ground under the "
            "aircraft; WGS84 against AMSL plus the geoid undulation. "
            "terrain: 'none' (not configured), 'unknown here', or ground_m. "
            "conditional_severity is what a CONDITIONAL zone raises (info or "
            "warning). zone_type, when present, replaces the type the "
            "restriction maps to: USPACE is U-space airspace, which ED-269 "
            "cannot express (ED-318 carries it), so the feature's restriction "
            "is a placeholder and the raise names no restriction. "
            "expected.raised is what one observation raises; counters show "
            "zone_checks_not_evaluated (silent, not judged) and "
            "zone_limits_not_judged (warned because an AGL or WGS84 limit "
            "could not be judged). "
            "reasons is the set of what was missing whenever a zone was not "
            "evaluated, a limit was not judged or the height limit was not "
            "evaluated (no_terrain, ground_unknown, no_geoid), every one of "
            "them; empty otherwise. Its order is not significant.",
            [
                "utm airspace/tests/test_monitor_geozones.py",
                "utm airspace/tests/test_pressure_altitude.py",
                "utm airspace/tests/test_height_limit.py",
                "utm airspace/monitor.py _judge_vertical, _check_height",
                "utm airspace/zones.py",
            ],
            {"alt_amsl_m/ground_m/limits": "metres unless the zone says FT", "FT": "0.3048 m exactly"},
            {"detail floats": 0.1, "severity and flags": "exact"},
            ["authority", "ussp"],
        ),
        cases,
    )


# =================================================================================
# zones_applicability.json
# =================================================================================


def gen_applicability() -> None:
    cases = []

    def check(name: str, periods: list[dict[str, Any]], moments: list[str], why: str, source: str) -> None:
        parsed = parse_applicability(periods)
        for i, moment in enumerate(moments):
            cases.append(
                case(
                    f"{name}-{i + 1}",
                    ["cisp", "authority", "ussp"],
                    {"applicability": periods, "at": moment},
                    {"applies": applies(parsed, datetime.fromisoformat(moment))},
                    why,
                    source=source,
                )
            )

    E = "test_ed269.py::"
    check("permanent", [{"permanent": "YES"}], ["1999-01-01T00:00:00Z"], "Permanent applies at any time.", E + "test_permanent_applies_at_any_time")
    check(
        "date-window-with-offset-end",
        [{"permanent": "NO", "startDateTime": "2026-10-01T07:00:00Z", "endDateTime": "2026-10-01T10:00:00+02:00"}],
        ["2026-10-01T06:59:59Z", "2026-10-01T07:00:00Z", "2026-10-01T08:00:00Z", "2026-10-01T08:00:01Z"],
        "Both ends included. 10:00+02:00 is 08:00Z: offsets are converted, never read as UTC.",
        E + "test_a_date_window_applies_inside_and_not_outside",
    )
    check(
        "weekly-mon-wed-nine-to-five",
        [{"permanent": "NO", "schedule": [{"day": ["MON", "WED"], "startTime": "09:00Z", "endTime": "17:00Z"}]}],
        ["2026-10-05T12:00:00Z", "2026-10-05T17:00:00Z", "2026-10-05T17:00:01Z", "2026-10-06T12:00:00Z", "2026-10-07T09:00:00Z", "2026-10-07T08:59:59Z"],
        "2026-10-05 is a Monday. Only listed days and hours; both ends included.",
        E + "test_a_weekly_schedule_applies_only_on_its_days_and_hours",
    )
    check(
        "night-across-midnight-utc",
        [{"permanent": "NO", "schedule": [{"day": ["FRI"], "startTime": "22:00Z", "endTime": "02:00Z"}]}],
        ["2026-10-09T21:59:59Z", "2026-10-09T22:00:00Z", "2026-10-09T23:59:59Z", "2026-10-10T00:00:00Z", "2026-10-10T02:00:00Z", "2026-10-10T02:00:01Z", "2026-10-09T01:00:00Z"],
        "A period whose end is before its start runs past midnight and belongs to the day it STARTS: FRI 22:00 to SAT 02:00. Friday 01:00 belongs to Thursday's night, which is not listed.",
        E + "test_a_night_period_runs_past_midnight_utc_into_the_next_day",
    )
    check(
        "schedule-in-an-offset-uses-that-offsets-day",
        [{"permanent": "NO", "schedule": [{"day": ["MON"], "startTime": "02:00+04:00", "endTime": "03:00+04:00"}]}],
        ["2026-10-04T22:30:00Z", "2026-10-05T22:30:00Z"],
        "Monday 02:30 in Tbilisi is Sunday 22:30 UTC: the weekday is judged in the schedule's own offset.",
        E + "test_a_schedule_in_an_offset_is_judged_on_that_offsets_day",
    )
    check(
        "schedule-bounded-by-dates",
        [{"permanent": "NO", "startDateTime": "2026-10-06T00:00:00Z", "schedule": [{"day": ["ANY"], "startTime": "00:00Z", "endTime": "23:59Z"}]}],
        ["2026-10-05T12:00:00Z", "2026-10-06T12:00:00Z"],
        "Dates bound the schedule; ANY is every day.",
        E + "test_a_schedule_is_bounded_by_its_dates",
    )
    check(
        "any-of-several-periods",
        [{"permanent": "NO", "endDateTime": "2026-01-01T00:00:00Z"}, {"permanent": "NO", "startDateTime": "2027-01-01T00:00:00Z"}],
        ["2025-06-01T00:00:00Z", "2026-06-01T00:00:00Z", "2027-06-01T00:00:00Z"],
        "A zone applies when any of its periods does; open-ended periods are allowed.",
        E + "test_any_of_several_periods_applies",
    )
    check(
        "published-end-23-59-59",
        [{"permanent": "NO", "schedule": [{"day": ["ANY"], "startTime": "00:00:00.00Z", "endTime": "23:59:59.00Z"}]}],
        ["2026-10-05T23:59:59Z", "2026-10-05T23:59:59.500000Z", "2026-10-06T00:00:00Z"],
        "Published files end days at 23:59:59. The half second before midnight is NOT covered by such a period (a real gap in 'all day' schedules), midnight is.",
        "airspace/ed269.py DailyPeriod.contains",
    )
    check(
        "night-zone-weekdays-fixture-tst003",
        [{"permanent": "NO", "startDateTime": "2026-09-01T00:00:00.00Z", "endDateTime": "2026-12-31T23:59:59.00Z", "schedule": [{"day": ["MON", "TUE", "WED", "THU", "FRI"], "startTime": "22:00:00.00Z", "endTime": "02:00:00.00Z"}]}],
        ["2026-10-10T00:30:00Z", "2026-10-10T03:00:00Z", "2026-10-11T00:30:00Z", "2026-10-12T23:00:00Z"],
        "Saturday 00:30 is Friday's night (applies); Saturday 03:00 is not; Sunday 00:30 belongs to Saturday, not listed; Monday 23:00 applies.",
        "test_monitor_geozones.py::test_a_weekly_night_zone_across_midnight_utc",
    )
    write(
        "zones_applicability.json",
        header(
            "When an ED-269 zone applies (UASZoneVersion.applicability). "
            "Evaluated at the aircraft's placed time in UTC, never at a naive "
            "time (the old code raises on one). Every input here is a valid "
            "applicability; invalid ones are in ed269_parse.json.",
            ["utm airspace/tests/test_ed269.py", "utm airspace/ed269.py Period, DailyPeriod, applies()"],
            {"times": "ISO 8601 with offset"},
            {"applies": "exact"},
            ["cisp", "authority", "ussp"],
        ),
        cases,
    )


# =================================================================================
# ed269_parse.json
# =================================================================================


def zone_summary(zone: Any) -> dict[str, Any]:
    volume = zone.volume
    return {
        "identifier": zone.identifier,
        "restriction": zone.restriction.value,
        "lower_m": volume.lower_m,
        "lower_reference": volume.lower_reference.value,
        "upper_m": volume.upper_m,
        "upper_reference": volume.upper_reference.value,
        "shape": "Circle" if volume.radius_m is not None else "Polygon",
        "radius_m": volume.radius_m,
        "periods": len(zone.applicability),
    }


def gen_ed269() -> None:
    fixture_bytes = (UTM / "airspace/tests/fixtures/ed269_valid.json").read_bytes()
    fixture = json.loads(fixture_bytes)
    cases = []
    doc = parse(fixture_bytes)
    round_trip = ed269.document(doc.zones, title=doc.title, description=doc.description)
    assert round_trip == fixture
    cases.append(
        case(
            "valid-file-round-trips-unchanged",
            "cisp",
            {"document": fixture},
            {"accepted": True, "zones": [zone_summary(z) for z in doc.zones], "export": round_trip},
            "U-03 done-when: an ED-269 file round-trips unchanged. Nothing is normalised on the way in except null optional fields (read absent, written absent); numbers round-trip by value (5.0 -> 5).",
        )
    )
    listed = {"formatVersion": "1.0", "createdAt": "2026-10-01T00:00:00Z", "UASZoneList": fixture["features"]}
    with_bom = b"\xef\xbb\xbf" + json.dumps(listed).encode()
    parsed = parse(with_bom)
    cases.append(
        case(
            "list-wrapper-and-byte-order-mark",
            "cisp",
            {"document_utf8_with_bom_base64": base64.b64encode(with_bom).decode()},
            {"accepted": True, "zones": [zone_summary(z) for z in parsed.zones]},
            "Both published wrappers are read (features; UASZoneList); Luxembourg's live file starts with a UTF-8 BOM.",
        )
    )
    feature = copy.deepcopy(fixture["features"][0])
    feature["name"] = None
    feature["message"] = None
    feature["geometry"][0]["upperLimit"] = None
    feature["zoneAuthority"][0]["phone"] = None
    zone = ed269.parse_zone(feature)
    cases.append(
        case(
            "null-optional-fields-read-absent-export-absent",
            "cisp",
            {"zone": feature},
            {"accepted": True, "export": ed269.feature(zone)},
            "An optional field that is null is absent, and written back absent.",
        )
    )

    def mutate(path: list[Any], value: Any = None, delete: bool = False, append_copy: bool = False) -> dict[str, Any]:
        f = copy.deepcopy(fixture["features"][0])
        target: Any = f
        for key in path[:-1]:
            target = target[key]
        if delete:
            del target[path[-1]]
        elif append_copy:
            target[path[-1]].append(copy.deepcopy(target[path[-1]][0]))
        else:
            target[path[-1]] = value
        return f

    V, P, PER = ["geometry", 0], ["geometry", 0, "horizontalProjection"], ["applicability", 0]
    invalid = [
        ("no-identifier", mutate(["identifier"], delete=True), "identifier", "missing"),
        ("long-identifier", mutate(["identifier"], "TOOLONG1"), "identifier", "at most 7"),
        ("bad-country", mutate(["country"], "GE"), "country", "ISO 3166-1 alpha-3"),
        ("lower-case-country", mutate(["country"], "geo"), "country", "ISO 3166-1"),
        ("no-type", mutate(["type"], delete=True), "type", "missing"),
        ("bad-restriction", mutate(["restriction"], "FORBIDDEN"), "restriction", "not one"),
        ("american-spelling-req-authorization", mutate(["restriction"], "REQ_AUTHORIZATION"), "restriction", "spells it REQ_AUTHORISATION"),
        ("bad-reason", mutate(["reason"], ["WEATHER"]), "reason[0]", "not one of"),
        ("repeated-reason", mutate(["reason"], ["NOISE", "NOISE"]), "reason[1]", "twice"),
        ("long-message", mutate(["message"], "x" * 201), "message", "at most 200"),
        ("unknown-field", mutate(["colour"], "red"), "colour", "unknown field"),
        ("no-authority-list", mutate(["zoneAuthority"], delete=True), "zoneAuthority", "missing"),
        ("bad-purpose", mutate(["zoneAuthority", 0, "purpose"], "AUTHORISATION"), "zoneAuthority[0].purpose", "not one of"),
        ("unknown-authority-field", mutate(["zoneAuthority", 0, "fax"], "1"), "zoneAuthority[0].fax", "unknown field"),
        ("no-applicability", mutate(["applicability"], delete=True), "applicability", "missing"),
        ("empty-applicability", mutate(["applicability"], []), "applicability", "at least"),
        ("permanent-with-an-end", mutate([*PER, "endDateTime"], "2027-01-01T00:00:00Z"), "applicability[0].endDateTime", "permanent YES"),
        ("bad-permanent", mutate([*PER, "permanent"], "Yes"), "applicability[0].permanent", "not one of"),
        ("not-permanent-and-never", mutate(PER, {"permanent": "NO"}), "applicability[0]", "to say when"),
        ("naive-date", mutate(PER, {"permanent": "NO", "startDateTime": "2026-01-01T00:00:00"}), "applicability[0].startDateTime", "no offset"),
        ("end-before-start", mutate(PER, {"permanent": "NO", "startDateTime": "2026-02-01T00:00:00Z", "endDateTime": "2026-01-01T00:00:00Z"}), "applicability[0].endDateTime", "not after"),
        ("schedule-without-offset", mutate(PER, {"permanent": "NO", "schedule": [{"day": ["MON"], "startTime": "08:00", "endTime": "09:00"}]}), "applicability[0].schedule[0].startTime", "with an offset"),
        ("schedule-with-two-offsets", mutate(PER, {"permanent": "NO", "schedule": [{"day": ["MON"], "startTime": "08:00Z", "endTime": "09:00+04:00"}]}), "applicability[0].schedule[0].endTime", "different offset"),
        ("schedule-start-equals-end", mutate(PER, {"permanent": "NO", "schedule": [{"day": ["MON"], "startTime": "08:00Z", "endTime": "08:00Z"}]}), "applicability[0].schedule[0].endTime", "same as startTime"),
        ("bad-day", mutate(PER, {"permanent": "NO", "schedule": [{"day": ["MONDAY"], "startTime": "08:00Z", "endTime": "09:00Z"}]}), "applicability[0].schedule[0].day[0]", "not one of"),
        ("no-geometry", mutate(["geometry"], delete=True), "geometry", "missing"),
        ("two-volumes", mutate(["geometry"], append_copy=True), "geometry", "one volume per zone"),
        ("bad-unit", mutate([*V, "uomDimensions"], "KM"), "uomDimensions", "not one"),
        ("bad-reference", mutate([*V, "upperVerticalReference"], "FL"), "upperVerticalReference", "not one of"),
        ("limit-as-a-string", mutate([*V, "lowerLimit"], "0"), "geometry[0].lowerLimit", "must be a number"),
        ("upper-not-above-lower", mutate([*V, "upperLimit"], 0), "geometry[0].upperLimit", "not above"),
        ("unclosed-ring", mutate([*P, "coordinates"], [[[44.8, 41.7], [44.82, 41.7], [44.82, 41.72], [44.8, 41.72]]]), "horizontalProjection.coordinates[0]", "not closed"),
        ("latitude-out-of-range", mutate([*P, "coordinates"], [[[44.8, 91.7], [44.82, 41.7], [44.82, 41.72], [44.8, 91.7]]]), "horizontalProjection.coordinates[0][0]", "outside"),
        ("ring-of-three-positions", mutate([*P, "coordinates"], [[[44.8, 41.7], [44.82, 41.7], [44.8, 41.7]]]), "horizontalProjection.coordinates[0]", "at least four"),
        ("unknown-shape", mutate([*P, "type"], "Ellipse"), "horizontalProjection.type", "not Polygon or Circle"),
        ("circle-without-radius", mutate(P, {"type": "Circle", "center": [44.8, 41.7]}), "horizontalProjection.radius", "above 0"),
        ("circle-with-coordinates", mutate(P, {"type": "Circle", "center": [44.8, 41.7], "radius": 5, "coordinates": []}), "horizontalProjection.coordinates", "not a field of a Circle"),
        ("bad-region", mutate(["region"], "7"), "region", "must be an integer"),
        ("bad-exemption", mutate(["regulationExemption"], "MAYBE"), "regulationExemption", "not one of"),
    ]
    for name, feature, field, reason in invalid:
        try:
            parse(json.dumps({"features": [feature]}).encode())
        except Ed269Error as refused:
            problems = [p.as_dict() for p in refused.problems]
        else:
            raise AssertionError(f"{name} was accepted")
        assert any(p["field"].endswith(field) and reason in p["reason"] for p in problems), (name, problems)
        cases.append(
            case(
                f"refuse-{name}",
                "cisp",
                {"document": {"features": [feature]}},
                {"accepted": False, "problems": problems, "must_include": {"field_endswith": field, "reason_contains": reason}},
                "The base feature (TST001) with one mutation is refused, naming the field path and the reason. Every problem path starts with features[0], so a report on a long file can be followed.",
            )
        )
    # Decided in uspace-core PR #7 (owner): `type` is ED-269's enumeration,
    # COMMON or CUSTOMIZED. utm accepted any string.
    name = "refuse-type-not-common-or-customized"
    feature = mutate(["type"], "STANDARD")
    utm_doc = parse(json.dumps({"features": [feature]}).encode())
    problems = [{"field": "features[0].type", "reason": "'STANDARD' is not one of COMMON, CUSTOMIZED"}]
    cases.append(
        case(
            name,
            "cisp",
            {"document": {"features": [feature]}},
            {"accepted": False, "problems": problems, "must_include": {"field_endswith": "type", "reason_contains": "not one of"}},
            "ED-269's zone type is an enumeration (COMMON, CUSTOMIZED). A reader that accepts any string publishes a zone other readers refuse.",
            decision=decided(
                "ed269_parse.json",
                name,
                {"accepted": True, "zones": [zone_summary(z) for z in utm_doc.zones]},
                {"accepted": False, "problems": problems},
                "type is refused unless COMMON or CUSTOMIZED (uspace-core PR #7, "
                "owner decision). utm accepted any string; the problem text is "
                "uspace-core's, and only must_include binds.",
            ),
        )
    )
    customized = mutate(["type"], "CUSTOMIZED")
    cases.append(
        case(
            "type-customized-is-accepted",
            "cisp",
            {"document": {"features": [customized]}},
            {"accepted": True, "zones": [zone_summary(z) for z in parse(json.dumps({"features": [customized]}).encode()).zones]},
            "The presence pair: CUSTOMIZED, spelt with a Z as ED-269 publishes it, is the other accepted type.",
        )
    )
    cases.append(
        case(
            "base-feature-is-accepted",
            "cisp",
            {"document": {"features": [fixture["features"][0]]}},
            {"accepted": True, "zones": [zone_summary(parse(json.dumps({"features": [fixture["features"][0]]}).encode()).zones[0])]},
            "The presence pair of every refusal above.",
        )
    )
    for name, data, field, reason in (
        ("not-json", b"{not json", "$", "not JSON"),
        ("not-utf8", b"\xff\xfe", "$", "not UTF-8"),
        ("not-an-object", b"[]", "$", "not a JSON object"),
        ("no-features", b'{"zones": []}', "features", "missing"),
        ("features-not-a-list", b'{"features": {}}', "features", "list of zones"),
        ("unknown-document-field", b'{"features": [], "author": "x"}', "author", "unknown field"),
        ("nested-too-deeply", b"[" * 100000 + b"]" * 100000, "$", "nested too deeply"),
    ):
        try:
            parse(data)
        except Ed269Error as refused:
            problems = [p.as_dict() for p in refused.problems]
        else:
            raise AssertionError(name)
        assert any(p["field"] == field and reason in p["reason"] for p in problems), (name, problems)
        inp = {"bytes_base64": base64.b64encode(data).decode()} if len(data) < 1000 else {"bytes_description": "100000 '[' then 100000 ']'"}
        cases.append(
            case(
                f"refuse-document-{name}",
                "cisp",
                inp,
                {"accepted": False, "problems": problems, "must_include": {"field": field, "reason_contains": reason}},
                "A document-level refusal with a named reason. Deep nesting must be refused, not crash the reader (a stack-exhaustion attack).",
            )
        )
    f0 = fixture["features"][0]
    try:
        parse(json.dumps({"features": [f0, f0]}).encode())
    except Ed269Error as refused:
        problems = [p.as_dict() for p in refused.problems]
    cases.append(
        case(
            "refuse-repeated-identifier-names-both-places",
            "cisp",
            {"document": {"features": [f0, f0]}},
            {"accepted": False, "problems": problems},
            "A duplicate identifier names the second place and the first.",
        )
    )
    features = copy.deepcopy(fixture["features"])
    features[0]["country"] = "XX"
    features[2]["restriction"] = "NONE"
    try:
        parse(json.dumps({"features": features}).encode())
    except Ed269Error as refused:
        problems = [p.as_dict() for p in refused.problems]
    cases.append(
        case(
            "refuse-reports-every-problem-not-only-the-first",
            "cisp",
            {"document": {"features": features}},
            {"accepted": False, "problems": problems},
            "All or nothing, and every problem named: half an authority's zones looks complete and is not.",
        )
    )
    write(
        "ed269_parse.json",
        header(
            "EUROCAE ED-269 geo-zone documents read strictly (U-03). "
            "accepted true: the zones as summarised (limits in metres) and, "
            "where given, the export, which must equal the input. accepted "
            "false: every problem with its JSON path and reason; a Go "
            "implementation must at least produce must_include (path suffix "
            "and reason phrase), and should report all problems, capped at "
            "100 with a count of the rest. Field names follow InterUSS "
            "uas_standards eurocae_ed269.py, Luxembourg's live file and the "
            "Swiss BAZL profile, since EUROCAE's text is paywalled. WGS84 as a "
            "vertical reference is this project's extension.",
            ["utm airspace/tests/test_ed269.py", "utm airspace/tests/fixtures/ed269_valid.json", "utm airspace/ed269.py"],
            {"lower_m/upper_m/radius_m": "metres (FT * 0.3048)", "coordinates": "GeoJSON order [lon, lat]"},
            {"metres": 1e-9, "problems": "path and phrase as in must_include"},
            ["cisp"],
            limits={"identifier_max": 7, "name_max": 200, "message_max": 200, "reasons_max": 9, "max_ring_vertices": 5000, "max_problems": 100},
        ),
        cases,
    )


# =================================================================================
# geodesy.json
# =================================================================================


def dms(d: float, m: float, s: float) -> float:
    sign = -1 if d < 0 else 1
    return sign * (abs(d) + m / 60 + s / 3600)


def gen_geodesy() -> None:
    cases = []

    def vin(name: str, a: tuple[float, float], b: tuple[float, float], why: str, reference: float | None = None) -> None:
        value = vincenty_m(*a, *b)
        expected: dict[str, Any] = {"distance_m": value}
        if reference is not None:
            expected["published_reference_m"] = reference
            assert abs(value - reference) < 0.001
        cases.append(case(name, ["cisp", "authority", "ussp"], {"function": "vincenty_inverse", "from": list(a), "to": list(b)}, expected, why))

    vin("flinders-peak-to-buninyong", (dms(-37, 57, 3.72030), dms(144, 25, 29.52440)), (dms(-37, 39, 10.15610), dms(143, 55, 35.38390)), "Geoscience Australia's worked example (GRS80; differs from WGS-84 in the 10th digit of the flattening): 54,972.271 m.", 54_972.271)
    vin("one-degree-longitude-on-equator", (0.0, 0.0), (0.0, 1.0), "Equal to the semi-major axis arc a*pi/180; cos2_alpha = 0 on the equator must not divide by zero.", 6_378_137.0 * 3.141592653589793 / 180)
    vin("quarter-meridian", (0.0, 0.0), (90.0, 0.0), "Equator to pole on WGS-84: 10,001,965.729 m.", 10_001_965.729)
    vin("same-point", (41.7, 44.8), (41.7, 44.8), "Zero, without iterating.")
    vin("tbilisi-short-there", (41.7151, 44.8271), (41.7239, 44.8401), "Short distances are symmetric.")
    vin("tbilisi-short-back", (41.7239, 44.8401), (41.7151, 44.8271), "Short distances are symmetric.")
    lat2 = 41.7151 + 1000 / 111_053.0
    ell = vincenty_m(41.7151, 44.8271, lat2, 44.8271)
    sph = great_circle_m(41.7151, 44.8271, lat2, 44.8271)
    cases.append(
        case(
            "ellipsoid-versus-sphere-1km-north-at-tbilisi",
            ["cisp", "authority", "ussp"],
            {"function": "compare", "from": [41.7151, 44.8271], "to": [lat2, 44.8271]},
            {"vincenty_m": ell, "haversine_mean_radius_m": sph, "difference_m": ell - sph},
            "On the mean-radius sphere this 1 km is 1.15 m longer than on the ellipsoid here; the error grows with latitude and direction (up to about 0.5 %, 5 m at the edge of a 1 km circle). Circular zones must be judged on the ellipsoid, as PostGIS geography buffers draw them. (The old test docstring said ~3 m; the computed value is 1.15 m.)",
        )
    )
    # Point in circle and polygon, through the monitor's zone shapes.
    circle = zone_of(zone_feature("C", circle=(41.71, 44.81, 500)), 0)
    feet = zone_of(zone_feature("CF", circle=(41.71, 44.81, 1000), uom="FT"), 1)
    for name, zone, lat, why in (
        ("circle-500m-inside-at-499m", circle, 41.71 + 499.0 / 111_195.0, "Judged by geodesic distance from the centre, not against the inscribed polygon a database stores for drawing."),
        ("circle-500m-outside-at-501m", circle, 41.71 + 501.0 / 111_195.0, "Just outside."),
        ("circle-1000ft-outside-at-320m", feet, 41.71 + 320.0 / 111_195.0, "Radius in feet: 304.8 m."),
        ("circle-1000ft-inside-at-300m", feet, 41.71 + 300.0 / 111_195.0, "Inside 304.8 m."),
    ):
        cases.append(
            case(
                name,
                ["cisp", "authority", "ussp"],
                {"function": "in_circle", "center": [41.71, 44.81], "radius": zone.circle.radius_m, "radius_unit": "m", "point": [lat, 44.81]},
                {"inside": zone.contains_horizontally(lat, 44.81), "distance_m": vincenty_m(lat, 44.81, 41.71, 44.81)},
                why,
            )
        )
    hole = [[44.805, 41.705], [44.815, 41.705], [44.815, 41.715], [44.805, 41.715], [44.805, 41.705]]
    sq = [[44.8, 41.7], [44.82, 41.7], [44.82, 41.72], [44.8, 41.72], [44.8, 41.7]]
    feature = zone_feature("H")
    feature["geometry"][0]["horizontalProjection"] = {"type": "Polygon", "coordinates": [sq, hole]}
    holed = zone_of(feature, 2)
    for name, point, why in (
        ("polygon-hole-is-outside", (41.71, 44.81), "Interior rings (holes) are honoured."),
        ("polygon-between-hole-and-edge-is-inside", (41.702, 44.802), "Inside the exterior, outside the hole."),
        ("polygon-outside-north", (41.73, 44.81), "Outside."),
    ):
        cases.append(
            case(
                name,
                ["cisp", "authority", "ussp"],
                {"function": "in_polygon", "rings_lon_lat": [sq, hole], "point": list(point)},
                {"inside": holed.contains_horizontally(*point)},
                why + " Ray casting on lon/lat with straight edges in degrees: metres of error for zones kilometres across.",
            )
        )
    north_m, east_m = local_offset_m(41.7151, 44.8271, 41.7251, 44.8271)
    cases.append(
        case(
            "tangent-plane-0.01-deg-latitude",
            ["ussp", "authority"],
            {"function": "local_offset_m", "origin": [41.7151, 44.8271], "point": [41.7251, 44.8271]},
            {"north_m": north_m, "east_m": east_m},
            "The CPA projection: 0.01 deg of latitude at 41.7 N is 1,110.7 m with the WGS-84 meridional radius at the origin.",
        )
    )
    north_m, east_m = local_offset_m(0.0, 179.999, 0.0, -179.999)
    cases.append(
        case(
            "tangent-plane-across-antimeridian",
            ["ussp", "authority"],
            {"function": "local_offset_m", "origin": [0.0, 179.999], "point": [0.0, -179.999]},
            {"north_m": north_m, "east_m": east_m},
            "Longitude differences are wrapped into (-180, 180]: a pair straddling the antimeridian is 223 m apart, not 40,000 km.",
        )
    )
    cases.append(
        case(
            "haversine-spoof-distance",
            ["authority", "ussp"],
            {"function": "haversine_6371008.8", "from": [41.7151, 44.8271], "to": [41.7151 + 300 / 111_195.0, 44.8271]},
            {"distance_m": haversine_m((41.7151, 44.8271), (41.7151 + 300 / 111_195.0, 44.8271))},
            "The S-10 spoof distance uses a sphere: fine against a 300 m threshold, wrong for zone edges. Know which one you are using.",
        )
    )
    write(
        "geodesy.json",
        header(
            "Distances and containment on WGS-84. vincenty_inverse is the "
            "geodesic used for circular zones; published_reference_m is an "
            "independent published value the old code reproduces to 1 mm. "
            "in_circle/in_polygon are zone containment as the monitor judges it.",
            ["utm airspace/tests/test_geodesy.py", "utm airspace/tests/test_zones.py", "utm airspace/tests/test_cpa.py", "utm airspace/geodesy.py", "utm airspace/zones.py"],
            {"positions": "[lat_deg, lon_deg] except rings_lon_lat (GeoJSON [lon, lat])", "distances": "metres"},
            {"vincenty distance_m": 0.001, "other distances": 1e-6, "inside": "exact"},
            ["cisp", "authority", "ussp"],
            ellipsoid={"a_m": 6378137.0, "f": "1/298.257223563"},
        ),
        cases,
    )


# =================================================================================
# terrain_geoid.json
# =================================================================================


def gen_terrain_geoid() -> None:
    cases = []
    for lat, lon in ((41.7151, 44.8271), (-0.5, -0.5), (0.0, 0.0), (-33.9, 151.2)):
        cases.append(case(f"cell-name-{lat}-{lon}", ["authority", "ussp"], {"function": "cell_name", "lat_deg": lat, "lon_deg": lon}, {"cell": cell_name(lat, lon)}, "1x1 degree cells are named after their south-west corner (floor, not truncate): -0.5 is S01W001."))

    width, height, offset, scale = 36, 19, -100.0, 0.01
    header_text = f"P5\n# Description test grid\n# Offset {offset}\n# Scale {scale}\n{width} {height}\n65535\n".encode()
    body = b"".join((1000 + 100 * r + c).to_bytes(2, "big") for r in range(height) for c in range(width))
    grid = GeoidGrid.parse(header_text + body)
    for lat, lon, why in (
        (90.0, 0.0, "Row 0 is latitude +90."),
        (0.0, 0.0, "The equator is the middle row (odd row count)."),
        (-90.0, 0.0, "The last row is -90."),
        (0.0, 10.0, "Column 0 is longitude 0, columns run east."),
        (5.0, 5.0, "Bilinear between samples."),
        (2.5, 7.5, "Bilinear, unequal weights."),
        (0.0, 355.0, "Longitude wraps past the last column (360 = 0)."),
        (0.0, -5.0, "Negative longitude is the same as 355."),
    ):
        cases.append(
            case(
                f"geoid-synthetic-{lat}-{lon}",
                ["authority", "ussp"],
                {"function": "geoid_undulation", "grid": "synthetic", "lat_deg": lat, "lon_deg": lon},
                {"undulation_m": grid.undulation_m(lat, lon)},
                why + " Pins the GeographicLib PGM layout without the real file.",
            )
        )
    sys.path.insert(0, str(UTM / "common" / "tests"))
    from common.tests.test_geoid import GEOGRAPHICLIB_EGM96_15, GEOGRAPHICLIB_EGM2008_2_5  # noqa: E402

    for model, table in (("egm2008-2_5", GEOGRAPHICLIB_EGM2008_2_5), ("egm96-15", GEOGRAPHICLIB_EGM96_15)):
        for lat, lon, value in table:
            cases.append(
                case(
                    f"geoid-{model}-{lat}-{lon}",
                    ["authority", "ussp"],
                    {"function": "geoid_undulation", "grid": model, "lat_deg": lat, "lon_deg": lon},
                    {"undulation_m": value},
                    "Computed by GeographicLib's own Geoid class (bilinear) from the "
                    "installed grid; copied from the old test, not recomputed here. "
                    "Needs the real grid file; EGM2008 is the default (the DEM is on "
                    "it; EGM96 differs by -2.3 to +4.7 m over Georgia).",
                    computed_by="GeographicLib (external reference), not gen_vectors.py",
                )
            )

    step = 0.25
    samples = bytearray()
    for r in range(5):
        for c in range(5):
            stored = 0xFFFF if (r, c) == (1, 1) else round((400 + 10 * r + c + 500.0) / 0.2)
            samples += stored.to_bytes(2, "big")
    tile = TerrainTile.parse(
        pgm.encode(
            5,
            5,
            {"Dataset": "COP-DEM GLO-30", "Offset": "-500.0", "Scale": "0.2", "LatFirst": "42.0", "LonFirst": "44.0", "LatStep": str(step), "LonStep": str(step)},
            bytes(samples),
        )
    )
    for lat, lon, why in (
        (42.0 - 2 * step, 44.0 + 3 * step, "A sample centre gives its own value (400 + 10*row + col)."),
        (42.0 - 1.5 * step, 44.0 + 2.25 * step, "Bilinear between samples."),
        (40.9, 45.3, "Past the last sample the edge sample is used (Copernicus drops each tile's shared east/south edge)."),
        (42.2, 43.9, "Before the first sample, clamped to it."),
        (42.0 - 0.5 * step, 44.0 + 0.5 * step, "A nodata sample among the four corners: unknown (null), never 0."),
        (42.0 - 3.5 * step, 44.0 + 3.5 * step, "Away from the nodata sample: known."),
    ):
        cases.append(
            case(
                f"terrain-synthetic-{lat:.4f}-{lon:.4f}",
                ["authority", "ussp"],
                {"function": "terrain_tile_elevation", "tile": "synthetic", "lat_deg": lat, "lon_deg": lon},
                {"elevation_m": tile.elevation_m(lat, lon)},
                why,
            )
        )
    write(
        "terrain_geoid.json",
        header(
            "Ground elevation (Copernicus DEM tiles) and geoid undulation "
            "(GeographicLib PGM grids). synthetic geoid grid: 36 x 19 samples "
            "at 10 deg, Offset -100, Scale 0.01, sample(row, col) = 1000 + "
            "100*row + col, row 0 = 90N, col 0 = 0E. synthetic terrain tile: "
            "5 x 5 at 0.25 deg, first sample 42.0N 44.0E, rows south, "
            "elevation = 400 + 10*row + col, sample (1,1) nodata, stored as "
            "(m + 500) / 0.2 in uint16 big-endian, 65535 nodata. A cell absent "
            "from the index is unknown (null), a 'sea' cell is 0 m.",
            ["utm common/tests/test_geoid.py", "utm common/tests/test_terrain.py", "utm common/geoid.py", "utm common/terrain.py"],
            {"elevation_m/undulation_m": "metres (DEM orthometric on EGM2008)"},
            {"synthetic": 1e-9, "geographiclib references": 1e-9},
            ["authority", "ussp"],
        ),
        cases,
    )


# =================================================================================
# rid_receiver_auth.json
# =================================================================================


def gen_receiver_auth() -> None:
    key = bytes(range(32))
    other = bytes(range(1, 33))
    now_s = 1_790_000_000.0

    def report(**changes: Any) -> bytes:
        fields = {"receiver_id": "rx-1", "transmitter": "AA:BB:CC:00:00:01", "payload_hex": "00", "sent_at_ms": int(now_s * 1000), "nonce": "n-1", **changes}
        return json.dumps(fields).encode()

    cases = []
    body = report()
    cases.append(
        case(
            "signature-is-hmac-sha256-hex-of-report-bytes",
            "authority",
            {"key_hex": key.hex(), "report_utf8": body.decode()},
            {"datagram_utf8": sign(body, key).decode(), "signature_hex": hmac.new(key, body, hashlib.sha256).hexdigest()},
            "The signature covers the exact report bytes: no JSON canonicalisation, so an ESP32 can sign with its SDK HMAC. Datagram = report + '\\nsig=' + hex.",
        )
    )

    def check_seq(name: str, datagrams: list[tuple[bytes, float]], why: str) -> None:
        auth = ReceiverAuthenticator(keys={"rx-1": key}, max_skew_s=30.0)
        out = []
        for datagram, at in datagrams:
            try:
                auth.check(datagram, now_s=at)
                out.append({"accepted": True})
            except AuthenticationError as error:
                out.append({"accepted": False, "error": str(error)})
        cases.append(
            case(
                name,
                "authority",
                {"keys_hex": {"rx-1": key.hex()}, "max_skew_s": 30.0, "datagrams": [{"utf8": d.decode("utf-8", "replace"), "now_s": at} for d, at in datagrams]},
                {"per_datagram": out},
                why,
            )
        )

    for name, datagram, why in (
        ("accept-signed-known-receiver", sign(report(), key), "Signed, known, current, new nonce: accepted."),
        ("refuse-unsigned", report(), "With keys configured an unsigned datagram is refused (without keys the ingest may only bind to loopback)."),
        ("refuse-unknown-receiver", sign(report(receiver_id="rx-2"), key), "Unknown receiver id."),
        ("refuse-wrong-key", sign(report(), other), "Signature from another key."),
        ("refuse-tampered", sign(report(), key).replace(b'"00"', b'"01"'), "One byte of the payload changed after signing."),
        ("refuse-31s-old", sign(report(sent_at_ms=int((now_s - 31) * 1000)), key), "More than 30 s from our clock: a captured datagram replayed later. Receivers need NTP."),
        ("refuse-31s-ahead", sign(report(sent_at_ms=int((now_s + 31) * 1000)), key), "Ahead by more than the window, too."),
        ("refuse-time-not-integer", sign(report(sent_at_ms="now"), key), "sent_at_ms must be an integer (a boolean is not)."),
        ("refuse-no-nonce", sign(report(nonce=""), key), "An empty nonce."),
        ("refuse-not-an-object", sign(b"[1]", key), "The signed report must be a JSON object."),
        ("accept-29.9s-old", sign(report(sent_at_ms=int((now_s - 29.9) * 1000), nonce="e0"), key), "Just inside the window."),
        ("accept-29.9s-ahead", sign(report(sent_at_ms=int((now_s + 29.9) * 1000), nonce="e1"), key), "Just inside, ahead."),
    ):
        check_seq(name, [(datagram, now_s)], why)
    check_seq(
        "refuse-replayed-nonce-accept-new-nonce",
        [(sign(report(), key), now_s), (sign(report(), key), now_s + 1), (sign(report(nonce="n-2"), key), now_s + 1)],
        "A datagram replayed at once inside the window is refused by its nonce; a new nonce is accepted. Nonces are remembered for twice the window (either side of now).",
    )
    write(
        "rid_receiver_auth.json",
        header(
            "Authenticating Remote ID receivers (P1-15). The broadcast can never "
            "be authenticated; the receiver that says it heard it can. HMAC-SHA256 "
            "per receiver key, a +-30 s window on sent_at_ms and a per-receiver "
            "nonce. Error texts are the old ones; match on accepted.",
            ["utm gateway/tests/test_remote_id_auth.py", "utm gateway/remote_id_auth.py"],
            {"now_s": "epoch seconds on the ingest's clock", "sent_at_ms": "epoch milliseconds, receiver's clock"},
            {"accepted": "exact", "signature_hex": "exact"},
            ["authority"],
        ),
        cases,
    )


# =================================================================================
# source_control.json
# =================================================================================


def gen_source_control() -> None:
    cases = []

    def state(controls: list[tuple[str, str | None, bool]], default_deny: bool = False) -> SourceControlState:
        return SourceControlState(
            version=1,
            default_deny=default_deny,
            controls=tuple(Control(t, i, e, "test", "admin", "2026-10-01T00:00:00+00:00") for t, i, e in controls),
        )

    for name, controls, deny, query, why in (
        ("nothing-published-everything-enabled", [], False, ("remote_id", "rx-1"), "Before any state, every source is enabled: followers never fail closed."),
        ("type-off-disables-every-instance", [("remote_id", None, False), ("remote_id", "rx-1", True)], False, ("remote_id", "rx-1"), "A type switched off wins over an instance switched on."),
        ("instance-off", [("relay", "gs-1", False)], False, ("relay", "gs-1"), "One station off."),
        ("other-instance-untouched", [("relay", "gs-1", False)], False, ("relay", "gs-2"), "Another station of the same type is enabled."),
        ("default-deny-unknown-instance", [], True, ("relay", "gs-9"), "With default deny, an instance with no row is disabled; the flag travels with the state so every follower agrees."),
        ("default-deny-instance-row-on", [("relay", "gs-9", True)], True, ("relay", "gs-9"), "An explicit row overrides default deny."),
        ("type-on-row-does-not-override-instance-off", [("relay", None, True), ("relay", "gs-1", False)], False, ("relay", "gs-1"), "A type row that is ON does not re-enable an instance switched off."),
        ("whole-type-query", [("relay", "gs-1", False)], True, ("relay", None), "Asking about the whole type: only a type row decides."),
    ):
        s = state(controls, deny)
        cases.append(
            case(
                name,
                ["authority", "ussp", "ansp"],
                {"controls": [{"source_type": t, "instance_id": i, "enabled": e} for t, i, e in controls], "default_deny": deny, "query": {"source_type": query[0], "instance_id": query[1]}},
                {"enabled": s.enabled(*query), "why_disabled": s.why_disabled(*query)},
                why,
            )
        )
    write(
        "source_control.json",
        header(
            "Whether a source (type, instance) is switched on (U-15). Applied at "
            "the adapter (refuse, count) and by every consumer (drop the "
            "tracks, clear alerts as source_disabled). Followers apply a state "
            "only if its version is strictly higher within the same epoch, and "
            "take any state from a new epoch (a restored database).",
            ["utm common/sources.py SourceControlState.why_disabled", "utm airspace/tests/test_source_control.py", "utm docs/runbooks/u15-source-control.md"],
            {},
            {"all": "exact"},
            ["authority", "ussp", "ansp"],
        ),
        cases,
    )


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    gen_odid()
    gen_rid_time()
    gen_rid_identity()
    gen_pressure()
    gen_identification()
    gen_fleet_match()
    gen_cpa()
    gen_lifecycle()
    gen_zones_vertical()
    gen_applicability()
    gen_ed269()
    gen_geodesy()
    gen_terrain_geoid()
    gen_receiver_auth()
    gen_source_control()
    sys.stdout.reconfigure(encoding="utf-8")  # type: ignore[union-attr]
    print(f"{len(DECISIONS)} cases carry a decision that supersedes utm:")
    for file, name, decision in DECISIONS:
        print(f"  {file}#{name}: {decision[:100]}")


if __name__ == "__main__":
    main()

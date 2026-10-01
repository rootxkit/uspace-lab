# WP-L5: `sitl-and-simulators` (KT-4)

Branch `feat/WP-L5-sitl-and-simulators`. Milestone KT-4. Owns `sim/`,
`cmd/sim-operator`, `cmd/sim-receiver`, `cmd/sim-ansp-feed`,
`cmd/sim-adsb`, `cmd/sim-ussp`, `cmd/scenario`, `internal/` (vehicle
stream, scenario model, assertions, results), `scenarios/` (the format,
`policy/demo.yaml`, the first scenarios), `make sim`, `make scenario`,
the `sim` compose profile. Depends on WP-L2 (issuer; the DSS for
`sim-ussp`); the operator and receiver bridges target contracts fixed in
`02 F5` and `F9` and need no sibling. Consumers: INV-02 of ussp WP-10,
WP-11, WP-12, WP-13, authority WP-12, ansp WP-5/6/8 (decision record
§4.3: this WP merges before their wave 4 starts); WP-L6..WP-L9.

Long-running; start on day one. The Python half (`sim/`) and the Go
half can be two agents on two branches if they agree the
`sim/vehicle/v1` line first.

## Read first

1. `docs/PLAN.md §1` (INV-01 to INV-03, D1, D2, D6), `§4`, `§7.1` L-Q4
   to L-Q7.
2. Spec `01 §5`, `02 F4` (the stream and snapshot the ANSP feed
   simulator serves), `F5` (operator telemetry WS and batch, `backlog`,
   the failure column), `F6` (what the peer must do), `F9` (receiver
   batches, bearer + HMAC, `sent_at_ms`, nonce), `F12`, `04 §2` (trust
   `simulated` never accepted by production ingest; envelope), `04 §3`
   `telemetry/v1`, `track/manned/v1`, `rid/observation/v1` (mirrors in
   `schemas/`, or the `02` text until they land), `07` KT-4, L-M1.
3. `knowledge/scenarios.md` in full: the convention block (policy,
   monitor defaults, "every step checked against the vehicle's own
   telemetry"), SC-01..SC-22.
4. `uspace-core`: `odid.Encode`, `EncodePack`, the System message
   encoder (`odid/types.go`), `auth.SignReport`, `auth.Datagram`,
   `geoid` (HAE = AMSL + N), `timeplace`, `f3411`/`f3548` types,
   `core.TimeSource`.
5. Predecessor, reference only: `utm/sim/run_sitl.sh` (SYSIDs, ports,
   `sitl.env`, the 25 m spacing note), `utm/tools/sitl_remote_id.py`
   (the MAVLink → ODID field table: `GLOBAL_POSITION_INT`,
   `GPS_RAW_INT.alt_ellipsoid`, `SCALED_PRESSURE`, `SYSTEM_TIME`,
   `HEARTBEAT`; S-36 invalid-altitude flag), `utm/tools/fake_rid_sp.py`
   (the SP shape), `utm/tools/remote_id_sim.py`.
6. pymavlink message definitions for every field the reader emits
   (E-03: derive, never remember); ArduPilot SITL docs for
   `sim_vehicle.py` options; the `--out` ports are the only thing the
   reader connects to.
7. LESSONS INV-01, INV-02, E-01, E-02, E-03, E-05, E-08, R-16, T-01..
   T-13 (time placement at the source), B-01..B-16 (queue and backlog
   semantics the operator simulator must reproduce).

## What to build

### `sim/` (Python, receive-only)

- `sim/run_sitl.sh -n N` and `sim/stop_sitl.sh` from the predecessor,
  with the `stop_sitl.sh` success path fixed and proven (the `pgrep`
  exit-code lesson in the predecessor's `CLAUDE.md`: a teardown that
  worked must say so and exit 0). Home from `sim/sitl.env`
  (`sitl.env.example` committed, no coordinates in scripts).
- `sim/mav_reader.py --sysid K --out udp:127.0.0.1:PORT`: opens the
  SITL output port with pymavlink **as a listener only** (no
  `mav.send`, no heartbeat towards the vehicle; a test asserts the
  connection object's send path is never called, and `grep` in CI
  refuses `.send(` and `mav.` writers in `sim/`), and emits one
  `sim/vehicle/v1` NDJSON line per `GLOBAL_POSITION_INT`: `sysid`,
  `ts` (UTC from `SYSTEM_TIME` + `time_boot_ms`), `lat_deg`, `lon_deg`,
  `alt_amsl_m`, `alt_hae_m` (from `GPS_RAW_INT.alt_ellipsoid` when
  valid, else null), `alt_pressure_m` (from `SCALED_PRESSURE`, ISA),
  `height_takeoff_m`, `speed_ms`, `track_deg`, `vspeed_ms`, `armed`,
  `status` (from `HEARTBEAT`: ground / airborne / emergency), `fix_ok`,
  `alt_invalid` (S-36). Field offsets come from pymavlink's class
  definitions; a test diffs two frames that differ in one field and
  pins the result. Schema for the line in `sim/schema/vehicle-v1.json`
  (lab-internal; not part of the KT-2 aggregate).
- `sim/fly.py` (the harness, the only thing that commands SITL, and
  only SITL): arm, climb, fly a path, hover, land, from a scenario's
  `paths`; every step waits for the vehicle's own telemetry to confirm
  it (E-08). Lives in `sim/`, is never imported by a bridge, and is
  never part of any image but the lab's own.

### `cmd/sim-*` (Go)

- `sim-operator`: per aircraft an F5 client with a token from the lab
  issuer (`ussp.telemetry`, `aud` = the USSP host), `telemetry/v1` at
  1 Hz over `WS /v1/telemetry` (`POST /v1/telemetry/batch` as an
  option), `ts` = the vehicle's time, `operator_position` from the
  scenario; queue up to 10 min on disconnect and replay as
  `backlog=true` (`02 F5`); files intents (`POST /v1/intents`, the ten
  Annex IV items from the scenario) and activates them; drop rate and
  latency knobs.
- `sim-receiver`: ODID Basic ID, Location, System and Operator ID
  through `odid.Encode` (HAE = AMSL + N from the configured geoid,
  R-16; `alt_hae_m` from the vehicle when the scenario says `gps`),
  message packs for BT5 / Wi-Fi and separate messages for BT4 legacy,
  per-transmitter MAC, `rssi_dbm`, batches ≤ 1 s to `POST
  /v1/rid/observations` signed with `auth.SignReport` under a run-time
  key the scenario registers at the authority (or the authority's fake
  in CI); knobs: drop rate, latency, backlog replay, address change,
  serial change (SC-10, SC-11), unknown-time until UTC is known (R-16).
- `sim-ansp-feed`: serves `WS /v1/manned-traffic/stream?bbox=` and
  `GET /v1/manned-traffic/snapshot` from a recorded ADS-B file
  (`testdata/adsb/*.jsonl`, a public recording with its source and
  licence in `SOURCE`), frames = envelope + `track/manned/v1` body at
  1 Hz, `console/status/v1` every 2 s, verifies the caller's token
  (`ansp.traffic`); outage and stale controls (SC-15).
- `sim-adsb`: writes a readsb `aircraft.json` or an SBS stream from the
  same recording for the USSP's e-conspicuity reader (L-Q5 default).
- `sim-ussp`: the peer. F3411 SP (`GET /uss/flights?view=`, details,
  ISA creation in the lab DSS, subscriber notifications), F3548 USS
  (`GET /uss/v1/operational_intents/{id}`, `POST .../notify`,
  references with `ovn` written to the DSS for the scenario's intents),
  `uss_base_url` published through the DSS; flights from the vehicle
  stream (SITL sysids it is given); enough to be the second USSP of
  S-M4 and the onboarding candidate of L-M4. Not a product: it serves
  the standard shapes from `uspace-core/f3411` and `f3548` and does no
  judgement.

### `cmd/scenario` and the format

`scenarios/<id>.yaml`: `id`, `source` (the `scenarios.md` SC or the `07`
done-when), `owners`, `policy` (file reference), `aircraft[]` (sysid,
serial, operator registration, path, receiver or operator sources,
intent), `receivers[]`, `feeds[]`, `zones` (an ED-318 file the
authority-role client publishes), `steps[]` (what `fly.py` does and
when), `expect[]` (event kind, aircraft, window, `clear` with its
reason, `never[]`), `measure[]` (latency, counters). The runner starts
the simulators with the scenario's knobs, runs the steps, collects the
console WS frames of each system (envelope + `alert/v1`,
`violation/v1`, `track/*`, `console/status/v1`), the systems' counters
and the `events` rows the scenario names, and asserts: every expected
raise and clear observed inside its window, nothing in `never[]`,
missed-alert and false-alert counts both zero, accepted + refused +
dropped = sent per source (no silent loss), latency distribution
(`captured_at` → raise). Results: `results/<run>/<scenario>.json` with
the commits of this repo and core, every image digest, the policy
file's `policy_version`, and the observed numbers (E-05, D8). The runner
exits non-zero on any miss and prints what it saw, not what it
expected (E-04).

First scenarios (the format's proof, run against the systems' fakes or
images as they exist): SC-01 (hover inside the minima), SC-02
(head-on), SC-03 (zone entry and exit), SC-22 (missing inputs are
visible, a design check against each system's startup), and the KT-4
baseline `kt4-baseline.yaml`: N SITL aircraft seen by `sim-operator`
and `sim-receiver`, every sample counted on both sides, zero loss.

## Tests

- `sim/tests/`: the reader's field table against pymavlink definitions
  (E-03); the no-send guard; `stop_sitl.sh` success path.
- Go: each simulator against `httptest` doubles of its target contract
  (from the `schemas/` mirrors or the `02` text), both the accepted and
  the refused path (E-01); the receiver's frames decode with
  `odid.Decode` to the inputs; the operator's backlog replay keeps
  `ts` and sets `backlog`; the ANSP feed's `stale` state; the peer's
  ISA and reference writes against the lab DSS in a compose test
  (`make dss-up`).
- The runner: a scenario with a deliberately wrong expectation fails
  with the observed events listed (E-02: the failure path exists and is
  readable).

## Done when

- [ ] `make lint` (Go and `ruff`/`mypy` on `sim/`), `go test -race
  -shuffle=on ./...`, `pytest sim/tests` clean.
- [ ] `make sim N=3` on the authoring machine: three vehicles, the
  reader's lines for each, `make sim-down` says what it stopped and
  exits 0 (paste the output).
- [ ] `kt4-baseline.yaml` run: both bridges count every sample; the
  result file in the PR. KT-4 is this line.
- [ ] SC-01, SC-02, SC-03 and SC-22 written and run against whatever
  exists (fakes if no image yet), their result files attached; the
  deliberately-wrong scenario fails as described.
- [ ] `grep -rn "\.send(" sim/` is empty and CI enforces it; the PR
  states where the only send path (`fly.py` → SITL) is and that no
  bridge imports it.
- [ ] `sim-ussp` writes an ISA and an operational intent reference to
  the lab DSS and serves `/uss/flights` for a SITL aircraft.

## Commits

`feat(sim): launch N SITL vehicles and read them receive-only into sim/vehicle/v1   [WP-L5 KT-4]`,
`test(sim): pin the MAVLink field table to pymavlink and guard against any send   [WP-L5 KT-4]`,
`feat(sim): stream operator telemetry and intents to the USSP with backlog replay   [WP-L5 KT-4]`,
`feat(sim): broadcast SITL aircraft as signed ODID receiver batches   [WP-L5 KT-4]`,
`feat(sim): serve a recorded ADS-B file as the ANSP manned feed and an e-conspicuity file   [WP-L5]`,
`feat(sim): stand up a peer USSP against the lab DSS   [WP-L5]`,
`feat(scenarios): define the scenario format and run it with observed results   [WP-L5]`,
`test(scenarios): write SC-01, SC-02, SC-03, SC-22 and the KT-4 baseline   [WP-L5 KT-4]`.

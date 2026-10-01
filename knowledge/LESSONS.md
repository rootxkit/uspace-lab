# Lessons carried over from `rootxkit/utm`

The Python monitoring system (`rootxkit/utm`, last read at commit
`484cd22`) found most of what is below the hard way: independent reviews,
SITL runs and one real aircraft. This file keeps the knowledge and leaves
the code behind. Each lesson has:

- **Rule**: what a new implementation must do;
- **Why**: the failure it prevents, with the old task, PR or run that found
  it (`P1-15`, `S-32`, `PR #21`, and so on resolve in utm's `TASKS.md`);
- **Applies to**: which of the five new repositories owns it.

A few lessons (T-13, E-14, E-15, G-12, C-18, C-19) were learnt later, in
the reviews of `rootxkit/uspace-core` waves 1 to 3 (PRs #3 to #15). Their
**Why** names the core PR. Each is a mistake that a second implementation
would be just as likely to make.

| Code | Repository | Role |
|---|---|---|
| `authority` | uspace-authority | Competent authority: registry, Remote ID monitoring, identification, violations, incidents |
| `cisp` | uspace-cisp | Common Information Service: geo-zones, dynamic restrictions, ED-269/ED-318 publication |
| `ussp` | uspace-ussp | U-space service provider: operators, network identification, flight authorisation, conformance, traffic information |
| `ansp` | uspace-ansp | ANSP interface: dynamic airspace reconfiguration, manned traffic |
| `lab` | uspace-lab | Specification, integration, SITL scenarios, load tests, demo |

Test vectors for most of these rules are in `vectors/`. Behaviour that
depends on a running system is in `scenarios.md`. Lessons marked **not
carried over** applied only to the MAVLink relay or the delivery fleet,
which the rewrite drops. They are listed so that leaving them out is a
decision, not an accident.

Contents:

0. [Invariants](#0-invariants)
1. [Engineering rules for every repository](#1-engineering-rules-for-every-repository)
2. [Time and clocks](#2-time-and-clocks)
3. [Remote ID and Open Drone ID](#3-remote-id-and-open-drone-id)
4. [Identity and spoofing](#4-identity-and-spoofing)
5. [Identification and registry](#5-identification-and-registry)
6. [Geodesy and datums](#6-geodesy-and-datums)
7. [Zones and ED-269](#7-zones-and-ed-269)
8. [CPA and alerting](#8-cpa-and-alerting)
9. [Ingest reliability and backpressure](#9-ingest-reliability-and-backpressure)
10. [Not carried over](#10-not-carried-over)

---

## 0. Invariants

**INV-01. Nothing in the system commands an aircraft.**
- Rule: no service has a path that sends anything towards a vehicle.
  Authorisation, geo-awareness, conformance and alerts act on operators
  and on the authority's picture. Any send path is out of scope.
- Why: the old system's only safety guarantee was that no bug could reach
  a flight controller. Every alert path was tested end to end against
  SITL with no way to touch the aircraft (utm `CLAUDE.md` hard rule 1, Wave
  U preamble).
- Applies to: all.

**INV-02. An alert path is not done until SITL or a scenario has raised
and cleared it.**
- Rule: a unit test alone does not close an alerting task. The aircraft
  must fly the condition in and out.
- Why: several of the most important rules below were found only in SITL,
  with green unit suites: hovering inside the minima (P5-07), raise-time
  numbers going stale on the console, `TERRAIN_REPORT` reading 0.0 on the
  real aircraft (P5-00), `SITL_HOME` 155 m under the ground, and serials
  changing on a reused address (U-16). Utm `CLAUDE.md` hard rule 5.
- Applies to: authority, ussp, ansp, lab.

**INV-03. Thresholds, coordinates, altitudes and geofences are data.**
- Rule: separation minima, the height limit, zone geometry and the
  pressure margin live in configuration or the database, are audited when
  changed, and take effect without a restart. Never relax a threshold to
  make a test pass.
- Why: utm `CLAUDE.md` hard rule 3 and "What not to do". The policy reload
  is S-13.
- Applies to: authority, ussp, cisp.

---

## 1. Engineering rules for every repository

These are utm's `CLAUDE.md` testing principles, kept word for word in
spirit. They apply to every new repository, whatever the language.

**E-01. Test presence, not only absence.**
- Rule: a test that asserts something does *not* happen (no gap, no drop,
  no rejection, no send, no alert) must be paired with a test that makes
  it happen and checks the result.
- Why: this went wrong three times, each with passing tests:
  - a PARAM_VALUE offset that made the probe unable to report
    BIDIRECTIONAL;
  - a stub that rejected tokens with a WebSocket close, so the relay's
    fatal-auth path never ran;
  - a relay `gap` that shipped unexecuted behind `assert gaps == []`.

  Every refusal in the old suites is paired with the acceptance it
  differs from by one thing (`test_remote_id_auth.py`, `test_ed269.py`).
  The vectors keep those pairs: most `refuse-*` cases have an `accept-*`
  or `base-*` twin. Branch coverage is the backstop, not the rule.
- Applies to: all.

**E-02. Run the branch that says nothing is wrong.**
- Rule: deliberately exercise the success, health and graceful-degradation
  paths. Make the thing succeed and read what it says. Take the dependency
  away and watch what happens.
- Why: a test suite builds working conditions, so the code that reports
  health is the code least likely to have run. It fails quietly. Four
  instances, all with green suites:
  - `stop_sitl.sh` exited 1 silently whenever teardown worked. `pgrep`
    found nothing, and `pipefail` with `set -e` killed the script before
    the success message.
  - "An unreachable bus leaves the console serving but empty" was false.
    `nats.connect` retried for minutes, so the page hung.
  - Station `unreachable` is defined by the *absence* of `status`
    messages, but the state was evaluated only when one arrived. The
    transition could never happen.
  - `publish_station` had no caller outside its own unit test. The
    console showed nothing.

  Corollary: a cleanup that reports success while doing nothing is worse
  than a failure. A warning that is sometimes false is a warning people
  learn to ignore. Verify, then report what was verified.
- Applies to: all.

**E-03. Never write a wire-format offset from memory.**
- Rule: derive offsets from the reference implementation (structs, field
  order, or the diff of two frames that differ in one field), and pin each
  one with a test that derives it the same way. For Open Drone ID, the
  reference is opendroneid-core-c; `vectors/odid_decode.json` carries 170
  frames it encoded.
- Why: an offset that is wrong in a plausible way gives a confident,
  meaningless answer, not an error. This happened three times in one
  session:
  - `param_id` read at offset 4 instead of 8;
  - payload bounds counted back from the end of a signed MAVLink v2 frame,
    which swallowed the signature;
  - the v2 sequence byte read at offset 2, where there is a constant.

  utm's `test_remote_id_time.py` finds the ODID timestamp by diffing two
  frames rather than counting struct bytes.
- Applies to: authority, ussp, lab.

**E-04. Never report an inference as an observation.**
- Rule: read an error before interpreting it. A mangled path or a
  permission denial is not evidence about the thing being checked. When
  the data source is unavailable, say the question is unanswered. Read
  generated files back.
- Why: examples that all happened:
  - the claim that "the SITL job has never run", contradicted by four
    merged PRs;
  - Git Bash rewriting `origin/main:path`, read as "file absent";
  - a generated workflow whose line continuations had been eaten;
  - a runbook's guess that deceleration had hidden a conflict (P5-07),
    disproved by the replay.

  utm runbooks mark each claim that was read from code rather than
  observed. For example, the U-02 re-check says the split the old code
  would have caused "is read from the code, not observed".
- Applies to: all.

**E-05. An expected result is not evidence. Record which tool version
produced a measurement.**
- Rule: every measurement records the commit of the tool that made it.
  Results from a tool later found defective are void.
- Why: ADR-001. The first `roundtrip` probe could only ever print
  TELEMETRY-ONLY because of the offset bug. Its output matched what was
  expected, which is why the defect survived review.
- Applies to: lab, and anything that produces evidence.

**E-06. Measure a silent baseline before injecting.**
- Rule: before you attribute a response to your stimulus, show that the
  response does not happen without it. If the baseline is not silent,
  refuse to answer.
- Why: ADR-001 finding 2. QGC requests parameters on its own, so a reply
  to an injected request could not be told apart from background traffic.
  The probe declined to answer until the baseline was quiet.
- Applies to: lab.

**E-07. SITL can hide what the real aircraft does.**
- Rule: before you trust a source that SITL validated, check it against
  real-hardware data.
- Why:
  - P5-00: SITL's `TERRAIN_REPORT` had tiles loaded. The real aircraft
    reported `loaded=0` and 0.0 m for 11,520 reports, so it would have
    been "on the ground" for a whole flight.
  - U-16: SITL reports `alt_ellipsoid` equal to `alt`, so a bridge using
    it would place aircraft 15-23 m low.
- Applies to: lab.

**E-08. A test harness checks every step against the vehicle's own
messages.**
- Rule: a scenario step is confirmed by what the aircraft reports, not by
  what the harness believes it commanded.
- Why: P5-07's first SITL run raised nothing, correctly. The harness
  believed both aircraft were armed, but the recorded state showed them
  disarmed on the ground.
- Applies to: lab.

**E-09. Everything refused, dropped or degraded is counted and logged at
a bounded rate.**
- Rule: every reject, fallback, drop or anomaly increments a named
  counter that appears in a periodic status line. The first occurrence is
  logged, then at most one line per interval per key, with the count
  suppressed in between.
- Why: silence is indistinguishable from health. A spoofer alternating
  serials changes identity on every message and must not fill the log
  (S-32). A replayed backlog of thousands of records is one log line, not
  thousands (S-11).
- Applies to: all.

**E-10. Every bounded structure has a test that drives it past its
bound.**
- Rule: queues, caches, maps keyed by external ids, nonce sets and rate
  limiters have explicit bounds and an eviction rule. A test exceeds each
  one.
- Why: S-07 (link-loss maps, rate-limiter eviction, task semaphores),
  S-01 (a backlog read loaded the whole queue), the monitor's per-source
  state (`SOURCE_STATE_MAX`), RID's in-memory store (50,000 rows).
- Applies to: all.

**E-11. Tests restore global state.**
- Rule: tests that change process-wide state (log level, clocks,
  environment) restore it, and the suite passes in any order.
- Why: S-26. A test left a log level set, so a later test failed only when
  run after it.
- Applies to: all.

**E-12. Commit conventions.**
- Rule: use Conventional Commits, one logical change per commit, and
  never include AI attribution of any kind. Do not add a dependency
  without a one-line reason in the commit body.
- Why: utm `CLAUDE.md` "Git conventions".
- Applies to: all.

**E-13. Units and datums in every name.**
- Rule: name every quantity with its unit (`alt_m`, `dist_m`, `speed_ms`,
  `timeout_s`). Every altitude also names its datum (`alt_amsl_m`,
  `alt_hae_m`, `height_agl_m`). Convert raw wire units at the parser
  boundary only.
- Why: utm `CLAUDE.md` "Coordinate and unit conventions". See D-01 and
  D-02 for what goes wrong otherwise.
- Applies to: all.

**E-14. Shared work never runs on one caller's cancellable context.**
- Rule: work that serves every caller (a JWKS refresh, a cache fill, a
  registry reload) runs on a context of its own, with its own timeout. A
  caller whose context is already done starts nothing and spends no rate
  limit; a caller that cancels mid-way is released, and the work finishes
  for the others.
- Why: uspace-core PR #8 review. The JWKS refresh ran on the request's
  context. An unauthenticated request with an unknown `kid` and a
  cancelled context aborted the fetch, stamped the once-a-minute refresh
  limit with a failure, and kept a rotated key out for a minute; repeated
  every minute, it held key rotation down for ever, with no credential at
  all. A key-rotation denial of service.
- Applies to: all.

**E-15. A zero or invalid threshold refuses the check, never disarms it.**
- Rule: a policy value or threshold that is zero, negative, NaN or
  infinite where that makes the check meaningless is refused and named.
  The check then reports "not judged", never "clear" and never the
  permissive verdict. Counted.
- Why: uspace-core PRs #3, #9 and #10. A zero value is what an unset Go
  field holds. A spoofing guard with `live_for_s` 0 took every row as
  history and let the broadcast speak for our aircraft; with
  `spoof_distance_m` 0 any distance was a spoof. A separation minimum of
  0 makes every pair clear. `fleet_match.json#zero-*`,
  `cpa.json#zero-*-minimum-is-invalid-policy`.
- Applies to: all.

---

## 2. Time and clocks

**T-01. Place every position on one clock, the ingest's, never the
source's.**
- Rule: each record carries three times:
  - `ts`: the source's own clock;
  - `rx_ts`: when the ingest received it, on the ingest's clock;
  - `captured_at`: where the ingest places it on that same clock.

  Comparisons between aircraft use `captured_at`.
- Why: S-11. Station and provider clocks may be wrong, drifting or
  stepped (relay-v1 §9). With one clock, two aircraft from two sources are
  compared at one instant with no skew to guess. A station clock that is
  wrong by any amount costs no alert. Vectors:
  `alert_lifecycle.json#station-clock-skew-costs-no-alert` (one station an
  hour slow, the other a day fast).
- Applies to: authority, ussp, ansp.

**T-02. A batch is not one instant.**
- Rule: place each row of a batch at `rx_ts - (newest ts in batch - its
  ts)`. The source's skew then cancels within the batch, and the rows keep
  their true spacing. Clamp a spacing that is negative or longer than
  120 s, and count it.
- Why: a draining relay frame held about 8 s of capture under one `rx_ts`.
  At 15 m/s that is 117 m against a 60 m minimum (gateway README). The
  same rule places a network Remote ID response: `captured_at = rx_ts -
  (response.timestamp - state.timestamp)`, so the provider's skew cancels
  (`rid_time.json#network-*`).
- Applies to: ussp, authority, ansp.

**T-03. The source clock orders samples within one source, and nothing
else.**
- Rule: within one source, ignore and count a sample that is older on
  that source's clock than the one held and was not received later. Never
  compare two sources' clocks.
- Why: S-11 review. Two stations' clocks agree only by accident.
  `alert_lifecycle.json#out-of-order-sample-ignored`.
- Applies to: authority, ussp.

**T-04. History is the ingest's verdict and is never alerted.**
- Rule: the ingest marks a record `backlog` when it was queued before the
  session that delivered it, or arrived while the sender was draining.
  Consumers record it and never raise a live alert from it. Do not infer
  backlog from timestamps.
- Why: S-11. A replayed outage must not raise alerts about where aircraft
  were minutes ago, and timestamp guessing fails with skewed clocks.
  `alert_lifecycle.json#backlog-raises-nothing-then-live-alerts`,
  `#backlog-raises-no-mismatch`. U-17 (operators publishing to a USSP)
  will have queued senders too.
- Applies to: ussp, authority.

**T-05. The lateness bound covers only the leg you control.**
- Rule: reject as late, and count, a message whose `wall - rx_ts` exceeds
  `live_max_age_s` (10 s). Never apply the bound to `wall - ts`. An ingest
  that is behind produces late alerts placed at `rx_ts`, not no alerts.
- Why: S-11 review scenarios 1 to 3 (`test_monitor.py`).
  `alert_lifecycle.json#late-delivery-not-evaluated`.
- Applies to: authority, ussp.

**T-06. Liveness counts from capture time, capped at now plus the
timeout, and a newer state is never overwritten by an older one.**
- Rule: an expiry-based "link lost" uses the record's capture time. A
  replayed backlog never makes a lost aircraft look live. The write is a
  compare-and-set, and a clock running behind fails towards "lost".
- Why: P1-05. Measured: 14.9 s to link-lost against a 15 s TTL.
- Applies to: authority, ussp.

**T-07. Reconstruct the Remote ID hour from the receive time, allowing
for tolerance and declared accuracy.**
- Rule: a Location carries tenths of a second after the UTC hour. Take the
  latest instant at that offset that is not after `rx + tolerance (1 s) +
  declared accuracy`.
- Why: S-27. A broadcast at xx:59:59.9 heard at (xx+1):00:00.2 must land
  in hour xx, not 59 minutes in the future. A clock 0.6 s ahead across the
  hour must land in the next hour. `rid_time.json#rollover-*`.
- Applies to: authority.

**T-08. Tell a fast clock apart from an old broadcast.**
- Rule: if the reconstructed time is more than 30 minutes old, the
  broadcast was really ahead of our clock (`clock_ahead`, `ts` = claimed
  time). If it is older than `max_latency_s` (5 s) plus the declared
  accuracy, it is `too_old`. If the timestamp is 0xFFFF it is `unknown`;
  if it is 3600.0 s or more it is `invalid`. In all four cases place the
  aircraft at receipt and count the reason.
- Why: S-27. A steady `clock_ahead` count usually means the receiving
  host's own clock is behind. `rid_time.json` has every boundary,
  including exactly at the tolerance and exactly at the latency bound,
  which are inside.
- Applies to: authority.

**T-09. Judge zone applicability at the aircraft's placed time, in UTC,
with an aware time.**
- Rule: evaluate applicability windows at `captured_at`, not at arrival or
  wall time. A naive time is a programming error.
- Why: U-03. An aircraft captured inside a window that ended before the
  message arrived *was* in the zone.
  `alert_lifecycle.json#applicability-at-placed-time-not-arrival`.
- Applies to: authority, ussp, cisp.

**T-10. Silence is not evidence, but the end of a condition can be
silence.**
- Rule: an alert clears as `resolved` only when messages show the
  condition false. A periodic tick with no message must still drop stale
  aircraft (15 s) and clear their alerts as `stale`.
- Why:
  - an aircraft that lands or goes quiet sends nothing that would clear
    its alert (P5-07);
  - a silent neighbour must not be "resolved" by the other aircraft's own
    messages (B2 review).

  `alert_lifecycle.json#silent-aircraft-cleared-stale-by-tick`,
  `#silent-neighbour-ends-stale-never-resolved`.
- Applies to: authority, ussp.

**T-11. An adapter that reads buffered input late must not stamp it as
now.**
- Rule: if a process stalls and then drains OS-buffered input, the records
  are old. Place them by the producer's own time base where one exists,
  and detect producer restarts.
- Why: S-24 (open in utm). A stalled relay read datagrams 30 s late and
  stamped them with the read time, which raised a false conflict in SITL
  on 2026-10-01. The relay is dropped, but any polling or reading adapter
  can stall in the same way. See `scenarios.md` SC-15.
- Applies to: authority, ussp, ansp.

**T-12. A message without a receive time is placed at arrival and
counted, not dropped.**
- Rule: missing `rx_ts` means "place at arrival time". Missing `ts` means
  "do not order within the source". Count both and log once per aircraft.
- Why: S-11. A missed alert costs more than a position one second out.
- Applies to: authority, ussp.

**T-13. A track only moves forward, and never past now.**
- Rule: a sample placed before the aircraft's latest one, from any source
  or station, is refused and counted (T-06 says so for one source; it
  holds across sources too). A sample placed ahead of its own receipt, or
  received ahead of the wall clock, by more than a small tolerance (1 s)
  is refused and counted. Hysteresis and staleness count on the wall
  clock, capped at it, never on a placement alone.
- Why: uspace-core PR #15 review. Backward: a sample of a held aircraft
  heard through another station, placed 6 s earlier, rewound the track,
  and the next tick cleared a live conflict as stale. Forward: a clear
  sample placed 5 s ahead of its receipt resolved a conflict at once, and
  one placed far in the future pinned the track so that every real sample
  after it was "older than held". utm took both.
  `alert_lifecycle.json#older-placement-from-another-station-is-refused`,
  `#placement-ahead-of-tolerance-is-refused`.
- Applies to: authority, ussp.

---

## 3. Remote ID and Open Drone ID

**R-01. Decode once, at the boundary, and turn the standard's "unknown"
values into null there.**
- Rule: these encodings are unknown and decode to null, never to the
  number:
  - altitude -1000 m;
  - direction 361;
  - horizontal speed 255;
  - vertical speed 63;
  - timestamp 0xFFFF;
  - latitude 0 and longitude 0 *together* (latitude 0 alone is the
    equator).
- Why: P1-15. An unknown altitude is not -1000 m, and an unknown position
  is not in the Gulf of Guinea. `odid_decode.json#position-zero-zero-*`
  and the reference cases with sentinels.
- Applies to: authority, ussp.

**R-02. The decoder agrees with opendroneid-core-c on every field, and
re-encoding gives the same bytes.**
- Rule: take the layout from the reference library's packed structs:
  little-endian, bit fields least significant bit first. Speed has two
  ranges (0.25 m/s steps, and 0.75 m/s steps above 63.75 m/s). Direction
  is 0-179 plus an east/west bit. An encoder must round 359.6 to 0, not
  360.
- Why: P1-15 checked 170 frames from the reference library.
  `odid_decode.json` (`reference-*`, `direction-rounding-to-360-is-north`).
- Applies to: authority, lab (for the simulator encoder).

**R-03. Refuse a malformed message pack whole.**
- Rule: refuse the whole pack when:
  - the message size is not 25;
  - the count is outside 1-9;
  - it is shorter than its count says;
  - it contains a pack;
  - it has more than 2 Basic IDs;
  - it has more than one Location, System, Self-ID or Operator ID;
  - any message in it is shorter than 25 bytes.
- Why: P1-15. This matches the reference library. With two Locations,
  which one is the aircraft's position would be a guess.
  `odid_decode.json#refuse-*`.
- Applies to: authority.

**R-04. Skip Self-ID and Authentication. Do not misread them.**
- Rule: Self-ID is free text the operator types. Authentication needs the
  manufacturer's keys, which we do not have. Both decode to nothing, and
  so does an unknown message type.
- Why: P1-15. `odid_decode.json#skip-*`.
- Applies to: authority.

**R-05. A broadcast is never authenticated, and every display and alert
says so.**
- Rule: every direct or network Remote ID track carries
  `authenticated: false` and its trust level (`broadcast`, `provider`).
  The console says "broadcast and unverified" wherever it shows one. An
  alert involving one is about a claimed position.
- Why: P1-15, ARCHITECTURE §2. Anyone with a phone can transmit. PR #21
  (`4d9a58f`) had to fix the console to say that a `registered` status is
  "as broadcast and unverified".
- Applies to: authority, ussp.

**R-06. Authenticate the receiver, because the broadcast cannot be
authenticated.**
- Rule: each receiver has its own key of at least 32 bytes. It signs the
  exact report bytes with HMAC-SHA256 (no JSON canonicalisation, so an
  ESP32 can do it) and adds `sent_at_ms` and a unique `nonce`. Refuse:
  - an unsigned report;
  - an unknown receiver;
  - a wrong signature;
  - a report more than 30 s from our clock;
  - a nonce seen before within twice the window.

  Without keys, the ingest binds to loopback only. A key file with an
  empty or duplicate entry is a startup error.
- Why: P1-15, S-08. Without this, anyone who can reach the port can put
  an aircraft on the map and into the airspace monitor.
  `rid_receiver_auth.json`.
- Applies to: authority.

**R-07. HAE is not AMSL. Without a geoid there is no AMSL altitude, and
the aircraft is not judged vertically.**
- Rule: `alt_amsl_m = alt_hae_m - N(lat, lon)`, with EGM2008 by default
  because the DEM uses it. If no geoid is configured, leave `alt_amsl_m`
  null and log at startup that such aircraft will not be evaluated.
- Why: P1-15. The geoid is 15.9 m above the ellipsoid at Tbilisi and
  22.5 m at Batumi. A separation computed with a 16-23 m error is a
  confident wrong answer against a 20 m minimum. EGM96 differs from
  EGM2008 by -2.3 to +4.7 m over Georgia.
- Applies to: authority, ussp.

**R-08. Use the pressure altitude only to replace a missing or poor
geodetic altitude, hold it for a while, and label it.**
- Rule:
  - "Poor" means the declared vertical accuracy code is known and below 2
    (worse than 45 m). An unknown accuracy (code 0) is not a flag.
  - Once a track switches to pressure altitude, it stays on it for 10 s
    after the last poor fix.
  - Pressure replaces a poor geodetic altitude. It never replaces a
    missing geoid.
  - Every observation carries `alt_source` (`geodetic`, `pressure` or
    null) and the raw pressure altitude.
  - A stored row says its height model is "pressure altitude, ISA
    1013.25 hPa".
- Why: S-33. Without a hold, an accuracy hovering at the threshold flips
  the source, and the alerts with it, every message.
  `pressure_altitude.json`.
- Applies to: authority, ussp.

**R-09. Downstream, a pressure altitude is a vertical position of unknown
accuracy.**
- Rule:
  - Conflicts: judge on the horizontal criteria alone, treating the
    vertical minimum as not met, with `vertical_separation_known: false`
    and `d_alt` null.
  - Zones with altitude limits: inside the band as indicated keeps the
    zone's severity. Inside only the band widened by 250 m each way is a
    warning (`within_band: false`).
  - Height limit: judged on the indicated height, flagged.
  - Zones without altitude limits: judged as for any aircraft.
- Why: S-33. Pressure altitude is referenced to 1013.25 hPa, not QNH:
  about 8 m per hPa, or about 160 m on a 20 hPa day, against a 20 m
  minimum. Compared as AMSL it could hide a conflict or invent one.
  `cpa.json#pressure-*`, `zones_vertical.json#pressure-*`.
- Applies to: authority, ussp.

**R-10. Remote ID velocity is track and speed, and vertical is up.**
- Rule: convert to north-east-down with `vn = v·cos(track)`, `ve =
  v·sin(track)` and `vd = -climb`. A speed without a direction is not a
  velocity: leave all three null, not zero, because zero would claim the
  aircraft is hovering. The heading is unknown, because Remote ID gives
  only the track.
- Why: P1-15 (`test_remote_id.py`).
- Applies to: authority, ussp.

**R-11. "Airborne" means the aircraft did not declare itself on the
ground.**
- Rule: only status GROUND is not airborne. UNDECLARED, EMERGENCY and
  REMOTE_ID_SYSTEM_FAILURE count as airborne. Remote ID has no arming
  state: leave it null and never guess.
- Why: P1-15. Erring towards "flying" is the safe side for alerting.
- Applies to: authority, ussp.

**R-12. Height over take-off is not height over ground.**
- Rule: a broadcast `height` maps to "above home" only when its reference
  is over take-off. Over ground is a different quantity.
- Why: P1-15, P5-00.
- Applies to: authority, ussp.

**R-13. Publish each Location once.**
- Rule: a Location is published when it arrives, or when the identity it
  was held for arrives. A repeated Basic ID, Operator ID or System
  message never republishes an old position as new.
- Why: P1-15. `rid_identity.json#repeated-identity-alone-does-not-republish`.
- Applies to: authority.

**R-14. Network Remote ID providers are authenticated, not trusted.**
- Rule: poll as an ASTM F3411 Display Provider using OAuth 2 client
  credentials, and refuse plain HTTP except to localhost.
  - Tiles: at most 7 km across the diagonal. A 413 response is split into
    four, at most three times.
  - Per poll: response bodies over 1 MiB are refused unread; at most 500
    flights per response, 64 tiles and 20 details fetches (4 at a time);
    a 5 s deadline that keeps whatever has arrived.
  - Each limit has a counter.
  - Do not republish an unchanged state. Do not show a state older than
    60 s.
  - A flight that appears in two tiles is one flight.
- Why: U-02. A provider that is down, refusing or malformed must cost one
  counter and one log line a minute, not the poll loop.
  `docs/runbooks/u02-identification.md`.
- Applies to: ussp.

**R-15. Keep every raw frame.**
- Rule: store each observation with the raw payload, the ellipsoid height
  as broadcast, the AMSL height with the model that produced it, the
  receiver and the transmitter.
- Why: P1-15. Remote ID has no other archive. A decode bug found later
  can be checked and replayed. U-12 evidence packs need the raw frames.
- Applies to: authority.

**R-16. A simulated transmitter sends HAE as AMSL plus N, and an unknown
time until it knows UTC.**
- Rule: a SITL-to-Remote ID bridge sends the vehicle's AMSL altitude plus
  the geoid undulation as its HAE, not the GPS's `alt_ellipsoid`. It sends
  the timestamp as unknown, and no System message, until the vehicle has
  sent UTC.
- Why: U-16. SITL reports `alt_ellipsoid` equal to `alt`, which placed
  aircraft 15-23 m low.
- Applies to: lab.

**R-17. The decoder has never seen a real broadcast in the air.**
- Rule: treat the first real receiver session as a test of the decoder.
  Capture raw frames and compare the decode with a second decoder.
- Why: P1-15, still open. Every check so far was against bytes the
  reference library encoded.
- Applies to: authority, lab.

---

## 4. Identity and spoofing

**I-01. Join Basic ID and Location by transmitter address only while
the identity is fresh.**
- Rule: an identity is usable only while all of these hold:
  - its Basic ID was heard within 15 s (five 3 s static periods);
  - the address has not been silent for more than 3 s (three Location
    periods);
  - no other Basic ID of the same ID type has arrived from that address
    since. If one has, drop everything known about the address (System
    and Operator ID too) and count it as a change.

  Never carry an identity across a silence.
- Why: S-32. U-16's drop-rate run stored two Locations under the previous
  serial after a module restarted with a new serial on the same address.
  With the old 60 s rules it happened; with these rules, 22 rows were
  stored after the restart, all under the new serial, and 20 more seeds
  stored none under the old one. `rid_identity.json`.
- Applies to: authority.

**I-02. A Location with no fresh identity waits 4 s, then is shown as
unidentified. It is never attached to an earlier serial.**
- Rule: the unidentified track's id is derived from the address (the same
  across receivers), its label is the address, it has an empty UAS ID and
  ID type 0. When the serial then arrives, the unidentified track is not
  merged: it goes stale. The monitor never pairs an unidentified track
  with another track on the same address.
- Why: S-32. One radio under two ids must not conflict with itself.
  `alert_lifecycle.json#unidentified-and-serial-of-same-transmitter-never-pair`.
  Accepted cost: other checks may raise twice, once per id, until the
  unidentified id goes stale.
- Applies to: authority.

**I-03. A receiver may borrow another receiver's fresh identity for the
same address.**
- Rule: freshness is per transmitter, across receivers. The receiver's
  own identity comes first, then another receiver's, under the same TTL.
- Why: S-32. Without borrowing, receiver A hears the Basic ID, receiver B
  hears only Locations, and the map shows one aircraft twice.
  `rid_identity.json#receiver-hearing-only-locations-borrows-a-fresh-identity`.
- Applies to: authority.

**I-04. Two fresh identities on one address is an anomaly. Count it and
keep judging both.**
- Rule: count "one transmitter, two identities" and log it at most once a
  minute per address. Two identified tracks on one address are judged like
  any other pair, so a spoofer is checked against its victim.
- Why: S-32, PR #17. Possible causes are two radios on one address or a
  spoofer. `alert_lifecycle.json#two-serials-on-one-transmitter-do-pair`.
  Open in utm: S-34 (show it in the console), S-35 (record which receiver
  lent an identity).
- Applies to: authority.

**I-05. Prefer the serial, and look up only the serial.**
- Rule: when a module sends a serial and a registration, the track is the
  serial. Only ID type 1 (a CTA-2063-A serial) is matched against the
  registry or the fleet. Another ID type is `unknown_operator` with reason
  `not_a_serial`.
- Why: a serial is fixed to the airframe, but a registration or session
  id can move between airframes. Matching one would attach a stranger's
  broadcast to a registered aircraft (P1-15, U-02).
  `identification_status.json#remote-id-block-caa-registration-is-not-a-serial`.
- Applies to: authority, ussp.

**I-06. Derive the aircraft id from the identity, never from the
address.**
- Rule: the same serial always gets the same id, across receivers and
  restarts (utm: uuid5 of `<id_type>:<ua_id>` in a fixed namespace).
  Never change the namespace.
- Why: P1-15. Some transmitters randomise their address.
- Applies to: authority, ussp.

**I-07. Known limit: a takeover within the gap that also loses its
Basic ID.**
- Rule: document this, do not paper over it. A different aircraft that
  takes over an address within 3 s, while the old identity is still fresh,
  and whose own Basic ID is lost, cannot be told apart from the old
  aircraft. Its Locations join the old serial until its own Basic ID
  arrives.
- Why: P1-15 runbook "Limit".
- Applies to: authority.

**I-08. The spoofing guard: one of our serials heard away from our
aircraft is not our aircraft.**
- Rule: while the aircraft's own authenticated telemetry is live (heard
  within 5 s):
  - a broadcast of its serial within 300 m is withheld, because the
    authenticated track is better;
  - a broadcast more than 300 m away is a separate unverified track:
    `unknown_operator`, `mismatch`, reason `serial_conflict`, with an
    `identification_mismatch` alert.

  When the telemetry is quiet, the broadcast speaks for the aircraft,
  still marked as a broadcast. The same rule applies to direct and network
  Remote ID.
- Why: S-10 and U-02. An unverified broadcast must never speak for a
  registered aircraft while authenticated data contradicts it.
  `fleet_match.json`.
- Applies to: authority, ussp.

**I-09. Only live authenticated rows vouch for where an aircraft is.**
- Rule: when deciding whether a link is live and where the aircraft is,
  ignore:
  - backlog rows;
  - rows captured more than 5 s before the ingest received them;
  - any broadcast row, direct or network.
- Why: U-02 review and SITL re-check (2026-10-01). A relay draining after
  an outage delivered positions up to 438 m from where the aircraft's
  broadcast placed it. The old rule took every row as live and current,
  and would have split our own aircraft off as a spoof.
  `fleet_match.json#relay-backlog-is-history`.
- Applies to: ussp (U-17 operator publishers), authority.

---

## 5. Identification and registry

**G-01. Every track resolves to one of four statuses, with a stable
reason code.**
- Rule: the statuses are `registered`, `suspended`, `unknown_operator` and
  `unidentified`. These decisions are deliberate:
  - suspension outranks the operator ID, and a mismatch is still flagged;
  - an unknown serial flown by a known operator is `unknown_operator`
    (nothing registered is flying);
  - our own fleet (no UAS operator) is `registered` on its serial alone;
  - a track whose aircraft an authenticated binding names is resolved by
    the binding, not by anything it broadcasts;
  - an owner missing from the projection is `unknown_operator`
    (`owner_unknown`);
  - a projection row with no registry aircraft is never `registered`
    (`not_in_registry`).
- Why: U-02, `gateway/identification.py`. `identification_status.json`
  has the full table.
- Applies to: authority, ussp.

**G-02. A mismatch is never `registered`. It is about who, not where.**
- Rule: a registered serial with an operator number that is not its
  owner's is `unknown_operator` with `mismatch: true` and the owner's
  number. `identification_mismatch` (a warning, configurable) is judged on
  every live message, including on the ground and without an altitude. It
  goes stale with the aircraft's messages, not with its track. A backlog
  message raises nothing.
- Why: U-02. `alert_lifecycle.json#mismatch-raised-on-the-ground-too`.
- Applies to: authority, ussp.

**G-03. An unidentified or unknown-operator aircraft in a PROHIBITED or
REQ_AUTHORISATION zone raises `identification`.**
- Rule: raise it beside the zone alert, at critical by default
  (configurable). It is the seam where an incident opens. Both alerts
  clear together. A CONDITIONAL zone or no zone raises nothing.
- Why: U-02, waiting for U-12.
  `alert_lifecycle.json#unidentified-in-prohibited-zone-raises-identification`.
- Applies to: authority.

**G-04. Compare operator numbers on their public part, ignoring case,
and never register the secret.**
- Rule: trim a broadcast number. Strip a trailing hyphen plus three ASCII
  letters or digits (the EU secret part) only when what precedes it is a
  registration number under the configured pattern, as given or with its
  ASCII letters upper-cased. Upper-case ASCII letters before comparing
  (G-12). A registration with a hyphen is refused: only the public part
  is registered.
- Pitfall (utm): utm stripped *any* three-character alphanumeric tail, so
  `GEO-OP-ABC` compared as `GEO-OP`. uspace-core decided against that (PR
  #4): `GEO-OP` is no registration number, so nothing is stripped.
- Why: U-01 and U-02. `serials_and_registration.json#public-part-*`,
  `identification_status.json#eu-secret-suffix-stripped`.
- Applies to: authority, ussp.

**G-05. Keep a serial's case. Match case-insensitively only when that is
unambiguous.**
- Rule: store serials trimmed but with their case as given. An exact match
  wins. Otherwise accept a case-insensitive match only if exactly one
  aircraft has it.
- Why: U-01. A legacy serial is whatever the maker printed, and folding
  case could merge two aircraft.
  `identification_status.json#ambiguous-*`.
- Applies to: authority, ussp.

**G-06. CTA-2063-A is required only for the classes that must broadcast.**
- Rule: C1, C2, C3, C5 and C6 need a valid ANSI/CTA-2063-A serial: a
  4-character manufacturer code, a length character (1-9, A-F), exactly
  that many characters, from 0-9 and A-Z without O and I, at most 20 in
  all. C0, C4 and unlabelled aircraft only need a serial that is not
  empty.
- Why: 2019/945, U-01. `serials_and_registration.json`.
- Applies to: authority.

**G-07. The operator registration number format is configuration.**
- Rule: use a configurable pattern, defaulting to the EU shape
  `^[A-Z]{3}[A-Za-z0-9]{8,16}$`.
- Why: U-01. Georgia's exact format is not confirmed.
- Applies to: authority.

**G-08. A projection is never an authority. Write it with the change and
repair it regularly.**
- Rule: a reader that may not reach the registry database reads a
  projection.
  - The registry writes the projection in the same transaction as the
    change. If the projection write fails, the change is rolled back.
  - A full re-projection runs at startup and every 300 s, under an
    advisory lock held from its read to its write, so a change made
    meanwhile waits and is never overwritten by the older state.
  - A projected row with no registry entry is marked unregistered, never
    left looking registered.
  - Readers refresh every 5 s. A failed read keeps the snapshot already
    held: a database hiccup must not turn every aircraft unknown.
- Why: P2-05. A drone inserted only in the registry was refused by the
  ingest's foreign key, and the cause was invisible from either side: the
  registry looked right and the ingest looked broken.
- Applies to: authority, ussp.

**G-09. Resolve identification in each adapter, as it publishes.**
- Rule: each source adapter resolves the track against its registry
  snapshot and publishes the track once, with its status.
- Why: U-02. A separate resolver adds a hop and a single failure that
  strips identity from every source at once. The registry outgrows a KV
  bucket value (NATS default 1 MB), so it lives in a database projection.
  The source switches (U-15) are small and must act within a round trip,
  so they live in a KV bucket.
- Applies to: authority, ussp.

**G-10. Expose only what the regulation gives an operator.**
- Rule: a traffic picture served to operators or USSPs exposes position,
  identification status, trust level and age (2021/664 Art. 8, 11). It
  never exposes registry personal data. Every call is audited.
- Why: U-17.
- Applies to: ussp.

**G-11. Use the authority's register only by agreement.**
- Rule: check registrations against `uas.gov.ge` only through an access
  method the authority has agreed. Nothing is scraped.
- Why: P2-08, P11-01.
- Applies to: authority.

**G-12. Fold identifiers on ASCII letters only.**
- Rule: a case-insensitive key of a serial or a registration number
  upper-cases `a` to `z` and nothing else. Never use a Unicode case
  mapping (`strings.ToUpper`, `str.upper()`, `casefold()`) on an
  identifier that is compared, and never match a pattern against a
  Unicode-folded value.
- Why: uspace-core PR #9 review. Unicode upper-cases U+017F (long s) to
  `S` and U+0131 (dotless i) to `I`. With a Unicode fold, a broadcast of
  `ſn-fleet` matched our fleet serial `SN-FLEET`, and a look-alike
  operator number compared equal to a registered one: a spoofer needs
  only a look-alike letter. utm folded with `str.upper()`.
  `serials_and_registration.json#serial-fold-*`, `#public-part-*[long-s]*`,
  `identification_status.json#serial-lookalike-*`.
- Applies to: authority, ussp.

---

## 6. Geodesy and datums

**D-01. Judge separation in AMSL, never AGL, and never mix datums in one
calculation.**
- Rule: vertical separation compares altitudes in one datum (AMSL). AGL is
  used for one thing, the height limit over the ground.
- Why: ARCHITECTURE §6.1. Two aircraft 15 m apart in AGL over ground that
  differs by 15 m are at the same height. The error is smooth, plausible
  and silent, and it lands in the last check before an alert.
- Applies to: authority, ussp.

**D-02. Do not store a height above ground. Derive it.**
- Rule: there is no `alt_agl_m` column, not even a nullable one. Height
  above ground is computed where it is needed, as AMSL minus the DEM.
- Why: ARCHITECTURE §5. A nullable column that is always null invites
  someone to fill it from "height above home", and an aircraft over rising
  ground then reads higher above it than it is.
- Applies to: authority, ussp.

**D-03. Every source says which datum it reports.**
- Rule: before mapping a field, check what the source's own definition
  says it is:
  - MAVLink `relative_alt` is height above home;
  - `GPS_RAW_INT.alt` is MSL;
  - `alt_ellipsoid` is HAE;
  - ODID gives HAE, pressure altitude, and a height whose reference is a
    flag;
  - ADS-B gives barometric and geometric altitude.
- Why: ARCHITECTURE §5, P5-00. Every past datum bug was a field mapped
  from its name, not its definition.
- Applies to: authority, ussp, ansp.

**D-04. Unknown ground means "not evaluated", never zero.**
- Rule: a position outside the fetched cells, or with nodata among the
  four surrounding samples, has unknown elevation. Do not evaluate the
  height limit there. A cell the index marks as sea is 0 m with dataset
  "sea". An unreadable tile is logged and treated as unknown, and is
  retried once a minute, not once per message.
- Why: P5-00, P5-19. A listed but missing tile was being reopened on every
  telemetry message (found in SITL). `terrain_geoid.json`,
  `zones_vertical.json#height-limit-ground-unknown-not-evaluated`.
- Applies to: authority, ussp.

**D-05. The DEM is a surface model.**
- Rule: Copernicus measures roofs and canopy, not bare ground. Height
  above it errs low: safe for clearance, a few metres early for the limit.
  Show the dataset and spacing beside every number. Carry the Copernicus
  attribution wherever the numbers are shown.
- Why: P5-00. Copernicus is free to use, but its licence requires the
  attribution.
- Applies to: authority, ussp.

**D-06. An onboard terrain report is not a height source.**
- Rule: never derive AGL from the aircraft's own terrain report. Where you
  cross-check with one, treat `loaded == 0` as no answer, not zero.
- Why: P5-00. The real aircraft sent 11,520 `TERRAIN_REPORT`s, all
  `loaded=0` and 0.0 m. SITL had tiles, so it hid this completely: a
  drone would have been reported on the ground for a whole flight.
- Applies to: authority, lab.

**D-07. DEM and flight-controller terrain disagree by a known amount.
Use the measured figure, not a guess.**
- Rule: 95 % of points on GLO-30 agree within 4.3 m on plains and 6.9 m
  in mountains (maximum 14.4 m). Tbilisi on GLO-90 is 27 m, from two
  causes compounded (90 m spacing and a different flight-controller
  source), not because cities are worse.
- Why: P5-00 measurement table.
- Applies to: authority, lab.

**D-08. Take the geoid grid layout from GeographicLib's source.**
- Rule: the grid is a binary PGM P5 with `# Offset` and `# Scale` comment
  lines, 16-bit big-endian samples. Row 0 is +90 latitude (an odd number
  of rows, so the equator is one) and column 0 is 0 longitude (an even
  number of columns). Interpolate bilinearly, wrapping in longitude.
  Default to EGM2008.
- Why: P1-15. This layout agreed with GeographicLib to 4e-13 m over
  5,005 points. `terrain_geoid.json` (a synthetic grid plus GeographicLib
  reference values).
- Applies to: authority, ussp.

**D-09. Judge circles on the WGS-84 ellipsoid (Vincenty), not on a
sphere and not against a polygon.**
- Rule: test a circular zone with the geodesic distance from its published
  centre and radius. That is what a PostGIS `geography` buffer draws. If
  Vincenty does not converge (nearly antipodal points), raise an error.
- Why: U-03. A sphere is off by up to about 0.5 %. For 1 km north at
  Tbilisi the computed difference is 1.15 m. (The old test's docstring
  said about 3 m; the vector records the computed value.) Vincenty is
  pinned to Geoscience Australia's worked example to 1 mm.
  `geodesy.json`.
- Applies to: cisp, authority, ussp.

**D-10. Compute CPA in a local tangent plane, and wrap longitudes.**
- Rule: project both aircraft about their mid-latitude using the WGS-84
  meridional and prime-vertical radii. Wrap longitude differences into
  (-180, 180], and wrap longitudes advanced across the antimeridian.
- Why: P5-07. The error is centimetres at an 800 m radius. Two aircraft
  straddling the antimeridian are 223 m apart, not 40,000 km.
  `geodesy.json#tangent-plane-*`.
- Applies to: ussp, authority.

**D-11. Know which distance you are using.**
- Rule: the spoof-distance check (a 300 m threshold) uses a haversine on a
  6,371,008.8 m sphere, which is fine for that purpose. Zone edges use
  Vincenty. Do not reuse one for the other without thinking.
- Why: U-02 and U-03. `geodesy.json#haversine-spoof-distance`.
- Applies to: authority, ussp.

**D-12. Fix the simulated home at the real ground height.**
- Rule: set SITL's home altitude from the DEM, not from a guess.
- Why: P5-00. `SITL_HOME` sat 155 m below the ground for years (450 m
  against 605 m). The DEM exposed it.
- Applies to: lab.

---

## 7. Zones and ED-269

**Z-01. Read ED-269 strictly, so the round trip is exact.**
- Rule: refuse a document that has any of:
  - unknown fields;
  - wrong types (a string where a number is required, such as `"0"`, is
    refused, never converted);
  - values outside an enumeration;
  - over-length strings;
  - unclosed rings;
  - periods that cannot be evaluated.

  Normalise nothing except reading a null optional field as absent and
  writing it back absent. Numbers round-trip by value (`5.0` is written
  as `5`). `export(parse(f)) == f`.
- Why: U-03's acceptance criterion. `ed269_parse.json`.
- Applies to: cisp.

**Z-02. Import all or nothing, and name every problem.**
- Rule: a document is accepted whole or refused whole. Each problem gives
  its JSON path (`features[3].geometry[0].upperLimit`) and a reason. Cap
  the report at 100 problems with a count of the rest. A duplicate
  identifier names both places.
- Why: U-03. Half an authority's zones looks complete and is not.
- Applies to: cisp.

**Z-03. Pin field names to sources you can cite.**
- Rule: EUROCAE's text is paywalled and has no published JSON schema. Pin
  field names to InterUSS `uas_standards` (`eurocae_ed269.py`),
  Luxembourg's live file and the Swiss BAZL INTERLIS profile.
  Coordinates are GeoJSON `[lon, lat]`, whatever the prose says. Accept
  both published wrappers (`features` and `UASZoneList`) and a UTF-8 byte
  order mark (Luxembourg's file has one).
- Why: U-03 (`airspace/ed269.py` docstring).
- Applies to: cisp.

**Z-04. Refuse the near-misses by name.**
- Rule: give a named reason for each of these:
  - `REQ_AUTHORIZATION` with a Z ("ED-269 spells it REQ_AUTHORISATION");
  - a zone with more than one volume, refused rather than half-imported;
  - a daily schedule whose start and end have different offsets, or are
    equal;
  - a permanent period with dates;
  - a non-permanent period with nothing that says when it applies.
- Why: U-03. Some states publish the Z spelling, and accepting it would
  change the file on export.
- Applies to: cisp.

**Z-05. WGS84 as a vertical reference is this project's extension.**
- Rule: accept and write `WGS84` (height above the ellipsoid), and say in
  the published interface that a file using it is not a published ED-269
  file.
- Why: U-03. The owner's field list added it. No source has it.
- Applies to: cisp.

**Z-06. Bound what an import can cost.**
- Rule: refuse documents nested too deeply to be ED-269, without crashing.
  Allow at most 5,000 positions per ring. Prefilter every zone with its
  bounding box before ray casting.
- Why: U-03. Thousands of nested brackets exhausted the JSON reader's
  stack. A published zone has tens to hundreds of vertices (Luxembourg's
  largest has 1,400), so 300,000 is an error or an attack. The monitor
  tests every nearby zone on every message.
- Applies to: cisp, authority, ussp.

**Z-07. Applicability semantics.**
- Rule:
  - Both ends of a window are included.
  - Offsets are converted, never read as UTC.
  - A daily period whose end is before its start runs past midnight and
    belongs to the day it starts on: FRI 22:00-02:00 is Friday 22:00 to
    Saturday 02:00, and Friday 01:00 belongs to Thursday's night.
  - The weekday is judged in the schedule's own offset.
  - Dates bound a schedule.
  - A zone applies when any of its periods applies.
- Pitfall: published files end days at `23:59:59`, which leaves the last
  second before midnight uncovered.
- Why: U-03. `zones_applicability.json`.
- Applies to: cisp, authority, ussp.

**Z-08. Judge each limit in its own reference, and let a limit that can
be judged decide.**
- Rule:
  - AMSL is compared with the AMSL altitude.
  - AGL is compared with AMSL minus the DEM.
  - WGS84 is compared with AMSL plus the geoid undulation.
  - Feet are 0.3048 m exactly.
  - A lower AGL limit at or below 0 is met by any airborne aircraft and
    needs no DEM.
  - A missing limit is unbounded.
  - A judged limit that excludes the aircraft decides, whatever an
    unjudged one would have said.
- Why: U-03. `zones_vertical.json`.
- Applies to: authority, ussp.

**Z-09. When a limit cannot be judged, warn for the zones that matter.**
- Rule:
  - A PROHIBITED or REQ_AUTHORISATION zone whose only unjudged limit is
    AGL raises a warning with `vertical_known: false` and
    `limit_not_judged: true`.
  - Otherwise (CONDITIONAL, or a WGS84 limit without the geoid) the zone
    is not evaluated: no alert, counted, logged once per zone and
    aircraft. An active alert is neither refreshed nor cleared.
  - While any PROHIBITED zone needs terrain and none is configured, the
    startup log and every status line are at error level.
- Why: U-03 review. A false warning beats a missed critical. S-37 (open)
  proposes the same warning for a WGS84 limit with no geoid. Until the
  owner decides it, the rule above stands: uspace-core (PR #12) and
  `zones_vertical.json#prohibited-wgs84-no-geoid-not-evaluated` pin "not
  evaluated" with reason `no_geoid`. Every missing reference is reported,
  not only the first (`#*-both-reported`).
  `zones_vertical.json#*-no-terrain-*`.
- Applies to: authority, ussp.

**Z-10. Map each restriction to a severity, and nothing lifts a
prohibition.**
- Rule:
  - PROHIBITED is critical.
  - REQ_AUTHORISATION is a warning, or nothing for an aircraft authorised
    there at that time (U-05).
  - CONDITIONAL is info or warning, as policy says.
  - NO_RESTRICTION raises nothing.
  - An authorisation never lifts a PROHIBITED zone.
- Why: U-03 and the U-05 seam.
- Applies to: authority, ussp.

**Z-11. A circle is its published centre and radius.**
- Rule: store the centre and radius as published. Any polygon the database
  holds for a circle is for drawing only and is never used to judge.
- Why: U-03 (`geozone_from_row`).
- Applies to: cisp, authority, ussp.

**Z-12. Polling zones gives up to a minute of latency. Dynamic
restrictions must be pushed.**
- Rule: in utm, the monitor re-read zones every 60 s, so a change alerted
  within a minute. A dynamic restriction (U-04) must reach every consumer
  within one telemetry tick, which needs a push.
- Why: U-03, U-04, U-09 (restriction visible within 1 s).
- Applies to: cisp, ansp, authority, ussp.

**Z-13. airspace.gov.ge publishes no feed, no limits and no times.**
- Rule: its importer needs a rules file from the authority (restriction,
  limits and times for each kind of zone) before it can be used on real
  data.
- Why: U-03 (`tools/gov_ge_zones.py`).
- Applies to: cisp.

---

## 8. CPA and alerting

**C-01. Horizontal and vertical are separate.**
- Rule: take `t_cpa` from the horizontal relative motion. Evaluate the
  vertical separation at `t_cpa`. Velocities are north, east and down
  (down positive).
- Why: P5-07, ARCHITECTURE §6.2. A single 3-D CPA lets a 200 m climb hide
  a head-on horizontal conflict. `cpa.json#climb-closes-vertical-gap`.
- Applies to: ussp, authority.

**C-02. Decide the degenerate cases explicitly.**
- Rule: a diverging pair (`t_cpa < 0`) is judged where it is now, with
  `t_cpa = 0`. With zero relative velocity, `t_cpa = 0` and the distance
  is the constant current one.
- Why: P5-07. A negative time reads to a pilot as "already happened,
  safe". The zero case would divide by zero.
- Applies to: ussp, authority.

**C-03. Inside the minima now is a conflict, whatever `t_cpa` says.**
- Rule: conflict = (inside both minima now) OR (`t_cpa` within the window
  AND inside both minima at CPA). An unknown vertical counts as inside.
  C-19 widens the second clause to any time in the window.
- Why: SITL, P5-07, 2026-09-29. Two aircraft hovering 25-30 m apart have
  centimetres per second of GPS velocity noise, which put the CPA
  3,000 s away. §6.2's three-part test alone cleared the alert as
  resolved while they were still 30 m apart. The same clause holds a
  diverging pair's alert until it is past the minimum.
  `cpa.json#hovering-inside-minima-with-velocity-noise`,
  `alert_lifecycle.json#hovering-inside-minima-stays-alerted`.
- Applies to: ussp, authority.

**C-04. Advance the older sample, and do not judge a pair on a stale
one.**
- Rule: before the CPA, carry the older track forward along its velocity
  to the newer track's time. If the two samples are more than 10 s apart,
  the pair is not judged by this message: not raised, refreshed, or
  shown clear.
- Why: S-11 and B2. A 5 s old sample at 15 m/s is 75 m wrong against a
  60 m minimum. `cpa.json#older-sample-*`, `#stale-neighbour-not-judged`.
- Applies to: ussp, authority.

**C-05. Alert only on flying aircraft.**
- Rule: flying means armed (MAVLink) or declared airborne (Remote ID). An
  unknown armed state is not flying.
- Why: P5-07, P6-03. Aircraft at a base are routinely within metres of
  each other, and alerting on them teaches operators to ignore alerts.
  `alert_lifecycle.json#on-the-ground-raises-nothing`.
- Applies to: ussp, authority.

**C-06. Raise once. Clear with hysteresis and a reason.**
- Rule:
  - Raise once per condition and refresh silently while it holds.
  - Clear as `resolved` only after messages have shown the condition
    false for longer than `clear_after_s` (3 s) since it was last true.
  - Clear as `stale` when an aircraft is no longer tracked (15 s).
  - Clear as `source_disabled` when its source is switched off.
  - When both hold at once, evidence outranks silence (`resolved`).
- Why: P5-07, B2, U-15. Without the delay, an aircraft on a zone boundary
  raises and clears every second. `alert_lifecycle.json`.
- Applies to: ussp, authority.

**C-07. A severity change is a new raise under the same key.**
- Rule: when an active alert's severity changes, raise it again, so the
  bus and the audit log carry the change. Never change it silently in
  place.
- Why: S-33. Pressure-altitude band warnings and AGL-unjudged downgrades
  change severity. `alert_lifecycle.json#severity-change-is-raised-again`.
- Applies to: ussp, authority.

**C-08. Republish active alerts with current numbers.**
- Rule: republish every active alert once a second, and replay active
  alerts to a console that connects later.
- Why: P5-07 SITL. A console opened after the raise showed "in 57 s" long
  after.
- Applies to: authority, ussp.

**C-09. Isolate the checks from each other.**
- Rule:
  - An error in one check (height, zone, one neighbour's arithmetic) must
    not lose the alerts the others found.
  - A failed check neither refreshes nor clears what it could not
    evaluate.
  - Reject non-finite numbers before they reach the spatial index.
- Why: S-12. An `inf` latitude reached `floor()` in the neighbour grid and
  overflowed there.
- Applies to: authority, ussp.

**C-10. The prediction is a straight line.**
- Rule: treat the CPA as a conservative linear prediction. It cannot know
  that an aircraft will stop short, so expect alerts that clear when one
  aircraft stops.
- Why: P5-07, replay of 2026-09-29. An alert at CPA 42.7 m cleared one
  second after one aircraft stopped 204 m away. That was correct, and an
  earlier note guessing the opposite was wrong (see E-04). Still open: an
  aircraft slowing to hover near another can hide a conflict (scenario
  SC-21).
- Applies to: ussp, authority, lab.

**C-11. Advice must be deterministic.**
- Rule: the same conflict evaluated twice gives the same advice. Order by
  aircraft id: the lower id holds course, the higher descends 20 m or
  loiters 45 s. Never "whoever the server contacts first". The pair's
  alert is described identically from both aircraft's messages.
- Why: ARCHITECTURE §6.2, P5-08. Advice that is not reproducible cannot be
  audited, and two operators must be told compatible things.
- Applies to: ussp.

**C-12. Alert text must not claim a loss that has not happened.**
- Rule: "unreachable" or "lagging" means the source cannot be reached or
  is behind, not that data is lost. Use loss wording only for a recorded
  gap or a drop counter that moved.
- Why: P6-03, P1-02. A pilot who learns that alerts overstate things will
  discount the one that does not.
- Applies to: authority, ussp, ansp.

**C-13. Keep audit writes and slow I/O off the alert path.**
- Rule: audit and database writes go through a bounded queue, and losses
  are counted. Terrain reads happen off the hot path behind a cache.
- Why: S-13. A slow database must not delay alerts.
- Applies to: authority, ussp.

**C-14. A clear carries its own numbers and the right reason.**
- Rule: a clear reports the separation that cleared it, not the last
  active value. A landed aircraft clears with `landed`, not `stale`.
- Why: S-25 (open in utm). The old monitor cleared a disarm as `stale`.
  uspace-core decided `landed` (PR #15), and
  `alert_lifecycle.json#disarming-clears-as-landed` now pins it.
- Applies to: ussp, authority.

**C-15. Neighbour lookup uses a grid at least one radius wide, checked
against brute force.**
- Rule: cells are at least the 800 m policy radius. When the radius
  changes, rebuild the index with its aircraft.
- Why: P5-06. The grid agreed with brute force over 300 aircraft, and the
  slowest of 100 lookups among 100 aircraft took under 5 ms.
- Applies to: ussp, authority.

**C-16. The alert lead time must be checked against human delay.**
- Rule: the 60 s window assumes an operator acts. Measure delivery time
  to the operator's phone, not assume it (SMS can take 5-30 s or more). A
  failed or late delivery is itself an alert. The console never waits for
  delivery. P5-14's delay study decides whether 60 s is enough.
- Why: P5-09, P5-14, P5-16 (not built).
- Applies to: ussp, lab.

**C-17. Do not assume an existing UTM does tactical conflict
detection.**
- Rule: before you rely on a component for live track-to-track alerts,
  establish by running it what it does.
- Why: the P5-17 evaluation. OpenUTM Flight Blender ingested 1,858
  observations of a SITL head-on pass and raised nothing. Its conflict
  handling is strategic (F3548), and its conformance monitoring covers
  only declared flights.
- Applies to: ussp, lab.

**C-18. A bound on aircraft never clears an alert.**
- Rule: past the cap on aircraft held (E-10), evict only an aircraft
  without an active alert (not flying first, then unidentified, then the
  least recently heard). When every aircraft held has an alert, refuse
  the new id, count it, and report that the monitor is full. Bound each
  source's share of the cap in alert-holding aircraft, so that one
  receiver cannot fill it.
- Why: uspace-core PR #15 review. With plain least-recently-heard
  eviction, a flood of spoofed Remote ID ids evicted a real aircraft and
  cleared its real conflict: eviction became a way to switch off safety
  alerts, a denial of service on the one output that matters. utm held
  every aircraft. `alert_lifecycle.json#eviction-*`, `#full-of-*`,
  `#source-share-*`.
- Applies to: authority, ussp.

**C-19. Judge loss of separation over the whole window, not only at
`t_cpa`.**
- Rule: a pair is in conflict when it is inside both minima at any time
  in `[0, t_cpa_max_s]`: the interval where the horizontal distance is
  below its minimum overlaps the interval where the vertical gap is below
  its minimum. With the vertical unknown the horizontal interval alone
  decides. Report when the loss of separation starts, and rank conflicts
  by it.
- Why: uspace-core PR #10. The vertical gap at the horizontal `t_cpa` is
  one sample of a line. A pair 21.4 m apart vertically at `t_cpa` was
  under 20 m three seconds before it; and a pair whose `t_cpa` (64 s) lay
  beyond the 60 s window entered the minima at 58 s. utm judged both
  clear. `cpa.json#vertical-gap-under-minimum-before-t-cpa`,
  `#enters-minima-before-window-end-t-cpa-beyond`.
- Applies to: ussp, authority.

---

## 9. Ingest reliability and backpressure

**B-01. Drain must clearly exceed intake, or any outage becomes
permanent lag.**
- Rule: measure intake and drain as separate rates, from a deliberate
  outage, at several fleet sizes. Find the bottleneck by instrumentation.
  Proposed requirement: drain at least 5 times the combined intake at
  design load. Recovery takes outage / (N − 1).
- Why: P1-10 and ADR-002. After a two-minute outage with 11 sources, the
  queue grew by 790 records/s and never drained. Every component was
  honest and the map grew quietly older. Drain was stuck near 300/s
  whatever the load: 96 % of the time went into one database query per
  record. Batching (P1-13) raised it to 1,796/s, which is 1.5× intake and
  still short of 5×.
- Applies to: ussp, authority, lab (load tests).

**B-02. Batch the lookups, but keep per-record meaning.**
- Rule: resolve once per batch, but each record still resolves against
  the state in force at its own capture time. A batch that crosses a
  change yields two answers.
- Why: P1-13.
- Applies to: ussp, authority.

**B-03. A "lagging" state is not data loss.**
- Rule: a source is `lagging` when its newest stored record is older than
  the liveness timeout (15 s) and its reported queue depth is rising
  (compared with five status messages earlier). Publish `lag_s`.
- Why: P1-14. Otherwise a station whose backlog grows looks healthy.
- Applies to: ussp, authority.

**B-04. "Unreachable" is not "lost".**
- Rule: keep "unreachable, data buffered at the source" separate from
  "data lost". Only a declared gap, a drop counter that moved, or a
  restart counter mean telemetry is gone.
- Why: P1-02. The ingest declared a station unreachable after 3 s, while
  the relay buffered correctly for up to 25 s.
- Applies to: authority, ussp, ansp.

**B-05. Persist before you acknowledge, and deduplicate replays.**
- Rule: acknowledge only what is durably stored. Only new records enter
  the pipeline: a replayed batch publishes nothing twice. A stalled
  pipeline does not stop acknowledgements until its bounded queue is full.
- Why: S-05.
- Applies to: ussp (U-17 inbound), authority.

**B-06. Keep blocking work off the hot path, and never hold a lock across
I/O.**
- Rule: disk, database, compression, fsync and password hashing run off
  the loop or goroutine that serves traffic. A cache lock never covers a
  disk read: a cached lookup must not wait behind another tile's 26 MB
  read.
- Why: S-03, S-04, S-13, S-15, and the terrain "should-fix 6".
- Applies to: all.

**B-07. While storage is down, keep a bounded, counted buffer.**
- Rule: buffer rows in memory and retry. Above the cap (50,000 rows,
  about 10 minutes of a busy sky) drop the oldest and count them.
- Why: P1-15. 40 observations sent before the table existed were all
  written once it did, and none was lost.
- Applies to: authority, ussp.

**B-08. Reconnect forever, and start degraded rather than not at all.**
- Rule: message-bus clients reconnect without limit. At startup, retry a
  dependency a few times with backoff, then start anyway with a warning
  that the state is unknown.
- Why: U-15. The NATS client's default closed for good after about two
  minutes of broker outage, and an initial connect that retried about 60
  times hung the console (E-02).
- Applies to: all.

**B-09. Switch state never fails closed, and only goes forward.**
- Rule:
  - A follower keeps the last state it read, and with none, everything is
    enabled.
  - A switch is written to the shared store inside its database
    transaction. If the store cannot take it, the change is refused (503)
    and not recorded.
  - Within an epoch, apply only a strictly higher version. A new epoch (a
    restored database) is taken whatever its version.
  - Writers serialise on an advisory lock.
  - Republish from the database periodically to repair a lost store.
- Why: U-15. `source_control.json`, `docs/runbooks/u15-source-control.md`.
- Applies to: authority, ussp, ansp.

**B-10. Refuse a disabled source in a way the client retries.**
- Rule: refuse a connection from a disabled source with 503 and
  `Retry-After`, never 401 or 403, which clients treat as fatal. Close an
  open session with 1013. A batch that arrives first is neither stored
  nor acknowledged. The refusal is counted.
- Why: U-15.
- Applies to: authority, ussp.

**B-11. Disabled is not silent.**
- Rule: a disabled source's tracks age out as *source disabled*, and
  their alerts clear with that reason at once. The console shows
  disabled, healthy, stale or never heard for each source. An instance
  disabled by the authority looks different from one that is merely
  silent.
- Why: U-15. Verified in SITL: the Remote ID zone alert cleared 5 ms
  after the switch committed, and other sources were untouched.
- Applies to: authority, ussp, ansp.

**B-12. Do not filter at the edge.**
- Rule: forward and archive everything. Choose what drives live state
  where the context is, and keep the choice reversible. Never make the
  choice depend on a particular stream rate.
- Why: P1-01 and ADR-001. The messages the live path did not need
  (attitude, vibration, EKF, ESC) are exactly what an incident
  investigation reads, and replay cannot recover what was never kept.
- Applies to: authority (raw Remote ID frames), ussp, ansp.

**B-13. Replay shows holes as holes.**
- Rule:
  - Draw the track only as segments, cut by silence (more than 3 s), by a
    declared gap however short, or by a sample without a position.
  - Label each hole with the logged cause, or "no recorded cause".
  - Never interpolate across a hole.
  - Refuse a window that is too large rather than thin it.
  - If the alert store cannot be read, say alerts are "unavailable", not
    "none".
- Why: P10-03. A smooth line through missing data invents evidence.
- Applies to: authority (evidence, U-12), lab.

**B-14. One session per source.**
- Rule: a reconnect replaces the old session, and the old session's
  teardown must not disturb the new one. An empty or duplicated credential
  in a credentials file is a startup error.
- Why: S-08.
- Applies to: ussp, authority.

**B-15. Ingest never reaches the business database, and the two schema
trees are never merged.**
- Rule: an ingest process reads only its own store and projections.
  Migrations for separate databases have separate histories.
- Why: utm `CLAUDE.md` "Database". A slow migration on business tables
  must not stall ingest. One tree means one head, so a migration written
  for one database runs against the other, and the version table that
  would have caught it is already gone.
- Applies to: authority, ussp.

**B-16. Every adapter is its own process with its own switch.**
- Rule: no adapter imports another. They share only the internal track
  format. Each can be stopped, redeployed or broken without touching the
  others.
- Why: ARCHITECTURE §2.1, U-15.
- Applies to: authority, ussp, ansp.

---

## 10. Not carried over

These lessons applied only to the MAVLink operator relay, QGC forwarding,
or the courier delivery fleet. The rewrite drops all three. Where a
principle generalises, its general form is above.

| ID | Lesson | Why it is not carried over |
|---|---|---|
| X-01 | QGC forwarding is telemetry-only; measure the radio port's `SR*_` rates separately from USB (ADR-001) | No QGC relay in the new system. |
| X-02 | Classify MAVLink endpoints by HEARTBEAT `type`/`autopilot` per `(sysid, compid)`, never by SYSID or volume; QGC's own heartbeat is forwarded back (P1-02) | MAVLink ingest dropped. |
| X-03 | relay-v1: lossless dumb relay, disk queue, `hello`/`resume_from_seq`/`newest_seq_held`, `gap`, half-open detection up to 25 s, drain detection by 32 KiB frames and queue depth (P1-01, S-09) | Relay dropped; the general rules are T-01, T-02, T-04, B-01 to B-05. |
| X-04 | `source_bindings` policy: a station may carry only the SYSIDs bound to it, resolved at capture time; handover accepted on both (P1-07, P1-13) | Station bindings dropped; operators authenticate as service accounts (U-17). |
| X-05 | Radio-link stream-rate budget, `NETID` separation, 2-3 vehicles per SiK net (P1-01b) | No radio relay. |
| X-06 | Record `AUTOPILOT_VERSION` by observing QGC's request at connect (P1-11) | MAVLink dropped. |
| X-07 | Round-trip link latency dropped because it needs a send (P1-09) | Moot without MAVLink. INV-01 still forbids sends. |
| X-08 | Stalled relay stamping OS-buffered datagrams with read time: fix with `time_boot_ms` anchoring and reboot detection (S-24) | Relay-specific fix. The general rule is T-11. |
| X-09 | Our own aircraft's unidentified broadcast may conflict with its own MAVLink track (accepted in P1-15) | Accepted only because MAVLink was leaving. |
| X-10 | MAVLink 2 signing to stop a compromised station replaying a bound SYSID (P1-07) | MAVLink dropped. |
| X-11 | Strategic 4D corridors, dispatch, `MISSION_ITEM_REACHED` progress inference, delivery tables (P-01, P5-01..05, P3-06) | Courier fleet removed. U-05 brings strategic checks back as a service to operators. |
| X-12 | `courier_*` database and volume names, rename runbook (S-14) | Naming debt of the old deployment. |
| X-13 | Windows ground-PC relay with SITL in WSL2, mirrored networking (P0-08) | No relay on a pilot laptop. lab chooses its own SITL host. |
| X-14 | `TERRAIN_REPORT` as a height source | Ruled out anyway (D-06). Nothing carries it over. |

---

## Index

| Area | Lessons |
|---|---|
| Invariants | INV-01 to INV-03 |
| Engineering rules | E-01 to E-15 |
| Time and clocks | T-01 to T-13 |
| Remote ID and ODID | R-01 to R-17 |
| Identity and spoofing | I-01 to I-09 |
| Identification and registry | G-01 to G-12 |
| Geodesy and datums | D-01 to D-12 |
| Zones and ED-269 | Z-01 to Z-13 |
| CPA and alerting | C-01 to C-19 |
| Ingest reliability | B-01 to B-16 |
| Not carried over | X-01 to X-14 |

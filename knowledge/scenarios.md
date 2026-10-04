# Scenarios that the vectors cannot hold

The JSON vectors in `vectors/` cover behaviour that is a function of its
inputs, including short stateful sequences fed to one component. The
behaviour below depends on processes, clocks, networks, a broker, a
database or a flying simulator. It is written as scenarios: exact steps
and the outcome each step must show. Every scenario either ran in utm
(the source says when, and the numbers are what was measured) or is marked
**not yet run**, with the outcome it must show.

Conventions:

- Policy: `t_cpa_max_s` 60, `d_horizontal_min_m` 60, `d_vertical_min_m`
  20, neighbour radius 800 m, height limit 120 m AGL.
- Monitor defaults: clear hysteresis 3 s, stale after 15 s, neighbour max
  age 10 s, live max age 10 s, pressure margin 250 m.
- "SITL" means ArduCopter SITL. Aircraft are commanded by a harness that
  checks every step against the vehicle's own telemetry (LESSONS E-08).
- Owner codes are as in `LESSONS.md`. `lab` runs each scenario end to end.
  The owners listed implement the behaviour under test.

Contents:

| ID | Scenario | Owners | utm status |
|---|---|---|---|
| SC-01 | Two aircraft hovering inside the minima | ussp, authority | ran 2026-09-29 |
| SC-02 | Head-on pass, and a return that stops short | ussp, authority | ran 2026-09-29 |
| SC-03 | Zone entry and exit | authority, ussp | ran 2026-09-29 |
| SC-04 | Height limit over falling ground | authority, ussp | ran 2026-09-30 |
| SC-05 | Remote ID aircraft against a hovering SITL aircraft | authority, ussp | ran 2026-09-29 |
| SC-06 | Four identification statuses at once | authority, ussp | ran 2026-10-01 |
| SC-07 | Unidentified aircraft through a PROHIBITED zone | authority | ran 2026-10-01 |
| SC-08 | Switching sources off and on | authority, ussp, ansp | ran 2026-10-01 |
| SC-09 | An authenticated feed drains after an outage: no false spoof | ussp, authority | ran 2026-10-01 |
| SC-10 | Serial change on a reused address with drops | authority | ran 2026-10-01 (in-process) |
| SC-11 | Receiver latency | authority | ran 2026-10-01 (in-process) |
| SC-12 | Zone applicability windows in flight | cisp, authority | ran 2026-10-01 |
| SC-13 | PROHIBITED AGL zone with no DEM | authority, cisp | ran 2026-10-01 |
| SC-14 | Drain against intake after a deliberate outage | ussp, authority | ran 2026-09-27/28 |
| SC-15 | A stalled adapter must not present old positions as live | authority, ussp, ansp | **not yet passed** |
| SC-16 | A network Remote ID provider switched off | ussp | ran 2026-10-01 |
| SC-17 | A registry change reaches every resolver | authority, ussp | **not yet run** as a timed scenario |
| SC-18 | Storage down while observations arrive | authority | ran 2026-09-30 |
| SC-19 | Replay holes with their causes | authority, lab | partly run |
| SC-20 | Soak and missed-alert count | lab | **not yet run** |
| SC-21 | Slowing to hover beside another aircraft | ussp, authority | **not yet run**; lab 2026-10-04 against the USSP image 6ec6238: FAIL, B's intent refused as filed second (results/20261004-systems) |
| SC-22 | A data source that does not exist yet must not be silence | authority, ussp | design check |

---

## SC-01. Two aircraft hovering inside the minima

Source: utm `docs/runbooks/p5-airspace-monitor.md`, 2026-09-29, P5-07.
Rule: LESSONS C-03.

1. Start two SITL aircraft whose homes are 25 m apart. Arm both and climb
   both to 30 m in GUIDED.
   - Expect: one critical conflict for the pair as soon as both are armed
     and placed (utm: raised at 20:26:34, "25 m < 60 m, zero relative
     velocity").
2. Hold the hover for at least 30 s.
   - Expect: no clear, in particular no `resolved`. Velocity noise puts
     the CPA thousands of seconds away, and the alert must still hold.
3. Fly one aircraft away until they are 60 m or more apart.
   - Expect: the alert clears as `resolved` 3 s or more after the last
     sample inside 60 m (utm: separated to 59.4 m and opening, cleared at
     20:27:07).

Fails if the alert clears while they are still less than 60 m apart. This
is the bug SITL found that unit tests had not.

## SC-02. Head-on pass, and a return that stops short

Source: same run.

1. Two aircraft at 30 m about 590 m apart, flying towards each other at
   10 m/s.
   - Expect: critical conflict raised before `t_cpa` reaches 60 s (utm:
     raised 57.2 s before the CPA, which was 2.8 m).
2. Let them pass.
   - Expect: cleared `resolved` once past 60 m and the hysteresis has run
     (utm: at 58.3 m and opening, cleared at 20:28:27).
3. Aircraft 1 holds position. Aircraft 2 flies towards it at 10 m/s,
   decelerates, and stops 204 m away.
   - Expect: the alert is raised while the straight-line prediction
     closes inside 60 m (utm: CPA 42.7 m in 57.7 s), and cleared about
     1 s after the stop.
   - This is correct: the prediction is linear (LESSONS C-10). Confirm it
     from the recorded tracks, not by reasoning (E-04).

## SC-03. Zone entry and exit

Source: same run, P5-15.

1. A 120 m square REQ_AUTHORISATION zone 300 m north of home, permanent,
   from the ground up. Fly an armed aircraft through it.
   - Expect: a warning naming the zone on entry, and a clear as
     `resolved` after leaving plus the hysteresis.
   - Expect: one audit row for each transition and each aircraft (utm: 16
     rows across the whole run).

## SC-04. Height limit over falling ground

Source: P5-19, 2026-09-30, Kazbegi. Needs the Copernicus DEM for N42E044.

1. One SITL aircraft holds 100 m above home, 1,861 m AMSL. Home ground is
   1,761 m.
2. Fly 1 km north at constant AMSL. The ground falls to about 1,720 m.
   - Expect: a height warning when AMSL minus DEM first exceeds 120 m
     (utm: at 06:38:03, height 120.3 m, ground 1,740.2 m, GLO-30).
   - Cross-check: the flight controller's own terrain crossed 120 m 3 s
     later. The two independent sources agree to about 30 m of flight.
3. Fly back.
   - Expect: a clear after the height has been at or below 120 m for
     longer than the hysteresis (utm: FC under 120 m at 06:39:36, monitor
     cleared at 06:39:42).
   - Neither the AMSL altitude nor the height above home changed during
     the run. Only the ground did.

## SC-05. Remote ID aircraft against a hovering SITL aircraft

Source: P1-15, 2026-09-29.

1. A SITL aircraft hovers 30 m above home at 475 m AMSL.
2. A simulated Remote ID aircraft flies east through its position at
   480 m AMSL, broadcasting 494.7 m HAE. Ingest and simulator use the same
   geoid.
   - Expect: a critical conflict raised about 475 m out (utm: CPA 1.2 m in
     59.6 s, 3.2 m apart vertically), cleared after the pass.
   - Expect: the console shows the Remote ID aircraft as broadcast and
     unverified, with the conflict line.
3. Repeat with the ingest running without a geoid.
   - Expect: the aircraft is on the map with no AMSL altitude, and no
     conflict is raised. A conflict guessed from HAE would be the error
     R-07 forbids.

## SC-06. Four identification statuses at once

Source: U-02, 2026-10-01. Uses SITL as a Remote ID source (U-16) plus a
fake network Remote ID provider.

Registry setup:

- Operator `GEOU02ACTIVE001`, active.
- UAS `U02REG0001`, active.
- UAS `U02SUS0002`, suspended.
- UAS `U02UNK0003`, active.
- All three UAS are owned by `GEOU02ACTIVE001`.

| Vehicle | Broadcast serial | Broadcast operator | Expect status / reason / mismatch |
|---|---|---|---|
| SYSID 1 | U02REG0001 | GEOU02ACTIVE001 | registered / matched / false |
| SYSID 2 (direct) | U02SUS0002 | GEOU02ACTIVE001 | suspended / uas_suspended / false |
| SYSID 2 (network, fake SP) | U02SUS0002 | - | suspended / uas_suspended / false; **same track id as the direct one** |
| SYSID 3 | U02UNK0003 | GEOU02NOTREG99 | unknown_operator / operator_mismatch / true, plus an `identification_mismatch` warning: "gives GEOU02NOTREG99; registered to GEOU02ACTIVE001" |
| SYSID 1, second transmitter, no Basic ID | - | - | unidentified / no_serial / false, labelled with its address |

Also expect:

- The direct and network tracks of SYSID 2 agree on position to the
  broadcast's 0.5 m altitude step (utm: 785.04 m against 785.1 m).
- The SYSID 1 pair (two radios at one point) raises a conflict between
  them. That is correct: two transmitters are two claims.

## SC-07. Unidentified aircraft through a PROHIBITED zone

Source: U-02, 2026-10-01.

1. A 160 m square PROHIBITED zone, 0-1500 m AMSL, 230 m east of SYSID 1.
   SYSID 1 carries both a registered transmitter and an unidentified one,
   as in SC-06.
2. Fly SYSID 1 east through the zone and back.
   - Expect on each pass:
     - the registered track raises only its zone alert (critical, detail
       `identification: registered`);
     - the unidentified track raises its zone alert *and* a critical
       `identification` alert;
     - all three clear `resolved` together on exit.
   - Expect: every `identification` raise and clear reaches the incident
     seam (utm: four candidates, from two raises and two clears).

## SC-08. Switching sources off and on

Source: U-15, 2026-10-01.

1. SYSIDs 1 and 2 report through authenticated feeds (utm: two relays).
   SYSID 3 reports through Remote ID. Each hovers inside its own
   PROHIBITED zone, so each has a critical zone alert.
2. Switch Remote ID off by type.
   - Expect: SYSID 3's alert clears as `source_disabled` within
     milliseconds of the commit (utm: 5 ms). No more Remote ID tracks are
     published. SYSIDs 1 and 2 and their alerts are untouched.
   - Expect: the console shows the receiver "disabled by type", with its
     refusal count rising.
3. Switch Remote ID on.
   - Expect: tracks return and the alert is raised again within 1 s.
4. Switch one authenticated station off.
   - Expect: its session closes with 1013, and reconnects are refused with
     503 and Retry-After while the client keeps queueing. Only that
     station's aircraft's alert clears as `source_disabled`.
5. Switch it on.
   - Expect: the client reconnects at its next retry (utm: 4 s) and
     resumes from where it stopped. Its queue is delivered as backlog,
     recorded but not alerted, and the live alert is raised again.
6. Try a switch as a viewer.
   - Expect: 403.
7. Check the audit log.
   - Expect: every switch is an audit row with the actor and the reason.
8. Start a follower while the shared store (KV) is unavailable.
   - Expect: it retries 3 times with backoff, then starts with every
     source enabled and logs that the switch state is unknown. A switch
     attempted meanwhile is refused with 503 and nothing changes.

## SC-09. An authenticated feed drains after an outage: no false spoof

Source: U-02 review re-check, 2026-10-01. LESSONS I-08, I-09.

1. Register a fleet aircraft with serial `U02FLEET01`. It reports through
   an authenticated feed *and* broadcasts the same serial on Remote ID.
   - Expect: the broadcasts are withheld (verdict `withhold`).
2. Stop the ingest for the authenticated feed. Fly the aircraft 440 m
   east.
   - Expect: the broadcasts are published as our aircraft (its id, still
     marked broadcast), about one every 10 s.
3. Restart the ingest. The feed drains its backlog (utm: 583 rows in 3 s,
   up to 438 m from where the broadcast placed the aircraft at the moment
   of delivery).
   - Expect:
     - no `serial_conflict`;
     - no `identification_mismatch`;
     - the backlog rows neither make the link live nor move its position;
     - the broadcasts return to withheld once live rows arrive.

utm verified only the fixed code in SITL. That the old code would have
split the aircraft was read from the code, not observed.

## SC-10. Serial change on a reused address with drops

Source: U-16 / S-32, 2026-10-01, run in-process on simulated clocks.

1. A bridge sends one message per datagram with 30 % dropped, for two
   30 s legs. The second leg restarts the module 4 s later with a **new
   serial on the same address**. Choose a seed that drops the restart's
   first Basic ID while two Locations get through.
   - Expect: zero Locations stored under the old serial after the restart
     (utm: 22 rows, all under the new serial; 20 further seeds stored none
     under the old one).
   - Control: with the old rules (60 s identity, 60 s memory) the same
     seed stores 2 Locations under the old serial. Run the control to
     prove the scenario can detect the bug (E-01).

## SC-11. Receiver latency

Source: S-27, 2026-10-01, in-process.

1. Deliver every datagram 2 s late (signed when it is sent), with no
   drops.
   - Expect: every observation is placed at its broadcast time, 1.9 to
     2.0 s before its receive time. The field holds tenths of a second.

## SC-12. Zone applicability windows in flight

Source: U-03, 2026-10-01. SYSID 3 on Remote ID only, hovering at 685.1 m
AMSL, flown east and west along one line at 10 m/s. A flat 15.9 m geoid
on both sides.

| Run | Zone state | Expect |
|---|---|---|
| 1 | PROHIBITED, 600-800 m AMSL, permanent | critical raised on entry, cleared `resolved` on exit |
| 2 | Same zone, window 1-30 September (the run is on 1 October) | nothing for 20 samples inside |
| 3 | Permanent again | critical raised and cleared |
| 4 | Plus a CONDITIONAL zone drawn in the editor and edited to 600-800 m AMSL | warning for it, critical for the first, each raised and cleared |

Also expect: zones created through the API take effect within the zone
refresh interval. In utm that was 60 s (created 01:20:55, loaded
01:21:43). A pushed restriction (U-04) must do better: within one
telemetry tick.

## SC-13. PROHIBITED AGL zone with no DEM

Source: U-03 review, 2026-10-01. LESSONS Z-09.

1. Without terrain configured, load a PROHIBITED zone of 0-120 m AGL.
   - Expect: the startup log line is at **error** level and names the
     zone. Every periodic status line is also at error level.
2. Fly the same pass as SC-12.
   - Expect: a **warning** (not critical) with `vertical_known: false`,
     `limit_not_judged: true` and `not_judged: ["AGL"]`, raised on entry
     and cleared `resolved` on exit. Both transitions are audited.
3. Repeat with a REQ_AUTHORISATION zone of 0-120 m AGL.
   - Expect: the same warning. A CONDITIONAL zone of 0-120 m AGL raises
     nothing and counts "not evaluated" once per sample inside.

## SC-14. Drain against intake after a deliberate outage

Source: P1-10, P1-13, ADR-002. LESSONS B-01. Applies to any authenticated
inbound feed (U-17 operators publishing, network Remote ID).

1. Put a severable TCP proxy between the producer and the ingest, so an
   outage is a dead link with the ingest still warm.
2. Run each fleet size (utm: 1, 3 and 11 sources) through these phases:
   settle, 30 s baseline, 60 s outage, 120 s recovery.
3. Measure intake (the producer's sequence counter) and drain (the
   ingest's durable watermark) as **separate** rates.
   - Expect: drain is at least N times intake at design load (proposed N
     = 5, at 15 aircraft).
   - Expect: the backlog clears within outage / (N − 1).
   - Expect: neither drop counter moves.
4. While drain is below intake:
   - Expect: the source is reported `lagging` with `lag_s`, not healthy,
     and not "lost".
5. Record the stage timings. Name the bottleneck from the measurement.

utm's numbers:

| | Intake | Drain | Ratio | Notes |
|---|---|---|---|---|
| Before batching (1 / 3 / 11 sources) | 194 / 416 / 1,281 per s | ~300 per s at every size | 1.50x / 0.96x / 0.23x | 96 % of time in one query per record |
| After batching (11 sources, bound) | 1,197 per s | 1,796 per s | 1.5x | 72,345-record backlog cleared to 769 in 120 s; still short of 5x |

## SC-15. A stalled adapter must not present old positions as live

Source: S-24 (open in utm). LESSONS T-11. **Not yet passed.**

1. Two aircraft hover 200 m apart, and their tracks reach the monitor
   through one adapter process. A second aircraft path is arranged so
   that its *old* positions would conflict with the first.
2. Pause the adapter process for 30 s (SIGSTOP), so its input buffers in
   the OS. Then resume it (SIGCONT).
   - Expect: no alert raised from the buffered positions. They are placed
     at their own capture time, or flagged as history, and the stall is
     counted.
   - In utm the relay stamped them with the read time, and one false
     conflict was raised from 30 s-old positions (SITL, 2026-10-01).

## SC-16. A network Remote ID provider switched off

Source: U-02 / U-15, 2026-10-01.

1. A fake ASTM F3411 SP serves SYSID 2. Direct Remote ID also hears SYSID
   2.
2. Switch provider `fake-ussp` off.
   - Expect: polling stops at once (utm: the SP's request count stayed at
     224). No network track is published after it. SYSID 2 stays on the
     map through direct Remote ID.
3. Switch it on.
   - Expect: polling and tracks resume within 1 s.

## SC-17. A registry change reaches every resolver

LESSONS G-08. **Not yet run as a timed scenario.** utm tests cover each
piece against PostgreSQL.

1. A registered UAS is broadcasting and resolves to `registered`.
2. Suspend it through the registry API.
   - Expect: every adapter's next track says `suspended` within the
     reader refresh (5 s) plus the transaction time.
3. Make the projection write fail (for example, a constraint).
   - Expect: the suspension is rolled back and the API reports the
     failure. Nothing is left half-applied.
4. Delete a projection row by hand.
   - Expect: the periodic full re-projection (every 300 s) restores it.
5. Make a registry change while a re-projection is running.
   - Expect: the change waits for the re-projection's lock and is not
     overwritten.
6. Make the projection unreadable for one refresh.
   - Expect: the readers keep the snapshot they hold. No aircraft turns
     unknown.

## SC-18. Storage down while observations arrive

Source: P1-15, 2026-09-30.

1. Send 40 simulated observations before the observations table exists.
   - Expect: all 40 are held in memory and retried, and each failure is
     logged.
2. Create the table.
   - Expect: all 40 are written and none is lost. Replay lists the
     aircraft as Remote ID: one flight, one segment, no holes,
     `authenticated: false`, and battery, mode and armed empty (never
     zero).
3. Push the held rows past the cap.
   - Expect: the oldest rows are dropped and counted, and the drop is
     logged.

## SC-19. Replay holes with their causes

Source: P10-03. **Partly run:** silence and no-position holes were
verified on 2026-09-29. A live hole with a logged cause was not
exercised.

1. Replay a flight whose first 26 s had telemetry without a position
   fix.
   - Expect: a "no position" hole at the start. The line starts where the
     position does, and the one early fix stands alone.
2. Cut a feed's uplink for 60 s in mid-flight (not yet run).
   - Expect: a hole marked "station unreachable" (not loss) if the
     backlog later fills it, or the logged loss cause if it does not.
     Never a line across the hole.
3. Make the alert store unreadable.
   - Expect: the page says alerts are "unavailable", not that there were
     none.

## SC-20. Soak and missed-alert count

Source: P5-13, P5-14, D-02. **Not yet run.**

1. Fly 15 or more SITL aircraft on scripted paths over one city square for
   2 hours. Script operator compliance after a simulated 10-20 s delay.
   - Expect:
     - zero separation violations under 30 m;
     - a log of conflicts detected, how each was resolved, and the worst
       separation seen;
     - memory over time;
     - alert latency;
     - a missed-alert count of **zero**.
2. Repeat with reaction delays of 5, 15, 30 and 60 s.
   - Expect: a report of the largest delay that still keeps 30 m. That
     number decides whether a 60 s lead time is enough.

## SC-21. Slowing to hover beside another aircraft

Source: P5-07 runbook, still open (P5-12). **Not yet run.**

1. Aircraft A hovers. Aircraft B approaches at 10 m/s and decelerates to
   hover 40 m from A.
   - Expect: a conflict raised no later than when B's position comes
     within 60 m (C-03 holds it while inside), and never cleared while B
     hovers there.
   - Risk: during deceleration the straight-line CPA may pass outside 60 m
     until B is actually inside. Measure how late the raise is.

## SC-22. A data source that does not exist yet must not be silence

LESSONS E-02, D-04, Z-09. **Design check:** run once for each
deployment.

1. Start the service with no terrain, no geoid, no registry projection
   and no switch state.
   - Expect, at startup and in every status line:
     - the height limit is not evaluated (no terrain);
     - Remote ID aircraft have no AMSL altitude (no geoid);
     - each zone that cannot be judged is listed with what it needs;
     - identification is null where there is no projection;
     - the switch state is unknown.

   Each item must be visible, and a PROHIBITED zone that needs terrain
   must be reported at error level. An empty console must never look like
   an empty sky.

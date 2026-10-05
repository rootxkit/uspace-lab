# Load run 20261005-reference-5000: tier 5000

**Verdict: FAIL.** 0 checks measured, 41 not measured (05 §7 is not fully observed by this run).

- Target: reference (reference). The lab's in-process reference target (internal/reftarget): it proves the harness and is never evidence for a system (docs/PLAN.md L-D4); generator and target share this host's CPUs.
- Host: dev workstation (Windows 11, 16 logical CPUs, 16 GB; not the droplet): windows/amd64, 16 CPUs, memory unknown, go1.27.1. the host the generator and the reference target ran on together (L-Q1)
- Commits: uspace-lab `e79b1202c5f0f16f276b9b3e65bb8a2291f86102`, uspace-core `v1.3.0`.
- Policy: version 1 (`sha256:19124b6573ade992e321dbb2f18313bf01ec7ac262db03d75216da38f94db8df`); figures marked pending GCAA are the spec's defaults until GCAA answers.
- Ran 2026-10-05T06:59:12Z to 2026-10-05T07:00:18Z; the generator ran 0 s of the tier's 600 s.

## Scaled from 05 §1 / 05 §7

| Quantity | Spec | This run | Factor | Why |
|---|---|---|---|---|
| console viewports | 60 consoles each viewing <= 200 tracks (05 §1) | the same count, each subscribed to the whole area | 1 | the reference picture has no viewport throttle; whole-area subscriptions are the worst case |
| duration (override) | 3600 s (this tier) | 600 s | 0.16666666666666666 | --duration-s given for this run |

## Not generated

- ANSP manned feed (tens of aircraft): the reference target has no manned intake; the systems mode is not built yet
- sim-ussp as the second USSP on a local DSS: the reference target has no DSS or peer interface; the F3411 and F3548 rows wait for the systems mode
- authority DP polls of the USSPs (05 §1: one view per 1 Hz): the reference authority has no DP poller
- optional national push USSP -> authority: disabled by default (02 F6) and not in the reference target

## Offered load

| Quantity | Observed | Tier target |
|---|---|---|
| Operator telemetry (msg/s) | 0.0 | 0.0 |
| Remote ID locations (msg/s) | 0.0 | 0.0 |
| Console frames in (frames/s, all consoles) | 0.0 | |
| Aircraft / operator clients / heard / receivers / consoles / watched | 0 / 0 / 0 / 0 / 0 / 0 | |
| Intents filed (errors) | 0 (0) | |

## 05 §7

| Property | Check | Criterion | Observed | n | Result | Figure from |
|---|---|---|---|---|---|---|
| **Offered load** (not measured) | `operator_rate` (required) | value >= 0.95 | not measured |  | not measured: the run stopped before measuring | lab |
|  | `rid_rate` (required) | value >= 0.95 | not measured |  | not measured: the run stopped before measuring | lab |
| **Ingest-to-picture latency** (not measured) | `operator_to_console_p99` (required) | p99 < 1 s | not measured |  | not measured: the run stopped before measuring | pending GCAA |
|  | `receiver_to_picture_p99` (required) | p99 < 1 s | not measured |  | not measured: the run stopped before measuring | pending GCAA |
| **F3411 timing** (not measured) | `sp_flights_p95` | p95 <= 1 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `sp_flights_p99` | p99 <= 3 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `dp_display_p95` | p95 <= 1 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `dp_display_p99` | p99 <= 3 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `details_p95` | p95 <= 2 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `details_p99` | p99 <= 6 s | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `dp_cache_24h` | value == 0 | not measured |  | not measured: F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
| **F3548 timing** (not measured) | `intent_change_notify` | max <= 5 s | not measured |  | not measured: F3548 timings need two USSPs against the lab DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `conflict_notify` | max <= 1 s | not measured |  | not measured: F3548 timings need two USSPs against the lab DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `details_answered` | max <= 1 s | not measured |  | not measured: F3548 timings need two USSPs against the lab DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
|  | `constraint_notify` | max <= 5 s | not measured |  | not measured: F3548 timings need two USSPs against the lab DSS; the load harness drives neither yet (WP-L8 left: systems mode) | standard |
| **Alert latency** (not measured) | `proximity_raise_p99` (required) | p99 < 2 s | not measured |  | not measured: the run stopped before measuring | pending GCAA |
|  | `zone_alert_tick` | value <= 1 | not measured |  | not measured: the load paths script no zone entry yet; zone alerts are proved by the scenario suite (sc-03) | spec |
|  | `art13_notice` | max <= 5 s | not measured |  | not measured: Art. 13(2) notices go to peers and the ANSP, neither of which the load harness drives yet | pending GCAA |
| **Missed alerts** (not measured) | `expected_set` (required) | value >= 1 | not measured |  | not measured: the run stopped before measuring | lab |
|  | `missed_raises` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `missed_clears` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `no_false_alarm` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | lab |
|  | `alerts_traceable` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | lab |
| **No silent loss** (not measured) | `operator_identity` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `receiver_identity` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `picture_identity` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `picture_traceable` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | lab |
| **CPA budget** (not measured) | `pair_checks` | value <= 1 | not measured |  | not measured: read from a system's /metrics by name; the reference target has no /metrics and systems mode is not built yet | spec |
|  | `evaluation_period` (required) | value <= 2 s | not measured |  | not measured: the run stopped before measuring | spec |
| **Storage** (not measured) | `writer_queue` | value < 10 s | not measured |  | not measured: the reference target has no database; storage is measured against the systems' images | spec |
|  | `compression` | value == 0 | not measured |  | not measured: the reference target has no database; storage is measured against the systems' images | spec |
|  | `disk_model` | value <= 1.2 | not measured |  | not measured: the reference target has no database; storage is measured against the systems' images | spec |
| **Restart** (not measured) | `restart_recovery` | value <= 10 s | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
|  | `restart_duplicates` | value == 0 | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
|  | `restart_backlog` | value == 0 | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
| **Partition** (not measured) | `partition_replay` | value == 0 | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
|  | `partition_isolation` | value == 0 | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
| **Cross-system outage** (not measured) | `outage_flagged` | value == 0 | not measured |  | not measured: the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet | spec |
| **Memory** (not measured) | `memory_monotonic` (required) | value == 0 | not measured |  | not measured: the run stopped before measuring | spec |
|  | `memory_growth` | value <= 1.5 | not measured |  | not measured: the run stopped before measuring | lab |
| **Vectors** (not measured) | `vectors_pass` | value == 0 | not measured |  | not measured: the vectors run in uspace-core's CI and per image in the conformance suite, not in a load run | spec |

## Loss accounting

- Operator telemetry: sent 0 = accepted 0 + refused 0 + dropped 0 + duplicate 0; 0 pending; 0 clients unbalanced.
- Receivers: observed 0, sent 0 = accepted 0 + duplicates 0 + refused 0; shed 0; pending 0; 0 receivers unbalanced.
- Picture (timed console): handed 0, shown 0, unshown 0, accounted 0 (refused, dropped_frames 0, sheds), silent 0; untraceable frames 0.
- Traffic streams (watched flights): handed 0, shown 0, unshown 0, untraceable 0.
- Generator sheds: operator 0, Remote ID 0.

## Alerts

Expected 0, raised 0, cleared 0, missed 0, clears missed 0, unexpected 0, untraceable 0.

## Failures

- operator_rate: required and not measured: the run stopped before measuring
- rid_rate: required and not measured: the run stopped before measuring
- operator_to_console_p99: required and not measured: the run stopped before measuring
- receiver_to_picture_p99: required and not measured: the run stopped before measuring
- proximity_raise_p99: required and not measured: the run stopped before measuring
- expected_set: required and not measured: the run stopped before measuring
- missed_raises: required and not measured: the run stopped before measuring
- missed_clears: required and not measured: the run stopped before measuring
- no_false_alarm: required and not measured: the run stopped before measuring
- alerts_traceable: required and not measured: the run stopped before measuring
- operator_identity: required and not measured: the run stopped before measuring
- receiver_identity: required and not measured: the run stopped before measuring
- picture_identity: required and not measured: the run stopped before measuring
- picture_traceable: required and not measured: the run stopped before measuring
- evaluation_period: required and not measured: the run stopped before measuring
- memory_monotonic: required and not measured: the run stopped before measuring
- the run measured nothing: no check had an observation
- run error: not ready: stream console-01 did not open within 1m0s: console-01: failed to WebSocket dial: failed to send handshake request: Get "http://127.0.0.1:5287/v1/picture/ws": dial tcp 127.0.0.1:5287: connectex: No connection could be made because the target machine actively refused it.


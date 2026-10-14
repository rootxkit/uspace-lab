# Load run 20261014-951fda2-5000-systems: tier 5000

**Verdict: FAIL.** 28 checks measured, 16 not measured (05 §7 is not fully observed by this run).

- Target: lab-load-5000 (systems). The systems' images as the targets file names them, seeded for this fleet (loadgen seed); evidence for the images and digests listed, on the host named, when the run passes (docs/PLAN.md L-D4).
- Receivers: each receiver under its own address of 10.77.0.0/16, named in X-Forwarded-For, which the lab Caddy and the authority trust from the host only (deploy/systems).
- Image ansp: `ghcr.io/rootxkit/uspace-ansp@sha256:1fcfb3c9762189f737250c65ad24166c025c12576abb7babffcf07d705a6921a`
- Image authority: `ghcr.io/rootxkit/uspace-authority@sha256:53d6f97cd745677da893a62669f45477d1eaa403b369bc174b6ccc8c11ee11cc`
- Image cisp: `ghcr.io/rootxkit/uspace-cisp@sha256:2e7861488e178de967829e7ebb623a3c5b480f0dcca8e5ee1acab4e45028f42a`
- Image ussp: `ghcr.io/rootxkit/uspace-ussp@sha256:854cca7146277e6dc767cd7efce279f350994c9f152344a8af457c917db5935d`
- Host: dev workstation (Windows 11, 16 logical CPUs, 16 GB); the systems stack on the same host in Docker Desktop 28.5.1 (16 CPUs, 7.7 GB), other projects' containers beside it: windows/amd64, 16 CPUs, memory unknown, go1.27.1. the host the generator ran on (L-Q1); where the systems ran is the --host name's to say
- Commits: uspace-lab `951fda2a15bcf06cb42aa26e441187bf2642b256`, uspace-core `v1.6.0`.
- Policy: version 1 (`sha256:19124b6573ade992e321dbb2f18313bf01ec7ac262db03d75216da38f94db8df`); figures marked pending GCAA are the spec's defaults until GCAA answers.
- Ran 2026-10-14T21:20:46Z to 2026-10-14T21:45:47Z; the generator ran 600 s of the tier's 600 s.

## Scaled from 05 §1 / 05 §7

| Quantity | Spec | This run | Factor | Why |
|---|---|---|---|---|
| console viewports | 60 consoles each viewing <= 200 tracks (05 §1) | the same count, each subscribed to the whole area | 1 | the reference picture has no viewport throttle; whole-area subscriptions are the worst case |
| duration (override) | 3600 s (this tier) | 600 s | 0.16666666666666666 | --duration-s given for this run |

## Not generated

- ANSP manned feed (tens of aircraft): the reference target has no manned intake; in a systems run the ANSP adapter polls a sim-adsb the load harness does not serve, so the USSP shows manned traffic unavailable (its degraded state, in the report)
- the second USSP's own intents: in a systems run the harness's peer USSP (peer) is the second USSP on the lab DSS: it serves its own flights and is notified of the fleet's intents in the U-space square, but files no intent of its own, so no conflict between the two USSPs is made (the reference target has no DSS or peer)
- authority DP polls of the USSPs (05 §1: one view per 1 Hz), against the reference target: the reference target has no SP; in a systems run the authority's display provider polls the USSP's and the peer's SPs over the oversight area the seed adds, and the lab polls the USSP's SP as a display provider too (dp)
- optional national push USSP -> authority: disabled by default (02 F6) and not in the reference target

## Offered load

| Quantity | Observed | Tier target |
|---|---|---|
| Operator telemetry (msg/s) | 4986.8 | 5000.0 |
| Remote ID locations (msg/s) | 1994.8 | 2000.0 |
| Console frames in (frames/s, all consoles) | 16931.6 | |
| Aircraft / operator clients / heard / receivers / consoles / watched | 5000 / 200 / 2000 / 80 / 60 / 47 | |
| Samples handed out more than a period late | 7300 | |
| Intents filed (errors) | 5000 (0) | |
| Intents ended after the run | 3868 | |

## 05 §7

| Property | Check | Criterion | Observed | n | Result | Figure from |
|---|---|---|---|---|---|---|
| **Offered load** (fail) | `operator_rate` (required) | value >= 0.95 | 0.997 |  | pass | lab |
|  | `rid_rate` (required) | value >= 0.95 | 0.997 |  | pass | lab |
|  | `generator_on_time` (required) | value <= 0.001 | 0.0024395949603983557 |  | fail: generator_late_ratio value is 0.0024395949603983557, the criterion is <= 0.001 | lab |
| **Ingest-to-picture latency** (fail) | `operator_to_console_p99` (required) | p99 < 1 s | not measured | 61 | not measured: only 61 samples, the check needs 1000 | pending GCAA |
|  | `receiver_to_picture_p99` (required) | p99 < 1 s | p50 230.583 / p95 230.583 / p99 230.583 / max 230.583 | 40392 | fail: receiver_to_authority_picture_s p99 is 230.583, the criterion is < 1 | pending GCAA |
| **F3411 timing** (fail) | `sp_flights_p95` (required) | p95 <= 1 s | p50 0.095 / p95 0.707 / p99 1.132 / max 2.649 | 107504 | pass | standard |
|  | `sp_flights_p99` (required) | p99 <= 3 s | p50 0.095 / p95 0.707 / p99 1.132 / max 2.649 | 107504 | pass | standard |
|  | `dp_display_p95` (required) | p95 <= 1 s | p50 0.05 / p95 2.175 / p99 6.267 / max 7.802 | 3296 | fail: f3411_dp_display_s p95 is 2.175, the criterion is <= 1 | standard |
|  | `dp_display_p99` (required) | p99 <= 3 s | p50 0.05 / p95 2.175 / p99 6.267 / max 7.802 | 3296 | fail: f3411_dp_display_s p99 is 6.267, the criterion is <= 3 | standard |
|  | `details_p95` (required) | p95 <= 2 s | p50 0.065 / p95 0.588 / p99 0.991 / max 2.455 | 104433 | pass | standard |
|  | `details_p99` (required) | p99 <= 6 s | p50 0.065 / p95 0.588 / p99 0.991 / max 2.455 | 104433 | pass | standard |
|  | `dp_cache_24h` | value == 0 | not measured |  | not measured: the authority's display provider has cached for 0.4 h (its first network track); the 24 h bound is judged once it has cached for 25 h (a day, and the hour disposal is checked in) | standard |
| **F3548 timing** (partial) | `intent_change_notify` (required) | max <= 5 s | p50 0.214 / p95 1.224 / p99 1.479 / max 1.479 | 43 | pass | standard |
|  | `notifications_complete` (required) | value == 0 | 0 |  | pass | standard |
|  | `conflict_notify` | max <= 1 s | not measured |  | not measured: no intent of the run conflicts with another USS's: the peer files none, so no conflicting-intent notification is due | standard |
|  | `details_answered` (required) | max <= 1 s | p50 0.002 / p95 0.008 / p99 0.011 / max 0.011 | 32 | pass | standard |
|  | `constraint_notify` | max <= 5 s | not measured |  | not measured: no constraint is created during the run: no system of the lab stack publishes F3548 constraints (uss_qualifier has no constraint management at the pinned commit, WP-L7) | standard |
| **Alert latency** (not measured) | `proximity_raise_p99` (required) | p99 < 2 s | not measured |  | not measured: no sample was observed | pending GCAA |
|  | `zone_alert_tick` (required) | value <= 1 | not measured |  | not measured: 10 zone entries due, no zone raise observed (each missed one is a missed alert) | spec |
|  | `art13_notice` | max <= 5 s | not measured |  | not measured: no aircraft of the load deviates from its authorisation, so no Art. 13(2) notice is due; and the lab's U-space airspace is designated without the conformance monitoring service (services_required NID, GEO, FA, TI; 2021/664 Art. 3(3)) | pending GCAA |
| **Missed alerts** (fail) | `expected_set` (required) | value >= 1 | 100 |  | pass | lab |
|  | `missed_raises` (required) | value == 0 | 100 |  | fail: missed_raises value is 100, the criterion is == 0 | spec |
|  | `missed_clears` (required) | value == 0 | 0 |  | pass | spec |
|  | `no_false_alarm` (required) | value == 0 | 0 |  | pass | lab |
|  | `alerts_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **No silent loss** (fail) | `operator_identity` (required) | value == 0 | 5000 |  | fail: operator_ledgers_unbalanced value is 5000, the criterion is == 0 | spec |
|  | `receiver_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_identity` (required) | value == 0 | 940375 |  | fail: picture_silent_loss value is 940375, the criterion is == 0 | spec |
|  | `picture_traceable` (required) | value == 0 | 74462 |  | fail: picture_untraceable_frames value is 74462, the criterion is == 0 | lab |
|  | `traffic_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **CPA budget** (partial) | `pair_checks` (required) | value <= 1 | 0.001 |  | pass | spec |
|  | `evaluation_period` (required) | value <= 2 s | not measured |  | not measured: no proximity alert carried evaluation_period_s | spec |
| **Storage** (pass) | `writer_queue` (required) | value < 10 s | 1.112075351 |  | pass | spec |
|  | `compression` (required) | value == 0 | 0 |  | pass | spec |
|  | `disk_model` (required) | value <= 1.2 | 0.219 |  | pass | spec |
| **Restart** (not measured) | `restart_recovery` | value <= 10 s | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `restart_duplicates` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `restart_backlog` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Partition** (not measured) | `partition_replay` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `partition_isolation` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Cross-system outage** (not measured) | `outage_flagged` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Memory** (not measured) | `memory_monotonic` (required) | value == 0 | not measured |  | not measured: no system process had 6 resident-memory samples after the warm-up (17 processes read) | spec |
|  | `memory_growth` | value <= 1.5 | not measured |  | not measured: no system process had 6 resident-memory samples after the warm-up (17 processes read) | lab |
| **Vectors** (pass) | `vectors_pass` (required) | value == 0 | 0 |  | pass | spec |

## Loss accounting

- Operator telemetry: sent 2992101 = accepted 1185208 + refused 0 + dropped 1806893 + duplicate 0; 2983184 pending; 5000 clients unbalanced.
- Receivers: observed 1531952, sent 1531952 = accepted 1531952 + duplicates 0 + refused 0; shed 0; pending 0; 216089 samples accepted as backlog; 0 receivers unbalanced.
- Picture (timed console): handed 1196900, shown 40392, unshown 1156508, accounted 216133 (refused, dropped_frames 44, sheds, accepted as backlog), silent 940375; untraceable frames 74462.
- Traffic streams (watched flights): handed 28130, shown 61, unshown 28069, untraceable 0.
- Generator sheds: operator 0, Remote ID 0.

## The systems (read by the probes)

| Process | Resident memory first / last / max (MB) | Samples | Growth | Monotonic |
|---|---|---|---|---|
| ansp-api | 35.7 / 29.8 / 35.7 | 59 | 0 | false |
| ansp-manned-feed | 30.0 / 17.7 / 30.0 | 59 | 0 | false |
| authority-api | 52.7 / 39.6 / 53.9 | 59 | 0 | false |
| authority-detect | 31.0 / 82.8 / 82.8 | 59 | 0 | false |
| authority-dp-poller | 40.7 / 46.0 / 55.8 | 59 | 0 | false |
| authority-manned-ingest | 27.4 / 18.0 / 27.4 | 59 | 0 | false |
| authority-picture-ws | 55.7 / 89.2 / 89.2 | 59 | 0 | false |
| authority-rid-ingest | 148.9 / 127.6 / 148.9 | 58 | 0 | false |
| authority-tsdb-writer | 33.6 / 43.8 / 43.8 | 58 | 0 | false |
| cisp-api | 34.2 / 25.5 / 34.2 | 58 | 0 | false |
| ussp-api | 45.9 / 43.6 / 45.9 | 58 | 0 | false |
| ussp-dss-sync | 22.4 / 16.9 / 22.4 | 58 | 0 | false |
| ussp-monitor | 105.1 / 85.2 / 479.3 | 58 | 0 | false |
| ussp-rid-sp | 64.7 / 32.8 / 104.2 | 58 | 0 | false |
| ussp-telemetry-ingest | 555.7 / 933.0 / 1123.6 | 58 | 0 | false |
| ussp-traffic-ws | 75.3 / 102.9 / 108.0 | 58 | 0 | false |
| ussp-tsdb-writer | 39.0 / 36.1 / 39.0 | 58 | 0 | false |

| Database | Fleet samples | Before (MB) | After (MB) | Model (MB) | Growth / model | Compression lag (chunks) |
|---|---|---|---|---|---|---|
| ussp | 2992300 | 23.4 | 90.2 | 1795.4 | 0.037 | 0 |
| authority | 2992300 | 20.4 | 741.2 | 1795.4 | 0.401 | 0 |

- Pair checks of ussp-monitor: 45.054 a second over the run.

| Process | CPU over the run (cores) |
|---|---|
| ansp-api | 0.019 |
| ansp-manned-feed | 0.006 |
| authority-api | 0.014 |
| authority-detect | 0.073 |
| authority-dp-poller | 0.287 |
| authority-manned-ingest | 0.002 |
| authority-picture-ws | 0.723 |
| authority-rid-ingest | 0.662 |
| authority-tsdb-writer | 0.252 |
| cisp-api | 0.003 |
| ussp-api | 0.18 |
| ussp-dss-sync | 0.002 |
| ussp-monitor | 1.57 |
| ussp-rid-sp | 0.662 |
| ussp-telemetry-ingest | 2.056 |
| ussp-traffic-ws | 0.826 |
| ussp-tsdb-writer | 0.064 |

| Container (docker stats) | Samples | CPU mean (% of a core) | CPU max |
|---|---|---|---|
| uspace-demo-ansp-api-1 | 60 | 0.677 | 4.04 |
| uspace-demo-ansp-manned-adapter-1 | 60 | 0.045 | 1.02 |
| uspace-demo-ansp-manned-feed-1 | 60 | 0.634 | 2.75 |
| uspace-demo-ansp-nats-1 | 60 | 1.091 | 4.25 |
| uspace-demo-ansp-timescaledb-1 | 60 | 4.281 | 11.78 |
| uspace-demo-authority-api-1 | 60 | 1.784 | 8.98 |
| uspace-demo-authority-detect-1 | 60 | 4.203 | 21.16 |
| uspace-demo-authority-dp-poller-1 | 60 | 22.941 | 45.98 |
| uspace-demo-authority-manned-ingest-1 | 60 | 0.294 | 2.72 |
| uspace-demo-authority-nats-1 | 60 | 38.033 | 54.04 |
| uspace-demo-authority-picture-ws-1 | 60 | 53.643 | 185.4 |
| uspace-demo-authority-postgres-1 | 60 | 15.258 | 43.57 |
| uspace-demo-authority-rid-ingest-1 | 60 | 74.906 | 137.74 |
| uspace-demo-authority-tsdb-writer-1 | 60 | 17.29 | 35.87 |
| uspace-demo-caddy-1 | 60 | 176.297 | 269.17 |
| uspace-demo-cisp-api-1 | 60 | 1.567 | 6.99 |
| uspace-demo-cisp-deliver-1 | 60 | 1.639 | 7.28 |
| uspace-demo-cisp-nats-1 | 60 | 1.799 | 5.9 |
| uspace-demo-cisp-postgres-1 | 60 | 4.729 | 12.57 |
| uspace-demo-dss-1 | 60 | 2.357 | 8.11 |
| uspace-demo-dss-crdb-1 | 60 | 17.621 | 63.88 |
| uspace-demo-lab-issuer-1 | 60 | 2.147 | 5.86 |
| uspace-demo-ussp-api-1 | 60 | 7.975 | 50.14 |
| uspace-demo-ussp-dss-sync-1 | 60 | 1.321 | 7.36 |
| uspace-demo-ussp-monitor-1 | 60 | 169.115 | 247.36 |
| uspace-demo-ussp-nats-1 | 60 | 42.987 | 57.05 |
| uspace-demo-ussp-rid-sp-1 | 60 | 82.682 | 135.5 |
| uspace-demo-ussp-telemetry-ingest-1 | 60 | 269.331 | 451.6 |
| uspace-demo-ussp-timescaledb-1 | 60 | 17.594 | 92.19 |
| uspace-demo-ussp-traffic-ws-1 | 60 | 77.793 | 99.7 |
| uspace-demo-ussp-tsdb-writer-1 | 60 | 6.906 | 17.32 |

Loss counters that moved during the run (05 §7: accounted for in the report; the systems' own counters, listed, not judged):

- authority-rid-ingest `uspace_authority_batches_refused{component="rid_ingest"}`: +86320
- authority-rid-ingest `uspace_authority_gap_records_written{component="rid_ingest"}`: +212707
- authority-rid-ingest `uspace_authority_queue_shed_full_batches{component="rid_ingest"}`: +212707
- authority-rid-ingest `uspace_authority_queue_shed_observations{component="rid_ingest"}`: +905078
- authority-rid-ingest `uspace_authority_refused_busy{component="rid_ingest"}`: +138
- authority-rid-ingest `uspace_authority_refused_queue_full{component="rid_ingest"}`: +84108
- authority-tsdb-writer `uspace_authority_gaps{component="tsdb_writer"}`: +214847
- ussp-api `ussp_record_gaps_recorded{component="records"}`: +13056
- ussp-telemetry-ingest `ussp_drain_shed_age{component="telemetry_drain"}`: +19455
- ussp-telemetry-ingest `ussp_dropped_queue_full{component="telemetry"}`: +1772520
- ussp-telemetry-ingest `ussp_dropped_queue_full{component="telemetry_outbox"}`: +2803201
- ussp-telemetry-ingest `ussp_dropped_rate{component="telemetry"}`: +9799
- ussp-telemetry-ingest `ussp_events_dropped_queue_full{component="telemetry_outbox"}`: +836861
- ussp-telemetry-ingest `ussp_flights_telemetry_lost{component="flights"}`: +420545
- ussp-telemetry-ingest `ussp_gap_samples_dropped{component="telemetry_status"}`: +19455
- ussp-telemetry-ingest `ussp_gaps_recorded{component="telemetry_status"}`: +18029
- ussp-telemetry-ingest `ussp_replay_window_put_dropped{component="replay_window"}`: +79367
- ussp-traffic-ws `ussp_picture_dropped_after_silence{component="traffic_ws"}`: +34564

The display provider's cache (authority): caching for 0.4 h, 0 entries older than 24 h.

## 04 §1: the hot path's JSON share (estimate)

One encoding/json decode and encode of a 1013-byte track frame: 18.8 us on this host. an estimate: one encoding/json decode and encode of a 1013-byte track frame on this host against each process's CPU per message over the run; the systems expose no profile.

| Process | Messages (series) | CPU (s) | CPU per message (us) | JSON share |
|---|---|---|---|---|
| authority-picture-ws | 10021387 (uspace_authority_frames_sent{component="picture"}) | 578.57 | 57.734 | 0.325 |
| ussp-telemetry-ingest | 1145899 (ussp_accepted{component="telemetry"}) | 1638.72 | 1430.074 | 0.013 |
| ussp-traffic-ws | 37828 (ussp_traffic_ws_frames_sent{component="traffic_ws"}) | 657.66 | 17385.535 | 0.001 |

## Zone LOADZ1

10 entries due, 0 raised, 0 cleared, 10 missed, 0 clears missed, 0 unexpected; the slowest raise 0.00 ticks of 1 s.

- ac-04998: no zone raise for the entry at 40 s
- ac-04998: no zone raise for the entry at 160 s
- ac-04998: no zone raise for the entry at 280 s
- ac-04998: no zone raise for the entry at 400 s
- ac-04998: no zone raise for the entry at 520 s
- ac-04999: no zone raise for the entry at 40 s
- ac-04999: no zone raise for the entry at 160 s
- ac-04999: no zone raise for the entry at 280 s
- ac-04999: no zone raise for the entry at 400 s
- ac-04999: no zone raise for the entry at 520 s

## Vectors against the images

18 knowledge/vectors files; 42 s.

| System | Binaries (uspace-core) | Error |
|---|---|---|
| ansp | `/api` (v1.4.0), `/manned-adapter` (v1.4.0), `/manned-feed` (v1.4.0) |  |
| authority | `/uspace-authority` (v1.4.0), `/usr/libexec/uspace-authority/api` (v1.4.0), `/usr/libexec/uspace-authority/detect` (v1.4.0), `/usr/libexec/uspace-authority/dp-poller` (v1.4.0), `/usr/libexec/uspace-authority/manned-ingest` (v1.4.0), `/usr/libexec/uspace-authority/picture-ws` (v1.4.0), `/usr/libexec/uspace-authority/rid-ingest` (v1.4.0), `/usr/libexec/uspace-authority/tsdb-writer` (v1.4.0) |  |
| cisp | `/usr/local/bin/api` (v1.3.0), `/usr/local/bin/cispctl` (v1.3.0), `/usr/local/bin/deliver` (v1.3.0) |  |
| ussp | `/usr/bin/ussp-api` (v1.4.0), `/usr/bin/ussp-dss-sync` (v1.4.0), `/usr/bin/ussp-monitor` (v1.4.0), `/usr/bin/ussp-rid-sp` (v1.4.0), `/usr/bin/ussp-telemetry-ingest` (v1.4.0), `/usr/bin/ussp-traffic-ws` (v1.4.0), `/usr/bin/ussp-tsdb-writer` (v1.4.0) |  |

| uspace-core | Passed | Failed | Skipped | Vectors differing | Error |
|---|---|---|---|---|---|
| v1.3.0 | 711 | 0 | 30 |  |  |
| v1.4.0 | 711 | 0 | 30 |  |  |

## Peer USSP

https://peer.uspace.test (aircraft LABXCP0200N000000, LABXCP0200N000001, LABXCP0200N000002, LABXCP0200N000003). The display provider (authority-01) polled it 2985 times (others 1313); the timed console showed 3312 frames of its flights, 3296 displays timed, 12 frames untied; 4377 of the fleet's tracks from the USSP's SP were on it.

F3548: 16 intents inside the U-space square, 48 changes owed to the peer; 48 notifications received (map[Accepted:16 Activated:16 deleted:16]), 0 naming no intent of the run; details 32 answered, 0 failed.

## F3411 display-provider polls of the USSP

182 views, 107504 polls, 1571019 flights returned, 104433 details answered, 431 failures, 0 of them inside a fault the run injected (statuses map[200:211937 404:431]).

## Alerts

Expected 90, raised 0, cleared 0, missed 50, clears missed 0, unexpected 0, untraceable 0.

## Failures

- generator_on_time: generator_late_ratio value is 0.0024395949603983557, the criterion is <= 0.001
- operator_to_console_p99: required and not measured: only 61 samples, the check needs 1000
- receiver_to_picture_p99: receiver_to_authority_picture_s p99 is 230.583, the criterion is < 1
- dp_display_p95: f3411_dp_display_s p95 is 2.175, the criterion is <= 1
- dp_display_p99: f3411_dp_display_s p99 is 6.267, the criterion is <= 3
- proximity_raise_p99: required and not measured: no sample was observed
- zone_alert_tick: required and not measured: 10 zone entries due, no zone raise observed (each missed one is a missed alert)
- missed_raises: missed_raises value is 100, the criterion is == 0
- operator_identity: operator_ledgers_unbalanced value is 5000, the criterion is == 0
- picture_identity: picture_silent_loss value is 940375, the criterion is == 0
- picture_traceable: picture_untraceable_frames value is 74462, the criterion is == 0
- evaluation_period: required and not measured: no proximity alert carried evaluation_period_s
- memory_monotonic: required and not measured: no system process had 6 resident-memory samples after the warm-up (17 processes read)
- run error: intent of ac-02228 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/061bf8e6-ecf9-44c3-b695-11d614d5764a
- run error: intent of ac-01505 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0d4765eb-1dff-4ac9-b507-195b52e3f151
- run error: intent of ac-02335 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/112acfc2-1537-420d-8af9-2c3be0fb018f
- run error: intent of ac-03516 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/08cb64e6-5c5d-4dcb-9b0f-47fe667c9478
- run error: intent of ac-04325 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0524bb05-24a3-4ee3-84e8-2bc180682985
- run error: intent of ac-04282 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/16150fff-0671-4435-b199-127504ea0ebd
- run error: intent of ac-04271 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/109fbb12-12a0-4ee3-b4eb-fb201694601a
- run error: intent of ac-02140 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/03a96ea3-5331-422b-95b9-19ffc49855c1
- run error: intent of ac-03001 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/099db211-ece1-4b11-882b-cac33019b374
- run error: intent of ac-02164 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0ccc5173-1fc0-403b-b74f-1c7b2838726c
- run error: intent of ac-02346 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/16fe46ec-ff02-4e91-a81b-230ec302c12e
- run error: intent of ac-01322 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/141c88eb-6e41-4f6e-8dcb-d4b8e565f254
- run error: intent of ac-04314 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/1017de58-1716-4fef-8650-75f31af5ee7b
- run error: intent of ac-04422 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/052b8d0d-1a31-4a14-bdcf-7b0873468ae6
- run error: intent of ac-00463 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/179139b9-7208-453b-b659-729445b928ac
- run error: intent of ac-04554 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/187e4268-3757-4c66-9201-2c7831b52e2d
- run error: intent of ac-04103 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/09f646b8-8058-4d89-8bb5-71314cf40a17
- run error: intent of ac-04849 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0b778bcb-798d-4bfe-a071-27024a05d924
- run error: intent of ac-02490 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0c78d1cc-f207-4dff-adbe-f605bce22f91
- run error: intent of ac-00838 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/12f4e0b4-9171-4b1c-816d-fb7d9f7de5be
- run error: intent of ac-00190 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/09f32b24-760c-4d85-8f82-d32c29f65884
- run error: intent of ac-03538 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/127c41ac-d8ff-433f-b354-f87cb0899320
- run error: intent of ac-00177 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/167bd110-e81e-418f-b38b-54d937f19560
- run error: intent of ac-04492 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/120f5d5d-399f-42e5-a4f7-7e3050c94218
- run error: intent of ac-03043 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/096194d3-416a-4886-8258-12061769243e
- run error: intent of ac-00554 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/16597f03-e597-4748-bebd-1f1f8c1609f5
- run error: intent of ac-04241 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0fc6c0ca-7b9e-4ffd-8747-0a84281a71e6
- run error: intent of ac-04502 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0791b949-0c39-43f9-a597-a54a66e332df
- run error: intent of ac-04136 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/182315b3-dd33-4783-94de-e3f119c6a9e1
- run error: intent of ac-00714 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/072bae1e-5ec2-4663-a8ab-61f433275d3e
- run error: intent of ac-02613 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0e1e261a-5b25-46df-9e35-58fb63b0ca9b
- run error: intent of ac-01625 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/01b283e2-e272-4e93-85a1-11a950a5fda1
- run error: intent of ac-00444 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/13f0cd12-c385-4816-9557-0592c69c15ff
- run error: intent of ac-01641 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/08c8cfde-cd50-4a99-b5ff-8a93c0cc47b0
- run error: intent of ac-03608 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/08da9095-d7db-478f-bb9b-cf5ed5e705de
- run error: intent of ac-03155 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/09ae2de9-afe4-44ca-ae87-d7e1cad2797b
- run error: intent of ac-03869 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/1022b84d-3b0e-44cc-8df5-7163b5554027
- run error: intent of ac-02292 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0a4c62b8-7e50-4523-be9d-24625318c73a
- run error: intent of ac-04247 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0e8e628c-d3b3-47ba-8cfe-f425bb4b8c2b
- run error: intent of ac-04730 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/12f0b17a-0213-4312-a3c0-a3eba81282b7
- run error: intent of ac-02059 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/131f04cb-9a54-4fb6-8d63-c0c4881fce6d
- run error: intent of ac-01720 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0767072d-c771-4e6a-91ae-25c5661d3fa3
- run error: intent of ac-04942 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0a6dbb50-0ec6-4548-9078-79153e6bbd4d
- run error: intent of ac-00731 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/14fa697a-ab6a-4c43-b355-8b47abce02ce
- run error: intent of ac-03184 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/02efdc3f-f312-4ae3-8f62-c785e3f7f2f3
- run error: intent of ac-00613 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/04da3ceb-6f99-4f55-b1ae-4b966e8a4ce0
- run error: intent of ac-04842 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/10c036b5-5903-4bcb-9aad-773a8825257a
- run error: intent of ac-00286 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0fae8e42-e128-4081-a260-90877e83765a
- run error: intent of ac-02149 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/15f4b617-c559-487d-86cf-eb331cd78e8c
- run error: intent of ac-01171 not ended: simop: end answered 409: {"type":"https://schemas.uspace.ge/problems/end_refused","title":"Conflict","status":409,"detail":"the intent is ended and cannot be ended","instance":"/v1/intents/0f81fbd5-b0f7-42ca-94a6-62b02f25c49a
- run error: and 1082 more intents not ended
- run error: drain: not settled within drain_s 120 s
- run error: stream console-00 broke 1 times outside every fault and credential renewal (1 connections); last: console-00: upgrade answered 429
- run error: stream console-08 broke 1 times outside every fault and credential renewal (1 connections); last: console-08: upgrade answered 429
- run error: stream console-10 broke 1 times outside every fault and credential renewal (1 connections); last: console-10: upgrade answered 429
- run error: stream console-14 broke 1 times outside every fault and credential renewal (1 connections); last: console-14: upgrade answered 429
- run error: stream console-22 broke 1 times outside every fault and credential renewal (1 connections); last: console-22: upgrade answered 429
- run error: stream console-26 broke 1 times outside every fault and credential renewal (1 connections); last: console-26: upgrade answered 429
- run error: stream console-28 broke 1 times outside every fault and credential renewal (2 connections); last: console-28: failed to get reader: received close frame: status = StatusTryAgainLater and reason = "the session could not be checked; reconnect later"
- run error: stream console-37 broke 1 times outside every fault and credential renewal (1 connections); last: console-37: upgrade answered 429
- run error: stream console-38 broke 1 times outside every fault and credential renewal (1 connections); last: console-38: upgrade answered 429
- run error: stream console-42 broke 1 times outside every fault and credential renewal (1 connections); last: console-42: upgrade answered 429
- run error: stream console-44 broke 1 times outside every fault and credential renewal (1 connections); last: console-44: upgrade answered 429
- run error: stream console-48 broke 1 times outside every fault and credential renewal (1 connections); last: console-48: upgrade answered 429
- run error: stream console-49 broke 1 times outside every fault and credential renewal (1 connections); last: console-49: upgrade answered 429
- run error: stream console-50 broke 1 times outside every fault and credential renewal (1 connections); last: console-50: upgrade answered 429
- run error: stream console-51 broke 1 times outside every fault and credential renewal (1 connections); last: console-51: upgrade answered 429
- run error: stream console-52 broke 1 times outside every fault and credential renewal (1 connections); last: console-52: upgrade answered 429
- run error: stream console-57 broke 1 times outside every fault and credential renewal (1 connections); last: console-57: upgrade answered 429
- run error: stream console-59 broke 1 times outside every fault and credential renewal (1 connections); last: console-59: upgrade answered 429
- run error: F3411: 431 of the SP requests failed outside any fault (statuses map[200:211937 404:431]; last: 2026-10-14T21:41:32Z GET /uss/flights/{id}/details answered 404)


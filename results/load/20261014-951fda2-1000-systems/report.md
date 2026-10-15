# Load run 20261014-951fda2-1000-systems: tier 1000

**Verdict: FAIL.** 40 checks measured, 4 not measured (05 §7 is not fully observed by this run).

- Target: lab-load-1000 (systems). The systems' images as the targets file names them, seeded for this fleet (loadgen seed); evidence for the images and digests listed, on the host named, when the run passes (docs/PLAN.md L-D4).
- Receivers: each receiver under its own address of 10.77.0.0/16, named in X-Forwarded-For, which the lab Caddy and the authority trust from the host only (deploy/systems).
- Image ansp: `ghcr.io/rootxkit/uspace-ansp@sha256:1fcfb3c9762189f737250c65ad24166c025c12576abb7babffcf07d705a6921a`
- Image authority: `ghcr.io/rootxkit/uspace-authority@sha256:53d6f97cd745677da893a62669f45477d1eaa403b369bc174b6ccc8c11ee11cc`
- Image cisp: `ghcr.io/rootxkit/uspace-cisp@sha256:2e7861488e178de967829e7ebb623a3c5b480f0dcca8e5ee1acab4e45028f42a`
- Image ussp: `ghcr.io/rootxkit/uspace-ussp@sha256:854cca7146277e6dc767cd7efce279f350994c9f152344a8af457c917db5935d`
- Host: dev workstation (Windows 11, 16 logical CPUs, 16 GB); the systems stack on the same host in Docker Desktop 28.5.1 (16 CPUs, 7.7 GB), other projects' containers beside it: windows/amd64, 16 CPUs, memory unknown, go1.27.1. the host the generator ran on (L-Q1); where the systems ran is the --host name's to say
- Commits: uspace-lab `951fda2a15bcf06cb42aa26e441187bf2642b256`, uspace-core `v1.6.0`.
- Policy: version 1 (`sha256:19124b6573ade992e321dbb2f18313bf01ec7ac262db03d75216da38f94db8df`); figures marked pending GCAA are the spec's defaults until GCAA answers.
- Ran 2026-10-14T16:09:16Z to 2026-10-14T17:13:12Z; the generator ran 3600 s of the tier's 3600 s.

## Scaled from 05 §1 / 05 §7

| Quantity | Spec | This run | Factor | Why |
|---|---|---|---|---|
| console viewports | 30 consoles each viewing <= 200 tracks (05 §1) | the same count, each subscribed to the whole area | 1 | the reference picture has no viewport throttle; whole-area subscriptions are the worst case |

## Not generated

- ANSP manned feed (tens of aircraft): the reference target has no manned intake; in a systems run the ANSP adapter polls a sim-adsb the load harness does not serve, so the USSP shows manned traffic unavailable (its degraded state, in the report)
- the second USSP's own intents: in a systems run the harness's peer USSP (peer) is the second USSP on the lab DSS: it serves its own flights and is notified of the fleet's intents in the U-space square, but files no intent of its own, so no conflict between the two USSPs is made (the reference target has no DSS or peer)
- authority DP polls of the USSPs (05 §1: one view per 1 Hz), against the reference target: the reference target has no SP; in a systems run the authority's display provider polls the USSP's and the peer's SPs over the oversight area the seed adds, and the lab polls the USSP's SP as a display provider too (dp)
- optional national push USSP -> authority: disabled by default (02 F6) and not in the reference target

## Offered load

| Quantity | Observed | Tier target |
|---|---|---|
| Operator telemetry (msg/s) | 1000.3 | 1000.0 |
| Remote ID locations (msg/s) | 400.0 | 400.0 |
| Console frames in (frames/s, all consoles) | 35883.8 | |
| Aircraft / operator clients / heard / receivers / consoles / watched | 1000 / 50 / 400 / 20 / 30 / 47 | |
| Samples handed out more than a period late | 0 | |
| Intents filed (errors) | 1000 (0) | |
| Intents ended after the run | 1000 | |

## 05 §7

| Property | Check | Criterion | Observed | n | Result | Figure from |
|---|---|---|---|---|---|---|
| **Offered load** (pass) | `operator_rate` (required) | value >= 0.95 | 1 |  | pass | lab |
|  | `rid_rate` (required) | value >= 0.95 | 1 |  | pass | lab |
|  | `generator_on_time` (required) | value <= 0.001 | 0 |  | pass | lab |
| **Ingest-to-picture latency** (fail) | `operator_to_console_p99` (required) | p99 < 1 s | p50 0.539 / p95 1.016 / p99 1.418 / max 2.512 | 162148 | fail: operator_to_ussp_console_s p99 is 1.418, the criterion is < 1 | pending GCAA |
|  | `receiver_to_picture_p99` (required) | p99 < 1 s | p50 0.074 / p95 0.247 / p99 1.199 / max 63.97 | 1261187 | fail: receiver_to_authority_picture_s p99 is 1.199, the criterion is < 1 | pending GCAA |
| **F3411 timing** (partial) | `sp_flights_p95` (required) | p95 <= 1 s | p50 0.009 / p95 0.053 / p99 0.114 / max 0.997 | 176046 | pass | standard |
|  | `sp_flights_p99` (required) | p99 <= 3 s | p50 0.009 / p95 0.053 / p99 0.114 / max 0.997 | 176046 | pass | standard |
|  | `dp_display_p95` (required) | p95 <= 1 s | p50 0.014 / p95 0.1 / p99 0.207 / max 64.202 | 13096 | pass | standard |
|  | `dp_display_p99` (required) | p99 <= 3 s | p50 0.014 / p95 0.1 / p99 0.207 / max 64.202 | 13096 | pass | standard |
|  | `details_p95` (required) | p95 <= 2 s | p50 0.004 / p95 0.029 / p99 0.07 / max 0.958 | 175694 | pass | standard |
|  | `details_p99` (required) | p99 <= 6 s | p50 0.004 / p95 0.029 / p99 0.07 / max 0.958 | 175694 | pass | standard |
|  | `dp_cache_24h` | value == 0 | not measured |  | not measured: the authority's display provider has cached for 2.2 h (its first network track); the 24 h bound is judged once it has cached for 25 h (a day, and the hour disposal is checked in) | standard |
| **F3548 timing** (partial) | `intent_change_notify` (required) | max <= 5 s | p50 0.457 / p95 1.028 / p99 1.583 / max 1.583 | 48 | pass | standard |
|  | `notifications_complete` (required) | value == 0 | 0 |  | pass | standard |
|  | `conflict_notify` | max <= 1 s | not measured |  | not measured: no intent of the run conflicts with another USS's: the peer files none, so no conflicting-intent notification is due | standard |
|  | `details_answered` (required) | max <= 1 s | p50 0.004 / p95 0.017 / p99 0.027 / max 0.027 | 72 | pass | standard |
|  | `constraint_notify` | max <= 5 s | not measured |  | not measured: no constraint is created during the run: no system of the lab stack publishes F3548 constraints (uss_qualifier has no constraint management at the pinned commit, WP-L7) | standard |
| **Alert latency** (fail) | `proximity_raise_p99` (required) | p99 < 2 s | p50 0.051 / p95 0.241 / p99 1.209 / max 1.292 | 600 | pass | pending GCAA |
|  | `zone_alert_tick` (required) | value <= 1 | 2.443 |  | fail: zone_alert_ticks value is 2.443, the criterion is <= 1 | spec |
|  | `art13_notice` | max <= 5 s | not measured |  | not measured: no aircraft of the load deviates from its authorisation, so no Art. 13(2) notice is due; and the lab's U-space airspace is designated without the conformance monitoring service (services_required NID, GEO, FA, TI; 2021/664 Art. 3(3)) | pending GCAA |
| **Missed alerts** (fail) | `expected_set` (required) | value >= 1 | 660 |  | pass | lab |
|  | `missed_raises` (required) | value == 0 | 0 |  | pass | spec |
|  | `missed_clears` (required) | value == 0 | 4 |  | fail: missed_clears value is 4, the criterion is == 0 | spec |
|  | `no_false_alarm` (required) | value == 0 | 2 |  | fail: unexpected_alerts value is 2, the criterion is == 0 | lab |
|  | `alerts_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **No silent loss** (fail) | `operator_identity` (required) | value == 0 | 1000 |  | fail: operator_ledgers_unbalanced value is 1000, the criterion is == 0 | spec |
|  | `receiver_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_traceable` (required) | value == 0 | 1194 |  | fail: picture_untraceable_frames value is 1194, the criterion is == 0 | lab |
|  | `traffic_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **CPA budget** (pass) | `pair_checks` (required) | value <= 1 | 0.237 |  | pass | spec |
|  | `evaluation_period` (required) | value <= 2 s | 1 |  | pass | spec |
| **Storage** (fail) | `writer_queue` (required) | value < 10 s | 1.21982123 |  | pass | spec |
|  | `compression` (required) | value == 0 | 0 |  | pass | spec |
|  | `disk_model` (required) | value <= 1.2 | 2.483 |  | fail: disk_growth_vs_model value is 2.483, the criterion is <= 1.2 | spec |
| **Restart** (fail) | `restart_recovery` (required) | value <= 10 s | 2.418 |  | pass | spec |
|  | `restart_duplicates` (required) | value == 0 | 164 |  | fail: restart_duplicate_alerts value is 164, the criterion is == 0 | spec |
|  | `restart_backlog` (required) | value == 0 | 2210841 |  | fail: restart_lost_backlog value is 2210841, the criterion is == 0 | spec |
| **Partition** (pass) | `partition_replay` (required) | value == 0 | 0 |  | pass | spec |
|  | `partition_isolation` (required) | value == 0 | 0 |  | pass | spec |
| **Cross-system outage** (fail) | `outage_flagged` (required) | value == 0 | 2 |  | fail: outage_unflagged value is 2, the criterion is == 0 | spec |
| **Memory** (fail) | `memory_monotonic` (required) | value == 0 | 1 |  | fail: memory_monotonic value is 1, the criterion is == 0 | spec |
|  | `memory_growth` | value <= 1.5 | 4.102 |  | fail: memory_growth_ratio value is 4.102, the criterion is <= 1.5 | lab |
| **Vectors** (pass) | `vectors_pass` (required) | value == 0 | 0 |  | pass | spec |

## Loss accounting

- Operator telemetry: sent 3601143 = accepted 3598994 + refused 0 + dropped 0 + duplicate 1006; 2210841 pending; 1000 clients unbalanced.
- Receivers: observed 1842116, sent 1842116 = accepted 1761513 + duplicates 0 + refused 80603; shed 0; pending 0; 97542 samples accepted as backlog; 0 receivers unbalanced.
- Picture (timed console): handed 1440000, shown 1261187, unshown 178813, accounted 191186 (refused, dropped_frames 13041, sheds, accepted as backlog), silent 0; untraceable frames 1194.
- Traffic streams (watched flights): handed 169200, shown 162148, unshown 7052, untraceable 0.
- Generator sheds: operator 0, Remote ID 0.

## The systems (read by the probes)

| Process | Resident memory first / last / max (MB) | Samples | Growth | Monotonic |
|---|---|---|---|---|
| ansp-api | 39.8 / 31.6 / 168.1 | 306 | 1.037 | false |
| ansp-manned-feed | 31.1 / 16.0 / 31.2 | 308 | 1.038 | false |
| authority-api | 51.9 / 35.3 / 72.7 | 316 | 0.918 | false |
| authority-detect | 32.5 / 43.3 / 60.6 | 315 | 0.94 | false |
| authority-dp-poller | 36.1 / 27.6 / 141.0 | 316 | 1.008 | false |
| authority-manned-ingest | 27.5 / 15.8 / 27.5 | 317 | 1.039 | false |
| authority-picture-ws | 40.0 / 36.0 / 64.0 | 316 | 0.864 | false |
| authority-rid-ingest | 128.0 / 50.7 / 128.0 | 316 | 0.983 | false |
| authority-tsdb-writer | 37.1 / 32.4 / 156.8 | 316 | 0.974 | false |
| cisp-api | 35.4 / 29.4 / 106.8 | 300 | 1.131 | false |
| ussp-api | 46.2 / 45.5 / 393.6 | 330 | 0.911 | false |
| ussp-dss-sync | 22.6 / 20.3 / 23.2 | 329 | 0.93 | false |
| ussp-monitor | 47.6 / 42.2 / 102.3 | 329 | 1.002 | false |
| ussp-rid-sp | 38.1 / 30.0 / 177.1 | 329 | 1.064 | false |
| ussp-telemetry-ingest | 158.5 / 1457.3 / 1457.7 | 328 | 4.102 | true |
| ussp-traffic-ws | 43.0 / 40.5 / 102.4 | 329 | 1.006 | false |
| ussp-tsdb-writer | 35.3 / 44.6 / 49.4 | 329 | 0.996 | false |

| Database | Fleet samples | Before (MB) | After (MB) | Model (MB) | Growth / model | Compression lag (chunks) |
|---|---|---|---|---|---|---|
| ussp | 3600000 | 223.0 | 1877.3 | 2160.0 | 0.766 | 0 |
| authority | 3600000 | 985.8 | 10059.7 | 2160.0 | 4.201 | 0 |

- Pair checks of ussp-monitor: 11859.494 a second over the run.

| Process | CPU over the run (cores) |
|---|---|
| ansp-api | 0.031 |
| ansp-manned-feed | 0.005 |
| authority-api | 0.011 |
| authority-detect | 0.161 |
| authority-dp-poller | 0.349 |
| authority-manned-ingest | 0.002 |
| authority-picture-ws | 1.493 |
| authority-rid-ingest | 0.274 |
| authority-tsdb-writer | 0.312 |
| cisp-api | 0.003 |
| ussp-api | 0.069 |
| ussp-dss-sync | 0.001 |
| ussp-monitor | 0.355 |
| ussp-rid-sp | 0.738 |
| ussp-telemetry-ingest | 0.706 |
| ussp-traffic-ws | 0.245 |
| ussp-tsdb-writer | 0.119 |

| Container (docker stats) | Samples | CPU mean (% of a core) | CPU max |
|---|---|---|---|
| uspace-demo-ansp-api-1 | 327 | 3.281 | 85.33 |
| uspace-demo-ansp-manned-adapter-1 | 327 | 0.036 | 0.33 |
| uspace-demo-ansp-manned-feed-1 | 328 | 0.423 | 1.41 |
| uspace-demo-ansp-nats-1 | 329 | 1.983 | 10.32 |
| uspace-demo-ansp-timescaledb-1 | 330 | 4.642 | 18.63 |
| uspace-demo-authority-api-1 | 325 | 1.344 | 7.64 |
| uspace-demo-authority-detect-1 | 326 | 18.665 | 116.55 |
| uspace-demo-authority-dp-poller-1 | 326 | 40.708 | 78.06 |
| uspace-demo-authority-manned-ingest-1 | 327 | 0.14 | 1.81 |
| uspace-demo-authority-nats-1 | 322 | 47.803 | 124.8 |
| uspace-demo-authority-picture-ws-1 | 328 | 168.934 | 270.72 |
| uspace-demo-authority-postgres-1 | 330 | 35.511 | 97.77 |
| uspace-demo-authority-rid-ingest-1 | 327 | 30.743 | 146.74 |
| uspace-demo-authority-tsdb-writer-1 | 329 | 35.305 | 112.52 |
| uspace-demo-caddy-1 | 360 | 109.157 | 362.28 |
| uspace-demo-cisp-api-1 | 328 | 1.164 | 10.72 |
| uspace-demo-cisp-deliver-1 | 328 | 1.361 | 8.23 |
| uspace-demo-cisp-nats-1 | 329 | 1.256 | 4.53 |
| uspace-demo-cisp-postgres-1 | 330 | 3.31 | 12.82 |
| uspace-demo-dss-1 | 329 | 1.746 | 14.94 |
| uspace-demo-dss-crdb-1 | 360 | 43.648 | 282.82 |
| uspace-demo-lab-issuer-1 | 360 | 1.838 | 6.24 |
| uspace-demo-ussp-api-1 | 360 | 6.338 | 112.84 |
| uspace-demo-ussp-dss-sync-1 | 360 | 0.871 | 14.43 |
| uspace-demo-ussp-monitor-1 | 360 | 36.339 | 104.37 |
| uspace-demo-ussp-nats-1 | 354 | 59.296 | 139.33 |
| uspace-demo-ussp-rid-sp-1 | 359 | 76.928 | 137.65 |
| uspace-demo-ussp-telemetry-ingest-1 | 360 | 72.716 | 364.33 |
| uspace-demo-ussp-timescaledb-1 | 360 | 13.574 | 77.33 |
| uspace-demo-ussp-traffic-ws-1 | 360 | 24.648 | 127.04 |
| uspace-demo-ussp-tsdb-writer-1 | 360 | 13.198 | 64.76 |

Loss counters that moved during the run (05 §7: accounted for in the report; the systems' own counters, listed, not judged):

- ansp-api `notices_sender_quota_refused`: +2692
- authority-api `uspace_authority_occurrences_refused_key_unavailable{component="occurrences"}`: +3814
- authority-dp-poller `uspace_authority_gap_records_lost{component="dp_rows"}`: +124
- authority-dp-poller `uspace_authority_rows_lost_handover_failed{component="dp_rows"}`: +1646
- authority-dp-poller `uspace_authority_rows_shed_queue_full{component="dp_rows"}`: +79494
- authority-picture-ws `uspace_authority_http_rate_limited{component="http"}`: +229
- authority-rid-ingest `uspace_authority_batches_refused{component="rid_ingest"}`: +8704
- authority-rid-ingest `uspace_authority_refused_busy{component="rid_ingest"}`: +25
- authority-rid-ingest `uspace_authority_refused_queue_unavailable{component="rid_ingest"}`: +8639
- authority-tsdb-writer `uspace_authority_gaps_deduplicated{component="tsdb_writer"}`: +1
- authority-tsdb-writer `uspace_authority_gaps{component="tsdb_writer"}`: +8601
- ussp-monitor `ussp_monitor_publish_dropped{component="conformance"}`: +8
- ussp-telemetry-ingest `ussp_replay_window_put_dropped{component="replay_window"}`: +9859
- ussp-traffic-ws `ussp_picture_dropped_after_silence{component="traffic_ws"}`: +2004

The display provider's cache (authority): caching for 2.2 h, 0 entries older than 24 h.

Probes that failed inside a fault the run injected: 175.

## 04 §1: the hot path's JSON share (estimate)

One encoding/json decode and encode of a 1011-byte track frame: 8.9 us on this host. an estimate: one encoding/json decode and encode of a 1011-byte track frame on this host against each process's CPU per message over the run; the systems expose no profile.

| Process | Messages (series) | CPU (s) | CPU per message (us) | JSON share |
|---|---|---|---|---|
| authority-picture-ws | 128069754 (uspace_authority_frames_sent{component="picture"}) | 5470.81 | 42.717 | 0.209 |
| ussp-telemetry-ingest | 3593042 (ussp_accepted{component="telemetry"}) | 2585.25 | 719.516 | 0.012 |
| ussp-traffic-ws | 254068 (ussp_traffic_ws_frames_sent{component="traffic_ws"}) | 896.87 | 3530.039 | 0.003 |

## Zone LOADZ1

60 entries due, 60 raised, 56 cleared, 0 missed, 4 clears missed, 2 unexpected; the slowest raise 2.44 ticks of 1 s.

- ac-00998: the raise for the entry at 280 s was not cleared within 30 s of the exit at 321 s
- ac-00998: the raise for the entry at 1360 s was not cleared within 30 s of the exit at 1401 s
- ac-00999: the raise for the entry at 280 s was not cleared within 30 s of the exit at 321 s
- ac-00999: the raise for the entry at 1360 s was not cleared within 30 s of the exit at 1401 s
- ac-00998: a zone raise at 371.4 s outside every entry
- ac-00999: a zone raise at 371.4 s outside every entry

## Vectors against the images

18 knowledge/vectors files; 32 s.

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

https://peer.uspace.test (aircraft LABXCP0050N000000, LABXCP0050N000001, LABXCP0050N000002, LABXCP0050N000003). The display provider (authority-01) polled it 10460 times (others 3762); the timed console showed 13096 frames of its flights, 13096 displays timed, 0 frames untied; 1000 of the fleet's tracks from the USSP's SP were on it.

F3548: 16 intents inside the U-space square, 48 changes owed to the peer; 88 notifications received (map[Accepted:16 Activated:28 Nonconforming:28 deleted:16]), 0 naming no intent of the run; details 72 answered, 0 failed.

## Faults

| Row | Script | At (s) | Hold (s) | Picture back after restore (s) | Traffic back (s) | Error |
|---|---|---|---|---|---|---|
| restart | `adapter.sh ussp-telemetry-ingest` | 300 | 5 | 0.065 | 2.418 |  |
| restart | `hotpath.sh ussp-monitor` | 420 | 5 | 0.009 | 0.294 |  |
| restart | `hotpath.sh ussp-traffic-ws` | 540 | 5 | 0.008 | 0.449 |  |
| restart | `hotpath.sh ussp-rid-sp` | 660 | 5 | 0.016 | 0.052 |  |
| restart | `hotpath.sh ussp-tsdb-writer` | 780 | 5 | 0.003 | 0.261 |  |
| restart | `adapter.sh authority-rid-ingest` | 900 | 5 | 1.162 | 0.096 |  |
| restart | `hotpath.sh authority-detect` | 1020 | 5 | 0.011 | 0.761 |  |
| restart | `hotpath.sh authority-picture-ws` | 1140 | 5 | 0.005 | 0.567 |  |
| restart | `hotpath.sh authority-tsdb-writer` | 1260 | 5 | 0.008 | 0.668 |  |
| partition | `nats.sh ussp` | 1380 | 60 | 0.025 | 2.902 |  |
| partition | `nats.sh authority` | 1500 | 60 | 7.049 | 0.025 |  |
| outage | `system.sh cisp` | 1620 | 300 | 0 | 0.228 |  |
| outage | `system.sh authority` | 2040 | 300 | 15.633 | 0.368 |  |
| outage | `dss.sh dss` | 2490 | 300 | 0.019 | 0.364 |  |
| outage | `system.sh ansp` | 2940 | 300 | 0.047 | 0.003 |  |

Outage of cisp (`system.sh cisp`, 300 s), judged on 168 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| cisp ready lost | yes | cisp not ready from 6s after the injection to the restore (117 samples) | the CISP is down (the fault, seen in place) |
| others ready always | yes | authority, ussp, ansp, issuer, dss ready in all 117 samples | 05 §6 CISP: subscribers run on cache; 02 F3: they keep serving |
| ansp cisp in degraded|down|stale|unknown | yes | ansp cisp down 53s after the injection (down since 2026-10-14T16:39:18Z (serving age 64 s); uspace_airspace: the CISP answered 502; ussp_list: no version published yet; ussp_list: the CISP answered 502; restrictions: no version published ye...) | 02 F2: the ANSP raises an alarm and takes its degraded path (lab bound 90 s: its CISP check polls) |
| ussp cis age grows | yes | ussp cis age from 55 s to 371 s over the hold (117 samples) | 05 §6 CISP: subscribers run on cache with cis_age_s (05 §7: everything flagged with age) |
| all ready always | yes | every system ready 1s after the restore | the stack recovers after the restore |

Outage of authority (`system.sh authority`, 300 s), judged on 191 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| authority ready lost | yes | authority not ready from 6s after the injection to the restore (124 samples) | the authority is down (the fault, seen in place) |
| others ready always | **no** | ansp not ready in 81 of 124 samples (last: 503  jwks=down) | 05 §6 Authority: no real-time service depends on the authority; the CISP keeps serving |
| ussp registry age grows | yes | ussp registry age from 25 s to 351 s over the hold (124 samples) | 05 §6 Authority, 02 F8: USSPs use the validity cache (05 §7: flagged with age) |
| all ready always | yes | every system ready 2s after the restore | the stack recovers after the restore |

Outage of dss (`dss.sh dss`, 300 s), judged on 177 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| dss ready lost | yes | dss not ready from 4s after the injection to the restore (115 samples) | the DSS is down (the fault, seen in place) |
| ussp dss in down|degraded | yes | ussp dss down 36s after the injection (f3411: down since 2026-10-14T16:53:32Z: answers 502 / f3548: uss_availability Unknown) | 02 F6: the USSP shows DSS state (lab bound 90 s: its DSS check polls) |
| others ready always | **no** | cisp not ready in 1 of 115 samples (last: 503  migrations=unknown: migrate relational status: failed to initialize: context deadline exceeded) | 05 §6 DSS: local intents and all in-flight services continue |
| all ready always | yes | every system ready 2s after the restore | the stack recovers after the restore |

Outage of ansp (`system.sh ansp`, 300 s), judged on 175 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| ansp ready lost | yes | ansp not ready from 6s after the injection to the restore (121 samples) | the ANSP is down (the fault, seen in place) |
| others ready always | yes | authority, cisp, ussp, issuer, dss ready in all 121 samples | 05 §6 ANSP feed: no other system stops with it |
| traffic shows manned | yes | the traffic stream showed manned 0s after the injection | 05 §6 ANSP feed: USSP traffic information marks manned traffic unavailable |
| all ready always | yes | every system ready 1s after the restore | the stack recovers after the restore |

## F3411 display-provider polls of the USSP

49 views, 176199 polls, 3809133 flights returned, 175694 details answered, 154 failures, 154 of them inside a fault the run injected (statuses map[200:351740 404:1 502:153]).

## Alerts

Expected 600, raised 600, cleared 600, missed 0, clears missed 0, unexpected 0, untraceable 0.

## Failures

- operator_to_console_p99: operator_to_ussp_console_s p99 is 1.418, the criterion is < 1
- receiver_to_picture_p99: receiver_to_authority_picture_s p99 is 1.199, the criterion is < 1
- zone_alert_tick: zone_alert_ticks value is 2.443, the criterion is <= 1
- missed_clears: missed_clears value is 4, the criterion is == 0
- no_false_alarm: unexpected_alerts value is 2, the criterion is == 0
- operator_identity: operator_ledgers_unbalanced value is 1000, the criterion is == 0
- picture_traceable: picture_untraceable_frames value is 1194, the criterion is == 0
- disk_model: disk_growth_vs_model value is 2.483, the criterion is <= 1.2
- restart_duplicates: restart_duplicate_alerts value is 164, the criterion is == 0
- restart_backlog: restart_lost_backlog value is 2210841, the criterion is == 0
- outage_flagged: outage_unflagged value is 2, the criterion is == 0
- memory_monotonic: memory_monotonic value is 1, the criterion is == 0
- memory_growth: memory_growth_ratio value is 4.102, the criterion is <= 1.5
- run error: drain: not settled within drain_s 60 s


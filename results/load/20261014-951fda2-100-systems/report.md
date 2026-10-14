# Load run 20261014-951fda2-100-systems: tier 100

**Verdict: FAIL.** 40 checks measured, 4 not measured (05 §7 is not fully observed by this run).

- Target: lab-load-100 (systems). The systems' images as the targets file names them, seeded for this fleet (loadgen seed); evidence for the images and digests listed, on the host named, when the run passes (docs/PLAN.md L-D4).
- Receivers: each receiver under its own address of 10.77.0.0/16, named in X-Forwarded-For, which the lab Caddy and the authority trust from the host only (deploy/systems).
- Image ansp: `ghcr.io/rootxkit/uspace-ansp@sha256:1fcfb3c9762189f737250c65ad24166c025c12576abb7babffcf07d705a6921a`
- Image authority: `ghcr.io/rootxkit/uspace-authority@sha256:53d6f97cd745677da893a62669f45477d1eaa403b369bc174b6ccc8c11ee11cc`
- Image cisp: `ghcr.io/rootxkit/uspace-cisp@sha256:2e7861488e178de967829e7ebb623a3c5b480f0dcca8e5ee1acab4e45028f42a`
- Image ussp: `ghcr.io/rootxkit/uspace-ussp@sha256:854cca7146277e6dc767cd7efce279f350994c9f152344a8af457c917db5935d`
- Host: dev workstation (Windows 11, 16 logical CPUs, 16 GB); the systems stack on the same host in Docker Desktop 28.5.1 (16 CPUs, 7.7 GB), other projects' containers beside it: windows/amd64, 16 CPUs, memory unknown, go1.27.1. the host the generator ran on (L-Q1); where the systems ran is the --host name's to say
- Commits: uspace-lab `951fda2a15bcf06cb42aa26e441187bf2642b256`, uspace-core `v1.6.0`.
- Policy: version 1 (`sha256:19124b6573ade992e321dbb2f18313bf01ec7ac262db03d75216da38f94db8df`); figures marked pending GCAA are the spec's defaults until GCAA answers.
- Ran 2026-10-14T15:01:39Z to 2026-10-14T16:02:50Z; the generator ran 3600 s of the tier's 3600 s.

## Scaled from 05 §1 / 05 §7

| Quantity | Spec | This run | Factor | Why |
|---|---|---|---|---|
| console viewports | 10 consoles each viewing <= 200 tracks (05 §1) | the same count, each subscribed to the whole area | 1 | the reference picture has no viewport throttle; whole-area subscriptions are the worst case |

## Not generated

- ANSP manned feed (tens of aircraft): the reference target has no manned intake; in a systems run the ANSP adapter polls a sim-adsb the load harness does not serve, so the USSP shows manned traffic unavailable (its degraded state, in the report)
- the second USSP's own intents: in a systems run the harness's peer USSP (peer) is the second USSP on the lab DSS: it serves its own flights and is notified of the fleet's intents in the U-space square, but files no intent of its own, so no conflict between the two USSPs is made (the reference target has no DSS or peer)
- authority DP polls of the USSPs (05 §1: one view per 1 Hz), against the reference target: the reference target has no SP; in a systems run the authority's display provider polls the USSP's and the peer's SPs over the oversight area the seed adds, and the lab polls the USSP's SP as a display provider too (dp)
- optional national push USSP -> authority: disabled by default (02 F6) and not in the reference target

## Offered load

| Quantity | Observed | Tier target |
|---|---|---|
| Operator telemetry (msg/s) | 100.0 | 100.0 |
| Remote ID locations (msg/s) | 40.0 | 40.0 |
| Console frames in (frames/s, all consoles) | 1253.9 | |
| Aircraft / operator clients / heard / receivers / consoles / watched | 100 / 10 / 40 / 4 / 10 / 26 | |
| Samples handed out more than a period late | 0 | |
| Intents filed (errors) | 100 (0) | |
| Intents ended after the run | 100 | |

## 05 §7

| Property | Check | Criterion | Observed | n | Result | Figure from |
|---|---|---|---|---|---|---|
| **Offered load** (pass) | `operator_rate` (required) | value >= 0.95 | 1 |  | pass | lab |
|  | `rid_rate` (required) | value >= 0.95 | 1 |  | pass | lab |
|  | `generator_on_time` (required) | value <= 0.001 | 0 |  | pass | lab |
| **Ingest-to-picture latency** (fail) | `operator_to_console_p99` (required) | p99 < 1 s | p50 0.414 / p95 0.976 / p99 1.023 / max 1.052 | 91404 | fail: operator_to_ussp_console_s p99 is 1.023, the criterion is < 1 | pending GCAA |
|  | `receiver_to_picture_p99` (required) | p99 < 1 s | p50 0.087 / p95 0.09 / p99 4.95 / max 37.141 | 128306 | fail: receiver_to_authority_picture_s p99 is 4.95, the criterion is < 1 | pending GCAA |
| **F3411 timing** (partial) | `sp_flights_p95` (required) | p95 <= 1 s | p50 0.002 / p95 0.005 / p99 0.007 / max 0.058 | 35928 | pass | standard |
|  | `sp_flights_p99` (required) | p99 <= 3 s | p50 0.002 / p95 0.005 / p99 0.007 / max 0.058 | 35928 | pass | standard |
|  | `dp_display_p95` (required) | p95 <= 1 s | p50 0.005 / p95 0.011 / p99 0.015 / max 0.026 | 12920 | pass | standard |
|  | `dp_display_p99` (required) | p99 <= 3 s | p50 0.005 / p95 0.011 / p99 0.015 / max 0.026 | 12920 | pass | standard |
|  | `details_p95` (required) | p95 <= 2 s | p50 0.002 / p95 0.003 / p99 0.004 / max 0.021 | 35886 | pass | standard |
|  | `details_p99` (required) | p99 <= 6 s | p50 0.002 / p95 0.003 / p99 0.004 / max 0.021 | 35886 | pass | standard |
|  | `dp_cache_24h` | value == 0 | not measured |  | not measured: the authority's display provider has cached for 1.0 h (its first network track); the 24 h bound is judged once it has cached for 25 h (a day, and the hour disposal is checked in) | standard |
| **F3548 timing** (partial) | `intent_change_notify` (required) | max <= 5 s | p50 0.297 / p95 0.926 / p99 1.008 / max 1.008 | 48 | pass | standard |
|  | `notifications_complete` (required) | value == 0 | 0 |  | pass | standard |
|  | `conflict_notify` | max <= 1 s | not measured |  | not measured: no intent of the run conflicts with another USS's: the peer files none, so no conflicting-intent notification is due | standard |
|  | `details_answered` (required) | max <= 1 s | p50 0.002 / p95 0.005 / p99 0.006 / max 0.006 | 48 | pass | standard |
|  | `constraint_notify` | max <= 5 s | not measured |  | not measured: no constraint is created during the run: no system of the lab stack publishes F3548 constraints (uss_qualifier has no constraint management at the pinned commit, WP-L7) | standard |
| **Alert latency** (fail) | `proximity_raise_p99` (required) | p99 < 2 s | p50 0.02 / p95 0.049 / p99 0.051 / max 0.054 | 320 | pass | pending GCAA |
|  | `zone_alert_tick` (required) | value <= 1 | 1.051 |  | fail: zone_alert_ticks value is 1.051, the criterion is <= 1 | spec |
|  | `art13_notice` | max <= 5 s | not measured |  | not measured: no aircraft of the load deviates from its authorisation, so no Art. 13(2) notice is due; and the lab's U-space airspace is designated without the conformance monitoring service (services_required NID, GEO, FA, TI; 2021/664 Art. 3(3)) | pending GCAA |
| **Missed alerts** (fail) | `expected_set` (required) | value >= 1 | 380 |  | pass | lab |
|  | `missed_raises` (required) | value == 0 | 0 |  | pass | spec |
|  | `missed_clears` (required) | value == 0 | 2 |  | fail: missed_clears value is 2, the criterion is == 0 | spec |
|  | `no_false_alarm` (required) | value == 0 | 0 |  | pass | lab |
|  | `alerts_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **No silent loss** (fail) | `operator_identity` (required) | value == 0 | 100 |  | fail: operator_ledgers_unbalanced value is 100, the criterion is == 0 | spec |
|  | `receiver_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_traceable` (required) | value == 0 | 0 |  | pass | lab |
|  | `traffic_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **CPA budget** (pass) | `pair_checks` (required) | value <= 1 | 0.018 |  | pass | spec |
|  | `evaluation_period` (required) | value <= 2 s | 1 |  | pass | spec |
| **Storage** (fail) | `writer_queue` (required) | value < 10 s | 1.179277435 |  | pass | spec |
|  | `compression` (required) | value == 0 | 0 |  | pass | spec |
|  | `disk_model` (required) | value <= 1.2 | 2.719 |  | fail: disk_growth_vs_model value is 2.719, the criterion is <= 1.2 | spec |
| **Restart** (fail) | `restart_recovery` (required) | value <= 10 s | 1.758 |  | pass | spec |
|  | `restart_duplicates` (required) | value == 0 | 36 |  | fail: restart_duplicate_alerts value is 36, the criterion is == 0 | spec |
|  | `restart_backlog` (required) | value == 0 | 216062 |  | fail: restart_lost_backlog value is 216062, the criterion is == 0 | spec |
| **Partition** (pass) | `partition_replay` (required) | value == 0 | 0 |  | pass | spec |
|  | `partition_isolation` (required) | value == 0 | 0 |  | pass | spec |
| **Cross-system outage** (fail) | `outage_flagged` (required) | value == 0 | 3 |  | fail: outage_unflagged value is 3, the criterion is == 0 | spec |
| **Memory** (fail) | `memory_monotonic` (required) | value == 0 | 1 |  | fail: memory_monotonic value is 1, the criterion is == 0 | spec |
|  | `memory_growth` | value <= 1.5 | 1.239 |  | pass | lab |
| **Vectors** (pass) | `vectors_pass` (required) | value == 0 | 0 |  | pass | spec |

## Loss accounting

- Operator telemetry: sent 360064 = accepted 359936 + refused 0 + dropped 0 + duplicate 64; 216062 pending; 100 clients unbalanced.
- Receivers: observed 184162, sent 184162 = accepted 179376 + duplicates 0 + refused 4786; shed 0; pending 0; 11867 samples accepted as backlog; 0 receivers unbalanced.
- Picture (timed console): handed 144000, shown 128306, unshown 15694, accounted 16653 (refused, dropped_frames 0, sheds, accepted as backlog), silent 0; untraceable frames 0.
- Traffic streams (watched flights): handed 93600, shown 91404, unshown 2196, untraceable 0.
- Generator sheds: operator 0, Remote ID 0.

## The systems (read by the probes)

| Process | Resident memory first / last / max (MB) | Samples | Growth | Monotonic |
|---|---|---|---|---|
| ansp-api | 167.0 / 39.4 / 167.3 | 311 | 1.005 | false |
| ansp-manned-feed | 30.6 / 30.7 / 32.4 | 312 | 0.984 | false |
| authority-api | 90.6 / 47.1 / 98.9 | 324 | 0.972 | false |
| authority-detect | 30.0 / 34.1 / 36.1 | 323 | 0.985 | false |
| authority-dp-poller | 31.2 / 35.3 / 74.0 | 325 | 1.004 | false |
| authority-manned-ingest | 28.7 / 28.2 / 30.6 | 325 | 0.931 | false |
| authority-picture-ws | 31.2 / 35.2 / 37.1 | 324 | 0.985 | false |
| authority-rid-ingest | 28.6 / 32.8 / 112.5 | 324 | 0.942 | false |
| authority-tsdb-writer | 28.1 / 29.0 / 53.6 | 325 | 0.968 | false |
| cisp-api | 104.4 / 35.4 / 106.9 | 306 | 0.984 | false |
| ussp-api | 43.6 / 45.1 / 167.6 | 338 | 0.924 | false |
| ussp-dss-sync | 22.2 / 23.4 / 25.4 | 338 | 0.918 | false |
| ussp-monitor | 32.9 / 36.8 / 42.7 | 338 | 0.889 | false |
| ussp-rid-sp | 29.4 / 36.2 / 47.5 | 338 | 0.854 | false |
| ussp-telemetry-ingest | 39.4 / 87.7 / 97.0 | 338 | 1.239 | true |
| ussp-traffic-ws | 32.8 / 34.2 / 39.4 | 338 | 0.912 | false |
| ussp-tsdb-writer | 29.9 / 28.6 / 45.3 | 338 | 0.851 | false |

| Database | Fleet samples | Before (MB) | After (MB) | Model (MB) | Growth / model | Compression lag (chunks) |
|---|---|---|---|---|---|---|
| ussp | 360000 | 19.3 | 222.0 | 216.0 | 0.939 | 0 |
| authority | 360000 | 12.1 | 984.0 | 216.0 | 4.5 | 0 |

- Pair checks of ussp-monitor: 890.75 a second over the run.

| Process | CPU over the run (cores) |
|---|---|
| ansp-api | 0.004 |
| ansp-manned-feed | 0.003 |
| authority-api | 0.006 |
| authority-detect | 0.019 |
| authority-dp-poller | 0.032 |
| authority-manned-ingest | 0.001 |
| authority-picture-ws | 0.07 |
| authority-rid-ingest | 0.033 |
| authority-tsdb-writer | 0.065 |
| cisp-api | 0.003 |
| ussp-api | 0.014 |
| ussp-dss-sync | 0.001 |
| ussp-monitor | 0.046 |
| ussp-rid-sp | 0.075 |
| ussp-telemetry-ingest | 0.08 |
| ussp-traffic-ws | 0.039 |
| ussp-tsdb-writer | 0.031 |

| Container (docker stats) | Samples | CPU mean (% of a core) | CPU max |
|---|---|---|---|
| uspace-demo-ansp-api-1 | 328 | 0.483 | 30.87 |
| uspace-demo-ansp-manned-adapter-1 | 329 | 0.02 | 0.24 |
| uspace-demo-ansp-manned-feed-1 | 329 | 0.369 | 1.72 |
| uspace-demo-ansp-nats-1 | 329 | 1.009 | 4.57 |
| uspace-demo-ansp-timescaledb-1 | 329 | 1.914 | 7.18 |
| uspace-demo-authority-api-1 | 326 | 0.674 | 3.13 |
| uspace-demo-authority-detect-1 | 326 | 2.079 | 4.09 |
| uspace-demo-authority-dp-poller-1 | 327 | 3.508 | 8.75 |
| uspace-demo-authority-manned-ingest-1 | 328 | 0.107 | 2.45 |
| uspace-demo-authority-nats-1 | 323 | 11.293 | 16.82 |
| uspace-demo-authority-picture-ws-1 | 327 | 7.87 | 15.29 |
| uspace-demo-authority-postgres-1 | 330 | 7.329 | 42.94 |
| uspace-demo-authority-rid-ingest-1 | 327 | 3.77 | 5.29 |
| uspace-demo-authority-tsdb-writer-1 | 328 | 7.359 | 13.13 |
| uspace-demo-caddy-1 | 360 | 12.542 | 75.32 |
| uspace-demo-cisp-api-1 | 328 | 0.795 | 5.43 |
| uspace-demo-cisp-deliver-1 | 329 | 1.099 | 5.68 |
| uspace-demo-cisp-nats-1 | 329 | 0.956 | 4.7 |
| uspace-demo-cisp-postgres-1 | 330 | 2.167 | 7.27 |
| uspace-demo-dss-1 | 329 | 0.581 | 3.96 |
| uspace-demo-dss-crdb-1 | 360 | 3.393 | 80.61 |
| uspace-demo-lab-issuer-1 | 360 | 1.228 | 4.36 |
| uspace-demo-ussp-api-1 | 360 | 1.939 | 11.36 |
| uspace-demo-ussp-dss-sync-1 | 360 | 0.797 | 4.55 |
| uspace-demo-ussp-monitor-1 | 359 | 5.37 | 10.85 |
| uspace-demo-ussp-nats-1 | 353 | 11.401 | 17.5 |
| uspace-demo-ussp-rid-sp-1 | 359 | 8.172 | 17.12 |
| uspace-demo-ussp-telemetry-ingest-1 | 359 | 8.783 | 15.42 |
| uspace-demo-ussp-timescaledb-1 | 360 | 3.909 | 40.12 |
| uspace-demo-ussp-traffic-ws-1 | 359 | 4.562 | 12.81 |
| uspace-demo-ussp-tsdb-writer-1 | 359 | 3.827 | 9.69 |

Loss counters that moved during the run (05 §7: accounted for in the report; the systems' own counters, listed, not judged):

- authority-api `uspace_authority_occurrences_refused_key_unavailable{component="occurrences"}`: +1384
- authority-dp-poller `uspace_authority_gap_records_lost{component="dp_rows"}`: +4
- authority-dp-poller `uspace_authority_rows_lost_handover_failed{component="dp_rows"}`: +53
- authority-rid-ingest `uspace_authority_batches_refused{component="rid_ingest"}`: +116
- authority-rid-ingest `uspace_authority_refused_queue_unavailable{component="rid_ingest"}`: +116
- authority-tsdb-writer `uspace_authority_gaps{component="tsdb_writer"}`: +9891
- ussp-monitor `ussp_monitor_publish_dropped{component="conformance"}`: +7
- ussp-traffic-ws `ussp_picture_dropped_after_silence{component="traffic_ws"}`: +108

The display provider's cache (authority): caching for 1.0 h, 0 entries older than 24 h.

Probes that failed inside a fault the run injected: 181.

## 04 §1: the hot path's JSON share (estimate)

One encoding/json decode and encode of a 1010-byte track frame: 9.1 us on this host. an estimate: one encoding/json decode and encode of a 1010-byte track frame on this host against each process's CPU per message over the run; the systems expose no profile.

| Process | Messages (series) | CPU (s) | CPU per message (us) | JSON share |
|---|---|---|---|---|
| authority-picture-ws | 4480020 (uspace_authority_frames_sent{component="picture"}) | 254.08 | 56.714 | 0.16 |
| ussp-telemetry-ingest | 357660 (ussp_accepted{component="telemetry"}) | 289.97 | 810.742 | 0.011 |
| ussp-traffic-ws | 139448 (ussp_traffic_ws_frames_sent{component="traffic_ws"}) | 142.86 | 1024.468 | 0.009 |

## Zone LOADZ1

60 entries due, 60 raised, 58 cleared, 0 missed, 2 clears missed, 0 unexpected; the slowest raise 1.05 ticks of 1 s.

- ac-00098: the raise for the entry at 1360 s was not cleared within 30 s of the exit at 1401 s
- ac-00099: the raise for the entry at 1360 s was not cleared within 30 s of the exit at 1401 s

## Vectors against the images

18 knowledge/vectors files; 22 s.

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

https://peer.uspace.test (aircraft LABXCP0010N000000, LABXCP0010N000001, LABXCP0010N000002, LABXCP0010N000003). The display provider (authority-01) polled it 3339 times (others 3539); the timed console showed 12920 frames of its flights, 12920 displays timed, 0 frames untied; 100 of the fleet's tracks from the USSP's SP were on it.

F3548: 16 intents inside the U-space square, 48 changes owed to the peer; 64 notifications received (map[Accepted:16 Activated:16 Nonconforming:16 deleted:16]), 0 naming no intent of the run; details 48 answered, 0 failed.

## Faults

| Row | Script | At (s) | Hold (s) | Picture back after restore (s) | Traffic back (s) | Error |
|---|---|---|---|---|---|---|
| restart | `adapter.sh ussp-telemetry-ingest` | 300 | 5 | 0.003 | 1.033 |  |
| restart | `hotpath.sh ussp-monitor` | 420 | 5 | 0.04 | 0.865 |  |
| restart | `hotpath.sh ussp-traffic-ws` | 540 | 5 | 0.011 | 1.758 |  |
| restart | `hotpath.sh ussp-rid-sp` | 660 | 5 | 0.086 | 0.934 |  |
| restart | `hotpath.sh ussp-tsdb-writer` | 780 | 5 | 0 | 0.842 |  |
| restart | `adapter.sh authority-rid-ingest` | 900 | 5 | 0.345 | 0.884 |  |
| restart | `hotpath.sh authority-detect` | 1020 | 5 | 0.069 | 0.8 |  |
| restart | `hotpath.sh authority-picture-ws` | 1140 | 5 | 0.179 | 0.699 |  |
| restart | `hotpath.sh authority-tsdb-writer` | 1260 | 5 | 0.08 | 0.697 |  |
| partition | `nats.sh ussp` | 1380 | 60 | 0.014 | 1.614 |  |
| partition | `nats.sh authority` | 1500 | 60 | 0.058 | 0.679 |  |
| outage | `system.sh cisp` | 1620 | 300 | 0.074 | 0.648 |  |
| outage | `system.sh authority` | 2040 | 300 | 22.189 | 0.831 |  |
| outage | `dss.sh dss` | 2490 | 300 | 0.006 | 0.719 |  |
| outage | `system.sh ansp` | 2940 | 300 | 0.086 | 0.554 |  |

Outage of cisp (`system.sh cisp`, 300 s), judged on 163 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| cisp ready lost | yes | cisp not ready from 4s after the injection to the restore (114 samples) | the CISP is down (the fault, seen in place) |
| others ready always | yes | authority, ussp, ansp, issuer, dss ready in all 114 samples | 05 §6 CISP: subscribers run on cache; 02 F3: they keep serving |
| ansp cisp in degraded|down|stale|unknown | yes | ansp cisp down 38s after the injection (down since 2026-10-14T15:29:28Z (serving age 65 s); uspace_airspace: the CISP answered 502; ussp_list: no version published yet; ussp_list: the CISP answered 502; restrictions: no version published ye...) | 02 F2: the ANSP raises an alarm and takes its degraded path (lab bound 90 s: its CISP check polls) |
| ussp cis age grows | yes | ussp cis age from 21 s to 330 s over the hold (114 samples) | 05 §6 CISP: subscribers run on cache with cis_age_s (05 §7: everything flagged with age) |
| all ready always | yes | every system ready 0s after the restore | the stack recovers after the restore |

Outage of authority (`system.sh authority`, 300 s), judged on 186 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| authority ready lost | yes | authority not ready from 4s after the injection to the restore (120 samples) | the authority is down (the fault, seen in place) |
| others ready always | **no** | ansp not ready in 117 of 120 samples (last: 503  jwks=down) | 05 §6 Authority: no real-time service depends on the authority; the CISP keeps serving |
| ussp registry age grows | yes | ussp registry age from 21 s to 338 s over the hold (120 samples) | 05 §6 Authority, 02 F8: USSPs use the validity cache (05 §7: flagged with age) |
| all ready always | yes | every system ready 2s after the restore | the stack recovers after the restore |

Outage of dss (`dss.sh dss`, 300 s), judged on 175 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| dss ready lost | yes | dss not ready from 2s after the injection to the restore (114 samples) | the DSS is down (the fault, seen in place) |
| ussp dss in down|degraded | yes | ussp dss down 10s after the injection (f3411: down since 2026-10-14T15:43:32Z: answers 502 / f3548: uss_availability Unknown) | 02 F6: the USSP shows DSS state (lab bound 90 s: its DSS check polls) |
| others ready always | **no** | cisp not ready in 3 of 114 samples (last: 503  migrations=unknown: migrate relational status: failed to initialize: context deadline exceeded) | 05 §6 DSS: local intents and all in-flight services continue |
| all ready always | yes | every system ready 2s after the restore | the stack recovers after the restore |

Outage of ansp (`system.sh ansp`, 300 s), judged on 161 samples:

| Claim | Met | Observed | Why |
|---|---|---|---|
| ansp ready lost | yes | ansp not ready from 4s after the injection to the restore (111 samples) | the ANSP is down (the fault, seen in place) |
| others ready always | **no** | cisp not ready in 1 of 111 samples (last: 503  migrations=unknown: migrate relational status: failed to initialize: context deadline exceeded) | 05 §6 ANSP feed: no other system stops with it |
| traffic shows manned | yes | the traffic stream showed manned 0s after the injection | 05 §6 ANSP feed: USSP traffic information marks manned traffic unavailable |
| all ready always | yes | every system ready 1s after the restore | the stack recovers after the restore |

## F3411 display-provider polls of the USSP

10 views, 35958 polls, 342184 flights returned, 35886 details answered, 30 failures, 30 of them inside a fault the run injected (statuses map[200:71814 502:30]).

## Alerts

Expected 320, raised 320, cleared 320, missed 0, clears missed 0, unexpected 0, untraceable 0.

## Failures

- operator_to_console_p99: operator_to_ussp_console_s p99 is 1.023, the criterion is < 1
- receiver_to_picture_p99: receiver_to_authority_picture_s p99 is 4.95, the criterion is < 1
- zone_alert_tick: zone_alert_ticks value is 1.051, the criterion is <= 1
- missed_clears: missed_clears value is 2, the criterion is == 0
- operator_identity: operator_ledgers_unbalanced value is 100, the criterion is == 0
- disk_model: disk_growth_vs_model value is 2.719, the criterion is <= 1.2
- restart_duplicates: restart_duplicate_alerts value is 36, the criterion is == 0
- restart_backlog: restart_lost_backlog value is 216062, the criterion is == 0
- outage_flagged: outage_unflagged value is 3, the criterion is == 0
- memory_monotonic: memory_monotonic value is 1, the criterion is == 0
- run error: drain: not settled within drain_s 30 s


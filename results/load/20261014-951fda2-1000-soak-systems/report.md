# Load run 20261014-951fda2-1000-soak-systems: tier 1000-soak

**Verdict: FAIL.** 34 checks measured, 10 not measured (05 §7 is not fully observed by this run).

- Target: lab-load-1000-soak (systems). The systems' images as the targets file names them, seeded for this fleet (loadgen seed); evidence for the images and digests listed, on the host named, when the run passes (docs/PLAN.md L-D4).
- Receivers: each receiver under its own address of 10.77.0.0/16, named in X-Forwarded-For, which the lab Caddy and the authority trust from the host only (deploy/systems).
- Image ansp: `ghcr.io/rootxkit/uspace-ansp@sha256:1fcfb3c9762189f737250c65ad24166c025c12576abb7babffcf07d705a6921a`
- Image authority: `ghcr.io/rootxkit/uspace-authority@sha256:53d6f97cd745677da893a62669f45477d1eaa403b369bc174b6ccc8c11ee11cc`
- Image cisp: `ghcr.io/rootxkit/uspace-cisp@sha256:2e7861488e178de967829e7ebb623a3c5b480f0dcca8e5ee1acab4e45028f42a`
- Image ussp: `ghcr.io/rootxkit/uspace-ussp@sha256:854cca7146277e6dc767cd7efce279f350994c9f152344a8af457c917db5935d`
- Host: dev workstation (Windows 11, 16 logical CPUs, 16 GB); the systems stack on the same host in Docker Desktop 28.5.1 (16 CPUs, 7.7 GB), other projects' containers beside it: windows/amd64, 16 CPUs, memory unknown, go1.27.1. the host the generator ran on (L-Q1); where the systems ran is the --host name's to say
- Commits: uspace-lab `951fda2a15bcf06cb42aa26e441187bf2642b256`, uspace-core `v1.6.0`.
- Policy: version 1 (`sha256:19124b6573ade992e321dbb2f18313bf01ec7ac262db03d75216da38f94db8df`); figures marked pending GCAA are the spec's defaults until GCAA answers.
- Ran 2026-10-14T17:17:06Z to 2026-10-14T19:20:08Z; the generator ran 7200 s of the tier's 7200 s.

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
| Operator telemetry (msg/s) | 1000.0 | 1000.0 |
| Remote ID locations (msg/s) | 400.0 | 400.0 |
| Console frames in (frames/s, all consoles) | 41486.8 | |
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
| **Ingest-to-picture latency** (fail) | `operator_to_console_p99` (required) | p99 < 1 s | p50 0.556 / p95 1.002 / p99 1.046 / max 1.305 | 335622 | fail: operator_to_ussp_console_s p99 is 1.046, the criterion is < 1 | pending GCAA |
|  | `receiver_to_picture_p99` (required) | p99 < 1 s | p50 0.109 / p95 0.247 / p99 0.457 / max 1.721 | 2877623 | pass | pending GCAA |
| **F3411 timing** (partial) | `sp_flights_p95` (required) | p95 <= 1 s | p50 0.014 / p95 0.061 / p99 0.115 / max 0.635 | 352800 | pass | standard |
|  | `sp_flights_p99` (required) | p99 <= 3 s | p50 0.014 / p95 0.061 / p99 0.115 / max 0.635 | 352800 | pass | standard |
|  | `dp_display_p95` (required) | p95 <= 1 s | p50 0.019 / p95 0.092 / p99 0.18 / max 0.941 | 28908 | pass | standard |
|  | `dp_display_p99` (required) | p99 <= 3 s | p50 0.019 / p95 0.092 / p99 0.18 / max 0.941 | 28908 | pass | standard |
|  | `details_p95` (required) | p95 <= 2 s | p50 0.007 / p95 0.035 / p99 0.072 / max 0.587 | 352781 | pass | standard |
|  | `details_p99` (required) | p99 <= 6 s | p50 0.007 / p95 0.035 / p99 0.072 / max 0.587 | 352781 | pass | standard |
|  | `dp_cache_24h` | value == 0 | not measured |  | not measured: the authority's display provider has cached for 4.3 h (its first network track); the 24 h bound is judged once it has cached for 25 h (a day, and the hour disposal is checked in) | standard |
| **F3548 timing** (partial) | `intent_change_notify` (required) | max <= 5 s | p50 0.832 / p95 1.642 / p99 1.908 / max 1.908 | 48 | pass | standard |
|  | `notifications_complete` (required) | value == 0 | 0 |  | pass | standard |
|  | `conflict_notify` | max <= 1 s | not measured |  | not measured: no intent of the run conflicts with another USS's: the peer files none, so no conflicting-intent notification is due | standard |
|  | `details_answered` (required) | max <= 1 s | p50 0.003 / p95 0.01 / p99 0.01 / max 0.01 | 32 | pass | standard |
|  | `constraint_notify` | max <= 5 s | not measured |  | not measured: no constraint is created during the run: no system of the lab stack publishes F3548 constraints (uss_qualifier has no constraint management at the pinned commit, WP-L7) | standard |
| **Alert latency** (fail) | `proximity_raise_p99` (required) | p99 < 2 s | p50 0.066 / p95 0.131 / p99 0.176 / max 0.24 | 1200 | pass | pending GCAA |
|  | `zone_alert_tick` (required) | value <= 1 | 1.143 |  | fail: zone_alert_ticks value is 1.143, the criterion is <= 1 | spec |
|  | `art13_notice` | max <= 5 s | not measured |  | not measured: no aircraft of the load deviates from its authorisation, so no Art. 13(2) notice is due; and the lab's U-space airspace is designated without the conformance monitoring service (services_required NID, GEO, FA, TI; 2021/664 Art. 3(3)) | pending GCAA |
| **Missed alerts** (pass) | `expected_set` (required) | value >= 1 | 1320 |  | pass | lab |
|  | `missed_raises` (required) | value == 0 | 0 |  | pass | spec |
|  | `missed_clears` (required) | value == 0 | 0 |  | pass | spec |
|  | `no_false_alarm` (required) | value == 0 | 0 |  | pass | lab |
|  | `alerts_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **No silent loss** (pass) | `operator_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `receiver_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_identity` (required) | value == 0 | 0 |  | pass | spec |
|  | `picture_traceable` (required) | value == 0 | 0 |  | pass | lab |
|  | `traffic_traceable` (required) | value == 0 | 0 |  | pass | lab |
| **CPA budget** (pass) | `pair_checks` (required) | value <= 1 | 0.248 |  | pass | spec |
|  | `evaluation_period` (required) | value <= 2 s | 1 |  | pass | spec |
| **Storage** (fail) | `writer_queue` (required) | value < 10 s | 1.2523574800000001 |  | pass | spec |
|  | `compression` (required) | value == 0 | 0 |  | pass | spec |
|  | `disk_model` (required) | value <= 1.2 | 2.757 |  | fail: disk_growth_vs_model value is 2.757, the criterion is <= 1.2 | spec |
| **Restart** (not measured) | `restart_recovery` | value <= 10 s | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `restart_duplicates` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `restart_backlog` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Partition** (not measured) | `partition_replay` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
|  | `partition_isolation` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Cross-system outage** (not measured) | `outage_flagged` | value == 0 | not measured |  | not measured: no fault plan in the run (loadgen run --faults load/faults/<plan>.yaml) | spec |
| **Memory** (pass) | `memory_monotonic` (required) | value == 0 | 0 |  | pass | spec |
|  | `memory_growth` (required) | value <= 1.5 | 1.05 |  | pass | lab |
| **Vectors** (pass) | `vectors_pass` (required) | value == 0 | 0 |  | pass | spec |

## Loss accounting

- Operator telemetry: sent 7200000 = accepted 7200000 + refused 0 + dropped 0 + duplicate 0; 0 pending; 0 clients unbalanced.
- Receivers: observed 3683138, sent 3683138 = accepted 3683138 + duplicates 0 + refused 0; shed 0; pending 0; 62 samples accepted as backlog; 0 receivers unbalanced.
- Picture (timed console): handed 2880000, shown 2877623, unshown 2377, accounted 11444 (refused, dropped_frames 11382, sheds, accepted as backlog), silent 0; untraceable frames 0.
- Traffic streams (watched flights): handed 338400, shown 335622, unshown 2778, untraceable 0.
- Generator sheds: operator 0, Remote ID 0.

## The systems (read by the probes)

| Process | Resident memory first / last / max (MB) | Samples | Growth | Monotonic |
|---|---|---|---|---|
| ansp-api | 32.6 / 29.4 / 34.1 | 721 | 0.929 | false |
| ansp-manned-feed | 16.6 / 16.8 / 19.3 | 721 | 1.05 | false |
| authority-api | 42.2 / 36.5 / 71.3 | 721 | 0.996 | false |
| authority-detect | 32.4 / 62.6 / 65.0 | 721 | 0.992 | false |
| authority-dp-poller | 31.8 / 30.8 / 43.7 | 721 | 1.008 | false |
| authority-manned-ingest | 16.6 / 14.2 / 17.6 | 721 | 1.004 | false |
| authority-picture-ws | 32.5 / 37.1 / 49.0 | 721 | 1.033 | false |
| authority-rid-ingest | 132.1 / 42.4 / 132.1 | 721 | 1 | false |
| authority-tsdb-writer | 28.4 / 23.1 / 47.7 | 721 | 1.034 | false |
| cisp-api | 31.0 / 25.8 / 31.4 | 721 | 0.934 | false |
| ussp-api | 44.7 / 46.4 / 630.4 | 721 | 0.962 | false |
| ussp-dss-sync | 19.9 / 16.4 / 20.8 | 720 | 0.851 | false |
| ussp-monitor | 48.6 / 44.1 / 51.7 | 720 | 0.987 | false |
| ussp-rid-sp | 36.0 / 126.0 / 165.4 | 720 | 1.003 | false |
| ussp-telemetry-ingest | 1055.5 / 1477.1 / 1492.9 | 720 | 0.999 | false |
| ussp-traffic-ws | 41.7 / 41.8 / 47.2 | 720 | 1.011 | false |
| ussp-tsdb-writer | 41.3 / 34.1 / 43.1 | 720 | 0.978 | false |

| Database | Fleet samples | Before (MB) | After (MB) | Model (MB) | Growth / model | Compression lag (chunks) |
|---|---|---|---|---|---|---|
| ussp | 7200000 | 1878.2 | 5224.2 | 4320.0 | 0.775 | 0 |
| authority | 7200000 | 10061.1 | 30535.4 | 4320.0 | 4.739 | 0 |

- Pair checks of ussp-monitor: 12403.074 a second over the run.

| Process | CPU over the run (cores) |
|---|---|
| ansp-api | 0.029 |
| ansp-manned-feed | 0.006 |
| authority-api | 0.013 |
| authority-detect | 0.183 |
| authority-dp-poller | 0.417 |
| authority-manned-ingest | 0.002 |
| authority-picture-ws | 1.696 |
| authority-rid-ingest | 0.309 |
| authority-tsdb-writer | 0.355 |
| cisp-api | 0.003 |
| ussp-api | 0.052 |
| ussp-dss-sync | 0.001 |
| ussp-monitor | 0.367 |
| ussp-rid-sp | 0.844 |
| ussp-telemetry-ingest | 0.73 |
| ussp-traffic-ws | 0.194 |
| ussp-tsdb-writer | 0.125 |

| Container (docker stats) | Samples | CPU mean (% of a core) | CPU max |
|---|---|---|---|
| uspace-demo-ansp-api-1 | 720 | 3.039 | 14.97 |
| uspace-demo-ansp-manned-adapter-1 | 720 | 0.077 | 0.78 |
| uspace-demo-ansp-manned-feed-1 | 720 | 0.589 | 3.36 |
| uspace-demo-ansp-nats-1 | 720 | 2.478 | 10.29 |
| uspace-demo-ansp-timescaledb-1 | 720 | 5.741 | 22 |
| uspace-demo-authority-api-1 | 720 | 0.925 | 12.24 |
| uspace-demo-authority-detect-1 | 720 | 18.365 | 23.79 |
| uspace-demo-authority-dp-poller-1 | 720 | 43.161 | 74.29 |
| uspace-demo-authority-manned-ingest-1 | 720 | 0.101 | 1.54 |
| uspace-demo-authority-nats-1 | 720 | 47.036 | 58.49 |
| uspace-demo-authority-picture-ws-1 | 720 | 169.645 | 220.28 |
| uspace-demo-authority-postgres-1 | 720 | 46.002 | 112.72 |
| uspace-demo-authority-rid-ingest-1 | 720 | 31.006 | 40.11 |
| uspace-demo-authority-tsdb-writer-1 | 720 | 35.786 | 44.5 |
| uspace-demo-caddy-1 | 720 | 109.639 | 133.53 |
| uspace-demo-cisp-api-1 | 720 | 0.961 | 6.85 |
| uspace-demo-cisp-deliver-1 | 720 | 1.384 | 6.17 |
| uspace-demo-cisp-nats-1 | 720 | 1.314 | 4.84 |
| uspace-demo-cisp-postgres-1 | 720 | 3.609 | 12.7 |
| uspace-demo-dss-1 | 720 | 1.703 | 7.42 |
| uspace-demo-dss-crdb-1 | 720 | 192.816 | 331.29 |
| uspace-demo-lab-issuer-1 | 720 | 1.871 | 5.65 |
| uspace-demo-ussp-api-1 | 720 | 5.182 | 168.41 |
| uspace-demo-ussp-dss-sync-1 | 720 | 0.906 | 5.75 |
| uspace-demo-ussp-monitor-1 | 720 | 38.454 | 58.36 |
| uspace-demo-ussp-nats-1 | 720 | 61.82 | 202.16 |
| uspace-demo-ussp-rid-sp-1 | 720 | 85.165 | 121.03 |
| uspace-demo-ussp-telemetry-ingest-1 | 720 | 75.194 | 377.61 |
| uspace-demo-ussp-timescaledb-1 | 720 | 15.725 | 71.24 |
| uspace-demo-ussp-traffic-ws-1 | 720 | 20.525 | 37.43 |
| uspace-demo-ussp-tsdb-writer-1 | 720 | 13.866 | 24.86 |

Loss counters that moved during the run (05 §7: accounted for in the report; the systems' own counters, listed, not judged):

- ansp-api `notices_sender_quota_refused`: +24384
- authority-api `uspace_authority_occurrences_refused_key_unavailable{component="occurrences"}`: +11215
- authority-picture-ws `uspace_authority_session_refused{component="sessions"}`: +60
- authority-tsdb-writer `uspace_authority_gaps{component="tsdb_writer"}`: +12225
- ussp-telemetry-ingest `ussp_flights_telemetry_lost{component="flights"}`: +902
- ussp-telemetry-ingest `ussp_replay_window_put_dropped{component="replay_window"}`: +10370

The display provider's cache (authority): caching for 4.3 h, 0 entries older than 24 h.

## 04 §1: the hot path's JSON share (estimate)

One encoding/json decode and encode of a 1009-byte track frame: 11.6 us on this host. an estimate: one encoding/json decode and encode of a 1009-byte track frame on this host against each process's CPU per message over the run; the systems expose no profile.

| Process | Messages (series) | CPU (s) | CPU per message (us) | JSON share |
|---|---|---|---|---|
| authority-picture-ws | 298676256 (uspace_authority_frames_sent{component="picture"}) | 12225.73 | 40.933 | 0.284 |
| ussp-telemetry-ingest | 7194839 (ussp_accepted{component="telemetry"}) | 5261.23 | 731.251 | 0.016 |
| ussp-traffic-ws | 512113 (ussp_traffic_ws_frames_sent{component="traffic_ws"}) | 1395.15 | 2724.301 | 0.004 |

## Zone LOADZ1

120 entries due, 120 raised, 120 cleared, 0 missed, 0 clears missed, 0 unexpected; the slowest raise 1.14 ticks of 1 s.

## Vectors against the images

18 knowledge/vectors files; 29 s.

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

https://peer.uspace.test (aircraft LABXCP0050N000000, LABXCP0050N000001, LABXCP0050N000002, LABXCP0050N000003). The display provider (authority-01) polled it 22088 times (others 7220); the timed console showed 28908 frames of its flights, 28908 displays timed, 0 frames untied; 1000 of the fleet's tracks from the USSP's SP were on it.

F3548: 16 intents inside the U-space square, 48 changes owed to the peer; 48 notifications received (map[Accepted:16 Activated:16 deleted:16]), 0 naming no intent of the run; details 32 answered, 0 failed.

## F3411 display-provider polls of the USSP

49 views, 352800 polls, 7656631 flights returned, 352781 details answered, 0 failures, 0 of them inside a fault the run injected (statuses map[200:705581]).

## Alerts

Expected 1200, raised 1200, cleared 1200, missed 0, clears missed 0, unexpected 0, untraceable 0.

## Failures

- operator_to_console_p99: operator_to_ussp_console_s p99 is 1.046, the criterion is < 1
- zone_alert_tick: zone_alert_ticks value is 1.143, the criterion is <= 1
- disk_model: disk_growth_vs_model value is 2.757, the criterion is <= 1.2


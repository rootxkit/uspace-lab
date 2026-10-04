# 05 — Scale and reliability

## 1. Load model

Assumptions: 1 Hz telemetry per airborne UAS (the authority's determination under 2021/664 Art. 8(3); equals the F3411 `NetMinUasLocRefreshFrequencyHz`); average internal `track/telemetry/v1` 600 B JSON; direct Remote ID at 1–3 frames/s per heard aircraft and ~40 % of airborne UAS heard by at least one receiver; manned traffic tens of aircraft; 2.5 intents per drone-day; consoles 10 / 30 / 60 concurrent, each viewing a cell set with ≤ 200 tracks; the authority's F3411 DP views cover every ISA (≤ 7 km diagonal per view).

| Quantity | 100 drones | 1000 drones | 5000 drones |
|---|---|---|---|
| Operator telemetry into USSP (msg/s) | 100 | 1 000 | 5 000 |
| Authority DP → USSP `GET /uss/flights` polls (1 Hz per active view; flights returned/s equals airborne UAS) | 10 views / 100 flights | 50 / 1 000 | 200 / 5 000 |
| Optional national push USSP → authority (msg/s, if enabled) | 100 | 1 000 | 5 000 |
| Direct RID observations into authority (msg/s) | ≈ 100 | ≈ 1 000 | ≈ 5 000 |
| Internal track fan-out per system (msg/s, with CPA, zone, conformance, console consumers ≈ 4×) | 400 | 4 000 | 20 000 |
| CPA pair checks per second (800 m radius, grid index; worst-case clustering 10 % of fleet in one cell) | < 1 000 | ≈ 50 000 | ≈ 1 250 000 |
| Peer-USSP F3411 polls (1 Hz per DP tile) | 10 | 50 | 200 |
| Intent operations per day | 250 | 2 500 | 12 500 |
| Raw telemetry stored per day per system (uncompressed) | 5 GB | 52 GB | 260 GB |
| Same after Timescale compression (×10–15 typical for telemetry) | 0.4 GB | 4–5 GB | 20–25 GB |
| Console WebSocket out (msg/s) | 2 000 | 6 000 | 12 000 |

CPA at 5000 is the first thing that breaks a naive design: it is quadratic within a cell. Cell size and the neighbour index are chosen so that the pair count per consumer stays under ~50 000/s per core; above that, consumers split cells.

## 2. Hot path vs control plane

Both halves are Go (`00 §6`); the split is between processes (`cmd/`) of one module, made where scaling or failure isolation differ, and both halves import the same `internal/` packages.

| System | Hot-path processes (stateless ingest, partitioned consumers, sub-second) | Control-plane process `api` (transactional, seconds, PostgreSQL) |
|---|---|---|
| authority | `rid-ingest` (decode, time placement); `dp-poller` (ISA discovery, SP polls, 24 h cache); `manned-ingest`; `detect` (identification from a registry projection; 120 m, zone, unregistered, no-authorisation detectors); `tsdb-writer`; `picture-ws` by viewport | registry, zones, certificates, incidents, occurrence reports, evidence, source control, audit, token service, violation review |
| cisp | `deliver` (webhook fan-out from a JetStream work queue) | publications, versions, subscriptions, ED-318 reads from a materialised current-version table with `ETag` and cache, change feed, public map data |
| ussp | `telemetry-ingest`; `monitor` (conformance per flight, CPA / traffic information per cell, geo-awareness evaluation, alert state machine); `rid-sp` (F3411 SP); `traffic-ws`; `dss-sync`; `tsdb-writer` | intent intake, deconfliction and DSS write (package call, no hop), registry validation, accounts, records, occurrences, weather |
| ansp | `manned-adapter` (normalisation and fan-out, one per feed) | restrictions lifecycle, coordination inbox, adapter status |

Rules: ingest processes hold no per-aircraft state beyond a short dedupe window and never touch the relational database; they publish to NATS and write to TimescaleDB through a batching writer. State needed on the hot path (registry validity, policy, zones, source switches) is projected into memory from a NATS KV bucket or the time-series database and refreshed by push plus periodic re-read; a failed refresh keeps the last state and logs. `api` is horizontally stateless behind Caddy (sessions in cookies, jobs in JetStream work queues); it is never on the per-sample path.

## 3. Partitioning and NATS subject design

Partition key (internal only; no standard governs it — a design choice): **a fixed latitude/longitude grid, `uspace-core/geodesy/cell`**: `cell5` = 0.1° × 0.1° (about 11 km north–south and 8 km east–west at Tbilisi), named `c5:<lat_idx>:<lon_idx>`, with the parent `cell3` = 1° × 1°, named `c3:<lat_idx>:<lon_idx>`, as the coarse key; `lat_idx = floor((lat_deg + 90) × n)` and `lon_idx = floor((lon_deg + 180) × n)` with n = 10 for `cell5` and 1 for `cell3`, and each `cell3` holds exactly 100 `cell5`s. The country's bounding box (`basemap/regions.yaml`) spans 24 `cell3`s. H3 is not used (its maintained Go binding is cgo). Cells never appear on an external interface; external areas are F3411 views, F3548 volumes and ED-318 geometries. A track's cell is computed at ingest from its position; a consumer owning a set of `cell5`s also subscribes to their ring-1 neighbours for CPA (800 m search radius ≪ cell edge, so one ring suffices).

Subjects (one NATS cluster per system; JetStream for durable subjects, core NATS for high-rate ephemeral ones):

| Subject | Stream | Purpose |
|---|---|---|
| `trk.v1.<cell3>.<cell5>.<track_id>` | core (ephemeral) + a JetStream mirror with 1 h retention for replay | the track hot path; consoles subscribe `trk.v1.<cell3>.<cell5>.>` for viewport cells; CPA workers `trk.v1.<cell3>.>` for a cell3 and filter cell5 ownership in process |
| `man.v1.<cell3>.<cell5>.<icao24>` | core | manned tracks (USSP, authority) |
| `alrt.v1.<kind>.<cell5>.<alert_id>` | JetStream, 7 d | alerts / violations; republished every 1 s while active |
| `ident.v1.<track_id>` | JetStream, 24 h | identification changes |
| `intent.v1.<state>.<intent_id>` | JetStream, 30 d | intent state changes |
| `cis.v1.<dataset>` | JetStream, 30 d + KV `cis_current` | CIS cache updates |
| `ctl.sources` + KV `source_control` | KV + push | source switches (predecessor U-15) |
| `ctl.policy` + KV `policy` | KV + push | thresholds |
| `src.v1.<type>.<instance>` | core | adapter status every 2 s |

The table is the reference shape, not a contract: NATS never crosses a system (`02 §1`), so each system's plan may deviate internally and does (see Errata, M30). The one rule kept is that a subject carrying an `04` message carries the `04 §2` envelope.
| `ingest.v1.<cell3>` | JetStream work queue, 10 min | raw ingest handoff when the ingest tier must shed to a durable queue under backpressure |

Consumer scaling: CPA / conformance / detector workers form a JetStream consumer group per `cell3` at small scale (one worker per `cell3` covers the country) and per `cell5` group at 5000 drones; ownership is a config map in KV, rebalanced by an operator action, not by auto-discovery (predictability over elegance). Consoles subscribe only to the `cell5`s intersecting their viewport plus a margin, and are throttled server-side to ≤ 2 Hz per track when a viewport holds > 200 tracks.

## 4. Storage, retention and compression

| Data | Store | Hot retention | Compressed / cold | Rationale |
|---|---|---|---|---|
| Telemetry, own tracks, manned tracks, RID observations | TimescaleDB hypertables, 1-day chunks, `compress_segmentby` = track/flight id, `compress_orderby` = `captured_at` | 7 days uncompressed | compressed to 90 days online; monthly archive to object storage (Parquet) for 2 years at the authority, 1 year at USSPs — **national choice (Q8)**: the regulatory floor is 30 days for USSP/CISP operational records, longer while pertinent to an investigation (2021/664 Art. 15(1)(g)); the authority's recorded-data demand is its Art. 18(b) determination; the ceiling is the storage-limitation principle (`06 §5`) | evidence and records |
| F3411 DP cache (`ussp_flights`, `peer_flights`) and F3548 peer data (`peer_intents`) | TimescaleDB / PostgreSQL | 24 h | **none** — disposed of at 24 h (F3411 `NetDpMaxDataRetentionPeriodSeconds = 86400`, F3548 `ExternalDataMaxRetentionTimeHours = 24`); what a decision or violation relied on is copied into that record | standard limit |
| Alerts, violations, conformance states | PostgreSQL | all | 5 years (national choice; floor 30 days) | regulatory |
| Intents, decisions, DSS log | PostgreSQL | all | 5 years (national choice; floor 30 days) | regulatory |
| Incidents, evidence packs | PostgreSQL + object storage, hash-sealed | all | indefinite (national choice) | oversight |
| Occurrence reports | PostgreSQL, segregated schema | all | indefinite, personal details removed at export and purged per DPO rule (national choice) | 376/2014 Art. 6(6), 16(3) |
| CIS publications and versions | PostgreSQL | all | indefinite | "what was published at T" |
| Audit `events` | PostgreSQL, append-only, partitioned by month | all | 10 years (national choice) | oversight |
| JetStream streams | broker disk | per subject table above | — | replay and restart only |

Capacity on the single staging droplet: at 100 drones every system together writes < 10 GB/day uncompressed; a 500 GB volume holds > 30 days hot for all. At 1000 drones each system needs its own database host; at 5000, TimescaleDB multi-node or per-`cell3` sharding of the telemetry hypertable.

## 5. Backpressure

| Point | Mechanism |
|---|---|
| Operator → USSP WebSocket | per-client rate limit 2 Hz; over-rate frames dropped and counted, client told via `status` frame; slow consumer on our side → `ingest.v1.<cell3>` work queue absorbs up to 10 min, then the oldest are dropped with a counted gap, never the newest |
| Receiver → authority | batches acked only when written to the work queue; receiver buffers and replays as `backlog` with original `rx_ts` |
| NATS core subjects | slow consumer disconnect is a console or dashboard concern only; safety consumers read JetStream pull consumers with explicit ack and `max_ack_pending` |
| TimescaleDB writer | batched `COPY`, bounded in-memory queue (10 s); beyond that, writes spill to the JetStream mirror and are replayed; the hot path never blocks on the database |
| CPA workers | if a cell's pair budget is exceeded the worker widens its tick to 2 s for that cell, reports `degraded`, and the alert carries `evaluation_period_s`; it never skips a cell silently |
| Console WS | server-side throttle per viewport; `dropped_frames` counter visible in the UI |
| Webhooks (CISP) | bounded retry queue per subscriber; the subscriber's mandatory 60 s pull makes delivery loss recoverable |
| Authority DP polls (F3411) | one poller per active view; a Service Provider slower than p99 3 s is marked `slow` and its view polled at 0.5 Hz with age shown; never more than one in-flight request per view |
| Optional national push (USSP → authority) | 10 min bounded buffer, then drop oldest with a gap record both sides can reconcile from records |

## 6. Failure domains

| Domain | Fails alone | Everyone else |
|---|---|---|
| One ingest adapter instance | its connections reconnect to a peer; dedupe window absorbs replays | unaffected |
| `api` process (one system) | no new intents, accounts, records or publications; the hot-path processes keep every live service running on their KV projections (telemetry, network ID, traffic, conformance, picture) with projection age shown; their events wait in JetStream | unaffected |
| One hot-path process (one system) | that service is shown as down (e.g. `monitor` down: conformance and traffic alerts stop and the console says so; `rid-sp` down: peers and the authority see the USSP as `unavailable`); `api` and the other processes continue | other systems unaffected |
| One source type or instance (disabled or dead) | its tracks age out as `source_disabled` / `stale`, counted | other sources unaffected; consoles show state |
| NATS (one system) | ingest spills to local disk queue for 5 min; consumers hold last state; consoles freeze with age shown | other systems unaffected (no cross-system NATS) |
| TimescaleDB (one system) | live picture continues from NATS; records queue in JetStream mirror; replay later | unaffected |
| PostgreSQL (one system) | control plane refuses writes; hot path runs on projections (registry validity, zones, policy, switches) with age shown | unaffected |
| CISP | subscribers run on cache with `cis_age_s`; new U-space authorisations refused after 300 s | ANSP degraded path to USSPs |
| Authority | USSPs use validity cache 24 h; its DP views stop (USSP SP interfaces unaffected); receivers buffer | CISP keeps serving; no real-time service depends on the authority |
| DSS | cross-USSP deconfliction unavailable; the authority's ISA discovery falls back to known ISAs; local intents and all in-flight services continue | — |
| ANSP feed | USSP traffic information marks manned traffic unavailable; its own e-conspicuity receiver continues at trust `broadcast` | — |
| Token service | tokens valid for their TTL (≤ 1 h), JWKS cached 24 h; new tokens fail → systems alarm; a second issuer instance is the first scaling step | — |
| The whole droplet (staging) | everything; flights are unaffected because nothing commands them | — |

Deployment (staging): one DigitalOcean droplet, Caddy terminating TLS for `uspace-authority.chikox.net`, `uspace-cisp.chikox.net`, `uspace-ussp.chikox.net`, `uspace-ansp.chikox.net`, `uspace-lab.chikox.net`; `courier.chikox.net` is the operator. The old `utm.chikox.net` and `ingest.chikox.net` stay with the predecessor until the new authority replaces it. Each system runs its own docker-compose project (its Go processes from one image with different entrypoints, `web` Next.js, one `timescale/timescaledb-ha:pg16` container holding both its databases, since the image ships PostGIS, and NATS) on an isolated network; a system moves its databases to two hosts only when it outgrows one (`§4`). The only shared component is Caddy. Production domains will be state-owned; nothing in a repo may hardcode a hostname.

## 7. What a load test must prove

Run from `uspace-lab` against the staging images with simulated operators, receivers, ANSP feed and a second USSP on a local DSS, at 100, 1000 and 5000 drones for 60 min each (2 h soak at the target tier):

| Property | Pass criterion |
|---|---|
| Ingest-to-picture latency | p99 < 1 s (operator telemetry to USSP console frame; receiver frame to authority picture) |
| F3411 timing | SP `GET /uss/flights` p95 ≤ 1 s, p99 ≤ 3 s under load; authority DP display p95 ≤ 1 s, p99 ≤ 3 s after SP response; details p95 ≤ 2 s, p99 ≤ 6 s; DP cache empty of anything older than 24 h |
| F3548 timing | peer notification of an intent change ≤ 5 s; conflicting-intent notification to the other USS ≤ 1 s; details request answered ≤ 1 s; constraint notification ≤ 5 s |
| Alert latency | proximity / nonconformance raised p99 < 2 s after the triggering sample's `captured_at`; zone alert within one tick of entry; Art. 13(2) notices to peers and ANSP within 5 s and acknowledged |
| Missed alerts | zero against the scenario's expected set; every expected clear observed |
| No silent loss | every `dropped_*`, `gap`, `degraded` counter accounted for in the report; sum of accepted + dropped = sent |
| CPA budget | pair checks per worker per second within budget; `evaluation_period_s` never above 2 s |
| Storage | write rate sustained with writer queue < 10 s; compression job keeps up; disk growth matches the model within 20 % |
| Restart | kill each component in turn mid-run: picture recovers within 10 s, no duplicate alerts, no lost backlog |
| Partition | kill NATS of one system for 60 s: ingest spills and replays; other systems show nothing |
| Cross-system outage | CISP, authority, DSS, ANSP each taken down for 5 min: the degraded behaviours of `02` observed, nothing hidden, everything flagged with age |
| Memory | no monotonic growth over the soak |
| Vectors | all `knowledge/vectors/` behaviour vectors pass against the packages in every image under test; schema examples pass everywhere |

## Errata

| Date | Where | Change | Source |
|---|---|---|---|
| 2026-10-02 | §3, subjects table | Accepted internal deviations: the ANSP uses `man.v1.<adapter>.<icao24>` with no cells (tens of aircraft); the authority adds `tsw.v1.<table>`, `zones.v1.changed`, `registry.v1.changed`; the USSP adds `conf.v1`, `peer.v1`, `traffic.product.v1`; the CISP uses `cis.v1.change.<dataset>` internally while consumers see `cis.v1.<dataset>`. No cross-plan conflict. | `docs/decisions/2026-10-02-cross-plan.md` M30 |
| 2026-10-04 | §3, partition key | The partition key is core's pure-Go `geodesy/cell` grid (`cell5` 0.1° × 0.1°, `cell3` 1° × 1°, names `c5:<lat_idx>:<lon_idx>` / `c3:<lat_idx>:<lon_idx>`, ring-1 neighbours, bbox → cell set), not H3 r5/r3; the ANSP and the CISP do not partition. | `docs/decisions/2026-10-02-cross-plan.md` M35; authority Q-A3, ussp Q2 |
| 2026-10-04 | §6, deployment paragraph | On the droplet one `timescale/timescaledb-ha:pg16` container per system holds both of its databases (the pair and the two migration trees stay). | `docs/decisions/2026-10-02-cross-plan.md` M37; cisp Q12 |

# 05 — Scale and reliability

## 1. Load model

Assumptions: 1 Hz telemetry per airborne UAS (F3411 network minimum); average internal `track/telemetry/v1` 600 B JSON; direct Remote ID at 1–3 frames/s per heard aircraft and ~40 % of airborne UAS heard by at least one receiver; manned traffic tens of aircraft; 2.5 intents per drone-day; consoles 10 / 30 / 60 concurrent, each viewing a cell set with ≤ 200 tracks.

| Quantity | 100 drones | 1000 drones | 5000 drones |
|---|---|---|---|
| Operator telemetry into USSP (msg/s) | 100 | 1 000 | 5 000 |
| USSP → authority display push (msg/s) | 100 | 1 000 | 5 000 |
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

| System | Hot path (stateless ingest, partitioned consumers, sub-second) | Control plane (transactional, seconds, PostgreSQL) |
|---|---|---|
| authority | RID observation decode and time placement; USSP display ingest; ANSP manned ingest; identification resolution from a registry projection; violation detectors (120 m, zones, unregistered, no authorisation); picture WS by viewport | registry, zones, certificates, incidents, evidence, source control, audit, token service |
| cisp | change notification fan-out; bbox reads from a materialised current-version table; public map | publications, versions, subscriptions, delivery log |
| ussp | telemetry ingest; conformance per flight; CPA / traffic information per cell; geo-awareness evaluation; alert state machine; F3411 SP serving; traffic WS | intents, DSS interaction, registry validation, accounts, records, occurrences |
| ansp | manned feed normalisation and fan-out | restrictions lifecycle, coordination inbox, adapters |

Rules: ingest processes hold no per-aircraft state beyond a short dedupe window and never touch the relational database; they publish to NATS and write to TimescaleDB through a batching writer. State needed on the hot path (registry validity, policy, zones, source switches) is projected into memory from a NATS KV bucket or the time-series database and refreshed by push plus periodic re-read; a failed refresh keeps the last state and logs.

## 3. Partitioning and NATS subject design

Partition key: **H3 cell at resolution 5** (`cell5`, average edge ≈ 8.5 km, area ≈ 250 km²; Georgia ≈ 280 cells) with the parent **resolution 3** (`cell3`, ≈ 12 cells for the country) as the coarse key. A track's cell is computed at ingest from its position; a consumer owning a set of `cell5`s also subscribes to their ring-1 neighbours for CPA (800 m search radius ≪ cell edge, so one ring suffices).

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
| `ingest.v1.<cell3>` | JetStream work queue, 10 min | raw ingest handoff when the ingest tier must shed to a durable queue under backpressure |

Consumer scaling: CPA / conformance / detector workers form a JetStream consumer group per `cell3` at small scale (12 workers cover the country) and per `cell5` group at 5000 drones; ownership is a config map in KV, rebalanced by an operator action, not by auto-discovery (predictability over elegance). Consoles subscribe only to the `cell5`s intersecting their viewport plus a margin, and are throttled server-side to ≤ 2 Hz per track when a viewport holds > 200 tracks.

## 4. Storage, retention and compression

| Data | Store | Hot retention | Compressed / cold | Rationale |
|---|---|---|---|---|
| Telemetry, tracks, manned tracks, RID observations | TimescaleDB hypertables, 1-day chunks, `compress_segmentby` = track/flight id, `compress_orderby` = `captured_at` | 7 days uncompressed | compressed to 90 days online; monthly archive to object storage (Parquet) for 2 years at the authority, 1 year at USSPs — **numbers are design defaults; 2021/664 Annex III and national rules decide (Q8)** | evidence and records |
| Alerts, violations, conformance states | PostgreSQL | all | 5 years | regulatory |
| Intents, decisions, DSS log | PostgreSQL | all | 5 years | regulatory |
| Incidents, evidence packs | PostgreSQL + object storage, hash-sealed | all | indefinite | 376/2014 |
| CIS publications and versions | PostgreSQL | all | indefinite | "what was published at T" |
| Audit `events` | PostgreSQL, append-only, partitioned by month | all | 10 years | oversight |
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
| Cross-system pushes (USSP → authority display) | 10 min bounded buffer, then drop oldest with a gap record both sides can reconcile from records |

## 6. Failure domains

| Domain | Fails alone | Everyone else |
|---|---|---|
| One ingest adapter instance | its connections reconnect to a peer; dedupe window absorbs replays | unaffected |
| One source type or instance (disabled or dead) | its tracks age out as `source_disabled` / `stale`, counted | other sources unaffected; consoles show state |
| NATS (one system) | ingest spills to local disk queue for 5 min; consumers hold last state; consoles freeze with age shown | other systems unaffected (no cross-system NATS) |
| TimescaleDB (one system) | live picture continues from NATS; records queue in JetStream mirror; replay later | unaffected |
| PostgreSQL (one system) | control plane refuses writes; hot path runs on projections (registry validity, zones, policy, switches) with age shown | unaffected |
| CISP | subscribers run on cache with `cis_age_s`; new U-space authorisations refused after 300 s | ANSP degraded path to USSPs |
| Authority | USSPs use validity cache 24 h; display push buffers; receivers buffer | CISP keeps serving; no real-time service depends on the authority |
| DSS | cross-USSP deconfliction unavailable; local intents and all in-flight services continue | — |
| ANSP feed | USSP traffic information marks manned traffic unavailable; optional own receiver at lower trust | — |
| Token service | tokens valid for their TTL (≤ 1 h), JWKS cached 24 h; new tokens fail → systems alarm; a second issuer instance is the first scaling step | — |
| The whole droplet (staging) | everything; flights are unaffected because nothing commands them | — |

Deployment (staging): one DigitalOcean droplet, Caddy terminating TLS for `uspace-authority.chikox.net`, `uspace-cisp.chikox.net`, `uspace-ussp.chikox.net`, `uspace-ansp.chikox.net`, `uspace-lab.chikox.net`; `courier.chikox.net` is the operator. The old `utm.chikox.net` and `ingest.chikox.net` stay with the predecessor until the new authority replaces it. Each system runs its own docker-compose project (API, workers, console, PostgreSQL+PostGIS, TimescaleDB, NATS) on an isolated network; the only shared component is Caddy. Production domains will be state-owned; nothing in a repo may hardcode a hostname.

## 7. What a load test must prove

Run from `uspace-lab` against the staging images with simulated operators, receivers, ANSP feed and a second USSP on a local DSS, at 100, 1000 and 5000 drones for 60 min each (2 h soak at the target tier):

| Property | Pass criterion |
|---|---|
| Ingest-to-picture latency | p99 < 1 s (operator telemetry to USSP console frame; receiver frame to authority picture) |
| Alert latency | proximity / nonconformance raised p99 < 2 s after the triggering sample's `captured_at`; zone alert within one tick of entry |
| Missed alerts | zero against the scenario's expected set; every expected clear observed |
| No silent loss | every `dropped_*`, `gap`, `degraded` counter accounted for in the report; sum of accepted + dropped = sent |
| CPA budget | pair checks per worker per second within budget; `evaluation_period_s` never above 2 s |
| Storage | write rate sustained with writer queue < 10 s; compression job keeps up; disk growth matches the model within 20 % |
| Restart | kill each component in turn mid-run: picture recovers within 10 s, no duplicate alerts, no lost backlog |
| Partition | kill NATS of one system for 60 s: ingest spills and replays; other systems show nothing |
| Cross-system outage | CISP, authority, DSS, ANSP each taken down for 5 min: the degraded behaviours of `02` observed, nothing hidden, everything flagged with age |
| Memory | no monotonic growth over the soak |
| Vectors | all language-neutral vectors pass on every image under test |

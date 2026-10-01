# 07 — Roadmap

Build order: knowledge transfer → cisp → authority → ussp → ansp → lab → courier client. The lab's scaffolding (SITL, schema vectors, scenario runner) starts in phase 0 because every later milestone's done-when needs it; its full load and demo work comes last.

## 1. Phases and milestones

### Phase 0 — Knowledge transfer and contracts (all repos created)

| Milestone | Done when |
|---|---|
| KT-1 Spec accepted | this `docs/spec/` reviewed by the owner; open questions sent to GCAA/ministry |
| KT-2 Shared contracts | `uspace-lab/schemas/` holds JSON Schemas for the catalogue of `04` and first vectors: identification resolution (predecessor `gateway/identification.py` table), time placement (`ts`/`rx_ts`/`captured_at`/`backlog`), CPA, zone evaluation per vertical reference, pressure-altitude widening, ED-318 round trip; a Go and a Python runner both pass them |
| KT-3 Repo skeletons | five repos with the same layout (`cmd/`, `internal/`, `api/openapi.yaml`, `schemas/`, `console/`, `migrations/{relational,timeseries}/`, `deploy/`), CI (lint, vet, test, gitleaks, vectors), Caddy entries for the five `uspace-*.chikox.net` names |
| KT-4 SITL baseline | `uspace-lab` runs N ArduCopter SITL instances and a MAVLink → operator-telemetry bridge and a MAVLink → ODID bridge (predecessor U-16), both receive-only |

### Phase 1 — `uspace-cisp`

| Milestone | Done when |
|---|---|
| **C-M1 Publish and read** (first demo) | the authority-role test client publishes an ED-318 zone set and a U-space airspace; `GET /v1/zones?bbox=` returns them with `ETag`; a second publication yields a diff in `/v1/changes`; a webhook subscriber receives the signed change within 1 s and the public map shows the zones |
| C-M2 Restrictions | ANSP-role client activates a restriction; subscribers notified within 1 s; `ended` and `cancelled` lifecycle; history by version and `at=` |
| C-M3 Hardening | 60 s reconciliation pull proven by killing the subscriber during a change; delivery log; rate-limited public API; CISP console (publications, subscriptions, deliveries) |

### Phase 2 — `uspace-authority`

| Milestone | Done when |
|---|---|
| **A-M1 Registry and zones** (first demo) | an operator, a pilot and two UAS registered, suspended and looked up by number and serial; `GET /v1/registry/validate` returns status only; an ED-318 zone authored in the console publishes to the CISP and appears on its map; every change in `events` |
| A-M2 Remote ID picture | a lab receiver posts signed ODID frames for SITL aircraft; decode, HAE → AMSL, pressure fallback, identity per transmitter, time placement; four identification statuses shown; source control by type and instance with `source_disabled` ageing |
| A-M3 Violations and incidents | 120 m (with DEM), zone incursion, unregistered and identification-mismatch violations raised and closed from SITL; an incident opened from a violation; evidence pack exported and its hash verified; 376/2014 intake endpoint accepts an occurrence from a test USSP |
| A-M4 Ecosystem token service and USSP display ingest | clients registered from certificates; JWKS; the USSP's display push appears as `trust: provider`; police realm with purpose-logged queries |
| A-M5 Replace the predecessor | `utm.chikox.net` and `ingest.chikox.net` retired; the authority holds the registry and the picture |

### Phase 3 — `uspace-ussp`

| Milestone | Done when |
|---|---|
| **S-M1 Intents and geo-awareness** (first demo) | an operator client files an intent; it is checked against the CIS cache (zones, restrictions) and existing intents; two overlapping intents filed in either order give the same decision; a refusal names the conflicting item; registry validity fetched from the authority and cached |
| S-M2 Telemetry, network ID, conformance | SITL aircraft stream telemetry over the operator WS; `/uss/flights` serves them (F3411 v22a); the authority's DP view shows them; conformance raises and clears on a SITL aircraft leaving its volume, including height above the authorised upper; lost link after the configured silence |
| S-M3 Traffic information | CPA proximity alerts between two SITL aircraft and between a SITL aircraft and a lab manned track; traffic WS by intent and bbox with trust class and age; stale and unavailable sources flagged |
| S-M4 DSS and peers | InterUSS DSS in the lab; intents written as F3548 references with ovn; a second lab USSP's intent conflicts are detected; `pending_dss` behaviour when the DSS is down; peer flights via F3411 as `provider` |
| S-M5 Records and occurrences | per-flight records fetched by the authority; an airprox occurrence posted; USSP console (flights, alerts, DSS state, degraded inputs) |

### Phase 4 — `uspace-ansp`

| Milestone | Done when |
|---|---|
| **N-M1 Restrictions and manned feed** (first demo) | a supervisor activates a restriction over a SITL aircraft: the CISP publishes within 1 s, the USSP raises `restriction_activated` on the affected intent within one tick, the authority shows it; a recorded ADS-B file streams as manned traffic to the USSP and the authority |
| N-M2 Coordination | Annex V inbox receives intents touching the restricted volume and non-conformance notices; degraded direct path to USSPs when the CISP is down |

### Phase 5 — `uspace-lab` (full)

| Milestone | Done when |
|---|---|
| **L-M1 Scenario suite** (first demo) | `make demo` on a clean checkout brings up all four systems, N SITL aircraft, a receiver, an ANSP feed and a peer USSP, and produces every planned event (zone alert, conformance breach, proximity, unregistered broadcast, restriction activation, 120 m violation) |
| L-M2 Load | the `05 §7` report at 100 and 1000 drones with every pass criterion met; 5000 attempted and the format decision revisited |
| L-M3 Chaos | each failure domain of `05 §6` exercised with the expected degraded behaviour recorded |

### Phase 6 — `courier` as a USSP client

| Milestone | Done when |
|---|---|
| **K-M1** | courier files intents for its missions, streams telemetry, displays traffic information and acknowledges alerts through the public operator API only; a mission into a restricted zone is refused with the zone named |

## 2. Mapping of the predecessor's tasks

| Old task | What it was | New home | Status |
|---|---|---|---|
| U-01 UAS operator registry | operators, pilots, UAS | authority A-M1 | kept |
| U-02 Network identification | statuses, resolution in adapters, network RID ingest, spoof guard | USSP S-M2 serves it (Art. 8); authority A-M2/A-M4 consumes and resolves for its picture; status table and `serial_conflict` rule as vectors (KT-2) | kept, split |
| U-03 Geo-awareness / ED-269 | zone model, import/export, editor | authority A-M1 authors (ED-318 supersedes ED-269 data model); CISP C-M1 publishes; USSP S-M1 evaluates | kept, split |
| U-04 Dynamic airspace reconfiguration | temporary restriction | ANSP N-M1 (owner), CISP C-M2, USSP alert | kept, re-assigned to ANSP |
| U-05 Flight authorisation | 4D intents, overlap checks | USSP S-M1 | kept |
| U-06 Conformance monitoring | track vs authorisation | USSP S-M2 (height conformance against the volume here) | kept |
| U-07 Traffic information | manned traffic, proximity | USSP S-M3 with manned feed from ANSP N-M1 | kept, re-assigned source |
| U-08 Weather | METAR, thresholds | USSP optional, after S-M5 | kept, deferred |
| U-09 Common Information Service | read API, ED-318 | CISP C-M1..M3 | kept, became a system |
| U-10 USSP interoperability | F3548 via DSS | USSP S-M4 | kept |
| U-11 Operator portal | zones, intents, decisions | USSP portal (S-M1/S-M2); registration part at the authority portal | kept, split |
| U-12 Incidents and evidence | incidents, evidence packs | authority A-M3 | kept |
| U-13 Authority role and oversight | regulator role, audit of views | authority (its whole console); USSP oversight views via records | kept |
| U-14 Non-cooperative detection | sensor adapter | authority, after A-M5 (`trust: sensor`) | deferred |
| U-15 Source isolation and control | per-type/instance switches, KV + push | authority (receivers, USSP feeds, ANSP feed) and USSP (operator clients, peers, ANSP feed); pattern in KT-2 vectors | kept, duplicated per system |
| U-16 SITL as Remote ID source | ODID bridge | lab KT-4 | kept |
| U-17 Operator-facing interoperability | operators publish network RID and read traffic | USSP public operator API (S-M2, S-M3); courier K-M1 | kept |
| S-10 Remote ID spoofing guard | distance guard | authority A-M2 and USSP broadcast fusion; vector | kept |
| S-11..S-13 airspace monitor robustness | track timestamps, check isolation, policy reload | USSP monitors and authority detectors; policy as versioned row | kept |
| S-27 broadcast capture time | time placement for ODID | authority A-M2; vector | kept |
| S-32 identity per transmitter TTLs | 15 s / 3 s rules | authority A-M2; vector | kept |
| S-33 pressure altitude fallback | ISA fallback, widening | authority A-M2, USSP zone evaluation; vector | kept |
| S-34, S-35 transmitter edge cases | two serials on one address, lent identity | authority A-M2 | kept |
| S-36 bridge invalid altitude flag | SITL bridge | lab KT-4 | kept |
| S-37 WGS84-only limit judgement | zone limit without geoid | authority and USSP zone evaluation; vector | kept |
| S-15, S-16 API hardening | login limits, UTC params | every API (platform baseline) | kept |
| S-20..S-23, S-31 CI/deploy/backups | SITL in CI, SHA tags, rollback, backups, zero-downtime | KT-3 platform baseline per repo | kept |
| S-25 alert transition numbers | clear reports its own values | alert schema `04 §3.3` | kept |
| P1-15, M-01 Remote ID ingest | receiver protocol | authority F9 (HTTPS + HMAC replaces UDP) | kept, transport changed |
| M-02, M-06, M-07 violations, regulator audit, reports | — | authority A-M3, A-M4 | kept |
| P1-01..P1-13, S-01..S-09, S-24, S-30 operator relay (relay-v1), MAVLink gateway, drain logic | QGC relay, backlog drain | **dropped**: operators feed network ID through the USSP API; the relay's `backlog`/drain semantics survive as the time fields and the operator client's queue rule | dropped (concepts kept) |
| P1-09 station link quality | relay link metrics | **dropped** | dropped |
| P5-08, P5-09, P5-16 deterministic resolution advice | lower id holds, higher descends | **dropped**: the USSP informs, it does not resolve; the remote pilot decides | dropped |
| P5-19 height limit as alert to operators | 120 m alert in the monitor | authority violation only; USSP height conformance is against the authorised volume | re-scoped |
| P5-17 OpenUTM evaluation | adopt or build | decided: InterUSS DSS adopted; USSP services built | closed |
| D-01..D-04 demo readiness | `make demo`, soak, staging check, script | lab L-M1..L-M3 | kept |

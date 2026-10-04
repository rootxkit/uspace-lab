# 07 — Roadmap

Build order: knowledge transfer → `uspace-core` → cisp (with `uspace-ui`) → authority → ussp → ansp → lab (conformance suite, load, demo) → courier client. `uspace-core` comes first because the knowledge vectors are ported as its tests and every system compiles it in; `uspace-ui` starts with the CISP, the first Next.js app. The lab's scaffolding (SITL, schema vectors, scenario runner) starts in phase 0 because every later milestone's done-when needs it; its conformance suite, load and demo work comes last.

## 1. Phases and milestones

### Phase 0 — Knowledge transfer and contracts (all repos created)

| Milestone | Done when |
|---|---|
| KT-1 Spec accepted | this `docs/spec/` reviewed by the owner; open questions sent to GCAA/ministry |
| KT-2 Shared contracts | each producing repo owns its JSON Schemas under `schemas/` (`04 §1`); `uspace-lab/schemas/` is the read-only aggregate from which the Go structs and, Go → TypeScript, the client types are generated and checked; `uspace-lab/api/` aggregates the OpenAPI files of every system (`00 §7`) |
| KT-3 Repo skeletons | `uspace-core` and `uspace-ui` (already created) plus five system repos with the same layout: `cmd/<process>/`, `internal/`, `migrations/{relational,timeseries}/`, `api/openapi.yaml`, `schemas/`, `web/` (Next.js), `deploy/`; CI: Go lint, vet, test, vectors; Next.js lint, build, generated types up to date, no-geometry-import and no-server-side-business-logic rules; gitleaks everywhere; Caddy entries for the five `uspace-*.chikox.net` names |
| KT-4 SITL baseline | `uspace-lab` runs N ArduCopter SITL instances and a MAVLink → operator-telemetry bridge and a MAVLink → ODID bridge (predecessor U-16), both receive-only |

### Phase 1 — `uspace-core`

| Milestone | Done when |
|---|---|
| **G-M1 Vectors as tests** (first tag `v0.1.0`) | every file in `uspace-lab/knowledge/vectors/` is loaded by the `vectors` harness and passes against its package (`odid`, `geodesy`, `terrain`/`geoid`, `ed269`/`ed318`, `regnum`/`serial`, `identify`, `zones`, `cpa`, `timeplace`, `sources`, `auth`); `go test ./...` is the whole proof; a failing vector fails the tag |
| G-M2 Standards types | `f3411` and `f3548` types generated from `uas_standards` with round-trip tests; `ed318` parse / validate with the ED-269 mapping and an ED-318 round-trip vector added to the knowledge set |
| G-M3 Policy | `CHANGELOG`, semver rules of `00 §6.3` enforced in CI (a changed vector without a major bump fails), `jwt_verify` vector added, `v1.0.0` tagged |

### Phase 2 — `uspace-cisp` and `uspace-ui`

| Milestone | Done when |
|---|---|
| **C-M1 Publish and read** (first demo) | the authority-role test client publishes an ED-318 zone set and a U-space airspace with its Art. 3(4) requirements, adjacency and USSP list with terms; `GET /v1/zones?bbox=` returns them with `ETag` and `cis_updated_at`; a second publication yields a diff in `/v1/changes`; a webhook subscriber receives the signed change within 1 s and the public map shows the zones; an ED-269 file round-trips through the import mapping |
| C-M2 Restrictions | ANSP-role client activates a restriction; subscribers notified within 1 s; the same restriction appears as an F3548 constraint in the lab DSS; `ended` and `cancelled` lifecycle; history by version and `at=` |
| C-M3 Hardening | 60 s reconciliation pull proven by killing the subscriber during a change; delivery log; rate-limited public API; CISP console (publications, subscriptions, deliveries) |
| **U-M1 `uspace-ui` first release** (with C-M1) | shadcn/ui theme and tokens, MapLibre map with the zone symbology, `ka`/`en` with Noto Sans Georgian, BFF session helpers; the CISP public map and console are built on it and nothing else |

### Phase 3 — `uspace-authority`

| Milestone | Done when |
|---|---|
| **A-M1 Registry and zones** (first demo) | an operator (all 2019/947 Art. 14(2) fields), a pilot and two UAS registered, suspended and looked up by number and serial; `GET /v1/registry/validate` returns status only; an ED-318 zone authored in the console publishes to the CISP and appears on its map; every change in `events` |
| A-M2 Remote ID picture | a lab receiver posts signed ODID frames for SITL aircraft; decode, HAE → AMSL, pressure fallback, identity per transmitter, time placement; four identification statuses shown; source control by type and instance with `source_disabled` ageing |
| A-M3 Violations and occurrences | 120 m (with DEM), zone incursion, unregistered and identification-mismatch violations raised and closed from SITL; an incident opened from a violation; evidence pack exported and its hash verified; 376/2014 intake accepts an occurrence from a test USSP, holds the reporter identity for `incident_officer` only, exports a de-identified ECCAIRS/ADREP-compatible record, and cannot be linked to a violation |
| A-M4 Ecosystem token service and F3411 Display Provider | clients registered from certificates; JWKS; the authority discovers the lab USSP's ISAs through the lab DSS, polls `/uss/flights` per view and shows flights as `trust: provider`; the DP cache is proven empty of data older than 24 h; USSP start-of-operations notice recorded; police realm with purpose-logged queries |
| A-M5 Replace the predecessor | `utm.chikox.net` and `ingest.chikox.net` retired; the authority holds the registry and the picture |

### Phase 4 — `uspace-ussp`

| Milestone | Done when |
|---|---|
| **S-M1 Intents and geo-awareness** (first demo) | an operator client files an intent with all ten Annex IV items; it is checked against the CIS cache (zones, restrictions, airspace constraints) and existing intents; two overlapping intents filed in either order give the same decision; a special-operation intent (SERA Art. 4) wins priority, equal priority is first come first served; a refusal names the conflicting item; an acceptance carries an authorisation number and deviation thresholds; activation is confirmed; registry validity fetched from the authority and cached; a C0 A1 flight is accepted without authorisation (Art. 1(3)) |
| S-M2 Telemetry, network ID, conformance | SITL aircraft stream telemetry over the operator WS; an ISA is created in the lab DSS and `/uss/flights` serves them (F3411 v22a, p99 ≤ 3 s); the authority's DP view shows them; conformance raises and clears on a SITL aircraft leaving its volume or its deviation thresholds, including height above the authorised upper; nearby operators get `nonconformance_nearby`, the lab ANSP acknowledges the notice, the peer sees `Nonconforming`; lost link after the configured silence |
| S-M3 Traffic information | CPA proximity alerts between two SITL aircraft and between a SITL aircraft and a lab manned track; a lab ADS-B e-conspicuity source feeds traffic information directly; traffic WS by intent and bbox with trust class and age; stale and unavailable sources flagged |
| S-M4 DSS and peers | InterUSS DSS in the lab; intents written as F3548 references with ovn; a second lab USSP's intent conflicts are detected and notified within 1 s; `pending_dss` behaviour when the DSS is down; peer flights via F3411 as `provider`; a lab constraint from the ANSP triggers `restriction_activated` and an authorisation update; peer data purged at 24 h |
| S-M5 Records and occurrences | per-flight records (≥ 30 days) fetched by the authority; an airprox occurrence posted within 72 h of awareness; start / cease notices; USSP console (flights, alerts, DSS state, degraded inputs, emergency workflow) |

### Phase 5 — `uspace-ansp`

| Milestone | Done when |
|---|---|
| **N-M1 Restrictions and manned feed** (first demo) | a supervisor activates a restriction over a SITL aircraft: the CISP publishes within 1 s and the DSS constraint is written, the USSP raises `restriction_activated` on the affected intent within one tick, the authority shows it; a recorded ADS-B file streams as manned traffic to the USSP and the authority (ATS.OR.127) |
| N-M2 Coordination | Annex V inbox receives intents touching the restricted volume and non-conformance notices and acknowledges them (Art. 13(2)); degraded direct path to USSPs when the CISP is down |

### Phase 6 — `uspace-lab` (conformance suite, load, demo)

| Milestone | Done when |
|---|---|
| **L-M1 Scenario suite** (first demo) | `make demo` on a clean checkout brings up all four systems, N SITL aircraft, a receiver, an ANSP feed and a peer USSP, and produces every planned event (zone alert, conformance breach, proximity, unregistered broadcast, restriction activation, 120 m violation) |
| L-M2 Load | the `05 §7` report at 100 and 1000 drones with every pass criterion met; 5000 attempted and the format decision revisited |
| L-M3 Chaos | each failure domain of `05 §6` exercised with the expected degraded behaviour recorded |
| L-M4 Conformance suite | `uspace-lab/conformance/` runs InterUSS `uss_qualifier` (F3411 SP/DP, F3548) against `uspace-ussp` and the lab DSS, the national OpenAPI contract tests against every system, and the ED-318 publication tests against `uspace-cisp`; a third-party onboarding procedure is documented and rehearsed with the lab's simulated peer USSP as the candidate; the signed report format is agreed with GCAA (Q7) |

### Phase 7 — `courier` as a USSP client

| Milestone | Done when |
|---|---|
| **K-M1** | courier files intents for its missions, streams telemetry, displays traffic information and acknowledges alerts through the public operator API only; a mission into a restricted zone is refused with the zone named |

## 2. Mapping of the predecessor's tasks

| Old task | What it was | New home | Status |
|---|---|---|---|
| U-01 UAS operator registry | operators, pilots, UAS | authority A-M1 | kept |
| U-02 Network identification | statuses, resolution in adapters, network RID ingest, spoof guard | USSP S-M2 serves it as an F3411 Service Provider (Art. 8); authority A-M4 consumes it as a standard F3411 Display Provider and resolves for its picture (the predecessor's push ingest survives only as the optional national extension); status table and `serial_conflict` rule as vectors (KT-2) | kept, split |
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
| P5-19 height limit as alert to operators | 120 m alert in the monitor | authority violation (947 Art. 18(k)); USSP height conformance is against the authorised volume and the airspace's Art. 3(4)(c) constraints, which cap the volume, so inside U-space airspace the two agree | re-scoped |
| P5-17 OpenUTM evaluation | adopt or build | decided: InterUSS DSS adopted; USSP services built | closed |
| D-01..D-04 demo readiness | `make demo`, soak, staging check, script | lab L-M1..L-M3 | kept |

## Errata

| Date | Where | Change | Source |
|---|---|---|---|
| 2026-10-04 | §1 Phase 2, C-M1 row | The read returns `ETag` and `cis_updated_at`, not `updateDateTime` (ED-318 metadata follows `uspace-core/ed318.Metadata`). | `docs/decisions/2026-10-02-cross-plan.md` M15 |

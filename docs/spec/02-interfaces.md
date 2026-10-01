# 02 — Interfaces

Every cross-system flow. Internal messaging (NATS) never crosses a system boundary; it is listed in `04-messages.md` and `05-scale-and-reliability.md`. Volumes assume 1 Hz telemetry per airborne UAS (F3411 network rate) and a fleet-wide average message of 600 B JSON.

## 1. Conventions

| Topic | Rule |
|---|---|
| Transport | REST (JSON, OpenAPI 3.1) for request/response; WebSocket for live streams to clients and consoles; signed webhooks plus pull reconciliation for server-to-server change notification. No cross-system NATS. |
| Auth | OAuth2 client credentials, JWT RS256 with `scope`, `aud` (the target system), `sub` (client id), TTL ≤ 1 h, issued by the ecosystem token service run by the authority (`06-security.md`). Operators authenticate to the USSP's own issuer. mTLS in addition for the ANSP streams. |
| Time | RFC 3339 UTC with `Z`, millisecond precision. Every record carries `ts` (source clock) and the receiver adds `rx_ts` (`04-messages.md`). |
| Geometry | GeoJSON, WGS84 (SRID 4326), `[lng, lat]`; altitudes in metres with explicit reference (`AMSL`, `AGL`, `WGS84`). |
| Versioning | Path version (`/v1`), schema `$id` version per message; unknown fields ignored, never rejected, within a major. |
| Failure rule | A consumer that loses a producer keeps the last data, marks it stale with age, and keeps serving. A producer that loses a consumer queues (bounded, counted) and reports. Nothing hides an aircraft or disables a source silently; every disable is an audited act by a person. |

## 2. Flow catalogue

### F1 Authority → CISP: zones, U-space airspace, USSP list

| Item | Value |
|---|---|
| Producer / consumer | authority (`publisher`) → cisp |
| Data | ED-318 `FeatureCollection` of `UASZone` (type PROHIBITED / REQ_AUTHORIZATION / CONDITIONAL / NO_RESTRICTION / USPACE), each with identifier, country `GEO`, vertical layer (`lower`, `upper`, `uom` m, references AGL/AMSL/WGS84), applicability (`TimePeriod`, `DailyPeriod`), reasons, authority contact, `restrictionConditions`; U-space airspace volumes with their service set; the USSP list (name, certificate id, base URL, services, validity). Field names follow `uas_standards.eurocae_ed318`. |
| API | `PUT /v1/publications/{dataset}` with the full dataset and `If-Match: <previous version>`; datasets `zones`, `uspace_airspace`, `ussp_list`. Full replacement per publication, diffed by the CISP. |
| Transport / auth | REST; scopes `cis.publish:zones`, `cis.publish:uspace`, `cis.publish:ussp_list`; payload detached-signed by the authority (JWS) so the CISP can prove provenance. |
| Frequency / volume | Tens of publications per month; a dataset of a few thousand zones is < 20 MB. Same at 100 and 1000 drones. |
| Failure | CISP down: the authority keeps the pending publication, retries with backoff, shows "not yet published" on its console with age; the CISP keeps serving the previous version. Authority down: the CISP serves the last version; nothing expires on its own except by the zone's own applicability. |

### F2 ANSP → CISP: dynamic restrictions

| Item | Value |
|---|---|
| Data | ED-318 `UASZone` features with reason `DAR`, time-bounded applicability, the U-space airspace they modify, the ANSP's reference; states `planned`, `active`, `ended`, `cancelled`. |
| API | `POST /v1/restrictions`, `PATCH /v1/restrictions/{id}` (end, extend), idempotency key = ANSP reference. |
| Transport / auth | REST; scope `cis.publish:restrictions`; mTLS client cert bound to the ANSP client id. |
| Frequency / volume | Expected a few per day, bursts of tens during events; bytes negligible. |
| Failure | CISP down: the ANSP retries, raises an alarm to its supervisor after 10 s, and sends the restriction directly to subscribed USSPs and the authority on the same contract (`F3` payload) as a degraded path; the CISP reconciles when back (restrictions carry ANSP-side version). ANSP down: active restrictions stay active until their `endDateTime`; the CISP flags "source stale" after 60 s of missed heartbeat. |

### F3 CISP → USSPs, authority, ANSP: publication and change notification

| Item | Value |
|---|---|
| Data | ED-318 `FeatureCollection`s; a change feed of `(dataset, version, feature ids changed, reason)`. |
| API | Pull: `GET /v1/{dataset}?bbox=&at=&since_version=` with `ETag` = version; `GET /v1/{dataset}/versions/{v}` for history; `GET /v1/changes?since=` cursor feed. Push: subscriber registers `POST /v1/subscriptions {callback_url, datasets, bbox}`; the CISP POSTs a signed change notification (`JWS`, body = change record) and the subscriber pulls the delta. Consoles and the public map use `WS /v1/stream` and the public `GET`. |
| Transport / auth | REST + webhook; scope `cis.read`; webhook signed with the CISP's key (JWKS published). |
| Frequency / volume | Change notifications: a few per day plus restrictions; pull reconciliation every 60 s per subscriber (cheap `HEAD` on `ETag`). Volume independent of drone count. |
| Failure | Subscriber unreachable: notifications retried with exponential backoff for 24 h, delivery log kept; the subscriber's own 60 s reconciliation pull is mandatory, so a missed webhook costs at most 60 s. CISP unreachable: subscribers keep their cache with `cis_version` and `cis_age_s` on every output; a USSP whose CIS cache is older than the configured bound (default 300 s) refuses new authorisations inside U-space airspace with `cis_stale` and keeps serving in-flight services. |

### F4 ANSP → USSP and authority: manned traffic

| Item | Value |
|---|---|
| Data | `manned_track.v1`: ICAO 24-bit address or callsign, position, altitude (barometric `pressure_alt_m` and geometric `alt_wgs84_m` when known), ground speed, track, vertical rate, source class (`ads_b`, `mode_s`, `ssr`, `atm_feed`, `ads_l`), `ts`, quality. Only tracks relevant to U-space airspace plus a configurable margin (default 5 km, 1500 m above). |
| API | `WS /v1/manned-traffic/stream?bbox=` (newline-delimited JSON frames, 1 Hz per aircraft), `GET /v1/manned-traffic/snapshot?bbox=` for bootstrap. |
| Transport / auth | WebSocket over TLS, ecosystem token with scope `ansp.traffic`, mTLS mandatory. |
| Frequency / volume | 1 Hz × N manned aircraft (tens, not thousands); independent of drone count. |
| Failure | ANSP stream down: USSP marks manned traffic `unavailable since T` on every traffic-information product, raises an operational alarm, and may fall back to its own ADS-B/ADS-L receiver (lower trust class `broadcast`). It never shows an empty sky as clear. Authority down: no effect on the ANSP. |

### F5 Operator ↔ USSP (public operator API)

| Sub-flow | Direction | Data | API | Frequency at 100 / 1000 drones | Failure |
|---|---|---|---|---|---|
| Registration lookup | op → ussp | operator registration number, UAS serial, pilot id → validity status (never PII) | `GET /v1/registry/validate?operator=&serial=&pilot=` | per intent; ≤ 1/s | USSP caches positive answers for 24 h (`03-data-models.md`), negative for 5 min; cache miss while authority down → `unknown`, intent held `pending_validation` |
| Operational intent (authorisation) | op → ussp | Annex IV request as F3548 `OperationalIntent`: volumes (`Volume4D`, altitudes WGS84 per F3548 and AMSL derived by the USSP), mode, contingency, operator/UAS/pilot refs, emergency contact | `POST /v1/intents`, `PATCH /v1/intents/{id}` (activate, modify, end), `GET /v1/intents/{id}` | ≈ 2–3 intents per drone-day: 300 / 3000 per day | DSS down: intents inside U-space airspace that need cross-USSP deconfliction are `pending_dss`; intents outside U-space airspace proceed on local checks |
| Network ID telemetry | op → ussp | `telemetry.v1` (position, height AGL/AMSL as available, velocity, status, `ts`, emergency), 1 Hz | `WS /v1/telemetry` (preferred) or `POST /v1/telemetry/batch` | 100 / 1000 msg/s; 0.06 / 0.6 MB/s | Operator link down: flight shows `telemetry_lost` after 5 s, conformance raises `lost_link` after a configured 15 s; USSP down: operator client queues up to 10 min and sends as `backlog=true` on reconnect |
| Traffic information | ussp → op | nearby traffic (manned, other UAS, broadcast RID if the USSP has it) with trust class and age; CPA proximity alerts | `WS /v1/traffic?intent_id=` or `?bbox=`; `GET /v1/traffic/snapshot` | 1 Hz per subscribed client, filtered by a 2 km radius default | stale sources carry `age_s`; when the USSP's own inputs are stale the stream says so (`degraded: [manned, dss]`) |
| Geo-awareness | ussp → op | applicable zones and restrictions for a bbox / intent, CIS version | `GET /v1/geo?bbox=&at=`, change push over the same WS | on planning and on every CIS change | if CIS cache stale beyond bound, response marked `stale` with age |
| Conformance alerts | ussp → op | `alert.v1` kinds `nonconformance`, `height_exceedance`, `zone_incursion`, `lost_link`, `restriction_activated`, `proximity`; acknowledgement back | WS push + `POST /v1/alerts/{id}/ack` | event-driven | delivery recorded; unacknowledged critical alerts repeat every 10 s and are escalated to the USSP supervisor console |

### F6 USSP ↔ USSP via DSS (F3548, F3411)

| Item | Value |
|---|---|
| Data | F3548: `OperationalIntentReference` (id, manager, version, state, ovn, time window, `uss_base_url`), `ConstraintReference`, `Subscription`; USS-to-USS `GET /uss/v1/operational_intents/{id}` and notifications `POST /uss/v1/operational_intents`. F3411: `IdentificationServiceArea` in the DSS, `GET /uss/flights?view=` and `/uss/flights/{id}/details` between SP and DP. |
| API | InterUSS DSS `/dss/v1/...` (F3548 v21), `/rid/v2/dss/...` (F3411 v22a); field names from `uas_standards`. |
| Auth | Ecosystem token with F3548 scopes `utm.strategic_coordination`, `utm.constraint_processing`, `utm.constraint_management` (ANSP/authority constraints), `utm.conformance_monitoring_sa`; F3411 `rid.service_provider`, `rid.display_provider`. |
| Frequency / volume | One DSS write per intent state change (≈ 1000 / 10 000 per day); ISA per flight; peer `GET flights` polls at 1 Hz per DP view. |
| Failure | DSS down: existing intents keep their ovn; new cross-USSP deconfliction is unavailable (`pending_dss`); own-USSP intents and all in-flight services continue; the USSP console shows DSS state. Peer USSP down: its intents are treated as still valid until `time_end`; its flights disappear from traffic info with an explicit `peer_unavailable` marker. |

### F7 USSP → authority: network identification display, records, incidents

| Sub-flow | Data | API | Frequency | Failure |
|---|---|---|---|---|
| Network ID display | F3411 DP view: the authority acts as a Display Provider and polls `GET /uss/flights?view=` across the country in tiles; or, by national arrangement, subscribes to `WS /v1/authority/flights` for a 1 Hz push of every flight (same `RIDFlight` shape) | 1 Hz × airborne UAS: 100 / 1000 msg/s | authority down: the USSP buffers 10 min, then drops with a counted gap (its own records remain the legal record); USSP down: the authority shows the USSP as `unavailable since T` on its picture, never an empty map |
| Service records | per flight: authorisation, telemetry summary, alerts, conformance timeline (Annex III record), fetched by the authority on demand or delivered as daily bundles | `GET /v1/records/flights/{id}`, `GET /v1/records/daily/{date}` | daily plus on demand | retried; a missing day is an alarm on both sides |
| Incidents / occurrences | `occurrence.v1`: type (airprox, nonconformance_in_prohibited, lost_link_in_uspace, emergency), time, aircraft, intent, evidence refs, narrative; E5X/ECCAIRS export is the authority's job | `POST /v1/occurrences` on the authority | event-driven, within 72 h | queued and retried; the USSP console shows undelivered occurrences |

### F8 USSP → authority: registry validity lookups

| Item | Value |
|---|---|
| Data | Request: operator registration number, UAS serial (ANSI/CTA-2063-A) or registration mark, pilot reference. Response: per entity `valid` / `suspended` / `revoked` / `unknown`, validity end, class label and MTOM band for the UAS, competency set for the pilot. **No names, addresses, phone numbers or emails.** |
| API | `GET /v1/registry/validate`, batch `POST /v1/registry/validate`; `GET /v1/registry/changes?since=` (status change feed of ids only, for cache invalidation). |
| Auth | Scope `registry.validate`; one client per USSP; every call audited with purpose = `authorisation` or `identification`. |
| Frequency / volume | On each intent and each new serial seen: ≤ 10/s at 1000 drones; change feed polled every 30 s. |
| Failure | Authority down: cached answers used up to 24 h with `cache_age_s`; uncached → `unknown`, which blocks a new authorisation inside U-space airspace and merely marks a flight `identification: unknown_operator` elsewhere. |

### F9 Remote ID receivers → authority

| Item | Value |
|---|---|
| Data | `rid_observation.v1`: receiver id, transmitter address (BT/Wi-Fi MAC), raw ODID message or pack as hex, `rssi_dbm`, receiver `rx_ts`, receiver position. Decoding (Basic ID, Location, System, Operator ID, Auth), HAE → AMSL via geoid, pressure-altitude fallback, time placement and identity-per-transmitter rules are the authority's (`03`, `04`). |
| API | `POST /v1/rid/observations` batches of ≤ 1 s; `GET /v1/rid/receivers/{id}/config`; receiver heartbeat every 10 s. |
| Auth | Per-receiver bearer key plus HMAC-SHA256 over the body with a per-receiver secret (defence against key reuse from a captured device); keys revocable from the console; receivers disabled per instance or per type, audited (predecessor U-15). |
| Frequency / volume | A receiver hears ≤ tens of aircraft; 1–3 msg/s per heard aircraft (BT4 legacy sends Basic ID and Location separately). 50 receivers × 20 aircraft × 3 = 3000 msg/s worst case; 60 B raw frame + 200 B envelope. |
| Failure | Authority ingest down: receivers buffer in RAM/flash (minutes), replay with original `rx_ts` and `backlog=true`. Receiver down: shown `silent since T`; a receiver disabled by an admin is shown `disabled by <who>`, never merely silent. |

### F10 Police / authority queries

| Item | Value |
|---|---|
| Data | "Who is flying here now / who flew here at T", "what do we know about serial S / registration number R", "export evidence pack for incident I". Answers include operator identity only for a stated lawful purpose; every query, result count and export is audited and reportable to the data protection officer. |
| API | Authority console (police realm) and `GET /v1/police/aircraft?bbox=&at=`, `GET /v1/police/operators/{reg}` with mandatory `purpose` and case reference; evidence pack export signed with content hash. |
| Auth | Per-agency OIDC or local accounts with MFA; scope `police.query`; IP allow-list. |
| Frequency | Human-rate. |
| Failure | Read-only; degraded sources shown as such. |

### F11 Authority ↔ ANSP (direct)

Restriction requests and coordination outside the CISP (e.g. the authority asks for a restriction over an event, the ANSP reports a manned-traffic airprox): `POST /v1/occurrences` on the authority (same as F7), and `POST /v1/restriction-requests` on the ANSP. Low volume; audited both sides.

### F12 `uspace-lab` → everything (non-production)

The lab speaks only the public contracts above: simulated operators on F5, simulated receivers on F9, a simulated ANSP feed on F4, a second simulated USSP on F6 against a local InterUSS DSS. No test hook enters a production image.

## 3. API surfaces per system (endpoint groups)

### authority

| Group | Purpose | Consumers |
|---|---|---|
| `/v1/registry/*` | operators, UAS, pilots, competencies; CRUD for registrars, public validity check, status change feed, uas.gov.ge import | registrars, USSPs (validate only), portal |
| `/v1/zones/*`, `/v1/uspace/*` | ED-318 authoring, versioning, publication to CISP, airspace.gov.ge import rules | inspectors, CISP |
| `/v1/certificates/*` | USSP / CISP certificates, USSP list publication | admins, CISP |
| `/v1/rid/*` | receiver fleet, keys, observation ingest, raw frame retrieval | receivers, incident officers |
| `/v1/picture/*` | the authority's own traffic picture: fused network-ID (from USSPs) and direct-RID tracks with identification status, trust class, age; WS by viewport | console, police |
| `/v1/violations/*`, `/v1/incidents/*`, `/v1/occurrences` | detection rules (120 m, zones, unregistered, no authorisation), incidents, evidence packs, 376/2014 intake and E5X export | incident officers, USSPs, ANSP |
| `/v1/police/*` | lawful queries with purpose | police realm |
| `/v1/sources/*` | enable/disable receivers, USSP feeds, ANSP feed (by type and instance), audited | admins |
| `/v1/audit/*` | append-only events, views and exports included | admins, auditors |
| `/oauth/*`, `/.well-known/jwks.json` | ecosystem token service | all systems |

### cisp

| Group | Purpose |
|---|---|
| `/v1/publications/*` | authority and ANSP publish; version, sign, diff |
| `/v1/restrictions/*` | ANSP dynamic restrictions lifecycle |
| `/v1/zones`, `/v1/uspace_airspace`, `/v1/ussp_list`, `/v1/{dataset}/versions/*`, `/v1/changes` | ED-318 read, history, change feed, bbox and time filters |
| `/v1/subscriptions/*` | webhook registration and delivery log |
| `/v1/stream` | WS change stream for consoles and the public map |
| `/public/*` | unauthenticated read subset, cacheable |

### ussp

| Group | Purpose |
|---|---|
| `/v1/intents/*` | operational intents: create, activate, modify, end; decisions and conflicts |
| `/v1/telemetry` | network ID ingest (WS, batch) |
| `/v1/traffic/*` | traffic information stream and snapshot |
| `/v1/geo/*` | geo-awareness from the CIS cache |
| `/v1/alerts/*` | conformance, proximity, restriction alerts; acknowledgements |
| `/v1/registry/validate` | proxy to the authority with cache |
| `/uss/flights`, `/uss/flights/{id}/details`, `/uss/identification_service_areas/{id}` | F3411 Net-RID SP |
| `/uss/v1/operational_intents*`, `/uss/v1/constraints*`, `/uss/v1/reports` | F3548 USS endpoints |
| `/v1/authority/*`, `/v1/records/*` | authority display push, records, occurrence delivery status |
| `/v1/weather/*` | optional weather information |
| `/v1/accounts/*`, `/oidc/*` | operator accounts and clients |

### ansp

| Group | Purpose |
|---|---|
| `/v1/restrictions/*` | plan, activate, extend, end dynamic restrictions; publish to CISP |
| `/v1/restriction-requests` | inbound requests from the authority |
| `/v1/manned-traffic/*` | stream and snapshot for USSPs and the authority |
| `/v1/coordination/*` | inbound Annex V data from USSPs: intents touching controlled airspace, non-conformance notices |
| `/v1/adapters/*` | status of surveillance adapters |

### lab

No production API. `make` targets and a scenario runner; a results API for CI dashboards only.

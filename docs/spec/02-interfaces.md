# 02 — Interfaces

Every cross-system flow. Internal messaging (NATS) never crosses a system boundary; it is listed in `04-messages.md` and `05-scale-and-reliability.md`. Volumes assume 1 Hz telemetry per airborne UAS (the authority's determination under 2021/664 Art. 8(3) and 11(3)(b); F3411 `NetMinUasLocRefreshFrequencyHz = 1`) and a fleet-wide average message of 600 B JSON. Standard endpoints and field names are those of `uas_standards` (F3411 v22a, F3548 v21, ED-318).

## 1. Conventions

| Topic | Rule |
|---|---|
| Transport | REST (JSON, OpenAPI 3.1) for request/response; WebSocket for live streams to clients and consoles; signed webhooks plus pull reconciliation for server-to-server change notification. No cross-system NATS. |
| Auth | OAuth2 client credentials, JWT RS256 with `scope`, `aud` = the host of the target's published base URL (`uspace-cisp.chikox.net`, the DSS's host, a peer's `uss_base_url` host; every verifier accepts a configured `*_AUDIENCES` list: its public host plus a lab alias such as the compose service name; `POST /oauth/token` takes `audience` or RFC 8707 `resource`, restricted per client for national scopes and free for `utm.*` / `rid.*`, since peers are discovered), `sub` (client id, one client per calling system: `authority-01`, `cisp-01`, `ansp-01`, `ussp-<code>-01`, `lab-01`), TTL ≤ 1 h, issued by the ecosystem token service run by the authority (`06-security.md`; who issues is a national choice — F3411/F3548 and the InterUSS DSS only require a configured trusted issuer). Operators authenticate to the USSP's own issuer. mTLS in addition on the mTLS routes (`/v1/restrictions*`, `/v1/publishers/heartbeat`, `/v1/manned-traffic/*`, `/v1/coordination/*`), switched by one flag per system, `<SYS>_MTLS_MODE` = `required` or `off` (`required` in production; `off` on the staging droplet and in the lab, printed at error level every status period): Caddy verifies a presented certificate and forwards `X-Client-Cert-Subject`, stripped on every other route, and the Go middleware binds the subject. Annex III A(4) authenticated transfer and B(1) encryption are met by TLS plus signed payloads. |
| Sessions and browsers | One console / portal session JWT, verified by the same `uspace-core/auth` verifier: `iss` = the system's own issuer, `aud` = the system's own host, `sub` = account id, `scope = "session"`, `roles: [string]`, `realm` (`console`; `police` at the authority; `portal` for USSP operators), `jti` = session id, `exp` ≤ 12 h. Cookies `uspace_session` and `uspace_csrf` (`HttpOnly; Secure; SameSite=Strict`), CSRF header `X-CSRF-Token`. A browser WebSocket authenticates with the session cookie on a same-origin upgrade plus an `Origin` allow-list; there is no ticket, and close code `4401` means sign in again. |
| Errors | RFC 9457 `application/problem+json`, schema `problem/v1`: `{type, title, status, detail, instance, errors: [{field, reason}], truncated?}`; `type` = `https://schemas.uspace.ge/problems/<slug>`, the slug being the counter or refusal name (`unauthenticated`, `forbidden`, `signature`, `not_a_publisher`, `cis_stale`, ...); `field` is the JSON path as `core.FieldError` writes it; `errors` is capped at 100 with `truncated`. The USSP's `conflicts[]` stays on `intent/decision/v1`, not on the problem. |
| Signatures | A publication (F1, F2) is signed as a detached JWS in the `X-JWS-Signature` header (RFC 7515 App. F, RFC 7797 `b64: false`, `crit: ["b64"]`, `alg RS256`, `kid`, `iat` ≤ 5 min) with the publisher's key from its own `/.well-known/jwks.json`. A change notification (F3, and the ANSP's degraded path) is a compact JWS, `Content-Type: application/jose`, with `aud` = the host of the `callback_url`, `sub` = subscription id, `jti` = delivery id, `iat`. |
| Time | RFC 3339 UTC with `Z`, millisecond precision. Every record carries `ts` (source clock) and the receiver adds `rx_ts` (`04-messages.md`). |
| Geometry | GeoJSON, WGS84 (SRID 4326), `[lng, lat]`; altitudes in metres with explicit reference (`AMSL`, `AGL`, `WGS84`). |
| Versioning | Path version (`/v1`), schema `$id` version per message; unknown fields ignored, never rejected, within a major; additive changes only within a major, breaking changes as a new major served in parallel for ≥ 12 months with `deprecated` markers and a `Sunset` header (`00 §7`). |
| Publication | Every national (non-standard) flow below is defined in the owning repo's `api/openapi.yaml` (OpenAPI 3.1) and aggregated in `uspace-lab/api/`; the standard flows are defined by `uas_standards` and ED-318. The conformance suite of `00 §7` tests both. |
| Discovery | USSPs are found through the DSS and the CIS USSP list; the CISP of record and the DSS through deployment configuration; nothing in code names a specific USSP or CISP. Any certified third-party USSP or CISP may stand on the other side of F1–F8. |
| Failure rule | A consumer that loses a producer keeps the last data, marks it stale with age, and keeps serving. A producer that loses a consumer queues (bounded, counted) and reports. Nothing hides an aircraft or disables a source silently; every disable is an audited act by a person. |

## 2. Flow catalogue

### F1 Authority → CISP: zones, U-space airspace, USSP list

| Item | Value |
|---|---|
| Producer / consumer | authority (`publisher`) → cisp |
| Data | ED-318 `FeatureCollection` (`type`, `name`, `metadata {issued, provider, validFrom, validTo, description}` (`uspace-core/ed318.Metadata`; the producer sets `issued` and `provider`), `features[]`) of `UASZone` features: `identifier`, `country` (`GEO`), `name`, `type` (`PROHIBITED` / `REQ_AUTHORIZATION` / `CONDITIONAL` / `NO_RESTRICTION` / `USPACE`), `variant` (`COMMON` / `CUSTOMIZED`), `restrictionConditions`, `region`, `reason[]` (`AIR_TRAFFIC`, `SENSITIVE`, `PRIVACY`, `POPULATION`, `NATURE`, `NOISE`, `EMERGENCY`, `DAR`, `OTHER`), `otherReasonInfo`, `regulationExemption`, `message`, `limitedApplicability` (`TimePeriod {startDateTime, endDateTime, schedule[]}` of `DailyPeriod {day, startTime, startEvent, endTime, endEvent}`), `zoneAuthority[]` (`purpose` `AUTHORIZATION` / `NOTIFICATION` / `INFORMATION`, `intervalBefore`, `name`, `service`, `contactName`, `siteURL`, `email`, `phone`), `dataSource`, `extendedProperties`; geometry with vertical limits in `m` or `ft` referenced `AGL` / `AMSL` / `WGS84` (ED-318 `CodeVerticalReferenceType`; the exact vertical-limit property names in the ED-318 geometry object are *(unverified)* against the module). U-space airspace volumes (type `USPACE`) carry in `extendedProperties` the Art. 3(4) requirements (`uas_requirements`, `service_performance` incl. update frequencies and latencies, `operational_conditions`, `airspace_constraints`), `services_required[]`, `adjacent[]` (Art. 5(1)(b), (d)). The USSP list (Art. 5(1)(c)): name, contact, certificate id, base URL, services, certification limitations, validity, `terms_url` (Art. 5(3)). Field names follow `uas_standards.eurocae_ed318`; an ED-269 import maps `restriction` → `type`, `REQ_AUTHORISATION` → `REQ_AUTHORIZATION`, `uomDimensions` `M`/`FT` → `m`/`ft`, `applicability` → `limitedApplicability`. |
| API | `PUT /v1/publications/{dataset}` with the full dataset and `If-Match: <previous version>`; datasets `zones`, `uspace_airspace`, `ussp_list`. Full replacement per publication, diffed by the CISP. |
| Transport / auth | REST; scopes `cis.publish:zones`, `cis.publish:uspace`, `cis.publish:ussp_list`; payload detached-signed by the authority (JWS) so the CISP can prove provenance. |
| Frequency / volume | Tens of publications per month; a dataset of a few thousand zones is < 20 MB. Same at 100 and 1000 drones. |
| Failure | CISP down: the authority keeps the pending publication, retries with backoff, shows "not yet published" on its console with age; the CISP keeps serving the previous version. Authority down: the CISP serves the last version; nothing expires on its own except by the zone's own applicability. |

### F2 ANSP → CISP: dynamic restrictions

| Item | Value |
|---|---|
| Data | ED-318 `UASZone` features with `reason` `DAR`, time-bounded `limitedApplicability`, the U-space airspace they modify, the ANSP's reference; states `planned`, `active`, `ended`, `cancelled` (ATS.TR.237(b): activation, deactivation, temporary limitation). The same restriction is written by the ANSP to the DSS as an F3548 `ConstraintReference` (`PUT /dss/v1/constraint_references/{entityid}`, scope `utm.constraint_management`) so that USSPs with `utm.constraint_processing` subscriptions are notified within F3548 `CstrPublishedNotificationLatencySeconds = 5`; the CISP publication remains the regulatory channel (Art. 5(2)). Also the ATS.OR.127 operational data items the ANSP agrees with the authority (Annex V SLA). |
| API | `POST /v1/restrictions`, `PATCH /v1/restrictions/{id}` (end, extend) with the `cis/restriction/v1` body (`ansp_ref`, `ansp_version`, `uspace_airspace_id`, `state`, `starts_at`, `ends_at`, `feature`); the idempotency key is the body pair `(ansp_ref, ansp_version)`, and an `Idempotency-Key` header, if sent, is ignored. Publisher heartbeat `POST /v1/publishers/heartbeat {sent_at, active_refs?: []}` every 15 s (`active_refs` = the ANSP's active `ansp_ref`s; the authority sends the same heartbeat without them); three misses (60 s) are stale. A restriction must name a current `USPACE` feature in `uspace_airspace_id`, on both sides. |
| Transport / auth | REST; scope `cis.publish:restrictions`; mTLS client cert bound to the ANSP client id when `CISP_MTLS_MODE=required` (`§1`). |
| Frequency / volume | Expected a few per day, bursts of tens during events; bytes negligible. |
| Failure | CISP down: the ANSP retries, raises an alarm to its supervisor after 10 s, and as a degraded path posts the same `cis/change/v1` compact JWS, signed with its own key, to `{base_url}/v1/cis/notifications` of every USSP on the CIS list and of the authority; the CISP reconciles when back (restrictions carry ANSP-side version). ANSP down: active restrictions stay active until their `endDateTime`; the CISP flags "source stale" after 60 s of missed heartbeat. |

### F3 CISP → USSPs, authority, ANSP: publication and change notification

| Item | Value |
|---|---|
| Data | ED-318 `FeatureCollection`s; a change feed of `(dataset, version, feature ids changed, reason)`. |
| API | Pull: `GET /v1/{dataset}?bbox=&at=&since_version=` with `ETag` = version (`?at=` filters to the applicable features; `?applies_at=<RFC 3339>` annotates every feature with `extendedProperties.cis_applicability` ∈ `applies` / `not_applicable` / `unknown` without filtering, also on the authority's `GET /v1/zones/export`); `GET /v1/{dataset}/versions/{v}` for history; `GET /v1/changes?since=` cursor feed. Push: subscriber registers `POST /v1/subscriptions {callback_url, datasets, bbox}`; the CISP POSTs a signed change notification (`JWS`, body = change record) to the registered `callback_url` and the subscriber pulls the delta. Every subscriber receives at one path, `POST /v1/cis/notifications`, and allow-lists two issuers there (the CISP and the ANSP, `*_CIS_NOTIFY_ISSUERS`); `pull_url` is followed only when its host is the issuer's configured base host; the reasons `subscription_test`, `republished` and any reason the receiver does not know are acknowledged `204` without a pull. Consoles and the public map use `WS /v1/stream` and the public `GET`. |
| Transport / auth | REST + webhook; scope `cis.read`; webhook signed with the CISP's key (JWKS published), `aud` = the host of the `callback_url` (`§1`, Signatures). |
| Frequency / volume | Change notifications: a few per day plus restrictions; pull reconciliation every 60 s per subscriber (cheap `HEAD` on `ETag`). Volume independent of drone count. Every response carries the CISP's top-level `cis_dataset`, `cis_version` and `cis_updated_at` and the `ETag` (Art. 9(2) time of update plus version number). |
| Failure | Subscriber unreachable: notifications retried with exponential backoff for 24 h, delivery log kept; the subscriber's own 60 s reconciliation pull is mandatory, so a missed webhook costs at most 60 s. CISP unreachable: subscribers keep their cache with `cis_version` and `cis_age_s` on every output; a USSP whose CIS cache is older than the configured bound (default 300 s, national figure) refuses new authorisations inside U-space airspace with `cis_stale` and keeps serving in-flight services. Rejected publications and delivery failures are counted and reported to the publisher (Annex III A(5) error reporting). |

### F4 ANSP → USSP and authority: manned traffic

| Item | Value |
|---|---|
| Data | `track/manned/v1`: ICAO 24-bit address or callsign, position, altitude (barometric `alt_pressure_m` and geometric `alt_wgs84_m` when known), ground speed, track, vertical rate, emergency / SPI flag when known (Art. 10(10), 11(3)(a)), source class (`ads_b`, `mode_s`, `ssr`, `atm_feed`, `ads_l`), `ts`, quality. Only tracks relevant to U-space airspace plus a configurable margin (default 5 km, 1500 m above). Scope: ATS.OR.127 obliges the ANSP only for U-space airspace **in controlled airspace**; for U-space airspace outside ATC service, manned aircraft make themselves conspicuous to the USSP directly (SERA.6005(c)), so the USSP MUST run or contract an e-conspicuity receiver (ADS-B 1090 MHz; ADS-L per Q14) there — it is a required input, not a fallback. |
| API | `WS /v1/manned-traffic/stream?bbox=` (newline-delimited JSON frames, 1 Hz per aircraft), `GET /v1/manned-traffic/snapshot?bbox=` for bootstrap. |
| Transport / auth | WebSocket over TLS, ecosystem token with scope `ansp.traffic`, mTLS with `ANSP_MTLS_MODE=required` in production (`§1`). Every frame carries the `04 §2` envelope with `schema`, and consumers dispatch on it: `track/manned/v1` bodies, and `console/status/v1` as the feed's status (`feed/status/v1` is retired). |
| Frequency / volume | 1 Hz × N manned aircraft (tens, not thousands); independent of drone count. |
| Failure | ANSP stream down: USSP marks manned traffic `unavailable since T` on every traffic-information product, raises an operational alarm, and continues with its own e-conspicuity receiver (trust class `broadcast`). It never shows an empty sky as clear. Authority down: no effect on the ANSP. |

### F5 Operator ↔ USSP (public operator API)

| Sub-flow | Direction | Data | API | Frequency at 100 / 1000 drones | Failure |
|---|---|---|---|---|---|
| Registration lookup | op → ussp | operator registration number, UAS serial, pilot id → validity status (never PII) | `GET /v1/registry/validate?operator=&serial=&pilot=` | per intent; ≤ 1/s | USSP caches positive answers for 24 h (`03-data-models.md`), negative for 5 min; cache miss while authority down → `unknown`, intent held `pending_validation` |
| Operational intent (authorisation) | op → ussp | The ten Annex IV items (`04 §3.5`: UA serial, mode of operation, type of flight / special operation, category and class or type certificate, 4D trajectory as F3548 `Volume4D` with altitudes `W84` and AMSL derived by the USSP, identification technology, connectivity methods, endurance, loss-of-C2 procedure, operator and UA registration numbers) plus contingency measures (Art. 6(8)) and emergency contact. Decision back: accepted / rejected with the conflicting item, `authorisation_number` (Art. 10(11)), `deviation_thresholds` (Art. 10(2)(d)), optional `alternative` (Art. 10(4)), conditions | `POST /v1/intents`, `PATCH /v1/intents/{id}` (activate — confirmed without unjustified delay, Art. 10(5); modify; end), `GET /v1/intents/{id}` | ≈ 2–3 intents per drone-day: 300 / 3000 per day | DSS down: intents inside U-space airspace that need cross-USSP deconfliction are `pending_dss`; intents outside U-space airspace proceed on local checks; a standing authorisation hit by a new restriction or an emergency manned aircraft is updated or withdrawn and the operator told (Art. 6(6), 10(10)) |
| Network ID telemetry | op → ussp | `telemetry.v1` (position, altitude WGS84 and AMSL, height above surface or take-off with its reference, course and ground speed, operational / emergency status, remote pilot or take-off position, `ts` = time generated), 1 Hz (A11) | `WS /v1/telemetry` (preferred) or `POST /v1/telemetry/batch` | 100 / 1000 msg/s; 0.06 / 0.6 MB/s | Operator link down: flight shows `telemetry_lost` after 5 s, conformance raises `lost_link` after a configured 15 s; USSP down: operator client queues up to 10 min and sends as `backlog=true` on reconnect |
| Traffic information | ussp → op | nearby traffic (manned from the ANSP and from e-conspicuity, other UAS from peers via F3411, broadcast RID if the USSP has it) with position, time of report, speed, heading, emergency status when known (Art. 11(3)(a)), trust class and age; CPA proximity alerts (national addition) | `WS /v1/traffic?intent_id=` or `?bbox=`; `GET /v1/traffic/snapshot` | 1 Hz per subscribed client (A11), filtered by a 2 km radius default | stale sources carry `age_s`; when the USSP's own inputs are stale the stream says so (`degraded: [manned, dss]`) |
| Geo-awareness | ussp → op | applicable operational conditions and constraints of the U-space airspace, geo-zones and temporary restrictions for a bbox / intent (Art. 9(1)), each with `updated_at`, `version` and `valid_from` / `valid_to` (Art. 9(2)) | `GET /v1/geo?bbox=&at=`, change push over the same WS | on planning and on every CIS change | if CIS cache stale beyond bound, response marked `stale` with age |
| Conformance alerts | ussp → op | `alert.v1` kinds `nonconformance` (deviation thresholds or Art. 6(1) breached), `height_exceedance`, `zone_incursion`, `lost_link`, `restriction_activated`, `proximity`, `nonconformance_nearby` (another UAS in the vicinity deviated, Art. 13(2)); acknowledgement back | WS push + `POST /v1/alerts/{id}/ack` | event-driven | delivery recorded; unacknowledged critical alerts repeat every 10 s and are escalated to the USSP supervisor console |

### F6 USSP ↔ USSP via DSS (F3548, F3411)

| Item | Value |
|---|---|
| Data | F3548-21: `OperationalIntentReference` (`id`, `manager`, `uss_availability`, `version`, `state` ∈ `Accepted` / `Activated` / `Nonconforming` / `Contingent`, `ovn`, `time_start`, `time_end`, `uss_base_url`, `subscription_id`), `ConstraintReference`, `Subscription`; USS-to-USS `GET /uss/v1/operational_intents/{entityid}` (answered within `MaxRespondToOIDetailsRequestSeconds = 1`), `GET /uss/v1/operational_intents/{entityid}/telemetry` (for `utm.conformance_monitoring_sa`), `POST /uss/v1/operational_intents` and `POST /uss/v1/constraints` notifications (sent within `UssOiChangeNotificationMaxSeconds = 5`; a conflicting-intent notification to the other USS within `ConflictingOIMaxUSSNotificationTimeSeconds = 1`), `POST /uss/v1/reports`, `GET /uss/v1/log_sets/{log_set_id}`. Priority is an integer "as defined by the regulator": `0` normal, higher for SERA Art. 4 special operations (Art. 10(8)); equal priority is first come first served (Art. 10(9)). Planning horizon ≤ `OiMaxPlanHorizonDays = 30`. F3411-22a: `IdentificationServiceArea` in the DSS per flight (`PUT /rid/v2/dss/identification_service_areas/{id}`), DSS subscriptions (≤ `NetDSSMaxSubscriptionPerArea = 10`, ≤ 24 h), ISA change notification `POST /uss/identification_service_areas/{id}` to subscribers, `GET /uss/flights?view=` (≤ `NetMaxDisplayAreaDiagonalKm = 7`) and `GET /uss/flights/{id}/details` (≤ `NetDetailsMaxDisplayAreaDiagonalKm = 2`) between SP and DP; SP answers within p95 1 s / p99 3 s and serves positions ≤ `NetMaxNearRealTimeDataPeriodSeconds = 60` old plus `recent_positions`. |
| API | InterUSS DSS `/dss/v1/...` (F3548 v21), `/rid/v2/dss/...` (F3411 v22a); operation and field names from `uas_standards`. |
| Auth | Ecosystem token with F3548 scopes `utm.strategic_coordination`, `utm.constraint_processing`, `utm.constraint_management` (ANSP constraints), `utm.conformance_monitoring_sa`, `utm.availability_arbitration` (authority only: `PUT /dss/v1/uss_availability/{uss_id}`); F3411 `rid.service_provider`, `rid.display_provider`. |
| Frequency / volume | One DSS write per intent state change (≈ 1000 / 10 000 per day); ISA per flight; peer `GET flights` polls at 1 Hz per DP view. Peer data retained ≤ 24 h (`ExternalDataMaxRetentionTimeHours`). Clocks within `TimeSyncMaxDifferentialSeconds = 5` of UTC. |
| Failure | DSS down: existing intents keep their ovn; new cross-USSP deconfliction is unavailable (`pending_dss`); own-USSP intents and all in-flight services continue; the USSP console shows DSS state. Peer USSP down: its intents are treated as still valid until `time_end`; its flights disappear from traffic info with an explicit `peer_unavailable` marker; the authority may set the peer's `uss_availability` to `Down`. A nonconforming flight that cannot be brought back within `MaxRecoverableTimeInNonconformingStateSeconds = 60` goes `Contingent`. |

### F7 Authority ↔ USSP: network identification display, records, occurrences, operating status

| Sub-flow | Data | API | Frequency | Failure |
|---|---|---|---|---|
| Network ID display (authority as a standard F3411-22a Display Provider) | The authority discovers ISAs — and through them every Service Provider, ours or third-party — through the DSS for the areas its users are viewing (`GET /rid/v2/dss/identification_service_areas?area=`, plus a DSS subscription per viewed area, ≤ 24 h, renewed), then pulls from each Service Provider `GET {uss_base_url}/uss/flights?view=` with a view diagonal ≤ 7 km (`NetMaxDisplayAreaDiagonalKm`) and `GET /uss/flights/{id}/details` only for views ≤ 2 km; it displays within `NetDpDataResponse` p95 1 s / p99 3 s and disposes of DP data within 24 h (`NetDpMaxDataRetentionPeriodSeconds`). Country-wide oversight is a set of DP views over the ISAs that exist, not a blanket poll. The authority also receives ISA notifications (`POST /uss/identification_service_areas/{id}`) on its subscriptions. | DSS and F3411 USS endpoints; the authority's own `/v1/picture/*` fans the DP view out to its consoles | 1 Hz per active DP view; views exist only where ISAs exist | USSP down or over the SP response time: the authority shows that USSP as `unavailable since T` on its picture, never an empty map; DSS down: existing subscriptions and known ISAs are used until `time_end`, shown as `dss_unavailable` |
| **Optional national extension** — authority flight feed | `WS /v1/authority/flights`: a 1 Hz push of every flight in the same `RIDFlight` shape, offered by a USSP under the authority's Art. 18(b) determination of live traffic data. It never replaces the F3411 DP path above; the DP path is the conformance baseline and must work with any certified USSP, including one that offers no extension. **NC** | `WS /v1/authority/flights` | 1 Hz × airborne UAS: 100 / 1000 msg/s | authority down: the USSP buffers 10 min, then drops with a counted gap (its own ≥ 30-day records remain the record); this feed is never a reason to skip the DP path |
| Service records | per flight: authorisation (number, thresholds, decision, conflicts), telemetry summary, alerts, conformance timeline, traffic shown (Art. 15(1)(g) record; which recorded data the authority may require is its Art. 18(b) determination, **NC**), fetched by the authority on demand or delivered as daily bundles; the USSP's base URL for this comes from its certificate record / the CIS USSP list | `GET /v1/records/flights/{id}`, `GET /v1/records/daily/{date}` (national OpenAPI, implemented by every certified USSP) | daily plus on demand | retried; a missing day is an alarm on both sides |
| Occurrences | `occurrence.v1`: type (airprox, nonconformance_in_prohibited, lost_link_in_uspace, emergency, other), time, aircraft, intent, evidence refs, narrative, reporter; ECCAIRS/ADREP-compatible (E5X) export is the authority's job (376 Art. 7(4)); personal details are stripped before the national database (376 Art. 16(3)) | `POST /v1/occurrences` on the authority | event-driven, within 72 h of awareness (376 Art. 4(8)) | queued and retried; the USSP console shows undelivered occurrences |
| Operating status | start of operations after certification, cease, restart (Art. 7(6)); the authority records them against the certificate and applies Art. 16(2) | `POST /v1/certificates/{id}/status` on the authority | rare | retried; manual fallback by letter is acceptable |

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
| Auth | Per-receiver bearer key plus HMAC-SHA256 over the exact body bytes with a per-receiver secret (defence against key reuse from a captured device); the body carries `sent_at_ms` (30 s window) and a unique `nonce` (LESSONS R-06; pinned by `rid_receiver_auth.json`); keys revocable from the console; receivers disabled per instance or per type, audited (predecessor U-15). |
| Frequency / volume | A receiver hears ≤ tens of aircraft; 1–3 msg/s per heard aircraft (BT4 legacy sends Basic ID and Location separately). 50 receivers × 20 aircraft × 3 = 3000 msg/s worst case; 60 B raw frame + 200 B envelope. |
| Failure | Authority ingest down: receivers buffer in RAM/flash (minutes), replay with original `rx_ts` and `backlog=true`. Receiver down: shown `silent since T`; a receiver disabled by an admin is shown `disabled by <who>`, never merely silent. |

### F10 Police / authority queries

| Item | Value |
|---|---|
| Data | "Who is flying here now / who flew here at T", "what do we know about serial S / registration number R", "export evidence pack for incident I". Answers include operator identity only for a stated lawful purpose; every query, result count and export is audited and reportable to the data protection officer. Occurrence reports and their reporters are never returned here (376 Art. 15(2), 16). The access level per user class is the authority's determination under Art. 18(b)–(c) and national law (**NC**). |
| API | Authority console (police realm) and `GET /v1/police/aircraft?bbox=&at=`, `GET /v1/police/operators/{reg}` with mandatory `purpose` and case reference; evidence pack export signed with content hash. |
| Auth | Per-agency OIDC or local accounts with MFA; scope `police.query`; IP allow-list. |
| Frequency | Human-rate. |
| Failure | Read-only; degraded sources shown as such. |

### F11 Authority ↔ ANSP (direct)

Restriction requests and coordination outside the CISP (e.g. the authority asks for a restriction over an event, the ANSP reports a manned-traffic airprox): `POST /v1/occurrences` on the authority (same as F7), and `POST /v1/restriction-requests` on the ANSP. Low volume; audited both sides. The coordination mechanism itself is the authority's duty under Art. 18(f).

### F13 USSP → ANSP and peers: conformance alerts (Art. 13(2))

On a detected deviation the USSP: sets the F3548 state to `Nonconforming` (peers are notified through their DSS subscriptions within 5 s); posts `coordination/annex_v/v1 {kind: nonconformance}` to the ANSP's `POST /v1/coordination/notices`, which answers `202 {ack_id, state: received, received_at}`; the USSP records the `ack_id` and reads the human acknowledgement by polling `GET /v1/coordination/notices/{ack_id}` (no push in v1); raises `nonconformance_nearby` to the operators of flights within the proximity radius. An unacknowledged ANSP notice is retried and shown on the USSP console.

### F12 `uspace-lab` → everything (non-production)

The lab speaks only the public contracts above: simulated operators on F5, simulated receivers on F9, a simulated ANSP feed on F4, a second simulated USSP on F6 against a local InterUSS DSS. No test hook enters a production image.

## 3. API surfaces per system (endpoint groups)

One origin per system; Caddy routes each group to the Go process that serves it (`00 §6.1`); `web` (Next.js) has only BFF routes under `/_bff/*`.

### authority

| Group | Process | Purpose | Consumers |
|---|---|---|---|
| `/v1/registry/*` | api | operators, UAS, pilots, competencies; CRUD for registrars, public validity check, status change feed, uas.gov.ge import | registrars, USSPs (validate only), portal |
| `/v1/zones/*`, `/v1/uspace/*` | api | ED-318 authoring, versioning, publication to CISP, airspace.gov.ge import rules | inspectors, CISP |
| `/v1/certificates/*` | api | USSP / CISP certificates, public register (Art. 18(a)), operating status (Art. 7(6), 16(2)), USSP list publication | admins, CISP, USSPs (status), public (register) |
| `/v1/rid/receivers/*` | api | receiver fleet, keys, config | admins |
| `/v1/rid/observations` | rid-ingest | observation ingest | receivers |
| `/v1/rid/frames/*` | api | raw frame retrieval | incident officers |
| `/v1/picture/*` | picture-ws | the authority's own traffic picture: F3411 DP views (ISA discovery via DSS, SP polls) and direct-RID tracks with identification status, trust class, age; WS by viewport; DP data disposed of within 24 h | console, police |
| `/v1/violations/*` | api (detectors in `detect` open and close them over JetStream) | detection rules (120 m, zones, unregistered, no authorisation), review | inspectors |
| `/v1/incidents/*` | api | incidents opened from the authority's own evidence, evidence packs | inspectors, incident officers |
| `/v1/occurrences/*` | api | 376/2014 intake (mandatory and voluntary), independent handling, safety risk classification (376 Art. 7(2)), de-identified ECCAIRS/ADREP export; segregated from violations | incident officers, USSPs, ANSP, operators |
| `/v1/police/*` | api | lawful queries with purpose | police realm |
| `/v1/sources/*` | api (writes KV; hot-path processes apply) | enable/disable receivers, USSP feeds, ANSP feed (by type and instance), audited | admins |
| `/v1/audit/*` | api | append-only events, views and exports included | admins, auditors |
| `/oauth/*`, `/.well-known/jwks.json` | api | ecosystem token service | all systems |

### cisp

| Group | Process | Purpose |
|---|---|---|
| `/v1/publications/*` | api | authority and ANSP publish; version, sign, diff |
| `/v1/restrictions/*` | api | ANSP dynamic restrictions lifecycle |
| `/v1/zones`, `/v1/uspace_airspace`, `/v1/ussp_list`, `/v1/{dataset}/versions/*`, `/v1/changes` | api | ED-318 read, history, change feed, bbox and time filters; versioned snapshots with `ETag`, `Cache-Control`, served from a materialised current-version table |
| `/v1/subscriptions/*` | api (delivery by `deliver`) | webhook registration and delivery log |
| `/v1/stream` | api | WS change stream for consoles and the public map (change events only, low rate) |
| `/public/*` | api, cached by Caddy | unauthenticated read subset |

### ussp

| Group | Process | Purpose |
|---|---|---|
| `/v1/intents/*` | api (Annex IV intake, registry check, deconfliction via `internal/intent`, DSS write, decision record, authorisation number) | operational intents: create, activate, modify, end; decisions and conflicts |
| `/v1/telemetry` | telemetry-ingest | network ID ingest (WS, batch) |
| `/v1/traffic/*` | traffic-ws | traffic information stream and snapshot |
| `/v1/geo/*` | api | geo-awareness from the CIS cache |
| `/v1/alerts/*` | traffic-ws (stream) / api (ack record) | conformance, proximity, restriction alerts; acknowledgements |
| `/v1/registry/validate` | api | proxy to the authority with cache; projected to the hot path via KV |
| `GET /uss/flights`, `GET /uss/flights/{id}/details`, `GET`/`POST /uss/identification_service_areas/{id}` | rid-sp | F3411-22a Net-RID Service Provider (and DP-side ISA notification receiver for its own peer views) |
| `GET /uss/v1/operational_intents/{entityid}`, `GET .../telemetry`, `POST /uss/v1/operational_intents`, `GET /uss/v1/constraints/{entityid}`, `POST /uss/v1/constraints`, `POST /uss/v1/reports`, `GET /uss/v1/log_sets/{log_set_id}` | api (notifications handed to `dss-sync`) | F3548-21 USS endpoints |
| `/v1/records/*` | api | records for the authority (Art. 15(1)(g), 18(b)), occurrence delivery status |
| `/v1/authority/flights` | rid-sp | **optional national extension**: 1 Hz flight push to the authority; never a substitute for the F3411 SP interface |
| `/v1/weather/*` | api | optional weather information (Art. 12 minimum content) |
| `/v1/accounts/*`, `/oidc/*` | api | operator accounts and clients |

### ansp

| Group | Process | Purpose |
|---|---|---|
| `/v1/restrictions/*` | api | plan, activate, extend, end dynamic restrictions (ATS.TR.237); publish to CISP and as F3548 constraints to the DSS |
| `/v1/restriction-requests` | api | inbound requests from the authority |
| `/v1/manned-traffic/*` | manned-adapter | stream and snapshot for USSPs and the authority (ATS.OR.127) |
| `/v1/coordination/*` | api | inbound Annex V data from USSPs: intents touching controlled airspace, non-conformance notices with acknowledgement (Art. 13(2)) |
| `/v1/adapters/*` | api (status via `src.v1.*`) | status of surveillance adapters |

### lab

No production API. `make` targets and a Python scenario runner; Go simulators; a results API for CI dashboards only.

## Errata

| Date | Where | Change | Source |
|---|---|---|---|
| 2026-10-02 | F4, `Data` row | The message is `track/manned/v1` (the `04 §3` name, which every plan uses), not `manned_track.v1`; the barometric altitude field is `alt_pressure_m` (the `03` column name and the E-13 unit-suffix convention), not `pressure_alt_m`. | `docs/decisions/2026-10-02-cross-plan.md` §1.1 (note after M13), ansp gap 14 |
| 2026-10-04 | F1 `Data` row; F3 `Frequency / volume` row | ED-318 collection metadata follows `uspace-core/ed318.Metadata` (`issued`, `provider`, `validFrom`, `validTo`, `description`), not `{creationDateTime, updateDateTime, originator}`, which core's `Parse` does not carry. Producers set `issued` and `provider`; the CISP adds the top-level `cis_dataset`, `cis_version`, `cis_updated_at` and the `ETag`. | `docs/decisions/2026-10-02-cross-plan.md` M15; cisp Q1 |
| 2026-10-04 | §1, `Auth` row | `aud` is the host of the target's published base URL for every machine token, national and standard, with a `*_AUDIENCES` list per verifier and `audience` / `resource` on the token request; not a system id, which discovery cannot yield for ISA notifications or third-party peers. One client per calling system. | `docs/decisions/2026-10-02-cross-plan.md` M18, M24 |
| 2026-10-04 | §1, `Auth` row | One mTLS flag per system, `<SYS>_MTLS_MODE = required` or `off`, on the mTLS routes only; Caddy `client_auth verify_if_given` forwards `X-Client-Cert-Subject`. Replaces the `header`/`off` values and `*_MTLS_REQUIRED`. | `docs/decisions/2026-10-02-cross-plan.md` M25 |
| 2026-10-04 | §1, new `Sessions and browsers` row | Console session claims `scope = "session"`, `roles[]`, `realm`, `aud` = own host; cookies `uspace_session` / `uspace_csrf`, not a per-system name such as `ansp_session`; browser WebSockets authenticate with the cookie on a same-origin upgrade and `Origin`, no ticket. | `docs/decisions/2026-10-02-cross-plan.md` M20, M21, M22 |
| 2026-10-04 | §1, new `Errors` row | One problem body `problem/v1` with `errors: [{field, reason}]` and `truncated`, `type` slug URIs; replaces `problems[]` and a singular `field`. | `docs/decisions/2026-10-02-cross-plan.md` M28 |
| 2026-10-04 | §1, new `Signatures` row | Detached publication JWS in `X-JWS-Signature` (not `Signature`); webhook JWS `aud` = callback host, not the subscription's client id. | `docs/decisions/2026-10-02-cross-plan.md` M19, M26 |
| 2026-10-04 | F2 `API`, `Transport / auth`, `Failure` rows | Body per `cis/restriction/v1` (`ansp_version`, not `version`), idempotency key = `(ansp_ref, ansp_version)`; heartbeat `POST /v1/publishers/heartbeat {sent_at, active_refs?}` every 15 s; strict `USPACE` placement; the degraded direct path posts to `{base_url}/v1/cis/notifications`. | `docs/decisions/2026-10-02-cross-plan.md` M3, M4, M5, M9 |
| 2026-10-04 | F3 `API`, `Transport / auth` rows | One receiver path `POST /v1/cis/notifications` (not `/v1/cis/notify`, `/v1/cis/webhook` or `/v1/cis/changes`) with the CISP and the ANSP as allowed issuers, the `pull_url` host check, `204` for `subscription_test`, `republished` and unknown reasons; `?applies_at=` beside `?at=`. | `docs/decisions/2026-10-02-cross-plan.md` M1, M5, M16, M17, M19 |
| 2026-10-04 | F4 `Transport / auth` row | mTLS per `ANSP_MTLS_MODE`; every frame enveloped; `console/status/v1` replaces `feed/status/v1`. | `docs/decisions/2026-10-02-cross-plan.md` M12, M25, M29 |
| 2026-10-04 | F13 | Coordination intake is `POST /v1/coordination/notices` (not `/v1/coordination/annex-v`), answered `202 {ack_id, state, received_at}`; the acknowledgement is read by `GET /v1/coordination/notices/{ack_id}`. | `docs/decisions/2026-10-02-cross-plan.md` M2 |

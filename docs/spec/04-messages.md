# 04 — Message catalogue

## 1. Format decision: JSON Schema

**Decision: JSON with versioned JSON Schema (draft 2020-12) for every internal and external message. Protobuf is not adopted now.**

| Consideration | JSON Schema | Protobuf |
|---|---|---|
| External standards (F3411, F3548, ED-318, GeoJSON) | native JSON; one representation end to end, no transcoding at the boundary | transcoding layer at every boundary, two sources of truth |
| Language-neutral test vectors | plain files, diffable, readable in review | binary or text-proto; needs tooling to read |
| Volume at 1000 drones (`05`) | 1000 msg/s × ~600 B = 0.6 MB/s; NATS and Go `encoding/json` handle this on one core with margin | 3–5× smaller, faster; the gain is not needed below ~5000 drones |
| Evolution | additive fields, `$id` versioning, `additionalProperties` ignored by consumers | field numbers; equally good |
| Debuggability in production | `nats sub` readable | needs decoder |

Re-evaluate at the 5000-drone load test: if hot-path CPU on the partitioned consumers exceeds 50 % of budget, adopt Protobuf **for the internal track subject only**, generated from the same schema, and keep JSON at every external boundary. The test-vector files stay JSON either way.

Schema rules: `$id` = `https://schemas.uspace.ge/<family>/<name>/v<major>.json`; every message carries `"schema": "<name>/v<major>"`; minor additions never break consumers; a major change is a new subject suffix and a parallel period. Schemas live in each producing repo under `schemas/` and are mirrored read-only in `uspace-lab/schemas/` with the vectors that pin them.

## 2. Common envelope fields

| Field | Type | Meaning |
|---|---|---|
| `schema` | string | `family/name/vN` |
| `msg_id` | ULID | unique per message; dedupe key |
| `producer` | string | system and instance, `ussp-1/ingest-3` |
| `ts` | RFC 3339 | capture time on the **source's** clock (operator client, receiver, broadcast, ANSP). Orders a source's own records; not comparable across sources. |
| `rx_ts` | RFC 3339 | when **this system** received the record, on its own clock. One clock per system. |
| `captured_at` | RFC 3339 | where the record sits in time on this system's clock: `rx_ts - (newest ts in the batch - ts)`, so source clock skew cancels within a batch; for a broadcast, the broadcast time when it is within tolerance (≤ 1 s ahead, ≤ 5 s behind `rx_ts`, widened by declared accuracy), else `rx_ts`. All geometry-time reasoning (CPA, zone applicability, conformance) uses `captured_at`; `rx_ts` is used only to judge lag. |
| `time_source` | enum | `source_clock`, `broadcast`, `receiver`, `provider`, `system` — which rule produced `captured_at`. |
| `backlog` | bool | `true` when the record is history: queued before the session that delivered it, or delivered during a drain. Backlog is recorded and never raises a live alert. |

Trust and source classes, on every track-like message:

| `trust` | Meaning | Typical source |
|---|---|---|
| `authenticated` | the sender holds an operator credential bound to that UAS and this telemetry came over that session | operator client → USSP |
| `provider` | an authenticated peer system asserts it; as trustworthy as the peer | USSP → authority (F3411), peer USSP via DSS |
| `surveillance` | ATS surveillance via the ANSP | ANSP manned feed |
| `broadcast` | unauthenticated radio broadcast; may be spoofed; always shown as such | direct Remote ID, own ADS-B/ADS-L receiver |
| `sensor` | non-cooperative detection without identity | RF / radar (future) |
| `simulated` | lab only; never accepted by production ingest | SITL |

`source` names the adapter type (`operator_ws`, `network_rid`, `direct_rid`, `ansp_feed`, `adsb_rx`, `sitl`), `source_instance` the instance (client id, receiver id, provider id). A disabled source's tracks age out as `source_disabled`, not silently.

## 3. Catalogue

### 3.1 Tracks

| Message | Producer → consumer | Key fields beyond envelope |
|---|---|---|
| `track/telemetry/v1` (internal, every system's picture) | ingest adapters → NATS → monitors, consoles | `track_id`, `trust`, `source`, `source_instance`, `position {lat, lng}`, `alt_wgs84_m`, `alt_amsl_m`, `alt_source` (`geodetic`/`pressure`/`network`/`none`), `alt_pressure_m`, `height_m` + `height_ref` (TakeoffLocation/GroundLevel), `speed_ms`, `track_deg`, `vspeed_ms`, `accuracy_h_m`, `accuracy_v_m`, `status` (F3411 operational status), `emergency`, `identification` (below), `flight_id`, `intent_id`, `cell` (partition cell, `05`) |
| `track/manned/v1` (external F4 and internal) | ANSP → USSP, authority | `icao24`, `callsign`, `alt_pressure_m`, `alt_wgs84_m`, `gs_ms`, `track_deg`, `vrate_ms`, `source_class`, `quality` |
| `rid/observation/v1` (external F9) | receiver → authority | `receiver_id`, `transmitter`, `payload_hex`, `rssi_dbm`, `receiver_position`, `rx_ts` (receiver clock) |
| F3411 `RIDFlight` / `RIDAircraftState` / `RIDFlightDetails` (external F6, F7) | USSP SP → DP (authority, peer USSPs) | standard fields: `id`, `aircraft_type`, `current_state {timestamp, timestamp_accuracy, operational_status (Undeclared/Ground/Airborne/Emergency/RemoteIDSystemFailure), position {lat, lng, alt, accuracy_h, accuracy_v, extrapolated, pressure_altitude, height {distance, reference}}, track, speed, speed_accuracy, vertical_speed}`, `operating_area`, `recent_positions`, `simulated`; details `uas_id {serial_number, registration_id, utm_id, specific_session_id}`, `operator_id`, `operator_location {position, altitude, altitude_type}`, `operation_description`, `auth_data`, `eu_classification` ([uas_standards f3411 v22a](https://github.com/interuss/uas_standards)). Mapping to 2021/664 Art. 8(2): (a) `operator_id`; (b) `uas_id.serial_number`; (c) `position` with `alt` (WGS84 → AMSL derived by the DP) and `height`; (d) `track`, `speed`; (e) `operator_location`; (f) `operational_status = Emergency`; (g) `timestamp`. Special values (`MaxSpeed 254.25`, `SpecialSpeed 255`, `SpecialHeight -1000`, `SpecialTrackDirection 361`) decode to null. |

Altitude rules carried from the predecessor: a broadcast gives HAE (WGS84) which becomes AMSL through the geoid; a missing or flagged-poor geodetic altitude falls back to pressure altitude (`alt_source: pressure`, ISA 1013.25 hPa, **not AMSL**), held for 10 s after the last poor fix, and every vertical judgement on a pressure altitude is widened by `pressure_uncertainty_m` (policy, default 250 m) with the result marked `within_band: false` when only the widened test passes. F3548 altitudes are W84 by standard; the USSP derives AMSL for conformance and keeps both.

### 3.2 Identification

Carried inside every `track/telemetry/v1` and emitted on its own when it changes.

| Field | Values |
|---|---|
| `status` | `registered`, `suspended`, `unknown_operator`, `unidentified` |
| `reason` | `matched`, `session_binding` (authenticated operator session), `uas_suspended`, `uas_revoked`, `operator_suspended`, `operator_revoked`, `serial_unknown`, `not_a_serial`, `operator_absent`, `operator_mismatch`, `owner_unknown`, `not_in_registry`, `serial_conflict`, `no_serial`, `registry_unavailable` |
| `serial`, `operator_reg`, `registered_operator_reg` | as broadcast / as registered (public part only; compared case-insensitively on the public part) |
| `mismatch` | `true` when a registered serial comes with another operator's number, or `serial_conflict` |
| `basis` | `authenticated` (USSP session) or `as_broadcast` (direct RID: "as broadcast and unverified") |

Who resolves: the USSP resolves its own flights against `registry_validity` (authenticated basis) and peer/broadcast tracks on the broadcast basis; the authority resolves every track in its picture against the registry itself. `serial_conflict` rule (predecessor S-10): while an authenticated session for a serial is live (non-backlog rows, captured ≤ 5 s before receipt), a broadcast of that serial more than `spoof_distance_m` (300 m) away is a separate `broadcast` track with `mismatch: true`, never merged; with the session quiet, the broadcast stands for the aircraft, still marked `broadcast`.

### 3.3 Alerts (USSP) and violations (authority)

| Message | Producer | Kinds | Key fields |
|---|---|---|---|
| `alert/v1` | USSP monitors → operator clients, USSP console | `proximity` (CPA: `t_cpa_s`, `d_cpa_h_m`, `d_alt_m`, `peer {track_id, trust}`; national addition), `nonconformance` (`reason` incl. `threshold_exceeded` / `constraint_breached`, `distance_outside_m`, `height_over_m`; Art. 13(1)), `nonconformance_nearby` (another UAS in the vicinity deviated; Art. 13(2)), `height_exceedance` (vs authorised upper), `zone_incursion` (`zone_id`, `zone_type`, `vertical_known`, `limit_not_judged`, `within_band`), `lost_link`, `restriction_activated` (`restriction_id`, affected intents, `authorisation_updated` / `withdrawn`; Art. 10(10)), `emergency_nearby` | `alert_id`, `kind`, `severity` (info/warning/critical), `state` (raised/updated/cleared), `clear_reason` (resolved/stale/source_disabled/flight_ended/acknowledged_timeout), `flight_id`, `intent_id`, `authorisation_number`, `captured_at` of the triggering sample, `policy_version` |
| `violation/v1` | authority detectors → authority console, incidents | `height_120m` (`height_agl_m`, `terrain_source`, evaluated only where ground known; UAS.OPEN.010(2), outside U-space airspace or where no authorisation caps it), `zone_incursion`, `unregistered`, `no_authorisation` (inside U-space airspace with no matching intent from any USSP, Art. 6(4)), `identification_mismatch`, `rid_absent` (authenticated flight seen without broadcast where required — future) | `violation_id`, `kind`, `track_ref`, `serial`, `operator_reg`, `zone_id`, `opened_at`, `closed_at`, `peak`, `evidence_refs[]`, `evidence_excerpt` |
| `occurrence/v1` (external F7, F11) | USSP, ANSP, operator → authority | `airprox`, `nonconformance_in_prohibited`, `lost_link_in_uspace`, `emergency`, `other` (376 Art. 4(1) categories; UAS classes of 2015/1018 *(unverified)*) | `report_ref`, `channel` (mandatory/voluntary), `occurred_at`, `became_aware_at`, `reporter {org, person_ref}` (person_ref encrypted for the authority's incident officers only, 376 Art. 16), `aircraft[] {serial, operator_reg, flight_id, authorisation_number}`, `manned[] {icao24, callsign}`, `intent_refs[]`, `min_separation {h_m, v_m, at}`, `narrative`, `evidence_urls[]`, `reported_at` |

Alert policy (CPA 60 s / 60 m / 20 m / 800 m search radius; zone severities; hysteresis) is a versioned row in each system's `policy` table and `policy_version` travels on every alert, as in the predecessor. Separation is judged in AMSL; AGL is used only for the authority's 120 m rule and for AGL-referenced zone limits.

### 3.4 Restrictions and zones

| Message | Producer → consumer | Key fields |
|---|---|---|
| ED-318 `FeatureCollection` (external F1–F3) | authority, ANSP → CISP → all | `metadata {creationDateTime, updateDateTime, originator}`, `features[] {type: Feature, geometry, properties: UASZone}` |
| `cis/change/v1` (external F3 webhook) | CISP → subscribers | `dataset`, `version`, `feature_ids[]`, `reason` (publication/restriction_activated/restriction_ended/…), `at`, `pull_url`; body JWS-signed |
| `restriction/state/v1` (internal in ANSP and CISP; external degraded path F2) | ANSP | `restriction_id`, `ansp_ref`, `state`, `starts_at`, `ends_at`, `feature`, `version` |
| `zone/applicable/v1` (internal) | each system's CIS cache → monitors | evaluated set of zones applicable at `at` for a cell; `cis_version` |

### 3.5 Intents

| Message | Producer → consumer | Key fields |
|---|---|---|
| `intent/request/v1` (external F5) | operator → USSP | The ten Annex IV items, numbered as in the Annex: (1) `uas_serial` (UA or add-on), (2) `mode` (VLOS/BVLOS), (3) `flight_type` (special operation per SERA Art. 4 or normal), (4) `category` (open/specific/certified) with `class_label` or `type_certificate`, (5) `volumes[]` (`Volume4D`, the 4D trajectory), (6) `identification_technology` (network/direct/both), (7) `connectivity_methods[]`, (8) `endurance_s`, (9) `loss_of_c2_procedure`, (10) `operator_reg` and `ua_registration` when applicable; plus `pilot_ref`, `takeoff`, `landing`, `contingency {procedure, landing_sites[]}` (Art. 6(8)), `emergency_contact_ref`, `authorisation_ref` (specific-category authorisation from the authority), `client_ref` (idempotency) |
| `intent/decision/v1` (external F5) | USSP → operator | `intent_id`, `authorisation_number` (Art. 10(11)), `state`, `decision` (authorised/rejected/pending_*), `deviation_thresholds {h_m, v_m, t_s}` (Art. 10(2)(d)), `alternative` (optional proposal, Art. 10(4)), `conflicts[] {kind: zone/restriction/intent, ref, overlap {h, v, t}}`, `conditions[]`, `valid_from`, `valid_to`, `cis_version_checked`, `change_reason` when the USSP changes a standing authorisation (Art. 6(6), 10(10)) |
| F3548 `OperationalIntentReference` / `OperationalIntentDetails` / `PutOperationalIntentDetailsParameters` (external F6) | USSP ↔ DSS ↔ peers | standard: `id`, `manager`, `uss_availability`, `version`, `state` (Accepted/Activated/Nonconforming/Contingent), `ovn`, `time_start`, `time_end`, `uss_base_url`, `subscription_id`; details `volumes[]`, `off_nominal_volumes[]`, `priority` |
| F3548 `ConstraintReference` / `ConstraintDetails` (external F2, F6) | ANSP → DSS → USSPs | standard reference fields; details `volumes[]`, `type` (`DAR`), `uss_base_url` of the ANSP for `GET /uss/v1/constraints/{entityid}` |
| `intent/state/v1` (internal) | USSP intent service → monitors, console | `intent_id`, `dss_state`, `local_state`, `volumes_amsl[]`, `deviation_thresholds`, `flight_id`, `cell_set[]` |
| `coordination/annex_v/v1` (external, USSP → ANSP, F13) | USSP → ANSP | `kind` (intent_notice / nonconformance / contingent / ended), intents touching controlled U-space airspace, non-conformance notices with intent refs, authorisation numbers and times; reply carries `ack_id` (Art. 13(2)) |

### 3.6 Control plane (internal, per system)

| Message | Purpose |
|---|---|
| `source/control` (KV bucket + push subject) | enable/disable by type and instance; `version` from a DB sequence, `epoch` random per table creation; followers apply only higher versions within an epoch, any version under a new epoch; never fail closed (unknown state = enabled, logged) |
| `source/status/v1` | each adapter every 2 s: instances known, enabled, last seen, accepted, refused |
| `policy/updated/v1` | policy row changed; `policy_version` |
| `registry/changed/v1` | authority internal and the F8 change feed: ids whose status changed |

## 4. Versioning policy

| Rule | Detail |
|---|---|
| Major in the name | `telemetry/v1` → `telemetry/v2` is a new subject suffix and a new `$id`; producers dual-publish for one release; consumers declare the majors they accept. |
| Minor is additive | new optional fields only; consumers ignore unknown fields; a vector that passes on v1.3 passes on v1.4. |
| Deprecation | a field is marked `deprecated: true` in the schema for at least one minor before removal in the next major. |
| Standards | F3411 v22a, F3548 v21 and ED-318 as published by `uas_standards`; a standard version bump is handled as a major on our side with dual support. Our own messages extend, never redefine, a standard object: F3548 states beyond the four DSS states live in `local_state`; ED-318 extras live in `extendedProperties` (its own extension mechanism, Annex V(2)(e)). |
| Pinning | `uspace-lab/schemas/vectors/<name>/vN/*.json` holds input/expected pairs; a producing repo's CI runs them; a change that breaks a vector fails unless the vector is changed in the same PR with a reason. |

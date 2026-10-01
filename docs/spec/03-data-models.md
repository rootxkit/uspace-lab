# 03 — Data models

Conventions (from the predecessor, kept): every unit in the column name (`alt_amsl_m`, `speed_ms`, `timeout_s`); AMSL and AGL never in one calculation and never in one column; `TIMESTAMPTZ` UTC; geometry `SRID 4326`, distance on `geography`; no `alt_agl_m` stored as if measured — AGL is derived from AMSL and a DEM where needed, and absent where the ground is unknown. Each system has one PostgreSQL + PostGIS database for entities and one TimescaleDB database for time series, with separate migration trees that are never merged; the hot-path ingest processes reach only the time-series database.

Key: **O** owned (source of truth), **P** projected or cached from another system (read-only locally, with `source_version` and `fetched_at`).

## 1. `uspace-authority`

### Owned entities

| Entity | Key fields | Notes |
|---|---|---|
| `uas_operators` O | `id`, `registration_number` (GCAA format, Q5; EU form `GEO` + 12 chars + 3 secret, unverified), `legal_name`, `operator_type` (natural/legal), `contact_email`, `contact_phone`, `postal_address`, `status` (active/suspended/revoked/expired), `valid_from`, `valid_until`, `source` (portal/uas_gov_ge_import/manual), `insurance_ref` | PII lives only here (`06`). Secret part of the registration number stored hashed. |
| `uas` O | `id`, `operator_id`, `serial` (ANSI/CTA-2063-A), `registration_mark` (certified), `manufacturer`, `model`, `class_label` (C0–C6, none), `mtom_g`, `rid_capability` (direct/network/both/none), `status`, `registered_at` | Serial unique per manufacturer code; `serial_norm` uppercased for lookup. |
| `remote_pilots` O | `id`, `operator_id` (nullable), `person_ref` (hashed national id), `name`, `status`, `competencies[]` (`A1_A3`, `A2`, `STS_01`, `STS_02`, `national_*`), each with `certificate_ref`, `valid_until` | |
| `geo_zones` O | ED-318 `UASZone`: `identifier`, `country`, `name`, `type` (PROHIBITED/REQ_AUTHORIZATION/CONDITIONAL/NO_RESTRICTION/USPACE), `variant`, `restriction_conditions`, `reasons[]`, `geom` (Polygon or circle `center`+`radius_m`), `lower_m`, `lower_ref`, `upper_m`, `upper_ref` (AGL/AMSL/WGS84), `applicability` (TimePeriod[]), `authority` (contact block), `zone_version`, `valid_from`, `valid_to`, `published_version` | One row per zone version; publication to CISP references the version. Limits in metres (uom `ft` converted at import, original kept in `ed318_extra`). |
| `uspace_airspaces` O | `id`, `name`, `geom`, `lower_m`/`upper_m` with refs, `services_required[]` (NID, GEO, FA, TI, WX, CM), `in_controlled_airspace` bool, `ats_provider_id`, `cisp_id`, `designated_from`, `designated_to`, `designation_ref` | Empty table until designation (Q2). |
| `certificates` O | `id`, `holder` (USSP/CISP), `holder_name`, `client_id`, `base_url`, `services[]`, `issued_at`, `valid_until`, `status`, `conditions` | Drives the USSP list and the token service's client registry. |
| `oauth_clients` O | `client_id`, `system` (ussp/cisp/ansp/lab/receiver), `scopes[]`, `jwks_or_secret_hash`, `mtls_subject`, `status` | Ecosystem token service. |
| `rid_receivers` O | `id`, `name`, `geom` (location), `owner`, `key_hash`, `hmac_secret_ref`, `status` (enabled/disabled), `last_seen_at`, `firmware` | |
| `rid_observations` O (TSDB) | `rx_ts` (receiver), `ingest_ts`, `receiver_id`, `transmitter`, `msg_type`, `payload` (bytea), `rssi_dbm`, decoded columns: `serial`, `operator_reg`, `id_type`, `lat`, `lng`, `alt_wgs84_m`, `alt_pressure_m`, `height_m`, `height_ref` (TakeoffLocation/GroundLevel), `speed_ms`, `track_deg`, `vspeed_ms`, `status` (Undeclared/Ground/Airborne/Emergency/RemoteIDSystemFailure), `ts_broadcast` | Raw frame always kept (evidence). Hypertable, compressed after 7 days. |
| `tracks` O (TSDB) | The authority's fused picture: `track_id`, `captured_at`, `ts`, `rx_ts`, `backlog`, `source` (direct_rid / network_rid / manned_ansp), `source_instance`, `trust`, `geom`, `alt_amsl_m`, `alt_source` (geodetic/pressure/network), `alt_pressure_m`, `height_agl_m` (derived, nullable), `speed_ms`, `track_deg`, `identification` (status, reason, mismatch), `ussp_id`, `flight_id` | Pressure altitude is ISA 1013.25 hPa, never AMSL; `alt_source` says which. |
| `violations` O | `id`, `kind` (height_120m, zone_incursion, unregistered, no_authorisation, identification_mismatch, rid_absent), `track_id`, `serial`, `operator_id` (nullable), `zone_id`, `opened_at`, `closed_at`, `peak_value` (e.g. `max_height_agl_m`), `evidence_track_ids[]`, `status` (new/reviewed/dismissed/escalated) | Detection thresholds in `authority_policy` row, not code. |
| `incidents` O | `id`, `kind` (airprox, nonconformance, lost_link, emergency, violation_escalated, other), `occurred_at`, `reported_by` (ussp/ansp/operator/authority), `report_ref`, `aircraft[]`, `intent_refs[]`, `narrative`, `severity`, `status` (open/assigned/closed), `assignee`, `e5x_export_ref`, `reported_within_72h` bool | 376/2014 intake. |
| `evidence_packs` O | `incident_id`, `created_at`, `content_hash` (SHA-256), `manifest` (track excerpts, raw frames, zone versions, intent versions, audit lines), `storage_ref` | Immutable. |
| `source_controls` O | `(source_type, instance_id)`, `enabled`, `reason`, `actor`, `changed_at`, `version`, `epoch` | Predecessor U-15 model: KV read path, push subject, DB sequence version + epoch. |
| `events` O | `id`, `ts`, `actor_type`, `actor_id`, `purpose`, `entity_type`, `entity_id`, `event_type`, `payload` | Append-only; includes views and exports. |

### Projected

| Entity | From | Fields | Refresh |
|---|---|---|---|
| `cis_cache` P | CISP (F3) | datasets + version + fetched_at | webhook + 60 s |
| `ussp_flights` P (TSDB) | USSPs (F7) | `RIDFlight` as received, `ussp_id`, `rx_ts` | 1 Hz stream |
| `manned_tracks` P (TSDB) | ANSP (F4) | `manned_track.v1` | 1 Hz stream |
| `terrain` P | DEM (SRTM/Copernicus, Q10) | raster tiles | static |
| `geoid` P | EGM2008 2.5' grid | raster | static |

## 2. `uspace-cisp`

| Entity | Key fields | Notes |
|---|---|---|
| `publications` O | `id`, `dataset` (zones/uspace_airspace/ussp_list/restrictions), `version` (monotonic per dataset), `publisher_client_id`, `received_at`, `signature` (publisher JWS), `feature_count`, `supersedes_version` | Full-dataset snapshot per publication. |
| `features` O | `publication_id`, `feature_id` (ED-318 identifier), `feature` (JSONB, ED-318 verbatim), `geom` (indexed), `lower_m`, `upper_m`, refs, `applicable_from`, `applicable_to`, `op` (added/changed/removed vs previous) | Query by bbox, time, dataset, version. |
| `restrictions` O | `id`, `ansp_ref` (idempotency), `uspace_airspace_id`, `state` (planned/active/ended/cancelled), `starts_at`, `ends_at`, `feature` (ED-318, reason DAR), versions | Lifecycle from the ANSP; the CISP never edits content. |
| `subscriptions` O | `client_id`, `callback_url`, `datasets[]`, `bbox`, `status`, `created_at` | |
| `deliveries` O | `subscription_id`, `change_id`, `attempt`, `sent_at`, `status_code`, `next_retry_at` | 24 h retry window. |
| `changes` O | `id` (cursor), `dataset`, `version`, `feature_ids[]`, `reason`, `at` | The change feed. |
| `events` O | audit | |

Projected: nothing. The CISP is the publication of others' masters; the authority and the ANSP keep theirs.

## 3. `uspace-ussp`

### Owned

| Entity | Key fields | Notes |
|---|---|---|
| `operator_accounts` O | `id`, `authority_registration_number`, `display_name`, `contact_email` (USSP's own customer record), `status`, `validated_at`, `validation_status` | Customer data of the USSP; registry truth is the authority's. |
| `oauth_clients` O | operator machine clients, scopes | |
| `operational_intents` O | F3548 shape: `id` (UUID = DSS entity id), `operator_id`, `uas_id`, `pilot_id`, `state` (Accepted/Activated/Nonconforming/Contingent/Ended/Rejected/Pending*), `priority`, `volumes[]` (`Volume4D`: polygon or circle, `altitude_lower/upper` value+`reference W84`+`units M`, `time_start/end`), `off_nominal_volumes[]`, `volumes_amsl` (derived, per volume `lower_amsl_m`, `upper_amsl_m` via geoid), `mode` (VLOS/BVLOS), `contingency` (Annex IV items), `dss_ovn`, `dss_version`, `decision` (authorised/rejected), `conflicts[]` (zone id / intent id / restriction id), `in_uspace_airspace` bool, `cis_version_checked` | Annex IV content maps onto F3548 plus a USSP extension block. Pending states: `pending_validation`, `pending_dss`, `pending_authority`. |
| `flights` O | `id`, `intent_id`, `uas_serial`, `operator_reg`, `started_at`, `ended_at`, `rid_flight_id` (F3411 `RIDFlight.id`), `isa_id`, `emergency`, `last_state` | One flight per activated intent (or per telemetry session without intent, outside U-space airspace). |
| `telemetry` O (TSDB) | `flight_id`, `captured_at`, `ts`, `rx_ts`, `backlog`, `geom`, `alt_wgs84_m`, `alt_amsl_m` (derived), `alt_pressure_m` (opt), `height_m`, `height_ref`, `speed_ms`, `track_deg`, `vspeed_ms`, `status`, `accuracy_h`, `accuracy_v`, `timestamp_accuracy_s`, `source_client_id` | Compressed after 7 days. |
| `conformance_states` O | `flight_id`, `at`, `state` (conforming/nonconforming/contingent/lost_link/unknown), `reason` (outside_volume_h, above_upper, below_lower, before_start, after_end, telemetry_lost), `distance_outside_m`, `height_over_m` | Height conformance is against the authorised volume, not 120 m. |
| `alerts` O | `id`, `kind` (proximity, nonconformance, height_exceedance, zone_incursion, lost_link, restriction_activated, emergency_nearby), `flight_id`, `peer_ref` (other flight / manned track), `severity`, `raised_at`, `cleared_at`, `clear_reason` (resolved/stale/source_disabled/flight_ended), `detail` (CPA: `t_cpa_s`, `d_cpa_h_m`, `d_alt_m`), `acked_at`, `acked_by` | Thresholds in `ussp_policy`. |
| `traffic_products` O (TSDB, sampled) | what was shown to whom when: `client_id`, `at`, `tracks_shown`, `degraded[]` | Annex III record; sampled 0.1 Hz. |
| `dss_state` O | own ISAs, subscriptions, `uss_availability`, notification log | |
| `occurrence_reports` O | `occurrence.v1` sent to the authority, delivery status | |
| `weather_products` O | optional | |
| `events` O | audit | |

### Projected / cached

| Entity | From | Notes |
|---|---|---|
| `cis_cache` P | CISP | zones, U-space, restrictions, USSP list; `cis_version`, `cis_age_s`; refusal bound 300 s for new intents inside U-space airspace |
| `registry_validity` P | authority F8 | `(entity_type, key)` → status, `valid_until`, `class_label`, `mtom_band`, `competencies[]`, `fetched_at`; TTL 24 h positive, 5 min negative; invalidated by the authority's change feed. No PII. |
| `peer_intents` P | DSS + peer USSPs | references and details as fetched; `peer_unavailable` flag |
| `peer_flights` P (TSDB) | peer USSPs via F3411 | `trust = provider` |
| `manned_tracks` P (TSDB) | ANSP F4 | `trust = surveillance` |
| `broadcast_tracks` P (TSDB) | own optional RID receivers | `trust = broadcast`, never fused with authenticated telemetry except by serial match with distance guard (predecessor S-10 rule) |
| `terrain`, `geoid` P | static | |

## 4. `uspace-ansp`

| Entity | Key fields | Notes |
|---|---|---|
| `restrictions` O | `id`, `ansp_ref`, `uspace_airspace_id`, `geom`, `lower_m`/`upper_m` + refs, `starts_at`, `ends_at`, `reason_text`, `state`, `created_by`, `activated_by`, `ended_by`, `published_version` | Master; CISP publishes. |
| `manned_tracks` O (TSDB) | `icao24`, `callsign`, `captured_at`, `ts`, `rx_ts`, `geom`, `alt_pressure_m`, `alt_wgs84_m` (nullable), `gs_ms`, `track_deg`, `vrate_ms`, `source_class`, `quality`, `adapter_id` | What was handed to U-space, as handed. |
| `coordination_inbox` O | Annex V items from USSPs: intent refs touching controlled airspace, non-conformance notices, with `received_at`, `acknowledged_by` | |
| `adapters` O | surveillance adapters: id, type (ads_b_receiver, asterix_bridge, atm_api), status, last frame | |
| `events` O | audit | |

Projected: `cis_cache` P (U-space airspace volumes it may reconfigure), `ussp_list` P.

## 5. `uspace-lab`

Owns no production entity. Holds: `test_vectors/` (JSON inputs and expected outputs per behaviour: identification resolution, time placement, CPA, zone evaluation with each vertical reference, pressure-altitude widening, ED-318 round trip), `scenarios/` (YAML: aircraft, paths, receivers, expected alerts and violations), `load/` (profiles at 100/1000/5000), run results.

## 6. Cross-system identifiers

| Identifier | Format | Minted by | Used by |
|---|---|---|---|
| Operator registration number | GCAA format (Q5) | authority | everyone; public part only in broadcasts |
| UAS serial | ANSI/CTA-2063-A | manufacturer | everyone |
| Zone identifier | ED-318 `identifier`, unique within `country` | authority (ANSP for DAR, prefixed `DAR-`) | CISP, USSP, ANSP |
| Operational intent id / flight id | UUID v4 (DSS entity id) | USSP | DSS, peers, authority, ANSP |
| Track id | `source:instance:local` | each picture owner | local only, never cross-system |
| Incident / occurrence id | ULID | authority (occurrence `report_ref` from the reporter) | authority, reporter |
| Receiver id | slug | authority | receivers |
| Client id | `sys-name-nn` | authority token service | all |

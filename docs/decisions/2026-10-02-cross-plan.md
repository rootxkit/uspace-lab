# Decision record: cross-plan reconciliation of 2026-10-02

Status: **accepted by the coordinator on 2026-10-02**. Every
coordinator-decidable decision (§1 M1–M38, §2.2, §3, §4, the per-plan
edits of §5 and the appendices) is applied as written. The owner-only
questions of §2.1 remain **open**: each is carried with its recommended
default until the named person answers, and nothing here or in
`docs/PLAN.md` invents the policy answer. The lab's own share of this
document (§3 L1–L5, §4, §5.6) is planned in `docs/PLAN.md` and
`docs/WORKPACKAGES/WP-L*.md`; the deployment share (§3 D1, D2) in
`docs/deploy/PLAN.md`.

The text below is the reconciliation document as reviewed, unchanged.
Its own status line ("read-only review, nothing has been edited") is the
state at the time it was written and is superseded by this header.

---

# Cross-plan reconciliation: uspace-cisp, uspace-ui, uspace-authority, uspace-ussp, uspace-ansp

Date: 2026-10-02. Inputs: the five `docs/PLAN.md` files and their
`docs/WORKPACKAGES/` and `CLAUDE.md` on branch `plan/initial`
(`plan-wt/<repo>`), the spec `uspace-lab/docs/spec/00..09`,
`uspace-lab/knowledge`, and `uspace-core` v1.0.0 (`CHANGELOG.md`,
`docs/RELEASING.md`, `auth`, `ed318`, `identify`, `core`).

Fixed by the owner and taken as given: Go, OpenAPI 3.1 spec-first with
oapi-codegen v2, pgx + sqlc, goose, NATS JetStream, slog, Prometheus,
OTel; Next.js + shadcn + MapLibre, ka/en; the authority is the RS256
issuer and everyone verifies with `uspace-core/auth`; images built in
CI; docker compose + Caddy on one droplet; the USSP is not assumed to be
ours; CI stays lean.

Read-only review. Nothing below has been edited, committed or posted.

---

## 1. Contract mismatches

Numbered M-nn. "Decision" is the recommended resolution; the per-plan
edits in §5 apply them. Where two plans merely differ on something
internal to one system (NATS subject names, process counts) it is noted
as "no cross-plan conflict" so nobody wastes a round on it.

### 1.1 Endpoints and owners (spec 02 F1..F13)

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M1 | The CIS change-notification receiver has three paths: authority `POST /v1/cis/notifications`, USSP `POST /v1/cis/notify`, ANSP `POST /v1/cis/webhook`; the ANSP's degraded direct delivery targets a fourth, `POST {base_url}/v1/cis/changes`, which no receiver implements. | authority PLAN §5, WP-6; ussp PLAN §6.1, WP-4; ansp PLAN §6, §15 gap 10, WP-7, WP-8 | One path on every subscriber: **`POST /v1/cis/notifications`**. The CISP posts to the registered `callback_url` (so it is free), and the ANSP's degraded path posts the same `cis/change/v1` JWS to `{base_url}/v1/cis/notifications` of every USSP on the CIS list and of the authority. |
| M2 | ANSP coordination intake is `POST /v1/coordination/annex-v` (ansp) while the USSP calls `POST /v1/coordination/notices` (ussp Q10, WP-15). Both agree on `GET /v1/coordination/notices/{ack_id}`. | ansp PLAN §6, WP-10; ussp PLAN §15 Q10, WP-15 | **`POST /v1/coordination/notices`**, response `202 {ack_id, state: received, received_at}`; the human acknowledgement is read by `GET .../notices/{ack_id}` (no push in v1, per ansp gap 6). |
| M3 | Publisher heartbeat: CISP defines `POST /v1/publishers/heartbeat {sent_at}` every 10 s; ANSP sends `POST /v1/restrictions/heartbeat {publisher, at, active_restriction_refs}` every 30 s; stale after 60 s on both sides. The authority has no heartbeat job at all. | cisp PLAN §6.1, §15 Q3, WP-3; ansp PLAN §15 gap 9, WP-8; authority WP-6 | **`POST /v1/publishers/heartbeat`**, body `{sent_at, active_refs?: []}` (the ANSP fills `active_refs` with `ansp_ref`s; the authority sends none), **every 15 s** (three misses = 60 s stale). The authority adds the job to WP-6. |
| M4 | Restriction publication body: CISP `cis/restriction/v1 {ansp_ref, ansp_version, uspace_airspace_id, state, starts_at, ends_at, feature}` and `PATCH {op, ansp_version, ends_at?}`; the ANSP sends `version` and relies on an `Idempotency-Key` header that the CISP never reads. | cisp PLAN §6.2; ansp PLAN §6 outbound, WP-8 | Body per the CISP schema (`ansp_version`, not `version`). The idempotency key **is the body pair `(ansp_ref, ansp_version)`**; the ANSP may still send `Idempotency-Key` but nothing depends on it, and ansp WP-8's "receives one POST with Idempotency-Key" assertion becomes "one POST with the pair". |
| M5 | Degraded direct delivery: the ANSP signs with its own key (`iss` = ANSP) but the USSP (WP-4) and the authority (WP-6) verify notifications only against the CISP's JWKS. The path exists on paper and nobody can receive it. | ansp PLAN §15 gap 10, WP-8; ussp WP-4; authority WP-6 | Receivers allow-list **two issuers** for `/v1/cis/notifications`: the CISP and the ANSP (`*_CIS_NOTIFY_ISSUERS`, JWKS URLs from config). `pull_url` is honoured only when its host equals the issuer's configured base host (SSRF guard). Reason values unknown to the receiver, and `subscription_test`/`republished`, are acknowledged `204` without a pull. |
| M6 | ISA change notifications into the authority: authority WP-14 says they come "from the DSS" with scope `rid.service_provider` and "the DSS audience". In F3411 the Service Provider (ussp `rid-sp`, WP-9) posts them to each subscriber's `uss_base_url`; the DSS only lists subscribers. | authority PLAN §5, WP-14; ussp WP-9 | Sender = the SP with `rid.service_provider`, `aud` = the authority's host (M15). Authority WP-14 wording corrected; no code difference once M15 is applied. |
| M7 | The USSP list and the Art. 3(4) block have schemas at the CISP (`cis/ussp_list/v1`, `cis/uspace_requirements/v1`) but the authority, which produces both, builds them "in the 02 F1 shape" and "as 02 F1 names the keys" without referencing those schemas. | cisp PLAN §6.7, §15 Q5, Q6; authority WP-5, WP-16 | The CISP owns both schemas (it is the API owner, M21). Authority WP-5 and WP-16 validate their output against a pinned copy of the CISP schema in CI (the same `api/clients/cisp.yaml` + `SOURCE` mechanism of authority Q-A2). |
| M8 | The authorisation number needs a short USSP code (`03 §6`); ussp Q7 proposes `certificates.id`, which the authority plan does not define as short, and `cis/ussp_list/v1` carries `ussp_id` with no stated format. | ussp PLAN §15 Q7; authority WP-16; cisp §6.7 | Authority adds **`certificates.code`** (≤ 8 upper-case alphanumerics, unique, assigned at issue); it is `ussp_id` in `cis/ussp_list/v1` and `USSP_SYSTEM_ID` at the USSP (`USSP-DEV` in the lab). |
| M9 | The CISP refuses a restriction whose `uspace_airspace_id` matches no current `USPACE` feature; the ANSP policy `require_uspace_airspace=false` lets a supervisor create a free-standing restriction, which the CISP would then refuse. | cisp PLAN §6.2, §15 Q6; ansp PLAN §15 gap 13 | **Strict on both sides**; the lab publishes a designation for every demo (C-M2, N-M1). The ANSP's `false` option is dropped (it only produces an unpublishable restriction). |
| M10 | Restriction identifier: spec `03 §6` says prefix `DAR-`; the CISP proposes `D` + 6 base-36; the ANSP proposes `DAR` + 4 base-36 (ED-318 caps `identifier` at 7, enforced by core). | cisp §15 Q17; ansp D4, §15 gap 4 | **`DAR` + 4 base-36** (no hyphen; 1.6 M ids; the prefix stays readable). The CISP enforces only `≤ 7` and uniqueness across datasets (D8), never a prefix. Spec erratum for `03 §6`. |
| M11 | Interim clients for sibling APIs: the authority copies each sibling's `api/openapi.yaml` into `api/clients/<system>.yaml` with a `SOURCE` commit and a CI diff; the USSP hand-builds from spec `02` with `// CONTRACT: pending` markers; the ANSP uses `x-pending-schema`. Three mechanisms for one problem. | authority Q-A2, WP-6, WP-14, WP-15; ussp Q8, §6.3; ansp §15 gap 15 | The authority's mechanism everywhere, from the day each sibling's WP-0/WP-3 lands its OpenAPI skeleton (§4 orders the skeletons first). The lab aggregate (KT-2, §3) replaces the copies; until then the copy + `SOURCE` + diff is the contract. |
| M12 | F4 stream frames: the ANSP emits NDJSON `track/manned/v1` plus a `feed/status/v1` frame every 2 s and `state: stale|source_disabled` frames; the authority (WP-15) and the USSP (WP-14) validate "each frame → `track/manned/v1`" and would refuse the status frames. | ansp WP-6; authority WP-15; ussp WP-14 | Every WS frame carries the common envelope with `schema` (M20); consumers dispatch on `schema` and treat `console/status/v1` as the feed's status. `feed/status/v1` is retired in favour of `console/status/v1`. |
| M13 | Occurrence `reporter.person_ref`: the USSP expects "the public key the authority publishes"; the ANSP sends it encrypted under its own secrets key, which the authority cannot read; the authority stores it encrypted under its own key. | ussp WP-15; ansp WP-10; authority WP-18 | `person_ref` is an **opaque reference in clear text over TLS**; the authority encrypts at rest for `incident_officer` (376 Art. 16). No public-key exchange. |

No cross-plan conflict, spec erratum only: the ANSP has three processes
(`manned-feed` added, ansp D1); `02 F4` `pressure_alt_m` vs the plans'
`alt_pressure_m` (ansp gap 14; the authority and USSP already use
`alt_pressure_m`); the message name `manned_track.v1` vs
`track/manned/v1` (all plans use the latter).

### 1.2 Message schemas and envelope

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M14 | Schema ownership is claimed twice or inconsistently: `track/telemetry/v1` by the USSP (D8) and by the authority (`schemas/track`); `source/status/v1` by the ANSP but produced by every system; `coordination/annex_v/v1` by the USSP (D8) although the USSP's own Q9 rule ("the API owner publishes the request schema") and ansp gap 15 point elsewhere; `occurrence/v1` "authority" per ussp Q9 but listed "as consumed" by the authority. | ussp D8, Q9, WP-15; authority PLAN §3 `schemas/`, WP-18; ansp §3, §15 gap 15 | One rule, two halves. **HTTP request and response bodies are owned by the repo whose `api/openapi.yaml` carries them**: `coordination/annex_v/v1` → ANSP; `occurrence/v1`, `rid/observation/v1` → authority; `telemetry/v1`, `intent/request/v1`, `intent/decision/v1` → USSP; `cis/change/v1`, `cis/restriction/v1`, `cis/ussp_list/v1`, `cis/uspace_requirements/v1` → CISP. **Pushed stream messages are owned by the producer**: `track/manned/v1` → ANSP; `alert/v1`, `traffic/product/v1` → USSP; `violation/v1` → authority. **Shapes produced by several systems** (`envelope/v1`, `track/telemetry/v1`, `source/status/v1`, `zone/applicable/v1`, `console/status/v1`, `console/snapshot/v1`, `console/subscribe/v1`) live in **`uspace-lab/schemas/common/`** (KT-2, §3) and are consumed by everyone. |
| M15 | ED-318 collection metadata: spec `02 F1`/`04 §3.4` name `{creationDateTime, updateDateTime, originator}`; `uspace-core/ed318.Metadata` has `{validFrom, validTo, issued, provider, description}`; the CISP follows core (Q1) and the authority's export (WP-5) uses the spec names, which core's `Parse` on the CISP would not carry. | cisp §15 Q1; authority WP-5 | **Follow core.** Producers set `metadata.issued` and `metadata.provider`; the CISP adds top-level `cis_dataset`, `cis_version`, `cis_updated_at` and the `ETag`. Spec erratum for `02`/`04`. |
| M16 | `cis/change/v1` reason enumeration at the CISP includes `subscription_test` and `republished`, which no consumer plan handles. | cisp §6.5, §6.7; ussp WP-4; authority WP-6; ansp WP-7 | Covered by M5: unknown reasons and these two are a `204` no-op at every receiver (additive-enum rule of `04 §4`). |
| M17 | Applicability on consoles: `uspace-ui` dims a zone on `applies === false` and asks the CISP and the authority for a per-feature flag (ui Q3); the CISP's `?at=` returns only the applicable features (plus `cis_applicability: unknown`), so a console cannot show "not applicable now" without two fetches. | ui §14 Q3; cisp §6.3, WP-4; authority WP-5 | Add **`?applies_at=<RFC 3339>`** (annotate, no filtering: `extendedProperties.cis_applicability` ∈ `applies` / `not_applicable` / `unknown`) beside the existing filtering `?at=`, on the CISP read API and on the authority's `GET /v1/zones/export`. Additive. |

### 1.3 JWT claims: audience, client ids, scopes, roles

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M18 | Audience strings: the authority's token service allows `cisp`, `ussp-<id>`, `dss`, `ansp`, `authority`; the USSP verifies `aud = USSP_SYSTEM_ID`, the CISP `cisp`, the ANSP `ANSP_SYSTEM_ID`. But F3411/F3548 discovery yields only a `uss_base_url` (ISA subscribers, peer intents), so a system-id audience cannot be derived for ISA notifications or peer calls to a third-party USSP, and InterUSS tooling (DSS `accepted_jwt_audiences`, `uss_qualifier`) uses hostnames. | authority WP-2, WP-14, WP-15; ussp WP-2, WP-9, WP-13; cisp §15 Q7; ansp WP-0, WP-9 | **`aud` = the host of the target's published base URL, for every machine token, national and standard** (`uspace-cisp.chikox.net`, the DSS's host, a peer's `uss_base_url` host). Each verifier accepts a configured list `*_AUDIENCES` (its public host plus a lab alias such as the compose service name). `POST /oauth/token` takes `audience` (and RFC 8707 `resource`); the per-client allowed-audience list stays for national scopes; for standard scopes (`utm.*`, `rid.*`) any audience may be requested, because peers are discovered, not configured (`00 §7`). `USSP_SYSTEM_ID` remains the USSP code (M8), not the audience. |
| M19 | Webhook JWS `aud`: the CISP sets it to the subscription's `client_id`; the USSP, ANSP and authority verify `aud` = their own system id. | cisp §6.5, §15 Q9; ussp WP-4; ansp WP-7; authority WP-6 | `aud` = **host of the `callback_url`** (M18 applied to webhooks). `sub` = subscription id, `jti` = delivery id, `iat`; `Content-Type: application/jose`. |
| M20 | Console session JWTs differ in every system: authority `aud = "console"`, claims `realm`, `roles[]`, `sid`; USSP `scope = "session:<role>"`; CISP `scope = "console:<role>"`, `aud = CISP_AUDIENCE`; ANSP `scope = <role>`, `aud = SystemID`; `uspace-ui` reads `role` and `realm` (ui Q9). | authority WP-2; ussp WP-2; cisp WP-8; ansp WP-2; ui §3.16, §14 Q9 | One session shape, verified by the same `core/auth.Verifier` as machine tokens: `iss` = the system's own issuer, **`aud` = the system's own host** (M18), `sub` = account id, **`scope = "session"`, `roles: [string]`** (one element where a system has single-role users), **`realm`** (`console` default; `police` at the authority; `portal` for USSP operators), `jti` = session id, `exp` ≤ 12 h, `kid`. `uspace-ui`'s `sessionClaimsUnverified` returns `roles: string[]` and `realm`. |
| M21 | Session cookie name: the ANSP uses `ansp_session`; the kit fixes `uspace_session` and `uspace_csrf`; the others do not name it. | ansp WP-2, WP-11; ui §6.3 | `uspace_session` / `uspace_csrf`, `X-CSRF-Token`, `HttpOnly; Secure; SameSite=Strict`, everywhere. |
| M22 | WebSocket authentication for browsers: the kit proposes a BFF `/_bff/ws-ticket` route and a 4401-reconnect; the ANSP, CISP and authority accept the session cookie on a same-origin upgrade with an `Origin` check. | ui §3.16, WP-5, WP-8; ansp §15 gap 19, WP-2, WP-6; cisp WP-7; authority WP-13 | **Cookie on the same-origin upgrade + `Origin` allow-list**, verified by the WS process with the shared verifier. No ticket: the BFF cannot proxy WebSockets and a ticket in a query string is logged. `uspace-ui` drops `wsTicket` and the ticket fetch; `useFeed` keeps `url: () => Promise<string>` as a generic hook and a 4401 means "re-login". |
| M23 | Scope catalogue: the ANSP introduces `ansp.coordination` and `ansp.requests`; the CISP reserves `cis.publish:ats_data`; the authority introduces `dp.observe` (lab-only) and uses `utm.conformance_monitoring_sa` for its own DSS reads; authority WP-2's test "refuses an unknown scope" against the `06 §3` catalogue would refuse the first four. | ansp §15 gap 7; cisp §15 Q4; authority Q-A5, Q-A7, WP-2 | The catalogue is `06 §3` **plus `ansp.coordination`, `ansp.requests`, `dp.observe` (issued to the lab client only) and the reserved `cis.publish:ats_data`**. `rid.observe` is not a JWT scope (receivers use bearer key + HMAC) and is removed from the JWT catalogue. Authority WP-2 holds the list; a sibling adding a scope opens a PR there first. |
| M24 | Client ids: spec `03 §6` `sys-name-nn`; the CISP expects `authority-cisp-01`, `ansp-cisp-01` (one client per caller-target pair); the authority registers one client per certificate holder. | cisp §15 Q7; authority WP-2, WP-16 | **One client per calling system** (`authority-01`, `cisp-01`, `ansp-01`, `ussp-<code>-01`, `lab-01`), audiences chosen per token request (M18). The CISP binds publishers by `sub` ∈ configured client ids as planned; the values are just these. |

### 1.4 mTLS

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M25 | F2 (ANSP → CISP): the CISP requires `X-Client-Cert-Subject` from Caddy on restriction and heartbeat routes (`CISP_MTLS_MODE=header|off`); the ANSP presents a client certificate "when configured". F4 (ANSP streams): the spec says mandatory, the ANSP makes it `ANSP_MTLS_REQUIRED` configurable, the authority and USSP present certificates from env. Three flag names, two modes, no shared Caddy rule. | cisp §8.3, §15 Q11, WP-2; ansp §15 gap 8, WP-6, WP-8; authority Q-A19, WP-15; ussp WP-14 | One flag name **`<SYS>_MTLS_MODE = required | off`** in every repo; `required` in production, `off` on the staging droplet and in the lab. Caddy terminates TLS with `client_auth { mode verify_if_given, trusted_ca_cert_file ... }` on the CISP and ANSP hosts and forwards `X-Client-Cert-Subject` (stripped on every other route); the Go middleware enforces presence and the subject binding only on the mTLS routes (`/v1/restrictions*`, `/v1/publishers/heartbeat`, `/v1/manned-traffic/*`, `/v1/coordination/*`). The Caddy snippet lives in the deployment repo (§3). `off` is printed at error level every status period (cisp WP-2 rule, adopted by all). |

### 1.5 JWS formats

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M26 | Detached JWS header for publications: the CISP specifies `X-JWS-Signature: <protected>..<signature>` (RFC 7515 App. F, RFC 7797 `b64:false`, `crit:["b64"]`, `alg RS256`, `kid`, `iat` ≤ 5 min); the ANSP sends it in a `Signature` header; the authority (WP-6) signs "as a detached JWS" without naming the header. | cisp §15 Q8, WP-2; ansp WP-8; authority WP-6 | **The CISP's specification, header `X-JWS-Signature`**, key = the publisher's signing key published in that system's `/.well-known/jwks.json` (the authority's token-service JWKS for the authority, `use: sig`, distinguished by `kid`; the ANSP's own JWKS for the ANSP). |
| M27 | Three repos plan their own JWS code on `jwx/v3` (cisp `internal/jws`, ansp `deliver`/`cis`, authority `cisp`); cisp Q20 asks core for `VerifyDetached` / `SignCompact`. | cisp §15 Q20; ansp §4; authority §13 | A core additive release (§3, core WP-14) ships `auth.SignDetached`, `auth.VerifyDetached`, `auth.SignCompact`, `auth.VerifyCompact` and a `KeyRing`; the three repos import it. The CISP's WP-2 may land first on its own `internal/jws` and switch in a follow-up (it is on the critical path); the ANSP and authority wait for core (they are not). |

### 1.6 Error body

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M28 | All four backends say RFC 9457 `application/problem+json`, but the field-error extension differs: CISP `problems: [{field, reason}]` + `truncated`; ANSP `field` (singular) and `reason`; USSP "the `conflicts[]` or the field path"; authority unspecified; `uspace-ui` needs `errors: [{field, reason}]` (ui Q2). | cisp §6, WP-0; ansp §6, WP-3; ussp §6; authority §5; ui §3.1, §14 Q2 | **`{type, title, status, detail, instance, errors: [{field, reason}], truncated?: bool}`**, `field` = the JSON path as `core.FieldError`/`ed269.Problems` write it, capped at 100 with `truncated`. `type` = `https://schemas.uspace.ge/problems/<slug>` (the same domain as schema `$id`s), `slug` = the counter or refusal name (`unauthenticated`, `forbidden`, `signature`, `not_a_publisher`, `cis_stale`, ...). The USSP's `conflicts[]` stays on the **decision** body (`intent/decision/v1`), not on the problem. |

### 1.7 The console WebSocket frame

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M29 | Four stream shapes for browsers: authority picture-ws `{track | violation | source | status}` with a `{viewport}` control message; CISP `/v1/stream` `{type: heartbeat | change | resync}`; ANSP console stream = the F4 NDJSON with `feed/status/v1`; USSP traffic-ws `traffic/product/v1` at 1 Hz. `uspace-ui` proposes one frame: the `04 §2` envelope + `body`, with `console/status/v1`, `console/snapshot/v1` and the client's `console/subscribe/v1` (ui §6.3, Q5). | authority WP-13; cisp WP-7; ansp WP-6; ussp WP-11; ui §6.3, §14 Q5, Q6 | **Adopt the kit's frame on every browser-facing WebSocket**: each frame = envelope (`schema`, `msg_id`, `producer`, `ts`, `rx_ts`, `captured_at`, `time_source`, `backlog`) + `body` named by `schema`. `console/status/v1` (connection id, `server_ts`, `policy_version`, `stale_after_s`, `live_max_age_s`, `dropped_frames`, `degraded[]`, `sources[]`, plus system extras such as `datasets{}`, `projection_age_s`, `cis_age_s`, `nats`) every 2 s and on connect; `console/snapshot/v1` on connect and re-subscribe; `console/subscribe/v1 {bbox, layers[]}` from the client. Bodies are the catalogued messages: `track/telemetry/v1`, `track/manned/v1`, `alert/v1`, `violation/v1`, `cis/change/v1`, `traffic/product/v1`. The CISP's `resync` becomes a `console/status/v1` with `resync_since`. Machine-facing streams (F4 to USSPs, `/v1/traffic` to operators) use the same envelope so one client code path parses both; their bodies are unchanged. |

### 1.8 NATS subjects

| # | Observation | Decision |
|---|---|---|
| M30 | Internal subjects diverge from spec `05 §3`: ANSP `man.v1.<adapter>.<icao24>` (no cells); authority adds `tsw.v1.<table>`, `zones.v1.changed`, `registry.v1.changed`; USSP adds `conf.v1`, `peer.v1`, `traffic.product.v1`; CISP uses `cis.v1.change.<dataset>` internally while consumers use `cis.v1.<dataset>`. | **No cross-plan conflict**: NATS never crosses a system (`02 §1`). Accept every deviation; the lab records them as a spec erratum to `05 §3`. The only rule kept: a subject that carries an `04` message carries the envelope. |

### 1.9 The lab aggregate (KT-2)

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M31 | Every plan depends on `uspace-lab/api/` and `uspace-lab/schemas/` (authority Q-A2; ussp Q8; ui Q16; ansp gap 15; cisp §6.7, §10.6), and on lab vector-owner changes (cisp Q18). `uspace-lab` has no plan and no owner among the five. | all | Prerequisite work, §3 (lab WP-L1..L4). Until it exists the copy + `SOURCE` + CI-diff mechanism (M11) is the contract. |

### 1.10 ED-318 identifier length vs `DAR-`

See M10. Core's `ed318` enforces 7 characters; `03 §6`'s `DAR-` prefix is a spec error.

### 1.11 `uspace-ui` distribution and versions

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M32 | Distribution: the kit publishes to npmjs with trusted publishing (ui D10, Q1); the CISP plans a `github:rootxkit/uspace-ui#v0.1.0` dependency "until a registry exists"; the others say "pinned" without a registry. A git-tag dependency needs a `prepare` build with full devDependencies inside every Docker build. | ui D10, §14 Q1; cisp §15 Q14, WP-9; authority WP-21; ansp WP-11; ussp WP-17 | **npmjs only**, exact pins. The kit publishes `0.1.0-rc.1` as soon as WP-0..WP-5 merge (a new ui WP-13a, "rc publish"), so the CISP's WP-9 starts on an rc and bumps. No git-tag installs. |
| M33 | Versions vs consumers: authority WP-21 needs `live`/`TrackLayer` (kit 0.2.0); USSP WP-17 needs `form`/`table` (0.2.0) and `alerts` (0.3.0); ANSP WP-11 (N-M1) needs `RestrictionLayer`, which the kit's WP table puts in WP-6 (0.1.0) but its §12 release list puts in 0.3.0. | ui §12, §13; ansp WP-11 | ui §12 corrected: `RestrictionLayer` ships in **0.1.0** (with WP-6, as the WP table says). Consumer pins: CISP ≥ 0.1, ANSP WP-11 ≥ 0.1, authority WP-21 ≥ 0.2, USSP WP-17 ≥ 0.2 (intents pages) and ≥ 0.3 (traffic pages), ANSP WP-12 ≥ 0.3 (`MannedLayer`). |
| M34 | Package manager: the kit and the authority use pnpm; the CISP `npm ci` + `package-lock.json`; the ANSP `npm run types`. | ui §10; authority WP-21; cisp §4, WP-9; ansp WP-11 | **pnpm** with `packageManager` pinned and `--frozen-lockfile`, in every `web/`. |

### 1.12 H3 vs grid partitioning

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M35 | Spec `05 §3` says H3 r5/r3; core left H3 out (cgo). The authority builds a 0.1° × 0.1° / 1° lat-lon grid (`internal/cell`); the USSP builds "≈ 8 km geodesic squares with 16 × 16 parents, `c3:<row>:<col>`"; two implementations of one internal idea. | authority D3, Q-A3; ussp D7, Q2 | **No H3. One pure-Go grid in core** (`geodesy/cell`, additive, §3): `cell5` = 0.1° × 0.1°, `cell3` = 1° × 1°, names `c5:<lat_idx>:<lon_idx>` / `c3:<lat_idx>:<lon_idx>`, ring-1 neighbours, bbox → cell set. Both systems import it; the ANSP and CISP do not partition. Spec erratum for `05 §3`. |

### 1.13 Migration tool

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M36 | All four backends choose goose (spec says golang-migrate), but the version tables differ: CISP `goose_db_version` in each database; authority `goose_version_relational` / `goose_version_timeseries`; USSP and ANSP `goose_db_version_relational` / `goose_db_version_timeseries`. Start-up behaviour differs too: the CISP refuses to start with pending migrations (one-shot `cispctl migrate` service); the authority's `api` migrates at start; the USSP's `api` and `tsdb-writer` migrate at start; the ANSP has `ANSP_MIGRATE_ON_START`. | cisp D11, §5.3, §11; authority D7, §4, §10; ussp D5, §5.3; ansp D8, §5.3 | goose, two embedded trees, version tables **`goose_db_version_relational`** and **`goose_db_version_timeseries`** (a tree run against the wrong database fails on the table name). A `migrate` subcommand on every system binary and a one-shot `migrate` compose service (the CISP pattern); long-running processes **never migrate** and refuse to start on a version lower than they need, printing which. The `*_MIGRATE_ON_START` flags are dropped. Spec erratum for `03`. |

### 1.14 TimescaleDB usage

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M37 | The CISP has one hypertable and asks whether to keep the pair (Q12); the authority puts projection tables (non-hypertables) in the telemetry database instead of NATS KV (D2, Q-A4); container layouts differ (CISP: one `timescale/timescaledb-ha:pg16` with two databases; authority: `postgis/postgis:16-3.4` + `timescaledb-ha`; USSP and ANSP: two containers). Ten PostgreSQL containers on a 2 vCPU droplet. | cisp D9, §11, §15 Q12; authority D2, §10, §13, Q-A4; ussp §11; ansp §11 | Keep the database pair everywhere (layout and tooling uniformity; `03`'s rule). **On the droplet, one `timescale/timescaledb-ha:pg16` container per system holding both databases** (it ships PostGIS; the CISP's pattern); two hosts only when a system outgrows it (`05 §4`). The authority's projection tables in the telemetry database are accepted (LESSONS G-09 is right about KV size); spec erratum for `00 §6.2`/`03`. |

### 1.15 Basemap and font hosting

| # | Mismatch | Where | Decision |
|---|---|---|---|
| M38 | The kit requires a self-hosted PMTiles bundle under `/basemap/` (`basemap.pmtiles`, `SOURCE.json`, glyphs with Georgian ranges, sprites) built by the lab and served by Caddy (ui D6, §6.3, Q4); no system plan mounts or serves it, and the CISP public map WP mentions only "tile sources". Fonts: the kit bundles Noto Sans + Noto Sans Georgian (ui D7, Q8); no consumer plan mentions `next/font/local` or a CSP. | ui D6, D7, §6.3, §7, §14 Q4, Q8; cisp WP-10; authority WP-21; ussp WP-17; ansp WP-11 | **The lab builds the bundle** (lab WP-L3, §3) from the predecessor's `fetch_basemap.sh` as a release artefact; **the deployment repo serves `/basemap/*` from one shared read-only volume on every host** (Caddy `file_server` with range requests and long cache headers; one copy for five systems). Fonts from the kit via `next/font/local`; every `web/` sets the kit's CSP (`connect-src 'self'`, `font-src 'self'`, `worker-src blob:`); no third-party tile or font request, ever. |

Count: **38 mismatches** (M1–M38), of which 3 are recorded as "no cross-plan conflict, spec erratum" (M11 note, M30, and the `pressure_alt_m` naming) and 35 need an edit in at least one plan.

---

## 2. Decisions on every open question

Grouped by the plan that asked. "Decision" is what to write into the plan now. Owner-only rows need a human (GCAA, the ministry, Sakaeronavigatsia, the DPO, or the owner's money); coordinator-decidable rows have a default that is applied unless the owner objects.

### 2.1 Owner-only

| Plan | Q | Question (verbatim from the plan) | Recommended default until answered | Why it is owner-only |
|---|---|---|---|---|
| authority | Q-A10 | "Registration-number format and whether the secret part is on air (Q5)." | `regnum` pattern from `authority_policy` (EU shape); received suffix hashed; compare on the public part. | GCAA fixes the format (spec Q5). |
| authority | Q-A11 | "Registry of record or mirror of uas.gov.ge (Q4); whether the public portal takes applications." | Build the registry as the record; import + applications behind `REGISTRY_APPLICATIONS=on`. | Data-sharing agreement with uas.gov.ge (spec Q4). |
| authority | Q-A12 | "Occurrence export format (Q9): E5X requires the ECCAIRS taxonomy." | De-identified JSON tagged `eccairs-compatible-draft`; E5X writer later. | GCAA's reporting obligations (spec Q9). |
| authority | Q-A13 | "Terrain and geoid sources (Q10) and their licence." | Copernicus GLO-30 + EGM2008 2.5′, attribution beside every AGL number. | Licence acceptance (spec Q10). |
| authority | Q-A14 | "Police access legal basis and access levels (Q8, Art. 18(b)–(c))." | Purpose-logged realm, status-only by default, PII only with a configured purpose list + case reference. | National law and the DPO (spec Q8). |
| authority | Q-A15 | "Retention above the floor (Q8)." | `05 §4` defaults as policy rows (90 d telemetry online, 2 y archive, 5 y alerts/intents, incidents indefinite, audit 10 y). | Legal retention (spec Q8). |
| authority | Q-A16 | "Who hosts the DSS and the issuer URL (Q7)." | Lab DSS beside the CISP compose on the droplet; issuer URL = the authority's host. | Production hosting of the DSS is a national choice (spec Q7). |
| authority | Q-A17 | "Spec 01 §1 has operators registering 'through the public portal'; 06 §3 lists no operator authentication at the authority." | Anonymous applications with e-mail verification; no operator accounts at the authority. | Depends on Q4 (registry of record). |
| authority | Q-A18 | "02 F7 records: which USSP records the authority pulls and when (Art. 18(b) determination)." | Daily `GET /v1/records/daily/{date}` from every operating USSP; per-flight on demand from an incident. | The authority's Art. 18(b) determination. |
| cisp | Q4 | "ATS.OR.127 operational data items (02 F2, 01 C2) have no format or scope." | Reserve dataset `ats_operational_data` and scope `cis.publish:ats_data`; no code until the SLA names the items. | Annex V SLA between GCAA and Sakaeronavigatsia. |
| cisp | Q10 | "What the public subset excludes (01 C1)." | Full `zones`, `uspace_airspace`, `restrictions`; `ussp_list` without `base_url` and `certificate_id`; no history, cursor or subscriptions. | GCAA decides what is public. |
| cisp | Q15 | "Retention of the delivery log and of refused attempts." | 90 d `delivery_attempts`, 1 y `publication_attempts`, publications and audit indefinite. | DPO (spec Q8). |
| cisp | Q16 | "Signing-key custody (06 T4: HSM/KMS, 90-day rotation)." | File-mounted PEM on the droplet, two-key JWKS overlap; the code reads a PEM path only. | Production custody is the state's. |
| ansp | 3 | "Vertical reference of a dynamic restriction: ED-318 allows AGL; F3548 constraints need W84." | AMSL or WGS84 only; AGL refused with a reason; AMSL → HAE via `geoid` with conservative undulation. | How Sakaeronavigatsia's ATC states levels. |
| ansp | 11 | "02 F2: 'the ATS.OR.127 operational data items the ANSP agrees with the authority (Annex V SLA)' are undefined." | Out of scope until the SLA; same outbox later. | Annex V SLA (spec Q3). |
| ansp | 12 | "Which surveillance formats the ANSP can hand over (Q3, Q14): ADS-B via dump1090-style outputs, ASTERIX CAT021/CAT062 from the ATM system, or an ATM API." | Replay + dump1090 readers in v1; ASTERIX stub; ATM API after the agreement. | Sakaeronavigatsia's data-release agreement (spec Q3, Q14). |
| ansp | 17 | "Occurrence reporting by the ANSP (376 Art. 4(8)) and the 2017/373 record-keeping period (ATM/ANS.OR.B.030, unverified)." | Console form queued to the authority; `05 §4` retention defaults. | Sakaeronavigatsia's safety office. |
| ussp | Q6 | "Deviation threshold defaults (Art. 10(2)(d)) and lost_link_s, telemetry_lost_s, nearby radius: national figures with no source." | Demo policy: `h_m 50`, `v_m 15`, `t_s 60`, `telemetry_lost_s 5`, `lost_link_s 15`, `nonconformance_nearby_radius_m 2000`, CPA 60 s / 60 m / 20 m / 800 m; shown on the console with `policy_version`. | GCAA's Art. 3(4) figures (spec Q17). |
| ussp | Q11 | "e-conspicuity receiver (spec Q18): hardware and feed format." | readsb/dump1090 `aircraft.json` or SBS stream; lab replays a file; ADS-L deferred. | GCAA (spec Q14, Q18). |
| ussp | Q13 | "Which uss_qualifier configurations are required for certification (spec Q7)." | F3411 v22a SP + F3548 strategic coordination and constraint processing; DP tests informative. | GCAA adopts the suite as a certification condition (spec Q7). |
| ussp | Q14 | "Public subset of network identification (Art. 8(4)(a), national choice)." | None served by the USSP; the authority decides and serves from its DP picture. | GCAA (Art. 8(4)(a)). |
| ussp | Q17 | "Specific-category authorisation workflow (spec Q12): escalate inside the system or reference an external authorisation id." | Reference (`authorisation_ref`); `pending_authority` is visible, nothing is sent. | GCAA (spec Q12). |
| ussp | Q18 | "Retention above the floor (spec Q8)." | `05 §4` defaults; jobs configurable. | DPO (spec Q8). |
| ui | Q1 | "Registry: 'npm or GitHub Packages'. GitHub Packages needs a token to install even a public package [...]; npmjs needs the @rootxkit scope and supports OIDC trusted publishing with provenance, so no secret is stored anywhere." | npmjs, trusted publishing (M32). | The owner must confirm access to the `@rootxkit` npm scope (an account matter). |
| ui | Q7 | "Visual regression without a hosted service (cost, secret) means no pixel diff of the map." | Accept D9 (stories as tests, axe, DOM snapshots, Pages Storybook). | Spending money on pixel snapshots is the owner's. |
| ui | Q11 | "Accessibility obligations for Georgian public interfaces (08 Q15) are unanswered." | WCAG 2.2 AA, `axe` gates CI; hand audit of the public map and registry check (ui WP-13). | Ministry (spec Q15). |
| (new) | — | Droplet sizing: the demo adds the InterUSS DSS (plus its CockroachDB) and a lab stack to five systems (each with PostgreSQL/Timescale, NATS, 3–8 Go processes and a Next.js server) on 2 vCPU / 3.8 GB. The per-system memory budgets in the plans (authority ≤ 1.2 GB, CISP ≤ 0.6 GB, ANSP ≤ 0.6 GB, plus USSP and lab) already exceed it. | Resize the droplet to ≥ 4 vCPU / 8 GB before L-M1, or run the DSS and lab on a second droplet. | Money. |
| (spec) | Q16 | "Hosting: when do production domains and state hosting take over from *.chikox.net" | Staging on the droplet until A-M5. | GCAA. |

### 2.2 Coordinator-decidable (decided here)

| Plan | Q | Decision | Reason (one line) |
|---|---|---|---|
| cisp | Q1 | Follow core's `ed318.Metadata` (`issued`, `provider`) + top-level `cis_*` members (M15). | Core is the parser everyone runs; the spec names are from ED-269-era text. |
| cisp | Q2 | The ANSP declares states; the CISP only expires `active → ended (ended_by: expiry)` at `ends_at`; a `planned` past `starts_at` stays `planned` and is flagged. | ANSP is the master (`03 §4`); the CISP never edits content. |
| cisp | Q3 | `POST /v1/publishers/heartbeat` `{sent_at, active_refs?}` every 15 s; stale after 60 s (M3). | One endpoint for both publishers; three misses = the spec's 60 s. |
| cisp | Q5 | The CISP owns `cis/ussp_list/v1` (M7, M14). | API-owner rule. |
| cisp | Q6 | The CISP owns `cis/uspace_requirements/v1` and validates the blocks; restrictions must name a current `USPACE` feature (M9). | API-owner rule; ATS.TR.237 applies inside U-space airspace. |
| cisp | Q7 | `aud` = the CISP's host; client ids `authority-01`, `ansp-01` (M18, M24). | InterUSS convention; one client per calling system. |
| cisp | Q8 | `X-JWS-Signature`, RFC 7515 App. F + RFC 7797 `b64:false`, `crit`, `kid`, `iat` ≤ 5 min; key from the publisher's JWKS (M26). | Standard detached JWS; provenance survives the CISP. |
| cisp | Q9 | Compact JWS, `application/jose`, `iss`, `aud` = callback host, `sub` = subscription id, `iat`, `jti` = delivery id (M19). | RFC 7515; one verifier. |
| cisp | Q11 | mTLS at Caddy, subject in a stripped header, Go binds it (M25). | Shared Caddy terminates TLS. |
| cisp | Q12 | Keep the pair; one `timescaledb-ha` container with two databases (M37). | Uniform layout; `delivery_attempts` is a true time series. |
| cisp | Q13 | Re-publish = a new `republished` change record for the current version; never a content change. | `01 §2` role text. |
| cisp | Q14 | npmjs only; start WP-9 on the kit's `0.1.0-rc` (M32). | Git-tag installs need a build in every image. |
| cisp | Q17 | `DAR` + 4 base-36; the CISP enforces length and uniqueness only (M10). | 7-char cap in ED-318; readable prefix. |
| cisp | Q18 | Lab drops `cisp` from `alert_lifecycle.json` owners (lab WP-L4). | The CISP has no monitor. |
| cisp | Q19 | No `ed318` package in the CISP; spec table updated (lab WP-L4). | Judgement once, in core. |
| cisp | Q20 | Core ships the JWS helpers in v1.1.0 (core WP-14); the CISP may land first on `internal/jws` and switch (M27). | One JOSE implementation; the CISP is on the critical path. |
| cisp | Q21 | goose; version tables `goose_db_version_relational` / `_timeseries` (M36). | Owner's stack; wrong-database guard. |
| cisp | Q22 | Unfiltered responses signed (snapshot); filtered ones carry `ETag`/version, unsigned in C-M1; WP-13 adds on-the-fly signing only if the owner asks. | Annex III A(4) is met by the signed full dataset the consumer can always fetch. |
| cisp | Q23 | WS hub in `api`. | Low rate, no history. |
| cisp | Q24 | Operators read `/public/v1/*` or their USSP's geo-awareness; no operator client at the CISP. | `01 §2` users table. |
| cisp | (ui Q3) | Add `?applies_at=` annotation mode (M17). | A console must show "not applicable now" without judging. |
| authority | Q-A1 | goose, tables as M36. | Owner's stack. |
| authority | Q-A2 | Copy + `SOURCE` + CI diff until the lab aggregate (M11, M31). | Already the best of the three mechanisms. |
| authority | Q-A3 | No H3; core `geodesy/cell` 0.1°/1° grid (M35). | No cgo; one implementation. |
| authority | Q-A4 | Projection tables in the telemetry database for registry, zones, restrictions; KV for switches, policy, cells. Spec erratum. | G-09: a registry outgrows a KV value. |
| authority | Q-A5 | `utm.conformance_monitoring_sa` for the authority's DSS reads; `no_authorisation` detector gated on a designation (WP-26). | F3548 allows it for that purpose; nothing to detect without U-space airspace. |
| authority | Q-A6 | `rid_absent` deferred, not in any wave. | Needs receiver coverage geometry (spec Q11). |
| authority | Q-A7 | `GET /v1/dp/observations?view=` with scope `dp.observe` issued to the lab client only; interface pinned to the lab's InterUSS commit before coding. | Conformance hook, not a product endpoint. |
| authority | Q-A8 | Core adds `core.BasisProvider = "provider"` (additive, v1.1.0); the authority uses `as_broadcast` until then; the kit's `IdentBasis` gains `provider`. | A DP flight is a peer's claim, neither authenticated nor broadcast. |
| authority | Q-A9 | Core adds `alerting.Config.SkipConflicts` (additive); counter `conflict_events_ignored` until then. | Do not pay CPA for alerts the authority discards. |
| authority | Q-A19 | Outbound mTLS: the client presents a certificate from env; inbound: Caddy `client_auth` + subject header bound to `oauth_clients.mtls_subject` (M25). | Consistent with the CISP and ANSP. |
| ussp | Q1 | Build `internal/conformance` and `internal/intent/deconflict` in the USSP now, pinned by vectors in the lab's shape; propose to core as additive packages after S-M2/S-M4. | The USSP is the only regulatory owner; waiting for core puts it on the critical path. |
| ussp | Q2 | Core `geodesy/cell` (M35). | One grid. |
| ussp | Q3 | goose (M36). | Owner's stack. |
| ussp | Q4 | Local accounts + BFF cookie for the portal; client credentials for machines; OIDC claims shape; provider endpoints later. | No relying party exists yet. |
| ussp | Q5 | Generated F3411/F3548 server interfaces and DSS clients in `internal/stdapi` from the same pinned files as core; boundary conversion in one place. | Core deliberately ships types only. |
| ussp | Q7 | `USSP_SYSTEM_ID` = `certificates.code` from the authority (M8). | Short, unique, assigned by the certifier. |
| ussp | Q8 | Copy + `SOURCE` + CI diff (M11); fakes in `internal/testfakes` stay as the executable reading of `02`. | One mechanism across repos. |
| ussp | Q9 | Ownership rule of M14: the USSP owns `telemetry/v1`, `intent/*`, `alert/v1`, `traffic/product/v1`; the ANSP owns `coordination/annex_v/v1`; the authority owns `occurrence/v1`. | API-owner rule applied consistently. |
| ussp | Q10 | `POST /v1/coordination/notices` (M2); poll `GET .../notices/{ack_id}` every 10 s for 5 min. | Matches the ANSP's existing GET. |
| ussp | Q12 | Weather adapter interface; first implementation METAR/TAF from a configured URL; "no source" is a visible 503. | Optional service; fail visible. |
| ussp | Q15 | One `monitor` owning every cell for the demo; ownership map honoured. | Demo scale. |
| ussp | Q16 | Peer conflict notification in the request path with a 900 ms deadline, then the outbox with `peer_notify_late`. | Measure before redesigning. |
| ansp | 1 | Three processes; spec erratum. | B-16: one adapter per feed; the feed needs the union. |
| ansp | 2 | No cells in the ANSP; `man.v1.<adapter>.<icao24>` (M30). | Tens of aircraft. |
| ansp | 4 | `DAR` + 4 base-36 (M10). | 7-char cap. |
| ansp | 5 | goose (M36). | Owner's stack. |
| ansp | 6 | Two states (`received`, `acknowledged`); no push to the USSP in v1; the USSP polls (M2). | Simplest correct reading of Art. 13(2). |
| ansp | 7 | Scopes `ansp.coordination`, `ansp.requests` registered at the authority (M23). | Least privilege per endpoint group. |
| ansp | 8 | `ANSP_MTLS_MODE=required|off` (M25); `off` in staging and lab. | The lab's simulated USSP has no certificate. |
| ansp | 9 | `POST /v1/publishers/heartbeat`, 15 s (M3). | The CISP already defines it. |
| ansp | 10 | `POST {base_url}/v1/cis/notifications` (M1, M5). | One receiver path. |
| ansp | 13 | `require_uspace_airspace` always true; the lab designates (M9). | The CISP refuses otherwise. |
| ansp | 14 | `alt_pressure_m`, `track/manned/v1`; spec erratum. | E-13 naming. |
| ansp | 15 | The ANSP owns `coordination/annex_v/v1` (M14); the CISP owns `cis/change/v1`. | API-owner rule. |
| ansp | 16 | `ConstraintDetails.type = "DAR"`; the ED-318 feature in `geozone` when `ToED269` can map it, else omitted. | Spec `04 §3.5`; F3548 `GeoZone` is ED-269-shaped. |
| ansp | 18 | Pin `uspace-core v1.0.0` (released). | It exists now. |
| ansp | 19 | Cookie on same-origin WS upgrade + `Origin` check; the kit drops the ticket route (M22). | The BFF cannot proxy WebSockets. |
| ansp | 20 | ≤ 600 MB / 0.5 vCPU, measured in WP-13; see the droplet-sizing row in §2.1. | Budget as planned. |
| ui | Q2 | `errors: [{field, reason}]` + `truncated` (M28). | One error body for the form kit. |
| ui | Q3 | `?applies_at=` on the CISP and the authority (M17). | Additive; avoids a double fetch. |
| ui | Q4 | The lab builds the bundle; the deployment repo serves `/basemap/*` from a shared volume; a Georgia-wide extract (city-level zooms for Tbilisi, Kutaisi, Batumi, Poti; z ≤ 12 elsewhere) to stay under ~1 GB; Storybook keeps a Tbilisi-only extract (M38). | Self-hosted maps on an isolated network; disk is finite. |
| ui | Q5 | The console frame is adopted by all four systems (M29). | One `live` client. |
| ui | Q6 | Thresholds come in `console/status/v1` (M29). | INV-03: the kit never defaults them. |
| ui | Q8 | Noto Sans + Noto Sans Georgian, OFL, bundled (M38). | Licence-clean, full Georgian coverage. |
| ui | Q9 | Session claims `roles: string[]`, `realm` (M20). | The authority's role model is the largest. |
| ui | Q10 | `UI_BRAND_*` variables and `/brand/` as proposed. | Branding is configuration (`06 §4`). |
| ui | Q12 | WP-0 pins what is current on its day (Next.js 15+, React 19, Tailwind v4). | Verify, do not assume. |
| ui | Q13 | Tailwind v4 `@source`; a prebuilt stylesheet only if a non-Tailwind consumer appears. | One theme. |
| ui | Q14 | Ship `uspace-ui-gen-api`; every `web/` uses it. | Identical generation and the `noHandWrittenApiTypes` header. |
| ui | Q15 | Acknowledgement flag per console; persistence is the app's POST. | Never leave an operator with a tone they cannot stop. |
| ui | Q16 | Synthetic fixtures until lab WP-L1; the kit's `v1.0.0` waits for it. | KT-2 is prerequisite work (§3). |
| ui | Q17 | The kit renders what it is given; no client-side obfuscation. | A server rule. |

Cross-cutting defaults applied in §5 (not asked by any single plan): the
JWT claim table of M18/M20; the scope catalogue of M23; the problem body
of M28; the console frame of M29; `pnpm` (M34); one Timescale container
per system on the droplet (M37); `*_MTLS_MODE` (M25).

---

## 3. Missing prerequisite work (no plan owns it)

| Id | Work | Proposed owner | Work package | Needed by |
|---|---|---|---|---|
| L1 | **KT-2 aggregate in `uspace-lab`**: `api/` (one directory per system holding a pinned copy of its `api/openapi.yaml` with `SOURCE`, a generated index, `openapi-typescript` and oapi-codegen clients, a CI job that diffs each copy against its repo at the pinned commit) and `schemas/` (mirror of every repo's `schemas/` plus **`schemas/common/`**: `envelope/v1`, `track/telemetry/v1`, `source/status/v1`, `zone/applicable/v1`, `console/status/v1`, `console/snapshot/v1`, `console/subscribe/v1`, `problem/v1`, each with examples; a job that validates every example against its schema). | uspace-lab | **lab WP-L1 `contracts-aggregate`** (one agent; starts day 1 with the common schemas and the directory layout; the per-system copies land as each WP-0 publishes a skeleton) | every repo (M11, M14, M29, M31); ui WP-14 |
| L2 | **DSS and fake-issuer compose for the lab and staging**: InterUSS DSS pinned by digest with its datastore, `accepted_jwt_audiences` = the hosts of M18, a lab token issuer (`core/auth.Issuer`) with a `lab-01` client, `make dss-up`. | uspace-lab | **lab WP-L2 `dss-compose`** | ussp WP-9, WP-13; ansp WP-9; authority WP-14; all conformance hooks |
| L3 | **Basemap bundle**: Georgia PMTiles extract, Protomaps glyphs including the Georgian ranges for the kit's `mapFontstack`, sprites, `SOURCE.json {bounds, osm_data_as_of}`, published as a lab release artefact with a size budget; a Tbilisi-only extract for the kit's Storybook. | uspace-lab | **lab WP-L3 `basemap-bundle`** | ui WP-3 (Storybook extract), cisp WP-10, every console (M38) |
| L4 | **Spec errata and vector-owner changes**: `03` migration tool and `DAR-`; `02`/`04` ED-318 metadata names; `02 F4` `alt_pressure_m` and `track/manned/v1`; `00 §6.1` ANSP processes and the CISP `ed318` package; `05 §3` grid instead of H3 and the subject deviations; `00 §6.2`/`03` projection tables; `06 §3` scope catalogue additions; `02 §1` the error body, JWT audience rule and session claims; `04 §1` the schema-ownership rule of M14; drop `cisp` from `alert_lifecycle.json` owners (a lab vector change: editorial, no behaviour change). | uspace-lab | **lab WP-L4 `spec-errata`** (one agent, one PR, after the owner reads this document) | documentation only; nothing blocks on it |
| L5 | **KT-3/KT-4 lab scaffolding**: SITL launch, the MAVLink → operator-telemetry bridge and the MAVLink → ODID bridge (receive-only), the simulated receiver, the simulated ANSP feed, the scenario runner, `make demo`. | uspace-lab | **lab WP-L5 `sitl-and-simulators`** (can run in parallel with everything; the bridges target the USSP `WS /v1/telemetry` and the authority `POST /v1/rid/observations` contracts, both already fixed in `02`) | INV-02 of every alert WP (ussp WP-10, WP-11, WP-12; authority WP-12; ansp WP-5/6) |
| C1 | **`uspace-core` v1.1.0 (additive)**: `auth.KeyRing`, `auth.SignDetached`, `auth.VerifyDetached`, `auth.SignCompact`, `auth.VerifyCompact` (M27); `core.BasisProvider` (Q-A8); `alerting.Config.SkipConflicts` (Q-A9); `geodesy/cell` (M35). Each with its tests and a CHANGELOG line; no vector change (additive per `RELEASING.md` §1). | uspace-core | **core WP-14 `v1.1-additive`** (one agent; four small PRs or one; tag `v1.1.0`) | cisp WP-2 (may proceed without), ansp WP-8, authority WP-6/WP-12/WP-14, ussp WP-6, authority WP-10 |
| C2 | **Core consumer hooks for the lab**: nothing new; the `RunOwned` adapters are per repo. (Listed to say it was checked.) | — | — | — |
| D1 | **Deployment repo `uspace-deploy` (private)**: the shared Caddyfile (five hosts; `client_auth verify_if_given` with the mTLS CA on the CISP and ANSP hosts; `X-Client-Cert-Subject` forwarding and stripping; `/basemap/*` `file_server`; public cache on the CISP `/public/*`; rate limits; `/metrics` never routed), the per-system compose includes (one `timescaledb-ha` container per system, M37), the DSS include from L2, the shared basemap volume, env files from `.env.example`s, `deploy.sh` that verifies cosign signatures and pulls by digest, nightly backups and the restore check, the mTLS CA tooling for staging, and the cutover runbook for A-M5. Each system's `deploy/` keeps its own compose and a Caddy *snippet*; this repo composes them. | owner (private repo); agents may prepare it as a PR | **deploy WP-D1 `droplet-compose`** then **WP-D2 `cutover`** | first staging deploy of any system (cisp C-M1 demo); L-M1 |
| D2 | **Droplet sizing** (see §2.1): decide before L-M1. | owner | — | L-M1, L-M2 |
| A1 | **`certificates.code`** at the authority (M8) and the scope catalogue (M23): additive edits to authority WP-16 and WP-2; listed here because two other repos depend on them. | uspace-authority | inside WP-2 and WP-16 (no new WP) | ussp WP-7 (authorisation number), ansp WP-10 |
| U1 | **Kit `0.1.0-rc` publish** (M32) and the `RestrictionLayer` move to 0.1.0 (M33). | uspace-ui | **ui WP-13a `rc-publish`** (split from WP-13: publish to npm as soon as WP-0..WP-5 merge; WP-13 keeps the example app and the final `0.1.0`) | cisp WP-9 |

---

## 4. Cross-repo build order and parallelism

Principle: never block an agent on an unfinished contract. Every
cross-repo contract is either (a) already fixed by spec `02` + the
decisions above, (b) a standard (F3411, F3548, ED-318), or (c) a schema
the lab's `schemas/common/` publishes on day 1. Each repo tests against
fakes of its siblings (authority `internal/ltest`, ussp
`internal/testfakes`, ansp httptest stubs, cisp `test/e2e/subscriber`);
the real integration happens in the lab's L-M1. The authority's token
service is replaced by the lab issuer (L2) until A-M4, so nobody waits
for authority WP-2.

### 4.1 First parallel batch (start now, 11 agents)

| # | Repo | WP | Why it can start today |
|---|---|---|---|
| 1 | uspace-lab | WP-L1 `contracts-aggregate` (common schemas first) | depends only on this document; unblocks M14/M29 for everyone |
| 2 | uspace-core | WP-14 `v1.1-additive` (JWS helpers, `BasisProvider`, `SkipConflicts`, `geodesy/cell`) | additive to v1.0.0; no vector change |
| 3 | uspace-ui | WP-0 `scaffold` (then WP-1..WP-5 fan out the next day) | no dependency |
| 4 | uspace-cisp | WP-0 `scaffold` | no dependency; its OpenAPI skeleton is what the authority and ANSP copy |
| 5 | uspace-authority | WP-0 `scaffold` | no dependency |
| 6 | uspace-ussp | WP-0 `scaffold` | no dependency |
| 7 | uspace-ansp | WP-0 `scaffold` | no dependency |
| 8 | uspace-lab | WP-L2 `dss-compose` (DSS + lab issuer) | pinned images; needed by every standards WP |
| 9 | uspace-lab | WP-L5 `sitl-and-simulators` | contracts F5/F9 are fixed in `02`; long-running, start early |
| 10 | uspace-lab | WP-L3 `basemap-bundle` | independent; big download, start early |
| 11 | uspace-deploy | WP-D1 `droplet-compose` (Caddy, mTLS CA tooling, volumes) | the Caddy rules of M25/M38 are decided; compose includes are filled as images appear |

Apply the §5 plan edits in the same first day (one small PR per repo on
`plan/initial`, or as the first commit of each WP-0) so every later agent
reads the reconciled plan.

### 4.2 Waves after the scaffolds (parallelism per wave)

```
wave 1 (day 2-3, ~16 agents)
  ui:        WP-1 theme-ui   WP-2 i18n-fonts   WP-3 map-core   WP-4 api-adapter   WP-5 bff-auth
  cisp:      WP-1 store-versions   WP-2 auth-jws
  authority: WP-1 store-audit-policy   WP-9 tsdb-writer   WP-10 bus-sources-cells   WP-11 ground
  ussp:      WP-1 store-migrations   WP-3 standards-codegen   WP-20 deploy-staging
  ansp:      WP-1 store-migrations   WP-2 auth-accounts   WP-3 openapi-contract   WP-4 manned-adapter
  lab:       WP-L4 spec-errata

wave 2 (~18 agents)
  ui:        WP-6 zones   WP-7 tracks   WP-8 live-status   WP-9 table   WP-10 form   WP-13a rc-publish
  cisp:      WP-3 publications   WP-4 read-api   WP-5 restrictions   WP-6 subscriptions-deliver   WP-8 console-api
  authority: WP-2 auth-tokens   WP-3 registry   WP-5 zones   WP-7 rid-receivers-ingest   WP-25 scenario-harness
  ussp:      WP-2 auth-accounts   WP-4 cis-cache   WP-5 registry-validity   WP-6 bus-cell-tsdb
  ansp:      WP-5 restrictions   WP-6 manned-feed   WP-7 cis-projection

wave 3 (~17 agents)                                   -> C-M1 + U-M1 (kit 0.1.0), A-M1, S-M0
  ui:        WP-11 alerts   WP-12 traffic-layers   WP-13 release-0.1
  cisp:      WP-7 stream-status   WP-9 web-scaffold   WP-12 ed269-bridge
  authority: WP-6 cisp-client   WP-8 rid-pipeline   WP-15 manned-ingest   WP-16 certificates   WP-18 occurrences
  ussp:      WP-7 intent-authorisation   WP-8 telemetry-ingest
  ansp:      WP-8 outbox-cisp   WP-9 dss-constraints   WP-10 coordination-inbox   WP-11 console-restrictions

wave 4 (~15 agents)                                   -> C-M2, A-M2, S-M1, N-M1
  cisp:      WP-10 web-public-map   WP-11 web-console
  authority: WP-12 detect-violations   WP-13 picture-ws   WP-14 display-provider   WP-20 registry-import-portal
  ussp:      WP-9 rid-sp   WP-10 conformance   WP-11 traffic-cpa   WP-12 geo-awareness   WP-17 web-portal (intents pages)
  ansp:      WP-12 console-picture-inbox   WP-13 deploy-proof (N-M1 runbook)
  ui:        WP-14 v1-gate (starts; finishes after two consoles are live)

wave 5 (~12 agents)                                   -> C-M3 (cisp v1.0.0), A-M3/A-M4, S-M2/S-M3/S-M4, N-M2
  cisp:      WP-13 hardening-release
  authority: WP-17 incidents-evidence   WP-19 police-realm   WP-21 web-foundation   WP-26 (gated on Q2)   WP-27 retention-archive
  ussp:      WP-13 dss-sync   WP-14 peers-manned   WP-15 coordination-records-occurrences   WP-16 weather
  ansp:      WP-13 deploy-proof (N-M2 runbook, N-M3)
  deploy:    WP-D2 cutover (prepare)

wave 6                                                -> A-M5, S-M5, S-M6, L-M1..L-M4
  authority: WP-22 web-registry-zones-certs   WP-23 web-oversight   WP-24 deploy-staging
  ussp:      WP-17 (traffic pages)   WP-18 web-console   WP-19 conformance-suite-hardening   WP-20 (refresh)
  lab:       L-M1 scenario suite, L-M2 load, L-M3 chaos, L-M4 conformance suite (new lab WPs, written after wave 4)
```

### 4.3 Critical paths and the rules that keep them short

- **Demo 1 (C-M1 + U-M1)**: ui WP-0 → WP-3 → WP-6 → WP-13a (rc) ∥ cisp WP-0 → WP-1 → WP-3 → WP-4 → WP-10. The kit's rc publish is the external leg; nothing else gates it.
- **Demo 2 (A-M1)**: authority WP-0 → WP-1 → WP-3/WP-5 → WP-6 (needs the CISP's OpenAPI skeleton from cisp WP-0, copied with `SOURCE`).
- **Demo 3 (S-M1)**: ussp WP-0 → WP-1 → WP-2/4/5/6 → WP-7; against fakes; needs no sibling.
- **Demo 4 (N-M1)**: ansp WP-0 → WP-1 → WP-5 → WP-8 → WP-9 → WP-13; needs lab L2 (DSS) and a published designation (authority WP-5 or the lab's authority-role client against the CISP).
- **The safety-relevant WPs** (ussp WP-10, WP-11, WP-12, WP-13; authority WP-12; ansp WP-5/6/8) are done only with a lab scenario (INV-02), so lab WP-L5 must be merged before wave 4 starts.
- Do not let the authority's WP-2 gate anybody: every repo verifies against any allow-listed issuer, and the lab issuer (L2) is one.
- Across repos the only shared files are the sibling OpenAPI copies (`api/clients/*.yaml` + `SOURCE`): bump them in a `build:` commit, never inside a feature PR.

---

## 5. Per-plan edits

Each row: file and section, the edit, the mismatch or decision it applies.

### 5.1 uspace-cisp (`plan-wt/uspace-cisp`)

| File, section | Edit | Applies |
|---|---|---|
| `docs/PLAN.md` §1.3 D11; §5.3; §15 Q21 | Version tables `goose_db_version_relational` / `goose_db_version_timeseries` (not `goose_db_version`). | M36 |
| `docs/PLAN.md` §6 (conventions paragraph); `docs/WORKPACKAGES/WP-0.md` (problem schema) | Error body: `errors: [{field, reason}]` + `truncated` replaces `problems[]`; `type` = `https://schemas.uspace.ge/problems/<slug>`. | M28 |
| `docs/PLAN.md` §6.1 heartbeat row; §15 Q3; `WP-3.md` | Body `{sent_at, active_refs?: []}`, interval 15 s, stale 60 s; "any `cis.publish:*` scope" stays. | M3 |
| `docs/PLAN.md` §6.2; `WP-5.md` | State that `(ansp_ref, ansp_version)` is the idempotency key and `Idempotency-Key` is ignored; identifier rule = "≤ 7 chars, unique across datasets" (drop the `D`+6 scheme from Q17; note the ANSP mints `DAR`+4). | M4, M10 |
| `docs/PLAN.md` §6.3; `WP-4.md` | Add `?applies_at=` (annotate `extendedProperties.cis_applicability` ∈ `applies` / `not_applicable` / `unknown`, no filtering) beside `?at=`. | M17 |
| `docs/PLAN.md` §6.5; §15 Q9; `WP-6.md` | Webhook `aud` = host of `callback_url` (not the client id). | M19 |
| `docs/PLAN.md` §6.5 (`WS /v1/stream`); `WP-7.md` | Frames carry the common envelope; `{type: heartbeat}` becomes `console/status/v1` (with `datasets{}`, `nats`), `{type: change}` a `cis/change/v1` body, `resync` a `console/status/v1` with `resync_since`. | M29 |
| `docs/PLAN.md` §6.6; §8.2; `WP-8.md` | Session JWT: `aud` = own host, `scope = "session"`, `roles: [role]`, `realm: "console"`, `jti`; cookies `uspace_session` / `uspace_csrf`. Drop `scope = console:<role>`. | M20, M21 |
| `docs/PLAN.md` §8.2; §15 Q7; `WP-2.md` config block | `CISP_AUDIENCES` (list of hosts) replaces `CISP_AUDIENCE=cisp`; client ids `authority-01`, `ansp-01`. | M18, M24 |
| `docs/PLAN.md` §8.3; §15 Q11; `WP-2.md` | Rename `CISP_MTLS_MODE` values to `required | off`; Caddy `client_auth verify_if_given`; the snippet moves to the deployment repo (the repo keeps a copy under `deploy/caddy/` for reference). | M25 |
| `docs/PLAN.md` §15 Q14; `WP-9.md` | npmjs only; start on `0.1.0-rc`; remove the `github:` tag option. | M32 |
| `docs/PLAN.md` §4 web list; `WP-9.md` | `pnpm` + `pnpm-lock.yaml`, `--frozen-lockfile` (not `npm ci`). | M34 |
| `docs/PLAN.md` §11 | Record that on the droplet one `timescaledb-ha` container holds both databases (already so) and that the Caddyfile is composed by `uspace-deploy`. | M37, D1 |
| `docs/PLAN.md` §15 Q1, Q5, Q6, Q8, Q10, Q12, Q13, Q17, Q18, Q19, Q20, Q22, Q23, Q24 | Mark "decided" with the §2 answers (Q8: add that the ANSP's publication key is its own JWKS; Q20: core WP-14 ships the helpers, switch in a follow-up). | §2 |
| `WP-6.md` | Delivery reasons `subscription_test`/`republished` documented as "receivers acknowledge without pulling". | M16 |
| `CLAUDE.md` "Cross-system contracts" | Add the audience rule (host), the error body, the envelope on every WS frame, the heartbeat endpoint. | M18, M28, M29 |

### 5.2 uspace-ui (`plan-wt/uspace-ui`)

| File, section | Edit | Applies |
|---|---|---|
| `docs/PLAN.md` §3.1 `model` | `IdentBasis` gains `"provider"`; `Problem` gains `truncated?: boolean`; session display type `{sub, roles: string[], realm, exp}`. | Q-A8, M28, M20 |
| `docs/PLAN.md` §3.16; §6.3 contract table; `WP-5.md`; `WP-8.md` | Remove `wsTicket` and `/_bff/ws-ticket`; `useFeed` connects same-origin with the cookie; `4401` = re-login; `bffHandlers` has three routes. | M22 |
| `docs/PLAN.md` §3.16 `sessionClaimsUnverified`; §14 Q9 | Reads `sub`, `exp`, `roles[]`, `realm`. | M20 |
| `docs/PLAN.md` §6.3 console frame | Mark "adopted by all four systems"; add `datasets{}`, `projection_age_s`, `cis_age_s`, `nats`, `resync_since` as optional extras of `console/status/v1`; note the schemas live in `uspace-lab/schemas/common/`. | M29, M14 |
| `docs/PLAN.md` §12 | `RestrictionLayer` listed under `v0.1.0` (not 0.3.0). | M33 |
| `docs/PLAN.md` §13 WP table and waves | Add `WP-13a rc-publish` (publish `0.1.0-rc.N` to npm after WP-0..WP-5; `release.yml` first run); WP-13 keeps the example app and `0.1.0`. | M32, U1 |
| `docs/PLAN.md` §14 Q1, Q2, Q3, Q4, Q5, Q6, Q8, Q9, Q10, Q16 | Mark decided per §2 (Q4: lab builds, deploy repo serves; Georgia extract with a size budget). | §2 |
| `docs/PLAN.md` §11 consumption | Add: every `web/` sets the kit's CSP; serves fonts via `next/font/local`; basemap at `/basemap/` served by the deployment's Caddy. | M38 |

### 5.3 uspace-authority (`plan-wt/uspace-authority`)

| File, section | Edit | Applies |
|---|---|---|
| `docs/PLAN.md` §1.2 D3; §3 `internal/cell`; §14 Q-A3 | `internal/cell` becomes a thin wrapper over core `geodesy/cell` once core v1.1.0 ships (same 0.1°/1° grid; names `c5:`/`c3:`). | M35 |
| `docs/PLAN.md` §1.2 D7; §4 (version tables); §13 | Version tables `goose_db_version_relational` / `_timeseries`; `api` no longer migrates at start: a `migrate` subcommand and a one-shot compose service; processes refuse to start on a lower version. | M36 |
| `docs/PLAN.md` §5 (conventions line) | Error body `errors: [{field, reason}]` + `truncated`; `type` slug URIs. | M28 |
| `docs/PLAN.md` §5 token-service row; §7 machine-clients row; `WP-2.md` | `aud` = target host; `audience`/`resource` parameter; allowed-audience list in hosts for national scopes, unrestricted for `utm.*`/`rid.*`; `*_AUDIENCES` list on the verifier (own host + lab alias) replaces `Audience = "authority"`; client ids `authority-01`, `cisp-01`, `ansp-01`, `ussp-<code>-01`, `lab-01`. Scope catalogue = `06 §3` + `ansp.coordination`, `ansp.requests`, `dp.observe`, reserved `cis.publish:ats_data`; `rid.observe` removed from the JWT catalogue. | M18, M23, M24 |
| `WP-2.md` sessions | Session JWT: `aud` = own host, `scope = "session"`, `roles[]`, `realm`, `jti` (= `sid`); cookie names `uspace_session` / `uspace_csrf` in `docs/runbooks/session-contract.md`. | M20, M21 |
| `docs/PLAN.md` §5 F3411 DP inbound row; `WP-14.md` | ISA notifications come from the SP (`rid.service_provider`, `aud` = this host); DP polls use `aud` = host of `uss_base_url`; DSS audience = the DSS host. | M6, M18 |
| `docs/PLAN.md` §5 CIS subscriber row; `WP-6.md` | Path stays `/v1/cis/notifications`; allow-list the ANSP issuer too (`AUTHORITY_CIS_NOTIFY_ISSUERS`), restrict `pull_url` to the issuer's host; `subscription_test`/`republished`/unknown reasons → `204` no pull; add the 15 s heartbeat job to the CISP; detached JWS in `X-JWS-Signature` per the CISP's Q8; publication payloads validated against the CISP's `cis/uspace_requirements/v1` and `cis/ussp_list/v1` (pinned copy). | M1, M3, M5, M7, M16, M26 |
| `WP-5.md` export | `metadata.issued` / `metadata.provider` (core names), not `creationDateTime`/`updateDateTime`/`originator`; add `?applies_at=` to `GET /v1/zones/export`. | M15, M17 |
| `WP-16.md` | Add `certificates.code` (≤ 8 upper alnum, unique) → `ussp_id` in the USSP list; the list is built to `cis/ussp_list/v1`. | M8, M7 |
| `WP-13.md`; `WP-21.md` | Picture frames adopt the console frame: envelope + `body`; `status` → `console/status/v1` (with `projection_age_s`, `cis_age_s`, `dp_state`), snapshot → `console/snapshot/v1`, `{viewport}` → `console/subscribe/v1`; bodies `track/telemetry/v1`, `violation/v1`; `schemas/picture/` reduced to the authority-specific extras. WS auth = cookie on same-origin upgrade + `Origin`. | M29, M22 |
| `WP-15.md` | Consume the envelope-wrapped F4 frames; `console/status/v1` as the feed status; state frames (`stale`, `source_disabled`) handled. | M12 |
| `WP-18.md` | `occurrence/v1` is **owned** here (not "as consumed"); `reporter.person_ref` arrives in clear and is encrypted at rest. | M14, M13 |
| `docs/PLAN.md` §3 `schemas/` | `track/telemetry/v1` and the picture status/snapshot frames are consumed from `uspace-lab/schemas/common/`, not defined here; `violation/v1`, `occurrence/v1`, `rid/observation/v1` are defined here. | M14 |
| `docs/PLAN.md` §7 ANSP-stream row; §14 Q-A19; `WP-15.md` | `AUTHORITY_MTLS_MODE=required|off`; inbound mTLS subject bound to `oauth_clients.mtls_subject`. | M25 |
| `docs/PLAN.md` §10; `WP-24.md` | One `timescaledb-ha` container with both databases on the droplet; the Caddyfile is composed by `uspace-deploy`; the basemap volume is mounted and served there; `pnpm` for `web/`. | M37, M38, M34, D1 |
| `docs/PLAN.md` §12 (WP-26 gating) and §14 Q-A5..Q-A9, Q-A16 | Mark decided per §2; Q-A8/Q-A9 reference core WP-14. | §2 |

### 5.4 uspace-ussp (`plan-wt/uspace-ussp`)

| File, section | Edit | Applies |
|---|---|---|
| `docs/PLAN.md` §2 D7; §4 `cell/`; §15 Q2 | `internal/cell` wraps core `geodesy/cell` (0.1°/1°, `c5:`/`c3:`) once v1.1.0 ships. | M35 |
| `docs/PLAN.md` §2 D8; §15 Q9; `WP-15.md` | `coordination/annex_v/v1` is **consumed** from the ANSP's published schema (the ANSP owns it); `track/telemetry/v1` is consumed from `uspace-lab/schemas/common/`; this repo owns `telemetry/v1`, `intent/*`, `alert/v1`, `traffic/product/v1`, `intent/state/v1`. | M14 |
| `docs/PLAN.md` §3.1 `api` row; §6.1; `WP-4.md` | `POST /v1/cis/notify` → `POST /v1/cis/notifications`; allow-list the CISP and the ANSP issuers (`USSP_CIS_NOTIFY_ISSUERS`); `pull_url` host check; `subscription_test`/`republished`/unknown → `204`. | M1, M5, M16 |
| `docs/PLAN.md` §6.3; §15 Q10; `WP-15.md` | ANSP call = `POST {ansp}/v1/coordination/notices`. | M2 |
| `docs/PLAN.md` §6 (error paragraph) | `errors: [{field, reason}]` + `truncated`; `conflicts[]` stays on `intent/decision/v1`. | M28 |
| `docs/PLAN.md` §8 first row; §11 env; `WP-2.md` | `USSP_AUDIENCES` (hosts) replaces `aud = USSP_SYSTEM_ID`; `USSP_SYSTEM_ID` = the certificate code; operator tokens `aud` = this host; outgoing `aud` = host of the target (`uss_base_url`, DSS host, CISP host, authority host, ANSP host). | M18, M8 |
| `WP-2.md` sessions | `scope = "session"`, `roles[]`, `realm` (`portal` for operators, `console` for staff); cookies `uspace_session` / `uspace_csrf`. | M20, M21 |
| `WP-9.md` | ISA subscriber notifications carry `aud` = host of each subscriber's `uss_base_url`. | M6, M18 |
| `WP-11.md`; `WP-17.md`; `WP-18.md` | `traffic-ws` frames = envelope + `body` (`traffic/product/v1`, `alert/v1`); `console/status/v1` every 2 s with `degraded[]`, `dropped_frames`, thresholds; `console/subscribe/v1` for the staff bbox mode; cookie on same-origin upgrade for staff. | M29, M22 |
| `WP-14.md` | Consume envelope-wrapped F4 frames and `console/status/v1`; `USSP_MTLS_MODE`. | M12, M25 |
| `WP-15.md` | `reporter.person_ref` sent in clear over TLS (no authority public key). | M13 |
| `docs/PLAN.md` §2 D5; §5.3 | Version tables `goose_db_version_relational` / `_timeseries` (already); migrations by a `migrate` subcommand + one-shot compose service, not at process start. | M36 |
| `docs/PLAN.md` §11; `WP-20.md` | One `timescaledb-ha` container with both databases; Caddy snippet consumed by `uspace-deploy`; basemap volume; `pnpm`. | M37, M38, M34, D1 |
| `docs/PLAN.md` §15 Q1, Q3, Q4, Q5, Q7, Q8, Q12, Q15, Q16 | Mark decided per §2. | §2 |
| `docs/PLAN.md` §13 WP-17 | Pin kit ≥ 0.2 for the intents pages, ≥ 0.3 for traffic pages. | M33 |
| `CLAUDE.md` | Add the audience rule, the error body, the envelope on every WS frame. | M18, M28, M29 |

### 5.5 uspace-ansp (`plan-wt/uspace-ansp`)

| File, section | Edit | Applies |
|---|---|---|
| `docs/PLAN.md` §6 (`POST /v1/coordination/annex-v`); `WP-10.md` | Rename to `POST /v1/coordination/notices`. | M2 |
| `docs/PLAN.md` §6 (`POST /v1/cis/webhook`); §6 outbound degraded row; §15 gap 10; `WP-7.md`; `WP-8.md` | Receiver path `POST /v1/cis/notifications`; degraded target `{base_url}/v1/cis/notifications`; drop the per-target path config. | M1, M5 |
| `docs/PLAN.md` §6 outbound heartbeat row; §15 gap 9; `WP-8.md` | `POST {cisp}/v1/publishers/heartbeat {sent_at, active_refs}` every 15 s (`cisp_heartbeat_s` default 15); drop `ANSP_CISP_HEARTBEAT_PATH`. | M3 |
| `WP-8.md` CISP publisher | Body field `ansp_version` (not `version`), per `cis/restriction/v1`; detached JWS in `X-JWS-Signature` per the CISP's Q8; the idempotency key is `(ansp_ref, ansp_version)` in the body, the header is optional; test assertions adjusted. | M4, M26 |
| `docs/PLAN.md` §6 (error paragraph); `WP-3.md`; `WP-5.md` | `errors: [{field, reason}]` + `truncated`; `type` slug URIs. | M28 |
| `docs/PLAN.md` §15 gap 13; §5.1 `ansp_policy` | `require_uspace_airspace` removed (always true); the lab designates. | M9 |
| `WP-2.md` | Verifier `ANSP_AUDIENCES` (hosts) replaces `Audience = cfg.SystemID`; session JWT `scope = "session"`, `roles[]`, `realm`; cookie `uspace_session` / `uspace_csrf` (not `ansp_session`); outgoing `aud` = host of the target. | M18, M20, M21 |
| `WP-6.md`; `docs/PLAN.md` §6 F4 rows | F4 frames = envelope + `body` (`track/manned/v1`); `feed/status/v1` → `console/status/v1`; the console stream uses `console/subscribe/v1`; mTLS flag `ANSP_MTLS_MODE=required|off`. | M12, M29, M25 |
| `docs/PLAN.md` §15 gap 8; `WP-6.md`; `WP-8.md` | `ANSP_MTLS_REQUIRED` → `ANSP_MTLS_MODE`. | M25 |
| `WP-10.md` | `occurrence/v1` `reporter.person_ref` sent in clear over TLS (encrypted at rest only). The ANSP **owns** `coordination/annex_v/v1` (`schemas/coordination/annex_v/v1.json` with examples) and the USSP consumes it; remove the `x-pending-schema` wait. | M13, M14 |
| `docs/PLAN.md` §5.3; §11 | `ANSP_MIGRATE_ON_START` removed; `ansp migrate` + one-shot compose service; one `timescaledb-ha` container with both databases on the droplet. | M36, M37 |
| `docs/PLAN.md` §4; §15 gap 18 | Pin `uspace-core v1.0.0`; JWS helpers from core v1.1.0 (WP-8 waits for it; ansp WP-8 is in wave 3, core WP-14 in wave 0). | M27 |
| `docs/PLAN.md` §11; `WP-11.md`; `WP-13.md` | `pnpm`; kit ≥ 0.1 for WP-11 (`RestrictionLayer`), ≥ 0.3 for WP-12 (`MannedLayer`); Caddy snippet consumed by `uspace-deploy`; basemap volume. | M34, M33, M38, D1 |
| `docs/PLAN.md` §15 gaps 1, 2, 4, 5, 6, 7, 11, 14, 15, 16, 19, 20 | Mark decided per §2. | §2 |

### 5.6 Outside the five plans

| Repo | Edit |
|---|---|
| uspace-lab | Create `docs/PLAN.md` with WP-L1..L5 (§3) and, after wave 4, the L-M1..L-M4 WPs; apply WP-L4's errata to `docs/spec/`. |
| uspace-core | `docs/WORKPACKAGES/WP-14.md` (`v1.1-additive`): `auth` JWS helpers + `KeyRing`, `core.BasisProvider`, `alerting.Config.SkipConflicts`, `geodesy/cell`; CHANGELOG under `[Unreleased]`; tag `v1.1.0`. |
| uspace-deploy (new, private) | `docs/PLAN.md` with WP-D1 (`droplet-compose`) and WP-D2 (`cutover`) per §3. |

---

## Appendix A. The reconciled JWT contract (one table for every repo)

| Token | `iss` | `aud` | `sub` | `scope` | Other claims | TTL |
|---|---|---|---|---|---|---|
| Ecosystem machine token | the authority's issuer URL | host of the target's base URL (`uspace-cisp.chikox.net`, DSS host, peer `uss_base_url` host) | client id `<system>-<code>-<nn>` | space-separated catalogue scopes | `exp`, `iat`, `jti`, `kid` | ≤ 1 h |
| Operator machine token (USSP issuer) | the USSP's issuer URL | the USSP's host | operator client id | `ussp.intents ussp.telemetry ussp.traffic ussp.geo` | `exp`, `iat`, `jti`, `kid` | ≤ 1 h |
| Console / portal session | the system's own issuer URL | the system's own host | account id | `session` | `roles: [..]`, `realm` (`console` / `police` / `portal`), `exp`, `iat`, `jti`, `kid` | ≤ 12 h, idle 30 min |
| Webhook / direct-delivery JWS (`application/jose`) | the CISP's or the ANSP's issuer URL | host of the `callback_url` / target base URL | subscription id (CISP) or restriction id (ANSP) | — | payload = `cis/change/v1`, `iat`, `jti` = delivery id | single use |
| Detached publication JWS (`X-JWS-Signature`) | — (key by `kid` from the publisher's JWKS) | — | — | — | protected header `alg RS256`, `kid`, `iat` (≤ 5 min), `b64: false`, `crit: ["b64"]` | — |

Every verifier: `core/auth.Verifier`, RS256 only, allow-listed issuers
with JWKS URLs, `*_AUDIENCES` list (own public host + lab alias), 30 s
skew, `jti` required, scope per endpoint.

## Appendix B. The scope catalogue (authority WP-2 holds it)

`cis.read`, `cis.publish:zones`, `cis.publish:uspace`,
`cis.publish:ussp_list`, `cis.publish:restrictions`,
`cis.publish:ats_data` (reserved), `registry.validate`, `ussp.records`,
`ansp.traffic`, `ansp.coordination`, `ansp.requests`,
`occurrences.write`, `certificates.status`, `police.query`,
`dp.observe` (lab only); F3548 `utm.strategic_coordination`,
`utm.constraint_processing`, `utm.constraint_management`,
`utm.conformance_monitoring_sa`, `utm.availability_arbitration`; F3411
`rid.service_provider`, `rid.display_provider`. At the USSP issuer only:
`ussp.intents`, `ussp.telemetry`, `ussp.traffic`, `ussp.geo`. Not JWT
scopes: receiver keys (`rid.observe` is retired), console roles
(`roles[]`).

## Appendix C. The console frame (owned by `uspace-lab/schemas/common/`)

```
{ "schema": "<family>/<name>/v1", "msg_id": "<ULID>", "producer": "<system>/<process>",
  "ts": "...", "rx_ts": "...", "captured_at": "...", "time_source": "...", "backlog": false,
  "body": { ... } }
```

Server → client: `console/status/v1` (on connect and every 2 s:
`connection_id`, `server_ts`, `policy_version`, `stale_after_s`,
`live_max_age_s`, `dropped_frames`, `degraded[]`, `sources[]`, optional
`datasets{}`, `projection_age_s`, `cis_version`, `cis_age_s`, `nats`,
`resync_since`); `console/snapshot/v1` (`tracks[]`, `alerts[]`,
`manned[]`, `zones_version`); then `track/telemetry/v1`,
`track/manned/v1`, `alert/v1`, `violation/v1`, `cis/change/v1`,
`traffic/product/v1` as bodies. Client → server: `console/subscribe/v1`
`{bbox, layers[]}`. Authentication: the session cookie on a same-origin
upgrade with an `Origin` allow-list, or a bearer for machine clients.

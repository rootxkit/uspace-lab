# 01 — Roles and responsibilities

Article references are to Reg. (EU) 2021/664 unless prefixed (`947` = 2019/947, `945` = 2019/945, `665` = 2021/665, `666` = 2021/666, `376` = 376/2014). Paragraph numbers that could not be re-read against the consolidated text are marked *(para. unverified)*.

## 1. `uspace-authority` — the competent authority

### MUST

| # | Obligation | Source |
|---|---|---|
| A1 | Keep the registration system for UAS operators and certified UAS: registration number, legal identity, contact, status, validity; interoperable and queryable | 947 Art. 14 |
| A2 | Define and publish UAS geographical zones in a common unique digital format | 947 Art. 15(1), 15(3); ED-269 / ED-318 |
| A3 | Designate U-space airspace and its U-space services; decide whether weather and conformance monitoring are mandatory there | Art. 3(1), 3(4) |
| A4 | Publish, through a CISP, the common information of Annex II (U-space airspace limits, applicable services, geo-zones, dynamic restrictions, USSP list, ATS provider coordination terms) | Art. 5(1)–(3), Annex II |
| A5 | Certify and oversee USSPs and the CISP; keep the certificate register | Art. 14–18 |
| A6 | Oversee operators and remote pilots: competency, declarations, specific-category authorisations | 947 Art. 5, 12, 18 |
| A7 | Detect and record violations from every evidence source: direct Remote ID receivers it operates, USSP network identification displays, USSP conformance reports, manned-traffic airprox | 947 Art. 18(a), (h) *(letters unverified)* |
| A8 | Receive, store and analyse occurrence reports, including airprox and infringements, within the 72 h regime; feed the national database | 376 Art. 4, 6, 7 |
| A9 | Answer lawful queries from police and other agencies about an aircraft, operator or flight, with audit | 947 Art. 14 (interoperable registers), national law |
| A10 | Keep an immutable audit of every oversight view, export and change | Art. 17, Art. 18 |

### MUST NOT

- Alert remote pilots or operators in real time, separate traffic, or issue resolution advice. Those are USSP services (Art. 11, 13).
- Command, configure or geofence an aircraft.
- Provide flight authorisation as a service. It approves specific-category operations and sets priorities; the per-flight conflict-free reservation is the USSP's (Art. 10).
- Release operator PII except as 947 Art. 14 and national law allow (registry validity answers are status-only; see `06-security.md`).

### Users and authentication

| User | Auth |
|---|---|
| GCAA inspectors, registry clerks, incident officers, admins | Local accounts, OIDC-ready; MFA mandatory; roles `viewer`, `inspector`, `registrar`, `incident_officer`, `admin` |
| Police / agency users | Separate realm; per-agency accounts; purpose code on every query |
| USSPs, CISP, ANSP (machine) | OAuth2 client credentials issued by the authority's ecosystem token service (`06-security.md`) |
| Remote ID receivers | Per-receiver key: HTTPS bearer plus body HMAC; receiver record owned here |
| Operators | Only through the public portal for registration and through USSPs for flights; never a console account |

### Source of truth

Operators, UAS, remote pilots and competencies; geo-zones and U-space designations (master copy; the CISP publishes them); USSP/CISP certificates; violations and incidents; Remote ID receiver fleet and its raw observations; audit log.

## 2. `uspace-cisp` — Common Information Service Provider

### MUST

| # | Obligation | Source |
|---|---|---|
| C1 | Make Annex II common information available in a common unique digital format, on a non-discriminatory basis, to USSPs, ATS providers, the authority and the public (public subset) | Art. 5(1), 5(4), Annex II; ED-318 |
| C2 | Carry static geo-zones and U-space airspace from the authority, dynamic restrictions from the ANSP, the USSP list from the authority, with the data quality and latency of Annex III | Art. 5, Annex III |
| C3 | Version every publication; make every historical version retrievable (what was published at time T) | Annex III (integrity), audit needs of A10 |
| C4 | Notify subscribers of changes with bounded latency; never require a subscriber to poll to learn of a restriction | Annex III latency *(numbers unverified; design target below)* |
| C5 | Be certified and audited as a CISP | Art. 14–16, Annex VII |

Design target for C4: a dynamic restriction accepted by the CISP is available on pull and pushed to every subscriber within 1 s (p99 ≤ 2 s).

### MUST NOT

- Author zones or restrictions. It publishes what the authority and the ANSP sign; it rejects, it does not edit.
- Hold operator PII, telemetry or flight data. It is airspace information only.
- Be the sole copy: the authority and the ANSP keep their masters.

### Users and authentication

| User | Auth |
|---|---|
| Public (map, downloads) | None; read-only, rate-limited, cacheable |
| Authority (publish zones, U-space, USSP list), ANSP (publish restrictions) | OAuth2 client credentials, scopes `cis.publish:zones`, `cis.publish:uspace`, `cis.publish:ussp_list`, `cis.publish:restrictions` |
| USSPs, authority, ANSP (subscribe) | OAuth2 client credentials, scope `cis.read`; webhook endpoints registered per client |
| CISP operators (console) | Local accounts, roles `viewer`, `publisher_admin`, `admin`; publishers cannot edit content, only manage subscriptions and re-publish |

### Source of truth

The publication log: every version of every published feature, who published it, when, and the subscriber delivery log.

## 3. `uspace-ussp` — U-space Service Provider

### MUST

| # | Obligation | Source |
|---|---|---|
| S1 | Network identification: process remote identification continuously throughout the flight and make it available to authorised users (authority, other USSPs, the public as the state allows): registration number, serial, position, height, track, speed, time, emergency status, operator position | Art. 8, Annex (AMC to Art. 8); F3411 network |
| S2 | Geo-awareness: give operators the applicable constraints from the CIS, including dynamic ones, at planning and in flight | Art. 9 |
| S3 | Flight authorisation: receive requests (Annex IV content), check against geo-zones, dynamic restrictions and other authorisations (own and via DSS), return authorised / rejected with the conflicting item, support activation and modification, priority rules | Art. 10, Annex IV; F3548 |
| S4 | Traffic information: inform the operator of other conspicuous traffic, manned and unmanned, in proximity, with position, time, speed, heading and emergency status, including a CPA-based proximity alert | Art. 11; 666 for manned e-conspicuity |
| S5 | Conformance monitoring (mandatory where the authority says so): detect deviation from the authorisation volumes and times, alert the operator, and inform the authority, other USSPs affected and the ATS provider when relevant | Art. 13 |
| S6 | Weather information (optional) | Art. 12 |
| S7 | Exchange data with other USSPs, the CISP, the authority and ATS providers with the protocols, quality and latency required; record operational data | Art. 7(4)–(5) *(para. unverified)*, Annex III, Annex V |
| S8 | Validate that the operator and UAS are registered and the pilot competent before authorising a flight | Art. 6 (operator duties) enforced by the USSP, 947 Art. 14 |
| S9 | Report to the authority: network identification display, service records, occurrences it detects (airprox, non-conformance in restricted zones) | Art. 18, 376 Art. 4 |

### MUST NOT

- Command an aircraft. Alerts go to the operator's client and portal.
- Grant an authorisation that conflicts with a geo-zone, a dynamic restriction or an existing intent; it may escalate specific-category cases to the authority.
- Store or forward operator PII beyond what Annex IV and Art. 8 require (registration number, contact for the active flight).
- Hide traffic: a source that fails is shown as stale or unavailable, never removed silently.

### Users and authentication

| User | Auth |
|---|---|
| Operators (humans) via portal | OIDC accounts owned by the USSP; `ka`/`en`; roles `operator_admin`, `remote_pilot`, `viewer` |
| Operator systems (courier, GCS apps) | OAuth2 client credentials per operator client, scopes `ussp.intents`, `ussp.telemetry`, `ussp.traffic`, `ussp.geo` |
| Other USSPs | Ecosystem tokens (authority-issued) with F3548/F3411 scopes, via DSS discovery |
| Authority | Ecosystem token, scope `rid.display_provider`, `ussp.records` |
| ANSP | Ecosystem token or mTLS for the manned-traffic stream |
| USSP staff (console) | Local accounts, roles `supervisor`, `support`, `admin` |

### Source of truth

Operational intents and authorisation decisions; flight sessions and operator telemetry it received; network identification it served; conformance state and alerts; traffic-information alerts issued; its subscription state in the DSS; service records for Annex III.

## 4. `uspace-ansp` — the ANSP's U-space interface

### MUST

| # | Obligation | Source |
|---|---|---|
| N1 | Dynamic airspace reconfiguration: temporarily limit where UAS may operate inside U-space airspace in controlled airspace, by adjusting lateral and vertical limits; publish via the CISP; inform USSPs | Art. 4, 665 (ATS.OR / ATS.TR amendments to 2017/373) |
| N2 | Provide relevant manned traffic to USSPs (positions of aircraft under its service or seen by its surveillance that enter U-space airspace) | Art. 11(3) *(para. unverified)*, Annex V |
| N3 | Receive from USSPs the operational intents and non-conformance notices that affect it | Annex V |
| N4 | Keep the audit of each reconfiguration: who, why, when, until when | 665 |

### MUST NOT

- Act as an ATM system: no flight-plan processing, no clearances, no radar data processing beyond the position feed it forwards.
- Separate UAS traffic or alert operators directly; that goes through the USSP.
- Edit geo-zones: it issues time-bounded restrictions only, under the authority's framework.

### Users and authentication

| User | Auth |
|---|---|
| ATC supervisors (console) | Local accounts, MFA; roles `watch_supervisor` (activate / end restrictions), `viewer`, `admin` |
| Surveillance feed in (ADS-B, ATM gateway, ASTERIX bridge) | Local adapters; auth per adapter (mTLS or network isolation) |
| CISP, USSPs, authority (machine) | Ecosystem OAuth2 tokens; mTLS available for the manned-traffic stream |

### Source of truth

Dynamic restrictions (master), the manned-traffic feed as handed over (with its own capture times), coordination messages received.

## 5. `uspace-lab`

MUST: run every other system from its images against SITL (ArduPilot, pymavlink), simulate operators, Remote ID receivers, manned traffic, a second USSP and a DSS; hold the language-neutral test vectors; run the load tests of `05-scale-and-reliability.md`; carry the demo. MUST NOT: ship any simulator or test key into another system's image; hold production data. Users: engineers; CI.

## 6. `courier` (operator, external)

MUST (as operator, Art. 6): register, request authorisation before each flight in U-space airspace, keep network identification data flowing (1 Hz) while airborne, act on traffic information and conformance alerts, respect geo-awareness. It integrates as one USSP client with the public operator API only; it never touches the authority, CISP, ANSP or DSS directly.

## 7. Responsibility matrix

R = responsible (does it), A = accountable / source of truth, C = consumes, I = informed, — = none.

| Function | authority | cisp | ussp | ansp | lab | operator |
|---|---|---|---|---|---|---|
| Operator / UAS / pilot registry | R, A | — | C (validity) | — | sim data | I (own record) |
| Geo-zones (ED-269 / ED-318) | R, A | R (publish) | C | C | sim | C |
| U-space airspace designation | R, A | R (publish) | C | C | sim | C |
| Dynamic restrictions | I | R (publish) | C | R, A | sim | C |
| USSP / CISP certification, USSP list | R, A | R (publish) | I | I | — | C |
| Flight authorisation (per flight) | escalations only | — | R, A | I (Annex V) | sim operator | R (request) |
| Network identification (serve) | C (display provider) | — | R, A | — | sim | R (feed telemetry) |
| Direct Remote ID reception | R, A | — | optional, non-authoritative | — | sim receiver | — |
| Traffic information and CPA warnings | — | — | R, A | C (feeds manned) | sim | C |
| Conformance monitoring (incl. height vs authorised volume) | I | — | R, A | I | sim | C |
| Geo-awareness and zone incursion warning | — | — | R, A | — | sim | C |
| 120 m open-category violation | R, A | — | — | — | sim | — |
| Zone incursion as violation | R, A | — | I (reports) | — | sim | — |
| Airprox / occurrence recording (376/2014) | R, A | — | R (report) | R (report) | sim | R (report) |
| Manned traffic feed | C | — | C | R, A | sim | — |
| DSS operation | — | hosts (proposed, Q7) | C | — | sim | — |
| Police queries | R, A | — | — | — | — | — |
| Load, SITL, demo | — | — | — | — | R, A | — |

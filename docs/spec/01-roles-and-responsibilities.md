# 01 — Roles and responsibilities

Article references are to Reg. (EU) 2021/664 unless prefixed (`947` = 2019/947, `945` = 2019/945 as amended by 2020/1058, `665` = 2021/665, `666` = 2021/666, `376` = 376/2014). Paragraph numbers were re-read from the published texts; the few that could not be are marked *(unverified)*. Rows marked **NC** are national choices the regulation leaves open (`09 §2`).

Every obligation below is implemented in Go (`00 §6`): the safety judgements (A7, S1, S2, S3, S4, S5, N2) in the shared packages pinned by `knowledge/vectors/`, run by the hot-path processes; records, workflow, accounts and certificates (A1, A5, A6, A8–A10, C1–C6, S8, S9, S11, N1, N3, N4) by each system's `api` process. Every console and portal is Next.js and only renders.

## 1. `uspace-authority` — the competent authority

### MUST

| # | Obligation | Source |
|---|---|---|
| A1 | Keep the registration systems for UAS operators and for UAS whose design is subject to certification, with the fields of 947 Art. 14(2) (name and date of birth or legal name and identification number, address, email, telephone, insurance policy number, the competency confirmation of legal persons, authorisations / LUCs / declarations held) and 14(3) (manufacturer, designation, serial, owner); digital and interoperable (14(4)); one unique digital registration number per operator and per registered UA (14(6)) | 947 Art. 14, 18(m) |
| A2 | Define and publish UAS geographical zones, including their period of validity, in a common unique digital format | 947 Art. 15(1), 15(3), 18(f); ED-318 |
| A3 | Designate U-space airspace on an airspace risk assessment; require the mandatory services and decide whether weather and conformance monitoring are also required; determine per airspace the UAS capability and performance requirements, the service performance requirements (including update frequencies and latencies) and the operational conditions and airspace constraints; coordinate the designation with other authorities | Art. 3(1)–(4), Annex I; Art. 18(f), (j) |
| A4 | Make available, through the CISP, the common information of Art. 5(1)–(2): U-space airspace limits, the Art. 3(4) requirements, the USSP list with contact, services and certification limitations, adjacent U-space airspaces, relevant geo-zones, static and dynamic restrictions, the ATS operational data of ATS.OR.127 and the ATS.TR.237 reconfigurations; publish the airspace through the AIS as well | Art. 3(6), 5(1)–(2), Annex II; 666 SERA.6005(d) |
| A5 | Certify USSPs and the single CISP (Annex VI–VII), keep the public register of certified providers, run risk-based oversight, record start / cease / restart notices, lapse a certificate not used within 6 months or idle for 12 | Art. 5(7), 7(6), 14–16, 18(a), (e), (g), (i) |
| A6 | Oversee operators and remote pilots: competency, declarations, specific-category authorisations | 947 Art. 5, 8, 12, 18(c)–(e), (h)–(j) |
| A7 | Detect and examine non-compliance from its own evidence sources: direct Remote ID receivers it operates, its F3411 Display Provider view of USSP network identification, USSP conformance notices, manned-traffic airprox | 947 Art. 18(a), (k) |
| A8 | Receive, store and analyse occurrence reports (mandatory within 72 h of awareness, and voluntary) in an independent mechanism, in a national database that is ECCAIRS/ADREP-compatible and holds no personal details; use them for safety only, never to attribute blame | 376 Art. 4(7)–(8), 5, 6(3), 6(6), 7(4), 15(2), 16(3), 16(6)–(7); 947 Art. 19(2) |
| A9 | Answer lawful queries from police and other agencies about an aircraft, operator or flight, with audit; the access level per user class is the authority's determination | Art. 18(b)–(c); national law **NC** |
| A10 | Keep an immutable audit of every oversight view, export and change | Art. 17(2), 18(g), (i) |
| A11 | Determine the traffic data, live or recorded, that USSPs, the CISP and ATS providers make available to authorised persons, with frequency and quality; determine the network identification and traffic information update frequency (1 Hz here) | Art. 8(3), 11(3)(b), 18(b) **NC** |
| A12 | Give USSPs access to the registration system data they need | Art. 3(5)(a) |

### MUST NOT

- Alert remote pilots or operators in real time, separate traffic, or issue resolution advice. Those are USSP services (Art. 11, 13).
- Command, configure or geofence an aircraft.
- Provide flight authorisation as a service. It approves specific-category operations (947 Art. 12) and sets the priority framework; the per-flight conflict-free authorisation is the USSP's (Art. 10).
- Release operator PII except as 947 Art. 14 and national law allow (registry validity answers are status-only; see `06-security.md`).
- Turn an occurrence report into a violation or use it against the reporter or the persons named in it (376 Art. 16(6)–(7)). Violations rest on the authority's own evidence.
- Keep Display Provider data beyond the F3411 limit: its F3411 DP cache is disposed of within 24 h (`NetDpMaxDataRetentionPeriod`); recorded traffic data for oversight is obtained from USSP records under Art. 18(b) and from its own receivers.

### Users and authentication

| User | Auth |
|---|---|
| GCAA inspectors, registry clerks, occurrence officers, admins | Local accounts, OIDC-ready; MFA mandatory; roles `viewer`, `inspector`, `registrar`, `incident_officer` (the independent persons of 376 Art. 6(3); sees occurrence reports), `admin`. `inspector` does not see occurrence reporter identities. |
| Police / agency users | Separate realm; per-agency accounts; purpose code on every query |
| USSPs, CISP, ANSP (machine) | OAuth2 client credentials issued by the authority's ecosystem token service (`06-security.md`); the issuer's identity is not fixed by any standard — **NC** |
| Remote ID receivers | Per-receiver key: HTTPS bearer plus body HMAC; receiver record owned here |
| Operators | Only through the public portal for registration and through USSPs for flights; never a console account |

### Source of truth

Operators, UAS, remote pilots and competencies; geo-zones and U-space designations with their Art. 3(4) requirements (master copy; the CISP publishes them); USSP/CISP certificates and operating status; violations; occurrence reports (segregated); Remote ID receiver fleet and its raw observations; audit log.

## 2. `uspace-cisp` — single Common Information Service Provider

### MUST

| # | Obligation | Source |
|---|---|---|
| C1 | Make the common information of Art. 5(1)–(3) available online through common open, secure, scalable technologies, on a non-discriminatory basis and with the same quality, latency and protection for authorities, ATS providers, USSPs and UAS operators; a public subset for everyone else | Art. 5(4)–(6), Annex II(1)–(2); ED-318 |
| C2 | Carry: U-space airspace limits and the Art. 3(4) requirements, adjacent U-space airspaces, relevant geo-zones, static and dynamic restrictions, the USSP list (identification, contact, services, certification limitations) and each USSP's terms and conditions (all from the authority); ATS.OR.127 operational data and ATS.TR.237 reconfigurations (from the ANSP) | Art. 5(1)–(3) |
| C3 | Data quality: verification and validation on receipt, metadata collected and preserved, authenticated transfer (signed publications), error reporting and corrective action; version every publication and keep every historical version retrievable | Annex III A(1)–(5) |
| C4 | Data protection: encryption in transit and at rest, protection of the open protocol against unauthorised electronic interaction, security risk assessment, storage-location rules, insider-risk awareness, threat detection | Annex III B(1)–(6) |
| C5 | Notify subscribers of changes within the latency the authority sets for the airspace; never require a subscriber to poll to learn of a restriction | Art. 3(4)(b), 9(2); ATS.TR.237(b) ("timely and effective") **NC** for the number |
| C6 | Be certified as the single CISP and audited under the oversight programme | Art. 5(7), 14–16, 18(g), Annex VII |

Design target for C5 (national figure, Annex III sets none): a dynamic restriction accepted by the CISP is available on pull and pushed to every subscriber within 1 s (p99 ≤ 2 s).

### MUST NOT

- Author zones or restrictions. It publishes what the authority and the ANSP sign; it rejects, it does not edit.
- Hold operator PII, telemetry or flight data. It is airspace information only.
- Be the sole copy: the authority and the ANSP keep their masters.

### Users and authentication

| User | Auth |
|---|---|
| Public (map, downloads) | None; read-only, rate-limited, cacheable |
| Authority (publish zones, U-space airspace with requirements, USSP list and terms), ANSP (publish restrictions and ATS operational data) | OAuth2 client credentials, scopes `cis.publish:zones`, `cis.publish:uspace`, `cis.publish:ussp_list`, `cis.publish:restrictions` |
| USSPs, authority, ANSP (subscribe) | OAuth2 client credentials, scope `cis.read`; webhook endpoints registered per client |
| CISP operators (console) | Local accounts, roles `viewer`, `publisher_admin`, `admin`; publishers cannot edit content, only manage subscriptions and re-publish |

### Source of truth

The publication log: every version of every published feature, who published it, when, and the subscriber delivery log.

## 3. `uspace-ussp` — U-space Service Provider

### MUST

| # | Obligation | Source |
|---|---|---|
| S1 | Network identification: process remote identification continuously throughout the flight and provide it, aggregated, to the authorised users of Art. 8(4): the public (public subset — **NC**), other USSPs, the ATS providers concerned, the single CISP, the authority. Message content per Art. 8(2): operator registration number, UA or add-on serial, position with altitude AMSL and height above surface or take-off point, course from true north and ground speed, remote pilot position or take-off point, emergency status, time of message generation. Update rate: as the authority determines (1 Hz, A11) | Art. 8(1)–(4); F3411-22a Net-RID Service Provider |
| S2 | Geo-awareness: give operators the operational conditions and airspace constraints of the U-space airspace, the relevant geo-zones and the temporary restrictions, in time for contingencies and emergencies, each with its time of update and a version number or valid time | Art. 9(1)–(2) |
| S3 | Flight authorisation: check completeness and correctness against Annex IV; accept only when free of intersection in space and time with other notified authorisations in the same U-space airspace (own and via the DSS) under the priority rules; check against U-space airspace restrictions and temporary limitations; notify acceptance or rejection and, on acceptance, the deviation thresholds; may propose an alternative; confirm activation without unjustified delay; resolve conflicts with other USSPs' requests; priority to special operations of SERA Art. 4, then first come first served; re-check standing authorisations against new dynamic restrictions and manned traffic in emergency and update or withdraw them; issue a unique authorisation number identifying the flight, the operator and the USSP; use weather information where applicable | Art. 6(4)–(7), 10(1)–(11), Annex IV; F3548-21 |
| S4 | Traffic information: inform the operator of any other conspicuous traffic near its position or intended route, manned and unmanned, from other USSPs, ATS units and the e-conspicuity of SERA.6005(c) received directly where the airspace is outside ATC service; with position, time of report, speed, heading and emergency status when known; update rate as the authority determines (1 Hz); a CPA-based proximity alert is an addition the regulation does not require (**NC**) | Art. 11(1)–(3); 666 SERA.6005(c) |
| S5 | Conformance monitoring (where the authority requires it, assumed here): verify compliance with Art. 6(1) and the authorisation; alert the operator when the deviation thresholds or the Art. 6(1) requirements are violated; on a detected deviation alert the other UAS operators in the vicinity, the other USSPs in the same airspace and the relevant ATS units, who acknowledge. Informing the authority is a national addition (**NC**) | Art. 13(1)–(2) |
| S6 | Weather information (optional): from trusted sources, before and during the flight, with at least wind (direction, m/s, gusts), lowest broken/overcast layer in hundreds of ft AGL, visibility, temperature and dew point, convective and precipitation indicators, observation or forecast location and time, QNH with its area | Art. 12(1)–(3) |
| S7 | Establish arrangements with ATS providers for coordination and Annex V data exchange; handle traffic data without discrimination; exchange safety-relevant information with other USSPs over a common secure interoperable open protocol (F3548 / F3411 here, **NC**), with Annex III quality and protection; retain recorded operational information and data for at least 30 days, longer while pertinent to an investigation | Art. 7(3)–(5), 15(1)(g), Annex III, Annex V |
| S8 | Validate that the operator and UAS are registered and the pilot competent before authorising a flight, through the registry access of Art. 3(5) | Art. 3(5), 6(3); 947 Art. 14, UAS.OPEN.050(4) |
| S9 | Report to the authority: start, cease and restart of operations; occurrences per ATM/ANS.OR.A.065 and 376/2014 (within 72 h); service records as the authority determines under Art. 18(b) | Art. 7(6), 15(1)(d), 18(b); 376 Art. 4(8) |
| S10 | Serve the F3411 Net-RID Service Provider interface (ISA in the DSS, `/uss/flights`, `/uss/flights/{id}/details`) to any authorised Display Provider including the authority, within the F3411 response times; serve the F3548 USS interface to peers and the DSS | F3411-22a; F3548-21 |
| S11 | Hold an emergency management plan to assist an operator in emergency and a communication plan; the console's emergency workflow implements it | Art. 15(2) |

### MUST NOT

- Command an aircraft. Alerts go to the operator's client and portal.
- Grant an authorisation that conflicts with a geo-zone, a dynamic restriction or an existing intent; it may escalate specific-category cases to the authority.
- Store or forward operator PII beyond what Annex IV and Art. 8 require (registration number, contact for the active flight, remote pilot position).
- Hide traffic: a source that fails is shown as stale or unavailable, never removed silently.
- Require a flight authorisation for operations outside the scope of 2021/664 (A1 with C0 or privately built < 250 g UA, Art. 1(3)); it may accept them voluntarily.
- Keep peer data (other USSPs' intents, flights) longer than 24 h (F3548 `ExternalDataMaxRetentionTimeHours`), except inside its own ≥ 30-day service record where the peer data was part of a decision.

### Users and authentication

| User | Auth |
|---|---|
| Operators (humans) via portal | OIDC accounts owned by the USSP; `ka`/`en`; roles `operator_admin`, `remote_pilot`, `viewer` |
| Operator systems (courier, GCS apps) | OAuth2 client credentials per operator client, scopes `ussp.intents`, `ussp.telemetry`, `ussp.traffic`, `ussp.geo` |
| Other USSPs | Ecosystem tokens (authority-issued) with F3548/F3411 scopes, via DSS discovery |
| Authority | Ecosystem token, scope `rid.display_provider` (standard F3411 DP), `ussp.records`, `utm.availability_arbitration` (F3548 USS availability) |
| ANSP | Ecosystem token or mTLS for the manned-traffic stream; `utm.constraint_management` for its DSS constraints |
| USSP staff (console) | Local accounts, roles `supervisor`, `support`, `admin` |

### Source of truth

Operational intents and authorisation decisions (authorisation number, deviation thresholds); flight sessions and operator telemetry it received; network identification it served; conformance state and alerts; traffic-information alerts issued; its subscription state in the DSS; service records for Art. 15(1)(g) and Annex III.

## 4. `uspace-ansp` — the ANSP's U-space interface

### MUST

| # | Obligation | Source |
|---|---|---|
| N1 | Dynamic airspace reconfiguration (ATC units): temporarily limit the area inside designated U-space airspace where UAS may operate, by adjusting its lateral and vertical limits; notify the USSPs and the single CISP of activation, deactivation and temporary limitations in a timely and effective manner | Art. 4, 5(2); 665 ATS.TR.237(a)–(b) |
| N2 | Provide, on a non-discriminatory basis, the manned traffic information needed for the common information services of U-space airspace in the controlled airspace it serves; establish coordination procedures and communication facilities with USSPs and the CISP | 665 ATS.OR.127(a)–(b); Art. 5(2), 11(2), Annex V |
| N3 | Receive from USSPs the operational intents and non-conformance notices that affect it, and acknowledge conformance alerts | Art. 7(3), 13(2), Annex V |
| N4 | Keep the audit of each reconfiguration: who, why, when, until when; exchange under a service level agreement with recognised encryption and an open protocol | Annex V(1), (3)–(4); 2017/373 record-keeping (ATM/ANS.OR.B.030 *(unverified)*) |

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

MUST (as operator, Art. 6): comply with 2019/947 first (registration, competency, authorisations; Art. 6(3)); use UAS meeting the airspace's Art. 3(4)(a) requirements (6(1)(a)); use the required U-space services (6(1)(b)); respect the operational conditions and constraints (6(1)(c)); submit an Annex IV request before each flight (6(4)); request activation and start only after the USSP confirms it (6(5)); fly within the authorisation and its deviation thresholds, accept changes the USSP makes, and request a new authorisation when it cannot comply (6(6)–(7)); hand its contingency measures and procedures to the USSP (6(8)); keep network identification data flowing (1 Hz) while airborne; act on traffic information to avoid collision (Art. 11(4)); report occurrences (947 Art. 19(2), 376 Art. 4). It integrates as one USSP client with the public operator API only; it never touches the authority, CISP, ANSP or DSS directly.

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
| Network identification (serve) | C (F3411 Display Provider via DSS) | — | R, A (F3411 Service Provider) | C (Art. 8(4)(c)) | sim | R (feed telemetry) |
| Direct Remote ID reception | R, A | — | optional, non-authoritative | — | sim receiver | — |
| Traffic information and CPA warnings | — | — | R, A | C (feeds manned) | sim | C |
| Manned e-conspicuity outside ATC (SERA.6005(c)) | — | — | R (receives) | — | sim | — |
| Conformance monitoring (incl. height vs authorised volume and Art. 3(4)(c) constraints) | I (national addition) | — | R, A | I, acknowledges | sim | C; nearby operators I |
| Geo-awareness and zone incursion warning | — | — | R, A | — | sim | C |
| 120 m open-category violation (outside U-space airspace; inside it the authorised volume already caps height) | R, A | — | — | — | sim | — |
| Zone incursion as violation | R, A | — | I (reports) | — | sim | — |
| Occurrence reporting and recording (376/2014) | R, A (intake, national database) | — | R (report, ≤ 72 h) | R (report) | sim | R (report) |
| Manned traffic feed | C | — | C | R, A | sim | — |
| DSS operation | arbitrates USS availability | hosts (national choice, Q7) | C | C (constraints) | sim | — |
| Ecosystem token issuer | R, A (national choice) | C | C | C | sim issuer | — |
| Police queries | R, A | — | — | — | — | — |
| Load, SITL, demo | — | — | — | — | R, A | — |

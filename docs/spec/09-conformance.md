# 09 — Conformance matrix

How the five systems stand against the regulations and the standards they claim to follow. Sources were re-read from the published texts (2021/664, 2021/665, 2021/666, 2019/947, 2019/945 as amended by 2020/1058, 376/2014, the `uas_standards` modules for F3411 v22a, F3548 v21, ED-318 and ED-269) unless a row says *unverified*. Status values: `met` (the spec implements it), `partial` (implemented in part; note says what is missing), `missing` (not implemented — none remain after this audit), `deviation` (spec contradicted the source — none remain; fixed rows are marked `met (fixed)`), `national choice` (the source leaves the point to the member state or to the ecosystem; the spec's choice is labelled), `org` (an organisational duty outside the software, recorded for completeness), `n/a` (not applicable to the system scope).

## 1. Matrix

### 1.1 Regulation (EU) 2021/664

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| Art. 1(2) | Applies in designated U-space airspace to operators, USSPs, CISPs | all | `00 §2` | met | 2019/947 layer works everywhere; 2021/664 layer per designated volume |
| Art. 1(3) | A1 flights with C0 / privately built < 250 g and IFR flights are out of scope | ussp | `01 §3` MUST NOT | met (fixed) | USSP must not require authorisation for them; may accept voluntarily |
| Art. 2(1), (4), (6) | Definitions: U-space airspace, CIS, dynamic reconfiguration | — | `00 §5` | met | |
| Art. 3(1) | Designation on an airspace risk assessment | authority | `01` A3, `03 §1` `risk_assessment_ref` | met | Georgian adoption unverified (Q1) |
| Art. 3(2) | Four mandatory services | ussp | `01` S1–S4 | met | |
| Art. 3(3) | Weather and conformance monitoring per airspace | authority, ussp | `01` A3, S5, S6; `03` `services_required` | met | conformance assumed required (Q2) |
| Art. 3(4), Annex I | Per-airspace UAS, service-performance and operational requirements | authority → cisp | `01` A3; `02 F1`; `03` `uspace_airspaces` | met (fixed) | requirements block added to the designation and the CIS |
| Art. 3(5)(a) | USSPs get access to the registration system | authority | `02 F8` | met | status-only answers; (b) cross-border repository n/a |
| Art. 3(6) | U-space airspace published per 947 Art. 15(3) and via AIS | authority, ansp | `01` A4; `03` `aip_ref` | partial | AIP publication is an AIS process outside the five systems; recorded by reference |
| Art. 3(7) | Cross-border U-space airspace | — | — | n/a | |
| Art. 4 | Dynamic reconfiguration in controlled U-space airspace per ATS.TR.237 | ansp | `01` N1; `02 F2` | met | |
| Art. 5(1)(a)–(f) | CIS content: limits, Art. 3(4) requirements, USSP list with contact / services / limitations, adjacent airspaces, geo-zones, static and dynamic restrictions | authority → cisp | `01` A4, C2; `02 F1`; `03 §2` | met (fixed) | requirements, adjacency and certification limitations were missing |
| Art. 5(2) | ATS.OR.127 operational data and ATS.TR.237 reconfigurations in the CIS | ansp → cisp | `01` C2; `02 F2` | met (fixed) | ATS operational data items added to F2 |
| Art. 5(3) | USSP terms and conditions in the CIS | authority → cisp | `02 F1` `terms_url`; `03` `certificates.terms_url` | met (fixed) | |
| Art. 5(4), Annex II–III | CIS per Annex II with Annex III quality, latency, protection | cisp | `01` C1, C3, C4 | met | Annex III has no numeric latency; figures are Art. 3(4)(b) determinations |
| Art. 5(5) | Non-discriminatory access with equal quality for authorities, ATS, USSPs, operators | cisp | `01` C1; `02 F3` | met | operators read through the public subset or their USSP |
| Art. 5(6)–(7) | Single CISP designation; certified under Chapter V | authority, cisp | `00 §5`; `01` C6 | national choice | state-run CISP; certification record in `certificates` |
| Art. 5(8) | Inform EASA of single-CISP decisions | — | — | n/a | Georgia is not a member state |
| Art. 6(1) | Operators: compliant UAS, use of services, operational conditions | operator; checked by ussp | `01 §6`; S5, S8 | met | |
| Art. 6(2) | Operator as its own USSP | — | `00 §5` | met | would need certification |
| Art. 6(3) | 2019/947 compliance before U-space flight | ussp (validation) | `01` S8; `02 F8` | met | |
| Art. 6(4), Annex IV | Request before each flight with the ten Annex IV items | operator → ussp | `02 F5`; `04 §3.5` | met (fixed) | items 3, 4 (class / type certificate), 6, 7, 8 were missing |
| Art. 6(5), 10(5) | Activation request; confirmation without unjustified delay | ussp | `02 F5` | met | |
| Art. 6(6)–(7) | Fly within authorisation and thresholds; USSP may change it and must inform; new request when unable | ussp | `02 F5`; `04` `intent/decision` `change_reason` | met (fixed) | |
| Art. 6(8) | Contingency measures given to the USSP | operator → ussp | `04 §3.5` `contingency` | met | |
| Art. 7(1)–(2) | USSP certified; provides services in all phases | authority, ussp | `01` A5, S10 | met | |
| Art. 7(3), Annex V | Arrangements with ATS providers; Annex V exchange | ussp, ansp | `01` S7, N3, N4; `02 F4`, `F13` | partial | the SLA (Annex V(1)) is a document between USSP and ANSP — `org`; exchange model, encryption and open protocol met |
| Art. 7(4) | Handle air traffic data without discrimination | ussp | `01` S7 | met | |
| Art. 7(5) | Exchange among USSPs over a common secure interoperable open protocol with Annex III quality | ussp | `02 F6` | national choice | F3548 / F3411 chosen; the regulation names no standard |
| Art. 7(6) | Report start, cease, restart of operations to the authority | ussp → authority | `01` S9; `02 F7` | met (fixed) | was missing |
| Art. 8(1) | Continuous remote identification throughout the flight, aggregated to authorised users | ussp | `01` S1 | met | |
| Art. 8(2)(a)–(g) | Message content incl. altitude AMSL and height, remote pilot position, emergency status, time generated | ussp | `01` S1; `03` `telemetry`; `04 §3.1` mapping | met (fixed) | AMSL derivation and operator position made explicit |
| Art. 8(3) | Update frequency as the competent authority determines | authority | `01` A11 | national choice | 1 Hz |
| Art. 8(4)(a)–(e) | Authorised users: public (as deemed public), other USSPs, ATS providers, single CISP, authorities | ussp | `01` S1; `06 §5` | met (fixed) / national choice | ATS and CISP added; what is public is national |
| Art. 9(1) | Geo-awareness: conditions and constraints, geo-zones, temporary restrictions | ussp | `01` S2; `02 F5` | met (fixed) | conditions and constraints added |
| Art. 9(2) | Timely; time of update plus version or valid time | ussp, cisp | `02 F3`, `F5` | met (fixed) | `updated_at`, `version`, `valid_from/to` |
| Art. 10(1) | Authorisation per flight with terms and conditions | ussp | `01` S3 | met | |
| Art. 10(2)(a)–(c) | Completeness check; accept if free of intersection under priority rules; notify | ussp | `01` S3; `02 F5` | met | |
| Art. 10(2)(d) | Deviation thresholds indicated on acceptance | ussp | `03`, `04` `deviation_thresholds` | met (fixed) | was missing |
| Art. 10(3) | Use weather information where applicable | ussp | `03` `weather_checked_ref` | met | optional service |
| Art. 10(4) | May propose an alternative | ussp | `04` `alternative` | met (fixed) | optional |
| Art. 10(6) | Arrangements to resolve conflicts between USSPs | ussp | `02 F6` | met | via DSS and F3548 |
| Art. 10(7) | Check against U-space restrictions and temporary limitations | ussp | `01` S3 | met | |
| Art. 10(8)–(9) | Priority to SERA Art. 4 special operations; first come first served otherwise | ussp | `01` S3; `03` `priority` | met (fixed) | explicit now; Q12 narrowed |
| Art. 10(10) | Continuous re-check against new restrictions and manned traffic in emergency; update or withdraw | ussp | `02 F5`; `04` `restriction_activated` | met (fixed) | manned emergency flag added to F4 |
| Art. 10(11) | Unique authorisation number identifying flight, operator, USSP | ussp | `03 §6` | met (fixed) | was missing |
| Art. 11(1)–(2) | Traffic information on conspicuous traffic from other USSPs and ATS units | ussp | `01` S4 | met | |
| Art. 11(3)(a) | Position, time of report, speed, heading, emergency status when known | ussp | `02 F5` | met | |
| Art. 11(3)(b) | Update frequency as the authority determines | authority | `01` A11 | national choice | 1 Hz |
| Art. 11(4) | Operator acts to avoid collision | operator | `01 §6` | met | the USSP informs, never resolves |
| Art. 12(1)–(3) | Weather service content and quality | ussp | `01` S6 | met | optional; minimum fields listed |
| Art. 13(1) | Conformance: verify Art. 6(1) and the authorisation; alert on thresholds and Art. 6(1) breaches | ussp | `01` S5; `03` `conformance_states` | met (fixed) | Art. 6(1) conditions added to the reasons |
| Art. 13(2) | Alert nearby operators, other USSPs, ATS units; they acknowledge | ussp, ansp | `02 F13`; `04` `nonconformance_nearby` | met (fixed) | nearby operators and acknowledgements were missing |
| Art. 14(1)–(6), Annex VI–VII | Certificates and their forms | authority | `03` `certificates` | met | |
| Art. 15(1)(a)–(c), (h)–(k) | Capability, systems, capital, business plan, liability, contingency plan | ussp, cisp | — | org | demonstrated at certification, not in software |
| Art. 15(1)(d) | Occurrence reporting per ATM/ANS.OR.A.065 | ussp, cisp | `01` S9; `02 F7` | met | point text not re-read (*unverified*) |
| Art. 15(1)(e)–(f) | Management system; security management system | ussp, cisp | `06` | org | the threat model supports it |
| Art. 15(1)(g) | Retain recorded operational data ≥ 30 days, longer while pertinent | ussp, cisp | `05 §4` | met | defaults exceed the floor; labelled national choice |
| Art. 15(2) | Emergency management plan and communication plan | ussp | `01` S11 | partial | console workflow planned; the plan itself is `org` |
| Art. 16(1)–(2) | Certificate validity; lapse after 6 / 12 months | authority | `03` `certificates.status` | met (fixed) | |
| Art. 16(3)–(4) | Performance assessment; conditions, suspension, revocation | authority | `01` A5; `06` T9 | met | |
| Art. 17 | Authority capabilities and enforcement | authority | `01` A10 | org | |
| Art. 18(a) | Registration system for certified USSPs and CISPs | authority | `02 §3` `/v1/certificates/*` | met (fixed) | public register added |
| Art. 18(b) | Determine traffic data (live or recorded) USSPs, CISP, ATS make available, with frequency and quality | authority | `01` A11; `02 F7` | national choice | basis for records and the optional push |
| Art. 18(c) | Access levels to common information | authority | `01` A9; `02 F10` | national choice | |
| Art. 18(d) | ATS–USSP exchange per Annex V | authority (oversight), ansp, ussp | `02 F4`, `F13` | met | |
| Art. 18(e)–(g), (i)–(k) | Application procedure, coordination mechanism, oversight programme, audits, safety performance | authority | `01` A3, A5, A10; `02 F11` | org / met | coordination (f) via `F11`; the rest organisational |
| Art. 18(h) | Require providers to make available all information needed for safety | authority | `02 F7` records | met | |
| Annex II(1)–(3) | Online, common open secure scalable technology; non-discriminatory; open protocol | cisp, ussp, ansp | `02 §1`, `F3` | met | |
| Annex III A(1)–(5) | Data quality, verification, metadata, authenticated transfer, error reporting | cisp, ussp | `01` C3; `02 F1`, `F3` | met (fixed) | metadata and error reporting made explicit |
| Annex III B(1)–(6) | Encryption, protocol protection, risk management, storage rules, insider risk, threat detection | cisp, ussp | `01` C4; `06` | met / org | B(5) training is organisational |
| Annex IV (1)–(10) | Ten request items | ussp | `04 §3.5` | met (fixed) | |
| Annex V(1)–(4) | SLA, exchange model, encryption, open protocol | ussp, ansp | `02 F4`, `F13`; `04 §4` | partial | SLA document is `org` |

Provisions checked: 2021/664 — 20 articles (every paragraph read) and 7 annexes; 72 matrix rows.

### 1.2 Regulation (EU) 2021/665 and 2021/666

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| 665 Annex I points (260)–(263) | Definitions added to 2017/373 | — | `00 §5` | met | |
| 665 ATS.OR.127(a) | ATS provides manned traffic information for U-space airspace in the controlled airspace it serves, non-discriminatory, as part of the CIS | ansp | `01` N2; `02 F2`, `F4` | met | scope: controlled airspace only |
| 665 ATS.OR.127(b) | Coordination procedures and communication facilities with USSPs and the CISP | ansp | `01` N2; `02 F13`; `03` `coordination_inbox` | met | |
| 665 ATS.TR.237(a) | ATC units temporarily limit the UAS area by adjusting lateral and vertical limits | ansp | `01` N1; `03` `restrictions` | met | |
| 665 ATS.TR.237(b) | Timely, effective notification of activation, deactivation, temporary limitation to USSPs and the CISP | ansp → cisp, ussp | `02 F2`, `F3` | met | 1 s target is a national figure |
| 666 SERA.6005(c) | Manned aircraft in U-space airspace not under ATC make themselves electronically conspicuous to USSPs | ussp (receiver) | `01` S4; `02 F4`; `03` `econspicuity_tracks` | met (fixed) | was a fallback; now a required input outside ATC service |
| 666 SERA.6005(d) | U-space airspace promulgated in the AIP | authority / AIS | `03` `aip_ref` | partial | AIS process outside the systems |
| 665 / 2017/373 ATM/ANS.OR.A.065, ATM/ANS.OR.B.030 | Occurrence reporting and record-keeping of ATS providers | ansp | `01` N4 | unverified | point texts not re-read |

Provisions checked: 2021/665 — 3 operative points; 2021/666 — 1 replaced point (4 sub-points).

### 1.3 Regulation (EU) 2019/947 and 2019/945

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| 947 Art. 3–6 | Categories open / specific / certified | authority, ussp | `03` `category`, `authorisations[]` | met | |
| 947 Art. 8, UAS.OPEN.020/030/040 | Remote pilot competency per subcategory | authority | `03` `remote_pilots.competencies` | met | |
| 947 Art. 12 | Specific-category operational authorisation | authority | `03` `authorisations[]`; `04` `authorisation_ref` | met | workflow Q12 |
| 947 Art. 14(1) | Registration systems for operators and certified UAS | authority | `01` A1 | met | |
| 947 Art. 14(2)(a)–(f) | Operator register fields | authority | `03` `uas_operators` | met (fixed) | date of birth, legal id, confirmation statement, authorisations were missing |
| 947 Art. 14(3)(a)–(d) | Certified UA register fields | authority | `03` `uas` | met (fixed) | owner reference added |
| 947 Art. 14(4) | Digital and interoperable registers | authority | `02 F8` | met | cross-border repository n/a |
| 947 Art. 14(5) | Who must register | authority (portal rules) | `03` `uas.mtom_g` | met | |
| 947 Art. 14(6) | Unique digital registration number on interoperability standards | authority | `03 §6`; Q5 | partial | AMC format (16 + 3 secret, checksum) from secondary sources — *unverified* |
| 947 Art. 14(7) | Certified UA registration mark per ICAO Annex 7 | authority | `03` `registration_mark` | met | |
| 947 Art. 14(8) | Display the registration number on the UA | operator | — | n/a | physical; upload into RID is 945 Part 2(12)(a) / Part 6(1) |
| 947 Art. 15(1)–(2) | Zone options; exemption zones | authority | `03` `geo_zones.type`, `regulation_exemption` | met | |
| 947 Art. 15(3), 18(f) | Zones with period of validity in a common unique digital format | authority, cisp | `02 F1`; `03` `geo_zones` | met | ED-318 |
| 947 Art. 17 | Designation of the competent authority | — | `00 §5` | n/a | |
| 947 Art. 18(a)–(e), (h)–(j) | Enforcement, certificates, competency, authorisations, records, oversight | authority | `01` A6 | met / org | |
| 947 Art. 18(k) | System to detect and examine non-compliance | authority | `01` A7; `03` `violations` | met | |
| 947 Art. 18(m) | Maintain registration systems | authority | `01` A1 | met | |
| 947 Art. 19(2) | Operators report occurrences per 376/2014 | operator → authority | `02 F7` | met | |
| 947 UAS.OPEN.010(2) | 120 m from the closest point of the surface | authority (detection) | `04` `violation/v1`; `01 §7` | met | DEM-based; owner decision kept; inside U-space airspace the authorised volume caps height |
| 947 UAS.OPEN.050(4), (6) | Operator ensures competency, class label | authority, ussp | `03` | met | |
| 947 UAS.OPEN.060(1)(b), (2)(c) | Remote pilot obtains zone information, obeys zone limits | ussp (geo-awareness) | `02 F5` | met | |
| 945 Art. 6 and Parts 1–5 | Class marks C0–C6; DRI for C1–C3 (Part 2–4 point (12)), C5/C6 via class requirements | authority | `03` `uas.class_label`, `rid_capability` | met | C5/C6 DRI points not re-read (*unverified*) |
| 945 Part 2(12)(a), Part 6(1) | Upload of the registration number with consistency check of the full string | operator / UAS | `06 §5` | met | secret part hashed; broadcast content question open (Q5) |
| 945 Part 2(12)(b) i–vi, Part 6(3) i–v | Broadcast content: registration number (+ verification code), serial, time stamp, position, height, course, speed, pilot / take-off position, emergency status (class UA only) | authority (decode) | `00 §5`; `03` `rid_observations` | met (fixed) | glossary corrected: add-on list has no emergency status |
| 945 Part 2(11), Part 6(2) | Serial per ANSI/CTA-2063-A | authority | `03 §6` | met | |
| 945 Part 2(12)(b) "open and documented transmission protocol" | | authority (receivers) | `00 §5` | met | EN 4709-002 / F3411 broadcast |

Provisions checked: 2019/947 — Art. 2–8, 12, 14, 15, 17, 18, 19 and UAS.OPEN.010/020/030/040/050/060 (20 rows); 2019/945 — Art. 6, Parts 1–6 and Part 2 point (11)–(12) (5 rows).

### 1.4 ASD-STAN EN 4709-002 and ASTM F3411-22a

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| EN 4709-002 (public descriptions) | Bluetooth 4 / 5 and Wi-Fi NAN / Beacon transports; Basic ID, Location, System, Operator ID, Self-ID, Authentication messages; dynamic messages ≥ 1 Hz; open protocol | authority (receivers) | `02 F9`; `03` `rid_observations` | partial / unverified | standard text not accessible; message set matches ASTM F3411 broadcast per public sources |
| F3411 Net-RID SP: ISA in DSS | SP creates / updates / deletes an ISA per flight (`PUT /rid/v2/dss/identification_service_areas/{id}`) | ussp | `02 F6`; `03` `flights.isa_id` | met | |
| F3411 Net-RID SP: `GET /uss/flights` | Serve flights for a view ≤ `NetMaxDisplayAreaDiagonalKm = 7`, p95 1 s / p99 3 s | ussp | `02 F6`; `05 §7` | met | |
| F3411 Net-RID SP: `GET /uss/flights/{id}/details` | Details for views ≤ 2 km | ussp | `02 F6` | met | |
| F3411 `NetMinUasLocRefreshFrequencyHz = 1`, `NetMinUasLocRefreshPercentage = 20` | Position refresh ≥ 1 Hz | ussp (and operator feed) | `02 §1` | met | matches the national 1 Hz |
| F3411 `NetMaxNearRealTimeDataPeriodSeconds = 60` | Near-real-time window; `recent_positions` | ussp | `02 F6` | met | |
| F3411 `NetMinSessionLengthSeconds = 5` | | ussp | — | met | implied by the flight model |
| F3411 Net-RID DP: ISA discovery via DSS, subscriptions ≤ 10 per area, ≤ 24 h | DP obtains ISA information from the DSS only for areas users are viewing | authority (and ussp for peers) | `02 F7` | met (fixed) | replaces the proposed push; push kept as an optional national extension |
| F3411 `NetDpInitResponse` 6 / 18 s, `NetDpDataResponse` 1 / 3 s, `NetDpDetailsResponse` 2 / 6 s | DP response times | authority | `05 §7` | met | |
| F3411 `NetDpMaxDataRetentionPeriodSeconds = 86400` | DP disposes of SP data within 24 h | authority, ussp | `03` `ussp_flights`, `peer_flights`; `05 §4` | met (fixed) | long-term oversight data comes from records (Art. 18(b)) |
| F3411 `NetMinClusterSizePercent = 15`, `NetMinObfuscationDistanceM = 300` | Clustering / obfuscation for public display | authority (public map, if any), ussp | — | unverified | applies to public Display Applications; no public network-ID map is specified yet; apply if one is built |
| F3411 data fields (`RIDFlight`, `RIDAircraftState`, `RIDFlightDetails`, `UASID`, `OperatorLocation`, `RIDOperationalStatus`) | | ussp, authority | `04 §3.1` | met | special values handled |
| F3411 scopes `rid.service_provider`, `rid.display_provider` | | token issuer | `06 §3` | met | |
| F3411 SP data retention | | ussp | `05 §4` | unverified | no SP retention constant in `uas_standards`; the 30-day floor of 2021/664 governs |

Provisions checked: F3411 — 13 rows (constants and operations from `uas_standards`; the standard's requirement text (NET0xxx) not accessible beyond the InterUSS requirement list).

### 1.5 ASTM F3548-21

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| Operational intent references in the DSS (`PUT/GET/DELETE /dss/v1/operational_intent_references/...`, query) | | ussp | `02 F6` | met | |
| `OperationalIntentState`: Accepted, Activated, Nonconforming, Contingent | Only these in the DSS | ussp | `03` `dss_state` / `local_state` | met (fixed) | local states separated |
| USS endpoints: `GET /uss/v1/operational_intents/{entityid}` (≤ 1 s), `.../telemetry`, `POST /uss/v1/operational_intents`, `GET /uss/v1/constraints/{entityid}`, `POST /uss/v1/constraints`, `POST /uss/v1/reports`, `GET /uss/v1/log_sets/{log_set_id}` | | ussp | `02 §3` | met (fixed) | telemetry and log_sets added |
| Constraint references (`/dss/v1/constraint_references/...`), `utm.constraint_management` / `utm.constraint_processing` | Dynamic restrictions as constraints | ansp, ussp | `02 F2`; `03` `restrictions.dss_constraint_id` | met (fixed) | ANSP is the constraint manager; limits `CstrMaxDurationHours = 24`, `CstrMaxPlanningHorizonDays = 56`, `CstrMaxAreaKm2 = 10000`, `CstrMaxVertices = 1000` |
| `CstrPublishedNotificationLatencySeconds = 5`, `UssOiChangeNotificationMaxSeconds = 5`, `ConflictingOIMaxUSSNotificationTimeSeconds = 1`, `MaxRespondToOIDetailsRequestSeconds = 1` | Notification and response times | ussp, ansp | `05 §7` | met | load-test criteria |
| `OiMaxPlanHorizonDays = 30` | | ussp | `03` `operational_intents` | met | |
| `MaxRecoverableTimeInNonconformingStateSeconds = 60` | Nonconforming → Contingent | ussp | `02 F6` | met | |
| `ExternalDataMaxRetentionTimeHours = 24` | Peer data retention | ussp | `03` `peer_intents`; `05 §4` | met (fixed) | |
| `TimeSyncMaxDifferentialSeconds = 5`, `TimeSyncMinPercentage = 99` | Clock sync | all | `02 F6` | met | NTP; measured in the lab |
| USS availability (`PUT /dss/v1/uss_availability/{uss_id}`, `utm.availability_arbitration`) | Arbitrator sets Normal / Down / Unknown | authority | `01 §3`; `02 F6` | met (fixed) | |
| `Priority` as defined by the regulator | | ussp | `03` `priority` | met | 2021/664 Art. 10(8)–(9) |
| Volume4D / Volume3D / Altitude (`reference W84`, `units M`) | | ussp | `03` | met | AMSL derived alongside |
| Aggregate conformance monitoring (`OiMinConformancePercent = 95`, `AggConfMon*`), `MaxNonPerformanceNotificationLatencyHours = 6` | | ussp (reporting), authority | — | unverified | requirement text not accessible; constants noted for the records design |

Provisions checked: F3548 — 12 rows (operations, enums and 40 constants from `uas_standards`).

### 1.6 EUROCAE ED-269 and ED-318

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| ED-318 `UASZone` properties (`identifier`, `country`, `name`, `type`, `variant`, `restrictionConditions`, `region`, `reason`, `otherReasonInfo`, `regulationExemption`, `message`, `extendedProperties`, `limitedApplicability`, `zoneAuthority`, `dataSource`) | Field names | authority, cisp | `02 F1`; `03` `geo_zones` | met (fixed) | `reasons` → `reason`, `authority` → `zoneAuthority`, `applicability` → `limitedApplicability` |
| ED-318 `CodeZoneType`: USPACE, PROHIBITED, REQ_AUTHORIZATION, CONDITIONAL, NO_RESTRICTION | | authority, cisp | `02 F1` | met | |
| ED-318 `CodeZoneReasonType` incl. `DAR` | | ansp | `02 F2` | met | |
| ED-318 `CodeVerticalReferenceType`: AGL, AMSL, WGS84; `UomDistance`: m, ft | | all | `02 F1`; `03` | met | property names of the vertical limits in the geometry object *unverified* |
| ED-318 `TimePeriod`, `DailyPeriod` (with `startEvent` / `endEvent` BMCT, SR, SS, EECT), `CodeWeekDayType` | Time applicability | authority, cisp, ussp | `02 F1`; `03` | met (fixed) | daylight events added |
| ED-318 `Authority` (`purpose` AUTHORIZATION / NOTIFICATION / INFORMATION, `intervalBefore`, contact) | | authority | `03` `zone_authority` | met | |
| ED-318 `Metadata` as `uspace-core/ed318.Metadata` carries it (`issued`, `provider`, `validFrom`, `validTo`, `description`), `DatasetMetadata` | | cisp | `04 §3.4`; `02 F3` | met | the version and time of update are the CISP's top-level `cis_version`, `cis_updated_at` and the `ETag` |
| ED-318 `extendedProperties` | Extension mechanism | authority, cisp | `04 §4` | met | used for the Art. 3(4) requirements block |
| ED-269 `restriction` (REQ_AUTHORISATION spelling), `uomDimensions` M / FT, vertical reference AGL / AMSL only, `applicability` | Import mapping | authority | `02 F1` | met (fixed) | |
| ED-318 U-space data provision services (beyond the zone model) | CIS service interfaces | cisp | `02 F3` | unverified | standard text not accessible; the REST/pull/push design follows Annex II |

Provisions checked: ED-318 — 8 rows; ED-269 — 1 mapping row (both from `uas_standards` modules).

### 1.7 Regulation (EU) 376/2014

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| Art. 4(1) | Mandatory occurrence categories | authority (intake) | `03` `occurrence_reports.category` | met | UAS classes of 2015/1018 *unverified* |
| Art. 4(2)–(3) | Organisations and the state run mandatory reporting systems | ussp, ansp (org), authority | `02 F7` | met | |
| Art. 4(6)–(7) | Reporters report within 72 h of awareness | operator, ussp, ansp staff | `01 §6`; `02 F7` | met | |
| Art. 4(8) | Organisations forward to the authority within 72 h | ussp, ansp | `03` `occurrence_reports` (USSP) | met | |
| Art. 5 | Voluntary reporting | authority | `03` `channel` | met (fixed) | |
| Art. 6(1), (3) | Independent handling, confidentiality, just culture | authority | `01 §1` users; `03` | met (fixed) | `incident_officer` role; segregated table |
| Art. 6(6), (10) | National database; CAA full access | authority | `03` `occurrence_reports` | met | |
| Art. 7(1), Annex I | Minimum report content | authority | `03` | unverified | Annex I not extracted |
| Art. 7(2) | Safety risk classification | authority | `03` `risk_classification` | met (fixed) | scheme itself national |
| Art. 7(4) | ECCAIRS- and ADREP-compatible format | authority | `02 F7` | met | E5X export when confirmed (Q9) |
| Art. 13 | Analysis and follow-up | authority | `03` `analysis`, `follow_up` | met | |
| Art. 15(1)–(2) | Confidentiality; safety use only; no blame | authority | `01 §1` MUST NOT; `06` T6 | met (fixed) | violations never derived from reports |
| Art. 16(1)–(3) | Personal details restricted; none in the national database | authority | `03`; `06 §5` | met (fixed) | |
| Art. 16(6)–(7) | No proceedings on inadvertent infringements known only from reports; reports not used against reporters | authority | `01 §1` MUST NOT | met (fixed) | |

Provisions checked: 376/2014 — 13 rows (Art. 4–7, 13, 15, 16).

### 1.8 GDPR (as the reference model; Georgian law applies)

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| Art. 5(1)(a), 6(1)(b), (c), (e) | Lawful basis | all | `06 §5` | met / national choice | police access basis is national |
| Art. 5(1)(b) | Purpose limitation | authority, ussp | `06 §5` | met | |
| Art. 5(1)(c) | Data minimisation | all | `06 §1`, `§5` | met (fixed) | USSP holdings bounded; public subset excludes pilot position |
| Art. 5(1)(e) | Storage limitation | all | `05 §4`; `06 §5` | partial / national choice | defaults above the floor await the DPO (Q8) |
| Art. 5(1)(f), 32 | Security | all | `06` | met | |
| Art. 5(2), 25, 30 | Accountability, by design, records of processing | all | `06 §5` | met (fixed) | records of processing added |
| Art. 35(3)(c) | DPIA for systematic monitoring of public areas | authority | `06 §5` | met (fixed) | before go-live |
| Law of Georgia on Personal Data Protection | National transposition | all | `06 §5` | unverified | article numbers not cited |

Provisions checked: GDPR — 8 rows.

### 1.9 Owner's technology rule (`00 §6`)

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| Rule: Go is the entire backend of every system (hot path and control plane; all of `uspace-cisp`) | every API, ingest, engine, workflow and the token service in Go | all | `00 §6.1`; `02 §3`; `05 §2` | met | |
| Rule: Next.js for every web UI, render only | public and internal UIs, BFF cookie layer only | all `web` | `00 §6.1`; `06 §3` | met | no DB / NATS access from `web` |
| Hard rule: safety logic once, in Go packages, pinned by `knowledge/vectors/` | identification, zone judging, CPA, conformance, strategic deconfliction | `uspace-core` packages used by every system | `00 §6`; `04 §1`; `06` T12; `07` KT-2 | met | vectors present in this repo (18 files, 682 cases) after merging `main` |
| Process decomposition: hot-path and control-plane processes of one Go module, shared `internal/`, NATS only where it decouples | | authority, cisp, ussp, ansp | `00 §6.1`–`6.2`; `05 §2`, `§6` | met | judgements are package calls, not internal hops |
| Generated bindings Go → TypeScript only | JSON Schema and OpenAPI → client types | all `web` | `04 §1`; `07` KT-2 | met | |
| One JWT verifier (`uspace-core/auth`) | JWKS, `kid`, RS256 only, `aud`, scope | all | `00 §6.2`; `06 §3` | met | |
| Data: PostgreSQL/PostGIS, TimescaleDB, NATS JetStream; Python only in `uspace-lab` | | all | `03`; `05 §3` | met | |
| Shared libraries: `rootxkit/uspace-core` (Go, compiled in, semver-pinned, vector harness) and `rootxkit/uspace-ui` (npm, shadcn/ui theme, MapLibre, symbology, ka/en, BFF auth helpers) | package list, versioning and compatibility policy; each system upgrades on its own schedule | all | `00 §6.3`; `07` Phase 1 | met | |

### 1.10 Interoperability with third-party systems (`00 §7`)

| Source and clause | Requirement | Owning system | Spec section | Status | Note |
|---|---|---|---|---|---|
| 2021/664 Art. 5(5), 7(5)(b), Annex II(3), Annex V(4) | open, non-discriminatory, interoperable protocols; any certified USSP or CISP may interoperate | all | `00 §7`; `02 §1` | met | standards first; national APIs published as OpenAPI |
| National API publication | every non-standard API in versioned OpenAPI 3.1, owned per repo, aggregated in `uspace-lab/api/` | all | `00 §7`; `02 §1` | met | |
| Discovery through the DSS and the CIS USSP list, never a configured address of our USSP | | authority, ussp | `00 §7`; `02 F7` | met | |
| Conformance suite: InterUSS `uss_qualifier` (F3411, F3548) plus national API and ED-318 tests; onboarding procedure | | lab | `00 §7`; `07` Phase 6 | met | as a certification condition: national choice (Q7) |
| Compatibility policy: additive within a major; breaking changes as a new major with ≥ 12 months deprecation | | all | `00 §7`; `02 §1`; `04 §4` | met | |

## 2. Deliberate national choices

| Choice | Where the source leaves it open | Justification |
|---|---|---|
| The authority runs the ecosystem token issuer | F3411 / F3548 and the InterUSS DSS require only a trusted OAuth2 issuer with the standard scopes; 2021/664 names none | the authority already holds the certificate register that defines who may hold a client; one issuer keeps `aud` / scope policy auditable |
| The single CISP is state-run (`uspace-cisp`) — **decided by the owner**; the DSS hosted beside it — still a national choice | 2021/664 Art. 5(6)–(7) allows a single designated CISP, which must be certified; no regulation assigns the DSS | neutrality towards USSPs; one operations team at the start; the DSS is a neutral broker like the CIS; a third-party CISP can replace ours behind the same contracts |
| The conformance suite as a condition of USSP / CISP certification | 2021/664 Art. 15(1)(a)–(b) require demonstrated capability and interoperable systems; the means is the authority's | an objective, repeatable demonstration; proposed to GCAA (Q7) |
| Retention: 90 days online / 2 years archive at the authority, 1 year at USSPs, 5 years alerts and intents, 10 years audit, incidents and occurrences indefinite | floor: 30 days (2021/664 Art. 15(1)(g)); ceiling: storage limitation; F3411 / F3548 caps of 24 h apply only to DP and peer caches and are respected | oversight and investigation needs; to be fixed by the DPO (Q8) |
| 120 m open-category check at the authority only; height conformance at the USSP against the authorised volume | 2019/947 Art. 18(k) gives detection to the authority; 2021/664 Art. 13(1) gives conformance to the USSP against the authorisation and the Art. 6(1) conditions | no duplication: inside U-space airspace the authorised volume is capped by the airspace constraints, so the USSP's conformance alert already covers a height ceiling; outside it only the authority detects |
| 1 Hz for network identification and traffic information | 2021/664 Art. 8(3), 11(3)(b): the competent authority determines | equals the F3411 minimum; one figure across the ecosystem |
| 1 s CIS change notification target, 300 s stale bound | Annex III sets no numbers; Art. 3(4)(b) performance is national | matches ATS.TR.237(b) "timely and effective"; measured in the lab |
| CPA proximity alerts and `nonconformance_nearby` radius | 2021/664 Art. 11 requires information, not alerts | kept from the predecessor as a service; the operator still decides (Art. 11(4)) |
| Optional 1 Hz USSP → authority flight push | 2021/664 Art. 18(b) lets the authority determine live traffic data | an extension on top of the F3411 DP baseline, never a substitute; a USSP without it is still conformant |
| Informing the authority of conformance deviations | Art. 13(2) names operators, USSPs and ATS units only | oversight value; delivered through records and occurrences, not as a real-time dependency |
| F3411, F3548, ED-318 as the "common secure interoperable open protocols" | 2021/664 Art. 7(5)(b), Annex II(3), Annex V(4) name no standard | the de-facto EU U-space choice; InterUSS reference implementation available |
| `geodesy/cell` grid partitioning (`cell5` 0.1° × 0.1°, `cell3` 1° × 1°) | internal; no standard governs it | never crosses an external interface; pure Go, no cgo |
| Public subset of network identification | Art. 8(4)(a): "as deemed public in accordance with applicable Union and national rules" | the broadcast-equivalent items without the remote pilot position, pending national rules |
| Police access levels and lawful basis | Art. 18(b)–(c); national data-protection law | purpose-logged realm pending the legal basis (Q8) |

## 3. Not verified — what must be confirmed and by whom

| Item | What could not be re-read | Must be confirmed | By |
|---|---|---|---|
| EASA AMC1 Article 14(6) of 2019/947 — registration number format | the AMC text itself; format taken from secondary sources (16 characters, checksum, 3 secret) | the exact format GCAA will issue and whether the verification code is broadcast or only checked at upload | GCAA registry owner; EN 4709-002 holder |
| ASD-STAN EN 4709-002 | the standard (paywalled); message set and 1 Hz dynamic rate from public descriptions | message formats and operator-ID encoding used by the receivers | engineering, with a licensed copy |
| ASTM F3411-22a requirement text (NET0xxx), SP retention, clustering and obfuscation rules | only `uas_standards` constants and the InterUSS requirement list were accessible | public-display obfuscation if a public network-ID map is built; SP retention | engineering, with a licensed copy |
| ASTM F3548-21 requirement text (aggregate conformance monitoring, log sets content, availability arbitration procedure) | only `uas_standards` API and constants | the records needed for `OiMinConformancePercent` reporting; who may arbitrate availability | engineering; GCAA as arbitrator |
| EUROCAE ED-318 vertical-limit property names in the geometry object and the U-space data provision service interfaces | the `uas_standards` summary did not show the geometry object; the service part of ED-318 is not in `uas_standards` | exact property names; whether the CIS REST design must follow ED-318 service definitions | engineering, with a licensed copy |
| 2017/373 ATM/ANS.OR.A.065 and ATM/ANS.OR.B.030 | point texts not extracted | ANSP record-keeping period and occurrence reporting route | Sakaeronavigatsia safety office |
| 376/2014 Annex I minimum fields; 2015/1018 UAS occurrence classes | not extracted | the mandatory field set of `occurrence/v1` | GCAA occurrence reporting officer (Q9) |
| 2019/945 Parts 5 (C5) and C6 direct-RID points | not re-read | whether C5/C6 broadcast content differs | engineering |
| Law of Georgia on Personal Data Protection | not read; GDPR used as the model | lawful basis for police access, retention ceilings, DPIA obligation | GCAA DPO (Q8) |
| Georgian adoption of 2021/664–666 and any designation | national legal status (Q1, Q2, Q17) | whether the U-space layer has legal force and its per-airspace requirements | Ministry / GCAA |

## Errata

| Date | Where | Change | Source |
|---|---|---|---|
| 2026-10-04 | §1.6, ED-318 `Metadata` row | The row names core's metadata members, not `creationDateTime`, `updateDateTime`, `originator`; status unchanged. | `docs/decisions/2026-10-02-cross-plan.md` M15 |
| 2026-10-04 | §2, partitioning row | The grid of `05 §3` replaces H3 resolution 5 / 3; the choice stays internal. | `docs/decisions/2026-10-02-cross-plan.md` M35 |

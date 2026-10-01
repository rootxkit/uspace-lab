# 06 — Security

## 1. Principles

| Principle | Consequence |
|---|---|
| No path to an aircraft | no system holds a credential, socket or protocol that can reach a flight controller; the lab's SITL bridges read MAVLink and never write |
| Registry PII stays at the authority | operator identity, contact and pilot identity live only in `uspace-authority`; a USSP holds only what Annex IV and Art. 8 require (registration number, serial, emergency contact reference, remote pilot / take-off position for the flight) plus its own customer account; the CISP, ANSP and lab hold none |
| Authenticated is not trusted | a token proves who sent a message, not that its content is true; trust class travels with the data (`04 §2`) |
| Audit everything that changes or reveals | writes, source switches, views of PII, exports, token issuance |
| Fail visible | an auth failure, a disabled source or a stale feed is shown with who/when/why; nothing hides an aircraft |

## 2. Threat model

| Threat | Asset | Attack | Control | Residual |
|---|---|---|---|---|
| T1 Remote ID spoofing | authority picture, violations | forge ODID broadcasts: phantom aircraft, another operator's number, fake position of a real serial | `trust: broadcast` on every direct-RID track; `basis: as_broadcast`; `serial_conflict` split when an authenticated session places the aircraft > 300 m away; a violation from broadcast-only evidence is `status: new` and needs an officer's review before escalation; receiver geometry check (RSSI plausibility, multi-receiver agreement) flags `plausibility: low` (future) | a well-placed spoof of an unregistered serial is indistinguishable from a real unregistered flight; by design it is a lead, not a conviction |
| T2 Receiver impersonation or capture | observation ingest | reuse a stolen receiver key to inject frames; replay old batches | per-receiver bearer key + HMAC over body with a separate secret; `msg_id` and `rx_ts` window dedupe (replay > 60 s refused as `backlog` only if the receiver's heartbeat sequence is continuous); keys revocable; receiver disable by instance is audited; receiver position pinned in the registry and compared with reported position | a captured live receiver can inject until noticed; rate limits bound the blast |
| T3 Operator credential compromise | USSP intents and telemetry | a stolen operator client secret files intents or feeds false telemetry under a real registration | per-client scopes, short tokens, secret rotation, binding of client to the operator's registered UAS serials (a client may only send telemetry for its own serials); anomaly flags (two sessions for one serial, teleport > 100 m/s); operator notified of new client creation; the USSP never grants an authorisation that conflicts with zones regardless of who asks | false telemetry for a real serial is still `authenticated`; the authority's direct-RID picture gives an independent check where receivers exist |
| T4 Inter-system impersonation | all F-flows | a rogue host pretends to be the CISP, a USSP or the ANSP | ecosystem OAuth2 issuer at the authority (national choice; F3411/F3548 require only a trusted issuer with the standard scopes): RS256 JWTs with `aud`, `sub`, scopes, TTL ≤ 1 h, JWKS; client registration tied to a certificate record; mTLS mandatory for ANSP streams and CISP publishing; webhooks JWS-signed by the sender with published JWKS, receivers verify `iss` and `aud`; publications detached-signed by the authority so provenance survives the CISP (Annex III A(4)) | compromise of the issuer's signing key is the root risk: HSM or KMS-held key, 90-day rotation, two-person control on client creation |
| T5 Token service outage | all machine auth | denial of service on the issuer | tokens valid to TTL; JWKS cached 24 h; issuer stateless behind Caddy; second instance first scaling step; clients pre-fetch at 50 % TTL | new clients cannot start during an outage |
| T6 PII leakage | operator data | a USSP, police user or export reveals identity beyond law | registry validity API returns status only; police realm requires `purpose` and case ref, logs the query, rate-limits, and the DPO report lists every PII view; evidence packs redact PII unless the pack type is `legal`; console roles separate `inspector` (sees PII) from `viewer`; Annex IV emergency contact held at the USSP as a reference resolvable only by the authority during an incident; occurrence reporter identities visible to `incident_officer` only and never exported (376 Art. 16); the public network-ID subset excludes the remote pilot position | insiders; mitigated by audit and review |
| T7 Replay or tamper of records | evidence, audit | alter telemetry or events after the fact | append-only `events` with monthly hash chain; evidence packs SHA-256 sealed and the hash recorded in `events`; TimescaleDB tables without `UPDATE` grants for application roles; raw RID frames kept verbatim | database admin with host access; mitigated by backups to a second account and periodic hash verification |
| T8 Denial of service on ingest | hot path | floods on telemetry WS, observation POST, public CISP map | per-client and per-receiver rate limits; connection caps; Caddy rate limits; the public map served from cache with `Cache-Control`; work queue sheds oldest with counted gaps | volumetric attacks need upstream filtering |
| T9 Malicious or faulty peer USSP | DSS, traffic info | bad intents, oversized responses, lies in F3411 | body size caps (1 MiB), flight count caps, tile caps, per-poll deadline (predecessor U-02 limits); peer data `trust: provider`; conflicts with a peer intent are reported to the authority with evidence; the authority can suspend a USSP's certificate, which revokes its client | a certified USSP lying within limits is a regulatory matter |
| T10 Supply chain / public repo | all code | secrets or test keys committed; dependency compromise | see §4; `gitleaks` in CI; SBOM; pinned module checksums (`go.sum`, `package-lock.json` with `npm ci`, `pnpm-lock.yaml`); images built in CI only, signed (cosign) and verified by the deploy | — |
| T11 Simulator in production | all | a lab adapter accepting `trust: simulated` reaches production | production ingest refuses `trust: simulated` and `source: sitl` at the schema validator; lab images are a different repo and never deployed behind the production Caddy | — |
| T12 Safety logic drift between languages | every judgement | a NestJS module or a Next.js route re-implements identification, zone, CPA, conformance or conflict logic and diverges from the Go engine | hard rule of `00 §6`: safety logic only in Go, pinned by `knowledge/vectors/`; CI runs the vectors against the engine images; lint fails a TypeScript import of geometry / geodesy libraries outside `uspace-lab`; `web` has no database or NATS access; code review checklist item | a reviewer missing a hand-written check in TypeScript; mitigated by the lint rule and by the app storing engine replies verbatim with `policy_version` |
| T13 JWT verifier divergence | all auth | the Go and NestJS verifiers accept different tokens (algorithm confusion, skew, missing `aud`) | one procedure (`00 §6.2`), `lestrrat-go/jwx` and `jose` pinned, `knowledge/vectors/jwt_verify.json` run by both in CI, `alg` allow-list RS256 only | a library CVE; mitigated by Dependabot and the vector catching behavioural change |

## 3. Authentication and authorisation summary

| Boundary | Mechanism | Scopes (examples) |
|---|---|---|
| Humans → any console | local accounts (argon2id) in the NestJS app, OIDC-ready, MFA mandatory for authority, ANSP and admin roles; the Next.js BFF route exchanges the login for an `HttpOnly`, `SameSite=Strict` cookie holding the session JWT and forwards it as a bearer to the app and the engine WS; CSRF tokens; no credential is ever handled in browser JavaScript | role-based per system (`01`) |
| Operators (humans) → USSP portal | USSP's own OIDC | `operator_admin`, `remote_pilot`, `viewer` |
| Operator systems → USSP | OAuth2 client credentials at the USSP issuer; client bound to registration number and serial list | `ussp.intents`, `ussp.telemetry`, `ussp.traffic`, `ussp.geo` |
| System ↔ system | ecosystem issuer at the authority; JWT RS256; `aud` = target system (the DSS audience for DSS calls); mTLS where stated | `cis.read`, `cis.publish:*`, `registry.validate`, `ussp.records`, `ansp.traffic`, `occurrences.write`, `certificates.status`; F3548 `utm.strategic_coordination`, `utm.constraint_processing`, `utm.constraint_management`, `utm.conformance_monitoring_sa`, `utm.availability_arbitration`; F3411 `rid.service_provider`, `rid.display_provider` |
| Receivers → authority | bearer key + HMAC | `rid.observe` |
| Police → authority | separate realm, MFA, IP allow-list | `police.query` |

Every token issuance and every refusal is an `events` row at the issuer. Scopes are least-privilege per client; a USSP's client for the authority has `registry.validate` and `occurrences.write` and nothing else.

## 4. Public-repository constraints

All five repositories are public. Therefore:

| Rule | How enforced |
|---|---|
| No secret, key, certificate or token in git, ever, including test keys | `gitleaks` pre-commit and CI; `.env.example` only; lab test keys generated at run time into `local/` (git-ignored) |
| No production hostname, IP, account or operator data in code or fixtures | configuration via environment; fixtures use `GEO-TEST-*` numbers and `TEST*` serials reserved by convention; a CI grep for `chikox.net` outside `infra/staging/` fails |
| No real registry exports, zone files from the authority under restricted licence, or incident data | synthetic data generators in the lab; the airspace.gov.ge import rules file is config, not repo content, until the authority licenses it |
| Branding is configuration | `authority` is the code name; GCAA name, logo and contact come from a config bundle outside the repo |
| Security-relevant design is public; keys and deployments are not | threat model and algorithms are in the repos (Kerckhoffs); deployment manifests with real values live in a private infra repo |
| Dependencies pinned and verified | `go.sum`, `package-lock.json` / `pnpm-lock.yaml` with frozen installs, Dependabot, SBOM per image, cosign-signed images |
| Vulnerability disclosure | `SECURITY.md` in each repo with a contact and a 90-day policy |
| Commit hygiene | Conventional Commits, no AI attribution, signed-off by a person; branch protection on `main`; CI (lint, vet, tests, vectors, SITL where relevant) required |

## 5. Data protection notes

GDPR does not apply in Georgia as such; the Law of Georgia on Personal Data Protection applies (national; its article numbers are not cited here). The design nevertheless follows the GDPR principles as the EU regulations assume them (2021/664 Art. 18(b) "without prejudice to personal data protection regulations"), so that it stays valid if the EU acquis is adopted (Q1):

| Principle (GDPR) | Rule here |
|---|---|
| Lawfulness (Art. 6(1)(c), (e)): legal obligation / public task | Registry processing rests on 2019/947 Art. 14 and its national transposition; network identification and records on 2021/664 Art. 8, 15(1)(g), 18(b); occurrence handling on 376/2014. A USSP's customer account rests on its contract with the operator (Art. 6(1)(b)). The lawful basis for police access is national law (Q8) — **national choice**. |
| Purpose limitation (Art. 5(1)(b)) | Every PII read carries a `purpose`; occurrence data is used for safety only (376 Art. 15(2)); the DP cache is used for display and detection only. |
| Data minimisation (Art. 5(1)(c)) | Registry validity answers are status-only; USSPs hold the Annex IV / Art. 8 fields and no more; the CISP holds none; the public network-ID subset carries the Art. 8(2) items the state deems public (Art. 8(4)(a), **national choice**) and never the remote pilot position; registry fields are exactly the 2019/947 Art. 14(2)–(3) set. |
| Storage limitation (Art. 5(1)(e)) | DP and peer caches 24 h (standard); operational records ≥ 30 days (Art. 15(1)(g)) and otherwise the `05 §4` defaults until the DPO fixes them (Q8); the remote pilot position in telemetry is dropped from the long-term archive after the 90-day online window unless an incident references it. |
| Integrity and confidentiality (Art. 5(1)(f), Art. 32) | `06 §2–3`; encryption in transit and at rest; Annex III B. |
| Accountability, by design, records, DPIA (Art. 5(2), 25, 30, 35) | Append-only audit of every PII view; a record of processing activities per system; a DPIA before go-live for the authority's picture and the police realm (systematic monitoring of a publicly accessible area, Art. 35(3)(c)). |

- The registration number's secret part (if the EU-style format is adopted) is stored hashed; comparisons use the public part, case-insensitively. 2019/945 (as amended) has the UA broadcast "the registration number and the verification code" after a consistency check at upload; whether the verification code is actually on air or only checked locally must be confirmed against EN 4709-002 (*unverified*). Either way a received secret part is hashed on ingest and never stored in clear.
- A remote pilot's national id is stored as a salted hash plus the last four characters for clerical verification.
- USSP service records sent to the authority contain registration numbers, serials and authorisation numbers, not names.
- Occurrence reports: personal details are visible to the incident officers only, never recorded in the national database export (376 Art. 16(3)), and never joined to violations.
- Evidence packs are produced on demand, hash-sealed, and their creation and every download are audited with the requester's purpose.

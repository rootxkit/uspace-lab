# WP-L7: `conformance-suite` (L-M4)

Branch `feat/WP-L7-conformance-suite`. Milestone **L-M4**. Owns
`conformance/` (`uss_qualifier/` configurations and participant
definitions, `national/` contract tests, `ed318/` publication tests,
`onboarding.md`, `report/`), `make conformance`, `conformance.yml`.
Depends on WP-L1 (the aggregate the national tests read), WP-L2 (the
DSS and issuer), WP-L5 (`sim-ussp` as the onboarding candidate), and on
the systems' standard interfaces (ussp WP-9, WP-13, WP-19; ansp WP-9;
authority WP-14; cisp WP-13). Consumers: GCAA (spec Q7, L-Q3, L-Q12),
every system's release (they pass the same suite on every release,
`00 §7`), third-party USSPs and CISPs.

## Read first

1. `docs/PLAN.md §1` D5, D8, `§7.1` L-Q3, L-Q10, L-Q12 (open: the
   suite reports; GCAA decides what certifies).
2. Spec `00 §7` (the conformance suite row in full), `02 §1`
   Publication row, `07` L-M4, `09 §1.4`–`1.6` (every F3411 and F3548
   row marked `met` is a scenario the suite must exercise), `09 §3`
   (what was not accessible: the F3411 NET0xxx and F3548 requirement
   texts; the suite reports what `uss_qualifier` checks, nothing more).
3. `docs/decisions/2026-10-02-cross-plan.md`: M18 (hostname audiences;
   `uss_qualifier` uses hostnames), Q-A7 (`GET /v1/dp/observations
   ?view=` with `dp.observe` for the lab client; the DP test interface
   pinned to the lab's InterUSS commit), M6, M23.
4. The system briefs that expose hooks: ussp WP-19 (its own
   `deploy/conformance/`; the lab owns the pass criteria), ansp plan
   `§10` conformance row (`make conformance-target`, `testdata/
   conformance/`), authority `§9` conformance row, cisp `§10.6`
   (`LAB_DIR`, `conformance/cisp/`).
5. InterUSS `monitoring` at the commit you pin (`conformance/
   uss_qualifier/SOURCE`): `uss_qualifier` configuration format,
   resources, participants, the `netrid` (F3411 v22a SP and DP) and
   `astm.utm` (F3548 v21 strategic coordination, constraint
   processing, constraint management) suites, the mock USS, the report
   format and its "pass/fail/not tested" semantics. Read the
   configuration keys at that commit; do not reuse ones from memory
   (E-04).
6. LESSONS E-01, E-02, E-04, E-05.

## What to build

### `uss_qualifier` configurations

`conformance/uss_qualifier/<target>.yaml` for: the USSP as F3411 SP
with the qualifier's mock DP; the authority as F3411 DP through its
`dp.observe` interface with the USSP (or `sim-ussp`) as SP; the USSP as
F3548 USS for strategic coordination with the mock USS as peer, and for
constraint processing with the mock constraint manager; the ANSP as
F3548 constraint manager; `sim-ussp` as a candidate (the onboarding
rehearsal). Resources point at the lab DSS and issuer (WP-L2);
participant ids = the client ids of M24; audiences = hosts. A wrapper
`conformance/run-qualifier.sh <target>` runs the pinned image and
writes the report under `report/<run>/`.

### National contract tests (`conformance/national/`, Go)

From the aggregate (`api/<system>/openapi.yaml`): every operation of
`02 §3` for that system is exercised against the system under test with
a token from the lab issuer: the success case with a schema-validated
response, and the refusals the spec names (401 without a token, 403 on
the wrong scope, 404, 409 on a stale `If-Match`, 422 with a `problem/v1`
body and `errors[]`), both directions (E-01). Per system, in addition:
CISP ED-318 publication tests (`conformance/ed318/`: schema, vertical
references, applicability with `?at=` and `?applies_at=`, versioning
and `ETag`, `/v1/changes` and the signed webhook within 1 s, the
heartbeat and stale rules); authority (registry validate never returns
PII, `certificates.status`, records pull, occurrence intake with the
reporter held); USSP (F5 intent flow with the ten Annex IV items,
authorisation number format `<USSP code>-<reg public part>-<ULID>`,
records, geo-awareness `stale` marking); ANSP (restriction publication
with the `(ansp_ref, ansp_version)` key and `X-JWS-Signature`,
coordination intake `202` and `GET .../notices/{ack_id}`, heartbeat
every 15 s). Public pages the suite loads get an `axe` run reported as
informative (L-Q10).

### Report and onboarding

`conformance/report/`: one JSON per run combining the qualifier report
and the national results with the image digest, the commits of this
repo, core and the pinned InterUSS commit, hashed and signed with the
lab issuer's key (L-Q12 default; "proposed" wording). `conformance/
onboarding.md`: how a third party runs the suite against the staging
DSS and CISP, what it submits, what "pass" means for each suite (the
gate of L-Q3 as the default, DP informative), and the rehearsal record
with `sim-ussp` as the candidate.

`make conformance TARGET=<system>` runs everything for one target;
`conformance.yml` on dispatch and on each system's release tag (the
system's workflow dispatches it with the digest).

## Done when

- [ ] `make lint`, `go test` clean; the national tests fail against a
  deliberately broken double (a missing `errors[]`, a PII field in
  `registry/validate`) and pass against the real images (E-01).
- [ ] The F3411 SP and F3548 suites run against `uspace-ussp`, the DP
  suite against `uspace-authority`, the constraint-management suite
  against `uspace-ansp`; reports in the PR with the InterUSS commit;
  every failed check filed as an issue on the owning repo with the
  check name.
- [ ] The onboarding rehearsal with `sim-ussp` run end to end and its
  signed report produced.
- [ ] `docs/PLAN.md §7.1` L-Q3 and L-Q12 updated with "what the suite
  reports" (not with a decision).

## Commits

`feat(conformance): configure uss_qualifier for the USSP, authority, ANSP and the candidate   [WP-L7 L-M4]`,
`feat(conformance): test every national operation both ways from the aggregate   [WP-L7 L-M4]`,
`feat(conformance): test ED-318 publication, change feed and webhooks at the CISP   [WP-L7 L-M4]`,
`feat(conformance): produce a signed combined report   [WP-L7 L-M4]`,
`docs(conformance): write the onboarding procedure and record the rehearsal   [WP-L7 L-M4]`,
`ci: run the conformance suite on dispatch and on release tags   [WP-L7 L-M4]`.

# Onboarding a third-party USSP or CISP (proposed)

**Status: proposed to GCAA, not adopted** (docs/PLAN.md §7.1 L-Q3, L-Q12;
spec 08 Q7). Whether passing this suite is a condition of certification
under 2021/664 Art. 15(1)(a)–(b) is GCAA's decision (spec 09 §2, national
choice). This document describes what the suite does and reports; it
certifies nothing.

## Who runs what

| Step | Who | What |
|---|---|---|
| 1 | candidate | Obtains a machine client at the ecosystem token service (in the lab, the lab issuer's `lab-01` stands in for the suite) and points its system at the staging DSS and the staging CISP. |
| 2 | candidate | Exposes the InterUSS automated-testing interfaces of its role on a test deployment: RID injection (F3411 Service Provider), RID observation (F3411 Display Provider), flight_planning v1 (F3548 strategic coordination). Without them uss_qualifier cannot drive the system and those requirements are reported **not applicable** (see "what pass means"). |
| 3 | candidate | Writes a target file (`conformance/targets/<name>.yaml`, from `sim-ussp.yaml` or `ussp.yaml`): base URL, participant id (its client id, M24), interfaces, credentials as `${VAR}`s. A CISP uses `cisp.yaml`. |
| 4 | candidate | Runs the qualifier configurations its role needs (`conformance/uss_qualifier/run-qualifier.sh`, pinned InterUSS image) and then `make conformance TARGET=<name>` with the reports, signing with the staging issuer's key (`--sign-key`). |
| 5 | candidate | Submits `report.json`, `report.json.sha256` and `report.json.jws`. |
| 6 | GCAA / the lab | Verifies the signature (`conformance verify --report R --jwks <issuer JWKS>`), checks the report's InterUSS commit, image digests and lab commit, and reads every requirement's status and reasons. |

## What "pass" means (defaults, pending GCAA)

| Suite | Default role (L-Q3) | Pass |
|---|---|---|
| F3411-22a Service Provider (`F3411-SP`) | gate for a USSP | every InterUSS requirement uss_qualifier checked for the candidate's participant passed and none failed |
| F3548-21 strategic coordination (`F3548-SCD`) | gate for a USSP | the same, for the F3548 run |
| F3548-21 constraint processing (`F3548-CP`) | gate for a USSP, **not applicable at the pinned commit**: uss_qualifier v0.36.0 has no constraint processing requirement set | — |
| F3411-22a Display Provider (`F3411-DP`) | informative | reported, never failing |
| National contract tests (`NAT-*`, `REG-NOPII`) | gate for a system that publishes a national API | every check that applied passed |
| ED-318 publication (`ED318-*`) | gate for a CISP | every check that applied passed |
| Accessibility (`A11Y-PUBLIC`) | informative (L-Q10) | reported, never failing |

The report's verdict is **pass** only when no gate requirement failed and
every gate requirement applied; **incomplete** when nothing failed but a
gate requirement could not be exercised; **fail** otherwise. An
incomplete report is not a pass.

## The report

One JSON per run: the target, the candidate's participant id, the
commits of uspace-lab and uspace-core, the image digests under test, the
pinned InterUSS commit and image digest, the contract's digest, the
policy with every pending-GCAA value, each requirement's status with its
reasons and counts, and every check's outcome. It is hashed (SHA-256)
and signed with the issuer's key as an RFC 7797 detached JWS over its
exact bytes. Proposed format (L-Q12).

## Rehearsal record (2026-10-05)

The procedure was run end to end with the lab's simulated peer USSP
(`cmd/sim-ussp`) as the candidate, on this machine (Windows, Docker
Desktop):

1. `make sim-ussp-up`: the lab DSS (InterUSS v0.23.0 by digest), the
   lab issuer and sim-ussp; sim-ussp wrote an ISA and an operational
   intent reference with a `sim-ussp-01` token, and the DSS returned
   them on a query (the script's own proof).
2. `conformance run --target conformance/targets/sim-ussp.yaml
   --sign-key deploy/local/signing-key.pem` at uspace-lab `d6b4cf1`
   (clean tree; sim-ussp image `sha256:b1174ae7...`): verdict
   **incomplete**, nothing passed and nothing failed. F3411-SP and
   F3548-SCD are not applicable: sim-ussp exposes neither the RID
   injection nor the flight_planning interface; F3548-CP is not
   applicable at the pinned InterUSS commit.
3. `conformance verify` against the issuer's JWKS: the signature and the
   digest verified.
4. `make dss-down`: nothing of the project left.

The record is `conformance/report/records/20261004T221142Z-sim-ussp/`
(the report, its digest and signature, the issuer JWKS that verifies
it). What it shows: the procedure works and the report says why the
candidate cannot pass; what a third-party USSP must add to pass is the
two InterUSS test interfaces.

The uss_qualifier half of the procedure was rehearsed separately with
InterUSS's own mock Service Provider as the candidate (participant
`mock-ridsp-candidate`, `conformance/report/records/20261004T221305Z-mock-ridsp-candidate/`):
the full `f3411-sp.yaml` run completed against the lab DSS and issuer,
uss_qualifier wrote its report, and the suite folded it: 3464 checks
passed for the lab DSS, and F3411-SP **failed** for the candidate on 7
checks, because the mock SP answered 401 to every injection. Observed
cause: mock_uss accepts only a string `aud`, and the lab issuer writes a
one-element array (a token from uspace-core `auth.Issuer`, decoded); see
`uss_qualifier/README.md`. A system verifying with uspace-core accepts
either; making the mock accept the lab's tokens needs a single-string
audience from the issuer (a uspace-core change, open).

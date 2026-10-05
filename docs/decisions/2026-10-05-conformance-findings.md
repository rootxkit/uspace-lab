# Decision record: defects the conformance suite found, 2026-10-05

Status: **open** for C2, C3, C8 and the USSP's part of C6; C1, C4, C5,
C7 and the authority's part of C6 are closed (below, each with the fix
and the run that shows it gone). Defects the conformance suite (WP-L7, L-M4)
observed in runs against real code: C1 to C3 in the first runs, C4 to
C8 in the runs against the ANSP, the authority and the USSP on the
systems stack (`conformance/report/records/README.md`, `*-demo/`). They are recorded here, in the lab,
instead of as issues on the owning repositories: the lab does not file
issues on other repositories. Each entry says what was observed and
where the evidence is, what the contract or specification asks for, who
owns the fix, and what the lab does meanwhile. A baseline that accepts
one of these failures (`conformance/baseline/<target>.json`) points its
`note` at the entry, and the entry is closed here, with the commit that
fixed it, when a run shows the failure gone and the baseline is updated.

Nothing below decides a policy question. The suite's own defaults for
GCAA's open questions (L-Q3, L-Q10, L-Q12) stay in
`conformance/policy.yaml`, each marked pending GCAA (`docs/PLAN.md`
§7.1).

| Id | Defect | Owner | Status |
|---|---|---|---|
| C1 | The CISP's `503 console_unavailable` problem body has no `errors[]` | uspace-cisp | closed (uspace-cisp#27) |
| C2 | Decision record Q-A7 names `dp.observe` where InterUSS observes with `dss.read.identification_service_areas` | uspace-authority, decision record 2026-10-02 | open |
| C3 | The lab issuer writes `aud` as an array; the InterUSS mock USS accepts only a string | uspace-core (`auth.Issuer`) | open |
| C4 | A WebSocket upgrade without credential or Origin is refused 403, not 401 | uspace-ansp, uspace-authority | closed (uspace-authority#49, uspace-ansp#26) |
| C5 | The ANSP's `login` and `verifyMfa` answer an empty body 401, not the declared 400 | uspace-ansp | closed (uspace-ansp#26) |
| C6 | A request without credential is answered 400 `validation`, not 401 | uspace-authority, uspace-ussp | open for the USSP until uspace-ussp#37; closed for the authority (uspace-authority#49) |
| C7 | The authority's `postDPISANotification` answers 401 and 403 with a body its contract does not declare | uspace-authority | closed (uspace-authority#49) |
| C8 | The USSP's telemetry and traffic streams answer a handshake without credential 101 | uspace-ussp | open until uspace-ussp#37 |

## C1: CISP 503 body without errors

**Observed.** uspace-cisp at `d89910109ac8`, its chaos stack through
`tools/conformance.sh` (record
`conformance/report/records/20261004T221103Z-cisp/`): every console
operation answers `503` with problem type `console_unavailable` (the
console is not configured on that stack), and the body has no `errors`
member. NAT-UNAUTH fails on 16 console operations and NAT-INVALID on
`POST /v1/console/session` and `POST /v1/console/session/mfa`; the
failing checks are listed in `conformance/baseline/cisp.json`.

**Expected.** `schemas/common/problem/v1` lists `errors` as required
(decision record M28), and the suite judges every refusal body against
it. The suite treats a 503 with the
declared problem body as "switched off on this target" (not applicable);
a 503 whose body is not the declared problem is a failure, which is what
it reported.

**Owner.** uspace-cisp: the console's 503 path writes the problem body
without `errors`.

**Meanwhile.** `conformance/baseline/cisp.json` accepts exactly these
failing checks; a further failing check of NAT-UNAUTH or NAT-INVALID is
still a regression. When the CISP answers with `errors: []`, the two
requirements become pass or not applicable, the gate reports "a known
failure now passes", and the baseline is rewritten from that run.

**Closed.** uspace-cisp#27 ("send errors on every problem body", merged
as `24659a5`). Record `conformance/report/records/20261005T021137Z-cisp/`:
the CISP at `24659a5` through its `tools/conformance.sh`, NAT-UNAUTH
(21 checks) and NAT-INVALID pass, nothing fails, and the gate reported
both as "a known failure now passes". `conformance/baseline/cisp.json`
is rewritten from that run with no known failure left, so a console
503 without `errors[]` is a regression again, and
`conformance/national/contracts/PINS` names `24659a5`, the CISP that
CI's gate builds.

## C2: Q-A7 scope name

**Observed.** Read at the pinned InterUSS commit (v0.36.0, `0fabe238`):
the RID observation interface that uss_qualifier's F3411 Display
Provider suite drives authorises with
`uas_standards.interuss.automated_testing.rid.v1.constants.Scope.Observe`,
whose value is `dss.read.identification_service_areas`
(`conformance/uss_qualifier/README.md`). Not yet observed against the
authority: its observation hook was not run (no authority conformance
target with lab TLS was brought up).

**Expected.** Decision record 2026-10-02 Q-A7 (and M23, `06 §3`) has the
authority's hook `GET /v1/dp/observations` admit the lab client by the
scope `dp.observe`. uss_qualifier will request
`dss.read.identification_service_areas` and an authority that admits
only `dp.observe` will refuse it, so F3411-DP cannot pass as specified.

**Owner.** uspace-authority (the hook's scope) together with the
decision record: either the hook also admits the InterUSS scope for the
lab client, or Q-A7 is amended. The lab does not decide it.

**Meanwhile.** The lab issuer issues both to `lab-01`
(`deploy/issuer/clients.yaml`); F3411-DP is informative (L-Q3 default,
pending GCAA), so the defect cannot fail a run, and its outcome says
why.

## C3: array aud

**Observed.** `conformance/uss_qualifier/run-qualifier.sh f3411-sp.yaml`
with the InterUSS mock Service Provider as the candidate (record
`conformance/report/records/20261004T221305Z-mock-ridsp-candidate/`):
every injection answered 401 and F3411-SP failed on 7 checks. Cause,
read in `monitorlib/auth_validation.py` at the pinned commit and seen
on a decoded token: mock_uss compares `aud` as a string, and the lab
issuer (uspace-core `auth.Issuer`) writes `aud` as a one-element array.

**Expected.** RFC 7519 §4.1.3 allows either form. The DSS and every
system verifying with uspace-core accept both; only the mock USS does
not.

**Owner.** uspace-core: an option for `auth.Issuer` to write a single
audience as a string, which the lab issuer would then use.

**Meanwhile.** A system under test is not affected. Running the mock
USS as a candidate against the lab issuer reproduces the 401s; that
rehearsal record stays as it is, as evidence.

## C4: upgrade without Origin is 403

**Observed.** Records `20261004T230115Z-ansp-demo/` (ANSP `d02b09a`)
and `20261004T230117Z-authority-demo/` (authority `a2bfeaa`): a
WebSocket handshake with neither a credential nor an `Origin` header is
answered `403` before any upgrade: ANSP `streamCoordination`,
`streamMannedTraffic`, `streamRestrictions` with problem type
`forbidden`; authority `getPictureWS` with type `origin`. The ANSP's
`internal/auth/guard.go` at `08731a3` judges an upgrade without
`Authorization` as a browser's cookie upgrade and checks the Origin
first.

**Expected.** NAT-UNAUTH holds a request without credential to `401`
with the problem body. The ANSP's contract declares `401` for these
operations; the authority's declares `403` (Origin) and a `default`
response, which the suite reads as admitting `401`.

**Owner.** The ANSP and the authority: check for a credential before
the Origin (an upgrade with neither is unauthenticated), or state in
the contract and decision record M22 that a credential-less upgrade is
refused as a browser's. The suite sends no `Origin`: it is a machine
client.

**Closed.** uspace-authority#49 (`9a35cba`) and uspace-ansp#26
(`a26e00b`): an upgrade without credential is answered 401 before the
Origin is judged. Records
`conformance/report/records/20261005T022343Z-authority-demo/` and
`conformance/report/records/20261005T022348Z-ansp-demo/` (their
published images at those commits): `getPictureWS` and the ANSP's
three streams answer 401, and NAT-UNAUTH passes on both systems.

## C5: ANSP login with an empty body

**Observed.** Record `20261004T230115Z-ansp-demo/`: `POST
/v1/auth/login` and `POST /v1/auth/mfa` with an empty JSON object
(every required member missing) answer `401` (`invalid_credentials`,
`mfa_refused`).

**Expected.** The ANSP contract declares `400` for a body its schema
refuses; NAT-INVALID asks for it, with `errors[]` naming the fields.

**Owner.** uspace-ansp: validate the body before judging the
credentials, or drop `400` from those operations' responses.

**Closed.** uspace-ansp#26 (`a26e00b`). Record
`conformance/report/records/20261005T022348Z-ansp-demo/`: `login` and
`verifyMfa` answer the empty body 400 with `errors[]`, and NAT-INVALID
passes.

## C6: 400 before 401

**Observed.** Records `20261004T230117Z-authority-demo/` and
`20261004T230122Z-ussp-demo/`: without a credential, the authority's
`downloadEvidencePack`, `getRegistryOperatorPersonalData`,
`getRegistryPilotPersonalData`, `listRIDFrames`, `deleteRIDReceiver`,
`getZoneApplicability` and the USSP's `openAlertStream` (a WebSocket
handshake) answer `400` `validation`. The suite fills each path parameter with a value its
schema admits and leaves out a required query value it has none for.

**Expected.** NAT-UNAUTH asks for `401` to a request without
credential, whatever else is wrong with it, so that a client learns it
is not authenticated before anything about the resource. The contracts
do not say which check comes first.

**Owner.** uspace-authority and uspace-ussp, or the decision record:
either authenticate first, or record that validation may precede
authentication, and the suite will then report these not applicable.

**Closed for the authority.** uspace-authority#49 (`9a35cba`). Record
`conformance/report/records/20261005T022343Z-authority-demo/`: the six
operations answer the request without credential 401. **Open for the
USSP** (`openAlertStream`) until uspace-ussp#37 is merged and a run
shows it.

## C7: authority ISA notification bodies

**Observed.** Record `20261004T230117Z-authority-demo/`:
`postDPISANotification` (`POST /uss/identification_service_areas/{id}`,
dp-poller) answers `401` with `{"message":"a bearer token is
required"}` and `403` with `{"message":"the token does not grant
rid.service_provider"}`.

**Expected.** Its contract declares `401` and `403` without content;
the suite refuses a body the contract does not declare.

**Owner.** uspace-authority: declare the InterUSS error body for this
InterUSS-facing operation, or answer without one.

**Closed.** uspace-authority#49 (`9a35cba`) declares the refusals'
problem body. Record
`conformance/report/records/20261005T022343Z-authority-demo/`:
`postDPISANotification` answers 401 and 403 as declared, and NAT-SCOPE
passes.

## C8: USSP streams upgrade without credential

**Observed.** Record `20261004T230122Z-ussp-demo/`: a handshake with no
credential to `openTelemetryStream` (`/v1/telemetry`) and
`openTrafficStream` (`/v1/traffic`) is answered `101`. By hand with
curl, `/v1/telemetry` then closed the connection with "sign in again".

**Expected.** NAT-UNAUTH asks for `401` before the upgrade. The USSP
contract declares no `401` for these operations, only a `default`
response, which the suite reads as admitting it; the description of a
refusal after the upgrade, if that is the design, is not in the
contract the suite reads.

**Owner.** uspace-ussp: refuse before upgrading, or declare that the
refusal of these streams is a close code after the upgrade, which the
suite would then test as such.

**Open** until uspace-ussp#37 is merged and a run against the USSP
shows the streams refused 401 before the upgrade.

## On the lab's side

What the runs showed wrong in the lab itself rather than in a system.
These are not defects of the systems and have no entry above.

- The lab Caddy answered `/metrics` 404 on every host and sent the
  authority's `/healthz` and `/readyz` to its api, where they are not
  served (they are on its admin listener). Fixed in this repository:
  `deploy/systems/Caddyfile` routes the authority's three to
  `authority-api:9090` and the ANSP's `/metrics` to `ansp-api`; the
  other hosts keep `/metrics` closed, as the droplet does.
- Once `/metrics` was reached, the suite judged its Prometheus text as
  JSON. Fixed: a `text/plain` answer is validated as the string its
  contract declares.
- `deploy/systems/gen-secrets.sh` stopped without a word under Git Bash
  when `MSYS_NO_PATHCONV` was set (as `deploy/demo-up.sh` sets it).
  Fixed: it drops the variable, and every failure names its step.
- Still failing on the authority: REG-NOPII and NAT-SUCCESS
  `validateRegistry`, because the lab has no operator registration to
  name (`AUTHORITY_CONFORMANCE_OPERATOR`; no fixture yet); and
  NAT-INVALID on `createOperatorOccurrence`,
  `submitRegistryApplication` and `requestOperatorLink`, which answer
  404 because the lab stack leaves the authority's registry portal
  switched off (`REGISTRY_APPLICATIONS`, `REGISTRY_OPERATOR_REPORTS`,
  default `off`). Both are open on the lab's side: an operator fixture,
  and either turning the portal on or the suite reading a switched-off
  404 as not applicable.

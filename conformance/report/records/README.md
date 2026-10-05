# Conformance records (WP-L7)

Reports of runs against real code, kept as evidence (the runs of
`make conformance` go to the git-ignored `conformance/report/runs/`).
Nothing here is expected rather than observed (E-04).

## `20261004T221142Z-sim-ussp/`: the onboarding rehearsal

The signed report of the rehearsal `conformance/onboarding.md` records,
with the issuer JWKS that verifies it:
`go run ./cmd/conformance verify --report conformance/report/records/20261004T221142Z-sim-ussp/report.json --jwks conformance/report/records/20261004T221142Z-sim-ussp/issuer-jwks.json`.

## `20261004T221103Z-cisp/`: the CISP's own conformance target

uspace-cisp at `d89910109ac8` (main; the commit then
`conformance/national/contracts/PINS` names), `tools/conformance.sh`
with `LAB_DIR` this repository at `d6b4cf1` (clean) and
`CONFORMANCE_PUBLISH=true`: its chaos stack built from that checkout
(`uspace-cisp:chaos-local`, `sha256:070c55af...`), the suite through
`conformance/cisp/run` against `CISP_BASE_URL` with the stack's
`CONFORMANCE_ENV`. The contract is the checkout's `api/openapi.yaml`
(`sha256:3dcf572b...`; the aggregate's `api/cisp` is unpinned).

Verdict **fail**: 9 pass, 2 fail, 3 not applicable.

- Pass: NAT-SCOPE, NAT-NOTFOUND, NAT-SUCCESS; ED318-SCHEMA, -VERTICAL,
  -APPLICABILITY (with the published fixture: the zone applying now kept
  and marked `applies`, the expired one dropped and marked
  `not_applicable`), -VERSIONING (428 without and 412 on a stale
  `If-Match` with the current ETag, a signed invalid body refused with
  `errors[]` and the version unmoved, 304 on the current ETag), -CHANGES,
  -HEARTBEAT.
- Fail, a finding for uspace-cisp: every console operation answers
  `503 console_unavailable` on this stack (the console is not
  configured), and that problem body has no `errors` member, which
  `schemas/common/problem/v1` requires. NAT-UNAUTH (16 operations) and
  NAT-INVALID (`POST /v1/console/session`, `/mfa`) fail on it. Recorded
  in `docs/decisions/2026-10-05-conformance-findings.md` (C1) and as a known failure in
  `conformance/baseline/cisp.json`.
- Not applicable: NAT-PRECONDITION (no operation with `If-Match` has an
  example body the contract offers), ED318-WEBHOOK (the chaos stack's
  CISP delivers to HTTPS subscribers; the suite's receiver is plain HTTP
  and the target named none), A11Y-PUBLIC (no public page named).

`conformance/baseline/cisp.json` was this run until the next record.

## `20261005T021137Z-cisp/`: the CISP after the C1 fix

uspace-cisp at `24659a5` (main, the merge of uspace-cisp#27, "send
errors on every problem body"; the commit
`conformance/national/contracts/PINS` now names), `tools/conformance.sh`
with `LAB_DIR` this repository at `62809d3` (clean; the report names
it; re-made as `e603682` with a shorter subject before it was pushed,
same tree) and `CONFORMANCE_PUBLISH=true`, as the first record: its chaos stack built
from that checkout (`uspace-cisp:chaos-local`, `sha256:4f5809ba...`),
the contract the checkout's `api/openapi.yaml` (`sha256:3f69439b...`).
On Windows the stack's test executes `conformance/cisp/run` directly,
which Windows cannot do for a shell script, so an untracked
`conformance/cisp/run.cmd` handed it to Git Bash for this run (the
report does not count untracked files as dirt; nothing else differed).

Verdict **incomplete** by the report's own rule (gate requirements not
applicable), **no regression** against the baseline, which accepts
those as not applicable: 11 pass, 0 fail, 3 not applicable.

- Pass: everything that passed in the first record, and now NAT-UNAUTH
  (21 checks, the 16 console operations included) and NAT-INVALID
  (`POST /v1/console/session`, `/mfa`): the console's `503
  console_unavailable` body carries `errors: []`, so it is judged as
  the declared problem. C1 is closed
  (`docs/decisions/2026-10-05-conformance-findings.md`).
- Not applicable, as before: NAT-PRECONDITION, ED318-WEBHOOK,
  A11Y-PUBLIC.

The gate reported both as "a known failure now passes", and
`conformance/baseline/cisp.json` was rewritten from this run
(`conformance baseline`): no known failure is left in it, so a 503
without `errors[]` on any console operation is a regression again. CI
runs the same target on every change to the suite, at the commit PINS
names, and fails on a regression against it.

## `20261004T221305Z-mock-ridsp-candidate/`: uss_qualifier end to end

`conformance/uss_qualifier/run-qualifier.sh f3411-sp.yaml` with the lab
DSS and issuer (`make sim-ussp-up`) and the InterUSS mock USS
(`compose.yaml`), the mock's Service Provider standing in as the
candidate (`mock-ridsp-candidate`), then folded with
`--qualifier-report F3411-SP=...`. uss_qualifier (v0.36.0, `0fabe238`)
completed and wrote its report: 3464 checks passed for the lab DSS, 1 for
the mock Display Provider; F3411-SP **failed** for the candidate on 7
checks (NET0500 and the injection API requirements), every injection
answered 401. Cause, read at the pinned commit and observed on a token:
mock_uss compares `aud` as a string, the lab issuer writes a one-element
array. See `conformance/uss_qualifier/README.md` and
`docs/decisions/2026-10-05-conformance-findings.md` (C3).

## `*-demo/`: the ANSP, the authority and the USSP on the systems stack

The three national systems as `make demo` runs them (`deploy/demo-up.sh`,
compose project `uspace-conf`, images of `deploy/demo.env.example`
at this branch): each behind the lab Caddy with the lab CA, tokens from
the lab issuer (`lab-01`). The suite at uspace-lab `578a737` (clean;
the reports name it) ran from the host; that commit was re-made as
`3b9f4fe` with a shorter subject before it was pushed, same tree. It
ran with target files that are the committed ones plus `ca_file` (the stack's CA) and `resolve` (the `*.uspace.test` hosts at
127.0.0.1), and with the contract at each image's own commit, so that
the run measures the image and not the distance between the image and
`conformance/national/contracts/PINS`:

| Record | Image | Contract | Verdict |
|---|---|---|---|
| `20261004T230115Z-ansp-demo/` | `uspace-lab/uspace-ansp:d02b09a` (built by `demo-up.sh` from `d02b09a`; local image id `sha256:01ed9677...`) | uspace-ansp `d02b09a` `api/openapi.yaml` (`sha256:e66744c8...`) with its `schemas/` | fail: 2 pass, 3 fail, 3 n/a |
| `20261004T230117Z-authority-demo/` | `ghcr.io/rootxkit/uspace-authority@sha256:8663cac1...` (revision label `a2bfeaa`) | uspace-authority `a2bfeaa` (`sha256:3dffc218...`) | fail: 1 pass, 4 fail, 4 n/a |
| `20261004T230122Z-ussp-demo/` | `ghcr.io/rootxkit/uspace-ussp@sha256:07718be4...` (uspace-deploy `compose/images.env`: `6ec6238`) | uspace-ussp `6ec6238` (`sha256:acc47d6f...`); the committed overrides less the five operations that commit does not have | fail: 1 pass, 3 fail, 7 n/a |

Each report is signed with that stack's issuer key; `issuer-jwks.json`
beside it verifies it (`conformance verify`). No session, operator token
or fixture was configured, so the session operations, REG-NOPII on the
USSP and NAT-PRECONDITION are not applicable with those reasons; no
InterUSS test interface is exposed, so F3411/F3548 are not applicable.

Two defects of the suite showed on the first runs and are fixed on this
branch before these records: a non-JSON body sent without its
Content-Type (the ANSP answered 415 to `receiveCisNotification`), and a
WebSocket marked only by its 101 sent as a plain GET (426 on the
authority's and the USSP's streams).

What the failures are, as observed (the defects of the systems are
`docs/decisions/2026-10-05-conformance-findings.md` C4 to C8):

- The lab Caddy answers 404 to `/metrics` on every host (as the
  droplet's routes do; `deploy/systems/Caddyfile`), so NAT-SUCCESS fails
  `getMetrics` on the ANSP and the authority: the lab's front, not the
  systems (the ANSP's api answers 200 on it inside the stack).
- ANSP: an upgrade without credential or Origin is 403 `forbidden`, not
  401 (C4); `login` and `verifyMfa` answer an empty body 401, not the
  declared 400 (C5).
- Authority: `/healthz` and `/readyz` answer 404 `not_found` (from
  authority-api, where the lab Caddy routes them); six operations answer
  the request without a credential 400 `validation`, not 401 (C6);
  `postDPISANotification` answers 401 and 403 with a `{"message"}` body
  where its contract declares none (C7); `getPictureWS` is 403 `origin` without an Origin (C4);
  `postRIDObservations` is 502 from the lab Caddy (cause not
  determined); `validateRegistry` is 400 without an operator
  registration to ask about (no `AUTHORITY_CONFORMANCE_OPERATOR`), so
  NAT-SUCCESS fails and REG-NOPII has nothing to inspect.
- USSP: `openTelemetryStream` and `openTrafficStream` answer a
  handshake with no credential 101, not 401 (C8);
  `openAlertStream` answers it 400 `validation` (C6);
  `openAuthorityFlights` is 404 from `ussp-rid-sp` at `6ec6238`.

None of these targets has a baseline: they are evidence that the suite
runs against each system, not a reviewed state CI gates on.

## `20261005T0223*-demo/`: the authority and the ANSP after their fixes

The two systems at the merges of their conformance fixes,
uspace-authority#49 (`9a35cba`: C4, C6, C7) and uspace-ansp#26
(`a26e00b`: C4, C5), on the systems stack of `deploy/systems/` at
uspace-lab `e425025` (clean; the reports name it; re-made as `dc7b4ee`
before it was pushed, when an earlier subject was shortened: the same
tree but for the note on `62809d3` above), which routes their
`/healthz`, `/readyz` and `/metrics` through the lab Caddy and judges a
`text/plain` answer as text. Compose project `uspace-close` with
`deploy/demo.env.example` less two images: the authority's
`ghcr.io/rootxkit/uspace-authority@sha256:cd31db6b...` (`sha-9a35cba`,
revision label `9a35cba`) and the ANSP's
`ghcr.io/rootxkit/uspace-ansp@sha256:97304cfc...` (tag `a26e00b`,
published by its CI). Only the DSS, the issuer, Caddy and those two
systems were started (the CISP and the USSP stack were not needed);
`gen-secrets.sh` ran with `MSYS_NO_PATHCONV=1` in the environment, the
case that used to stop it without a word. Target files as for the first
`*-demo` records (the committed ones plus `ca_file` and `resolve`, the
port 9443); each contract at its image's commit.

| Record | Contract | Verdict |
|---|---|---|
| `20261005T022343Z-authority-demo/` | uspace-authority `9a35cba` `api/openapi.yaml` (`sha256:07284432...`) | fail: 3 pass, 3 fail, 3 n/a |
| `20261005T022348Z-ansp-demo/` | uspace-ansp `a26e00b` `api/openapi.yaml` (`sha256:1b1596cf...`) | incomplete: 5 pass, 0 fail, 3 n/a (NAT-PRECONDITION, F3548-CM: no baseline accepts them) |

What changed against the first `*-demo` records:

- ANSP: NAT-UNAUTH passes (39 checks: an upgrade without credential is
  401, C4), NAT-INVALID passes (`login`, `verifyMfa` answer an empty
  body 400, C5), NAT-SUCCESS passes (`getMetrics` reached, 200 with the
  declared text). Nothing fails.
- Authority: NAT-UNAUTH passes (124 checks: `getPictureWS` without
  credential is 401, C4; the six operations of C6 answer 401 before
  validating), NAT-SCOPE passes (`postDPISANotification`'s refusals
  match the contract, C7), and `getHealthz`, `getReadyz`, `getMetrics`
  pass through the lab Caddy. `postRIDObservations` is no longer 502.

What still fails on the authority, all on the lab's side:

- `validateRegistry` answers 400 `validation`: the target names no
  operator registration (`AUTHORITY_CONFORMANCE_OPERATOR`), so
  NAT-SUCCESS fails on it and REG-NOPII has nothing to inspect. The lab
  has no operator fixture to name yet.
- NAT-INVALID on `createOperatorOccurrence`, `submitRegistryApplication`
  and `requestOperatorLink`: each answers an empty body 404
  `not_found`, not 400. They are switched off: the authority's
  `REGISTRY_APPLICATIONS` and `REGISTRY_OPERATOR_REPORTS` default to
  `off`, which its configuration documents as "404" for these
  operations, and the lab stack sets neither (they need a portal key and
  URL). In the first record the suite could construct no invalid body
  for them. Whether the lab turns them on or the suite reads a
  switched-off 404 as not applicable, as it reads the CISP's 503, is
  not decided here.

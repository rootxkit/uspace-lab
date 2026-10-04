# Conformance records (WP-L7)

Reports of runs against real code, kept as evidence (the runs of
`make conformance` go to the git-ignored `conformance/report/runs/`).
Nothing here is expected rather than observed (E-04).

## `20261004T221142Z-sim-ussp/`: the onboarding rehearsal

The signed report of the rehearsal `conformance/onboarding.md` records,
with the issuer JWKS that verifies it:
`go run ./cmd/conformance verify --report conformance/report/records/20261004T221142Z-sim-ussp/report.json --jwks conformance/report/records/20261004T221142Z-sim-ussp/issuer-jwks.json`.

## `20261004T221103Z-cisp/`: the CISP's own conformance target

uspace-cisp at `d89910109ac8` (main; the commit
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
  NAT-INVALID (`POST /v1/console/session`, `/mfa`) fail on it. Not filed
  as an issue by this work package; recorded as a known failure in
  `conformance/baseline/cisp.json`.
- Not applicable: NAT-PRECONDITION (no operation with `If-Match` has an
  example body the contract offers), ED318-WEBHOOK (the chaos stack's
  CISP delivers to HTTPS subscribers; the suite's receiver is plain HTTP
  and the target named none), A11Y-PUBLIC (no public page named).

`conformance/baseline/cisp.json` is this run: CI runs the same target on
every change to the suite and fails on a regression against it.

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
array. See `conformance/uss_qualifier/README.md`.

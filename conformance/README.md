# Conformance suite (WP-L7, L-M4)

The suite every system passes on every release and a third-party USSP
or CISP runs to be admitted (spec `00 §7`, `docs/WORKPACKAGES/WP-L7.md`).
It runs against one system's **conformance target** and reports **pass,
fail or not applicable per requirement** of `requirements.yaml`, in one
signed JSON report per run. What it could not exercise is reported as
not applicable with the reason, never as a pass (LESSONS E-04).

```
make conformance TARGET=cisp            # or ansp, authority, ussp, sim-ussp
make conformance-test                   # the suite's own tests, both ways
make conformance-qualifier-check        # every uss_qualifier config validated by the pinned image
make conformance-axe-test               # the axe runner's Playwright test
```

## What is in it

| Path | What |
|---|---|
| `requirements.yaml` | the requirements the suite decides, with their source in the spec |
| `policy.yaml` | the defaults GCAA has not answered (L-Q3, L-Q10, L-Q12), each marked **pending GCAA** |
| `national/` | the national contract test: every operation of a system's OpenAPI file classified by its credential (fail closed) and exercised both ways |
| `national/contracts/<system>.yaml` | what a contract states only in prose (a scope in a description); `PINS` the contracts the suite's CI classifies |
| `ed318/` | the CISP's ED-318 publication test (schema, vertical references, `?at=` / `?applies_at=`, versions and `ETag`, `/v1/changes`, the signed webhook, the heartbeat and stale rule) |
| `uss_qualifier/` | InterUSS `uss_qualifier` configurations at the pinned commit (`SOURCE`), the mock USS, the run and check scripts, `coverage.yaml` |
| `axe/` | the informative accessibility run (Playwright and axe) |
| `report/` | the combined report, its signature and the regression gate |
| `targets/<name>.yaml` | each system's conformance target: base URL, contract, credentials, all from the environment |
| `baseline/<name>.json` | the reviewed status of each requirement per target: CI fails on a regression against it |
| `cisp/run` | the CISP's entry point (its `make conformance`, uspace-cisp `docs/PLAN.md §15 Q47`) |
| `onboarding.md` | the procedure a third party follows, and the rehearsal record |

## Requirements and statuses

Each check produces an outcome (`result.Outcome`): the requirement it
decides, the operation or page it exercised, pass, fail or not
applicable, the HTTP status observed and the evidence. A requirement is
**fail** when any outcome failed, **pass** when at least one passed and
none failed, and **not applicable** otherwise, with every reason. A
requirement in `policy.yaml`'s `informative` list (pending GCAA) is
reported and never fails a run.

The verdict of a run: **fail** when a gate requirement failed;
**incomplete** when nothing failed but some gate requirement did not
apply (not a pass); **pass** otherwise.

### National contract tests

Every operation of the system's OpenAPI file (the aggregate's
`api/<system>/openapi.yaml`, or the system checkout's until the mirror
is pinned) is classified by the credential it takes: the ANSP's `x-auth`,
the authority's `x-scope` / `x-roles` / `x-session` / `x-receiver` /
`x-cis-delivery` / `x-dp`, `security` with the schemes in
`national/contracts/<system>.yaml`, and that file's scopes for what a
contract states in prose. An operation the suite cannot classify stops
the run: it is never silently left out.

| Requirement | Check |
|---|---|
| `NAT-UNAUTH` | no credential: 401 with `application/problem+json` matching `schemas/common/problem/v1` and the declared response |
| `NAT-SCOPE` | a valid token without the operation's scope: 403, same body |
| `NAT-NOTFOUND` | an identifier no system holds, well-formed for the parameter's schema: 404 where declared |
| `NAT-PRECONDITION` | a stale `If-Match` on a resource the target file names: the declared 409 or 412 |
| `NAT-INVALID` | a body the schema refuses (missing required members, a member of the wrong type): the declared 400 or 422 with `errors[]` naming the fields |
| `NAT-SUCCESS` | every read the suite can address: a declared 2xx whose body matches the contract; a WebSocket handshake: 101 |
| `REG-NOPII` | `validateRegistry` answers no member named in `policy.yaml`'s personal-data list |

A refusal answered 503 with the declared problem body (the operation is
switched off on that target, a console not configured) leaves the
refusal unobserved: not applicable. A 503 whose body is not the declared
problem is a failure.

### ED-318 publication tests (CISP)

The read half runs against any CISP. The publication half publishes a
two-zone fixture (`policy.yaml` `ed318_fixture`; one zone applying now,
one that ended yesterday) and **replaces the zones dataset**, so it runs
only with `ed318.publish: true` (`CONFORMANCE_PUBLISH=true`), on a
disposable stack.

### uss_qualifier

`uss_qualifier/coverage.yaml` maps F3411-SP, F3411-DP and F3548-SCD to a
configuration and the InterUSS automated-testing interface it drives;
F3548-CP and F3548-CM have no requirement set at the pinned commit and
are not applicable with that reason. A target that does not expose the
interface (its file's `interfaces`) is not applicable with that reason.
Reports from `uss_qualifier/run-qualifier.sh` are folded in with
`--qualifier-report <REQ>=report.json`; one of another InterUSS commit is
refused. See `uss_qualifier/README.md`.

### Report, signature, gate

`report/runs/<run>/report.json` (git-ignored) holds the target, the
commits of this repository and uspace-core, the images under test, the
pinned InterUSS commit, the contract's digest, the policy, every
requirement's status and every outcome. With `--sign-key` (the lab
issuer's `deploy/local/signing-key.pem`) the run writes `report.json.sha256`
and `report.json.jws`, an RFC 7797 detached JWS over the report's exact
bytes; `conformance verify --report R --jwks deploy/local/public/jwks.json`
checks it. The format is proposed (L-Q12, pending GCAA).

With `conformance/baseline/<name>.json` present, `make conformance`
judges the run against it: a gate requirement that fails without the
baseline recording that failure, or that passed in the baseline and does
not pass now, is a regression and the run exits 1. A known failure in a
baseline must say where it is tracked. `conformance baseline --report R
--out conformance/baseline/<name>.json --note ID=where` writes one from a
reviewed report.

## CI

`.github/workflows/conformance.yml`: on every change to the suite, its
tests with the systems' pinned contracts, the uss_qualifier
configurations validated by the pinned image, the axe runner's test,
and the CISP's conformance target (its chaos stack at the commit `PINS`
names) gated on `baseline/cisp.json`. On `workflow_dispatch` and on a
system's `repository_dispatch` (`system-release`, payload `{system,
ref}`), the suite against that system's target.

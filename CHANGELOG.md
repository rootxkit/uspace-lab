# Changelog

All notable changes to uspace-lab. One entry per work package pull
request (docs/PLAN.md §5).

## [Unreleased]

### Changed

- WP-L4: the spec errata of decision record §3 L4 applied to `docs/spec/`
  (`00`, `02`, `03`, `04`, `05`, `06`, `07`, `09`), each with a dated row
  in the file's errata table: goose and its version tables, `DAR` + 4,
  core's ED-318 metadata, the `geodesy/cell` grid, projection tables and
  one Timescale container per system, the audience, session, cookie,
  mTLS, problem and JWS conventions, the F2/F3/F4/F13 endpoints, the
  scope catalogue and the schema-ownership rule. Also the contract
  errata the system PRs found: a `409` is permanent, occurrence
  idempotency, content binding of a CISP snapshot to its signed version,
  the DSS's issuer keys, and client ids that keep the USSP code's case.
- WP-L4: `alert_lifecycle.json` no longer names `cisp` as an owner of
  two cases (cisp Q18). Regenerated; no input or expected value moved.

### Added

- WP-L9 (L-M3): the chaos matrix (`scripts/chaos/`, `make chaos`,
  `chaos.yml`, `docs/RUNBOOKS/chaos.md`). One script per failure domain
  of spec 05 §6 (`inject`/`restore`, each saying what it did and when,
  exit 0 only when the act took effect), and a harness that runs every
  row of `scripts/chaos/matrix.yaml` against the systems stack under a
  standing zone alert (`scripts/chaos/background.yaml`): each fault
  observed in place by the harness itself (a row whose fault never
  happened fails), the spec's degraded states read from every system's
  readiness and the consoles' status, recovery within bounds, and the
  alert kept, never duplicated, open again after every row. The figures
  GCAA has not answered are the spec's defaults, listed as pending GCAA.
  `results/20261005-chaos` is the observed matrix.

- WP-L7 (L-M4): the conformance suite (`conformance/`, `cmd/conformance`,
  `make conformance TARGET=<name>`, `conformance.yml`). It runs against
  a system's conformance target and reports pass, fail or not applicable
  per requirement of `conformance/requirements.yaml` in one signed JSON
  report: the national contract tests (every operation of the system's
  OpenAPI file classified by its credential, fail closed, and exercised
  both ways: 401, 403, 404, stale `If-Match`, invalid bodies with
  `errors[]`, the success case against the contract, no personal data
  in the registry validation), the CISP's ED-318 publication tests
  (schema, vertical references, `?at=` / `?applies_at=` against
  uspace-core's judgement, versions and `ETag`, `/v1/changes`, the
  signed webhook, the heartbeat and stale rule), uss_qualifier at
  InterUSS v0.36.0 (`f3411-sp`, `f3411-dp`, `f3548-scd`, the mock USS,
  each configuration validated by the pinned image), and an informative
  axe run. The defaults GCAA has not answered are in
  `conformance/policy.yaml`, marked pending GCAA. CI runs the suite's
  tests both ways and the CISP's chaos stack gated on
  `conformance/baseline/cisp.json`; dispatch and system releases run a
  named target. The first CISP run found a defect (its 503
  `console_unavailable` body has no `errors[]`); the onboarding
  rehearsal with sim-ussp and a uss_qualifier pipeline run are in
  `conformance/report/records/`.
- WP-L7: the lab issuer issues the InterUSS automated-testing scopes
  (`rid.inject_test_data`, `dss.read.identification_service_areas`,
  `interuss.flight_planning.*`) to `lab-01` only.

- WP-L2: `DSS_PUBLIC_KEY_FILES` lists the keys the DSS trusts
  (`-public_key_files`), defaulting to the lab issuer's key as before. A
  deployment lists the authority's token key beside it, so the DSS
  accepts the ecosystem issuer's tokens (plan D5's open item: on the
  staging droplet the DSS refused every authority-issued token).
- WP-L5 (KT-4): SITL, the simulators and the scenario runner.
  `sim/`: `run_sitl.sh` / `stop_sitl.sh` (`make sim N=…`, `make sim-down`;
  per-instance working directories, a refusal on a port another fleet
  holds, a teardown that says what it stopped and exits 0 when it
  worked), `mav_reader.py` (receive only; every field checked against
  pymavlink 2.4.49 at import; `sim/vehicle/v1`), `fly.py` (the only send
  path, towards SITL only, every step confirmed by the vehicle), their
  tests and the send guard (`sim.yml`). Go: the vehicle stream and a
  synthetic stand-in for SITL (`internal/vehicle`); `sim-operator` (F5
  telemetry over WS or batch with backlog replay, intents),
  `sim-receiver` (signed ODID batches through uspace-core's encoder),
  `sim-ansp-feed` (track/manned/v1 stream and snapshot, stale and outage
  knobs), `sim-adsb` (aircraft.json), `sim-ussp` (the peer: ISA and
  operational intent reference in the DSS, `/uss/flights`,
  `/uss/v1/operational_intents`); the scenario format, the runner
  (`cmd/scenario`, `make scenario`) and the reference target it proves
  itself against in CI (`scenarios.yml`); the KT-4 baseline, SC-01,
  SC-02, SC-03, SC-21, SC-22 and the owed system scenarios (ussp WP-7,
  WP-10, WP-12; ansp INV-02; authority SC-08) with their results.
  uspace-core v1.3.0. `sim-ussp` against the real lab DSS: compose
  profile `sim`, `make sim-ussp-up` (writes read back from the DSS, in
  the `lab-stack` workflow), `--client-secret-json` and `--jwks-file`;
  an operational intent reference's 201 is accepted, its key carries
  the OVNs of the references in its volume, and its id is new per run.
- WP-L2: the lab stack's DSS and issuer. `deploy/compose.yaml`
  (profiles `dss` and `issuer`, one internal network, nothing published,
  consumable as an include): the InterUSS DSS v0.23.0 (commit
  `af0648f5`, `deploy/dss/SOURCE`) and CockroachDB v24.1.3, both by
  digest, with the DSS trusting the lab issuer's public key and
  accepting the audiences `dss` and `DSS_PUBLIC_HOST`. `cmd/lab-issuer`
  and `internal/issuer`: `POST /oauth/token` (client credentials,
  `audience` or RFC 8707 `resource`, the host audience rule of M18),
  `/.well-known/jwks.json`, `/healthz`; clients from
  `deploy/issuer/clients.yaml`; key and secrets generated at first start
  into `deploy/local/` (git-ignored) and kept across restarts; refusals
  as `problem/v1`, counted. `make dss-up` proves the pair (401 without a
  token, 200 with one, 401 for another audience); `make dss-down`
  removes it and checks nothing is left. Memory measured idle: about
  350 MiB. The `lab-stack` and `gitleaks` CI workflows.
- WP-L1 (KT-2, KT-3 check): the contracts aggregate, day-one half.
  `schemas/common/` with the eight common schemas (`envelope/v1`,
  `track/telemetry/v1`, `source/status/v1`, `zone/applicable/v1`,
  `console/status/v1`, `console/snapshot/v1`, `console/subscribe/v1`,
  `problem/v1`), each with valid and invalid examples; the mirror layout
  `schemas/<system>/` and `api/<system>/` with `SOURCE` (all four
  unpinned: no system skeleton is published yet); `scripts/pin.sh`,
  `check-mirrors.sh`, `check-layout.sh`, `validate-examples.sh`,
  `gen-clients.sh` and their fixture test `test-scripts.sh`; the
  generated `api/index.md`; the enumeration pin against uspace-core
  v1.0.0 (`provider` under `identification.basis` pending core v1.1.0);
  the `contracts` CI workflow.

### Fixed

- WP-L9: `deploy/demo-up.sh` starts the ANSP and waits for its JWKS
  before the CISP (cisp-api refused to start on a fresh stack);
  `deploy/demo.env.example` pins a USSP image whose token client keeps
  the code's case; the scenario recorder reads the alerts a console
  snapshot carries, and shows a caller every event as recorded.

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

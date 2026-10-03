# Changelog

All notable changes to uspace-lab. One entry per work package pull
request (docs/PLAN.md §5).

## [Unreleased]

### Added

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

# Changelog

All notable changes to uspace-lab. One entry per work package pull
request (docs/PLAN.md §5).

## [Unreleased]

### Added

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

# WP-L1: `contracts-aggregate` (KT-2, KT-3 check)

Branch `feat/WP-L1-contracts-aggregate`. Milestone KT-2. Owns `schemas/`
(including `schemas/common/`, which this repo owns outright), `api/`,
`scripts/check-mirrors.sh`, `scripts/check-layout.sh`,
`scripts/validate-examples.sh`, `scripts/pin.sh`, the `contracts` CI
job and the `Makefile` targets `examples`, `mirrors`, `layout`. Depends
on `docs/PLAN.md` only for the day-one half; on each system's WP-0
skeleton for its mirror. Consumers: every repo (decision record M11,
M14, M29, M31), `uspace-ui` WP-14 (fixtures from the examples), WP-L7
(the national contract tests read the aggregate).

This WP starts on day one and never finishes in one PR: the common
schemas and the layout land first; each mirror lands in its own `build:`
PR as the owning repo publishes its skeleton.

## Read first

1. `docs/PLAN.md §1` D3, `§3` in full, `§6`.
2. `docs/decisions/2026-10-02-cross-plan.md`: M14 (ownership rule),
   M28 (problem body), M29 and Appendix C (the console frame), M12
   (`console/status/v1` as the feed status), M17 (`cis_applicability`
   values), M11 and M31 (the copy + `SOURCE` + diff mechanism this
   aggregate replaces), §4.3 last bullet (pins bump in `build:`
   commits).
3. Spec `04 §1` (schema rules, `$id`, mirror rule), `04 §2` (envelope
   fields, trust and source classes), `04 §3` (every catalogued message:
   the common ones are defined here, the rest mirrored), `04 §4`
   (additive rule), `00 §7` (national API publication, the aggregate
   with index and clients), `02 §1` (conventions), `07` KT-2, KT-3.
4. `uspace-core/core`: `TimeSource`, `Trust`, `Severity`, `IdentStatus`,
   `IdentReason`, `IdentBasis`, `FieldError` (the enumerations the
   common schemas pin); `uspace-core/vectors/testdata/VERSION` and
   `scripts/check-vectors.sh` (the pinning pattern to copy).
5. `uspace-ui` plan `§6.3` (the console frame as the kit consumes it)
   and `§3.1` `Problem`, `IdentBasis` with `provider` (Q-A8).
6. LESSONS E-01, E-02, E-14 (a mirror is a copy, not a fork).

## What to build

### Day one: `schemas/common/`

One directory per schema, `schema.json` (JSON Schema 2020-12,
`$id = https://schemas.uspace.ge/<family>/<name>/v1.json`,
`additionalProperties` left open inside a major per `04 §4`, but every
enumeration closed), `examples/*.json` (two or more, each a complete
message as a producer would emit it) and `examples/invalid/*.json` (one
or more, each named for the rule it breaks: `missing-msg-id.json`,
`unknown-time-source.json`). Schemas and their binding fields:

- `envelope/v1`: the `04 §2` fields; `body` object whose shape is
  named by `schema`. `producer` pattern `<system>/<process>` or
  `<system>-<n>/<process>-<n>`.
- `track/telemetry/v1`: the `04 §3` row plus `identification`
  (`status`, `reason`, `mismatch`, `basis` with `provider` added), `cell`
  as `c5:<lat_idx>:<lon_idx>` (M35; until core ships `geodesy/cell` the
  pattern is the contract).
- `source/status/v1`: `source`, `source_instance`, `state` ∈ `live`,
  `stale`, `disabled`, `down`, `unknown`; `since`, `age_s`,
  `disabled_by` (never merely silent, `02 F9`), counters object.
- `zone/applicable/v1`: ED-318 `identifier`, `type`, `applies` bool,
  `cis_applicability` ∈ `applies`, `not_applicable`, `unknown`,
  `version`, `valid_from`, `valid_to`.
- `console/status/v1`, `console/snapshot/v1`, `console/subscribe/v1`:
  exactly Appendix C and M29, the optional extras listed by name
  (`datasets{}`, `projection_age_s`, `cis_version`, `cis_age_s`,
  `nats`, `resync_since`, `dp_state`). `console/status/v1` carries the
  thresholds (`stale_after_s`, `live_max_age_s`): the kit never defaults
  them (ui Q6).
- `problem/v1`: M28; `errors[]` capped at 100 with `truncated`; `type`
  pattern `https://schemas.uspace.ge/problems/[a-z_]+`.

`scripts/validate-examples.sh`: every `examples/*.json` validates
against its schema, every `examples/invalid/*.json` fails (E-01, both
directions; a schema with no invalid example fails the script). A Go
test `schemas/schemas_test.go` does the same offline with the module's
validator so `go test ./...` is the whole proof, and a second test reads
`uspace-core/core` at the pinned tag and compares every enumeration
(`time_source`, `trust`, `identification.status`, `reason`, `basis`)
value for value.

### Day one: the layout and the pinning tool

`schemas/<system>/` and `api/<system>/` directories with a `SOURCE`
file each (`repo`, `commit`, `path`, `fetched_at`) and `README.md`
saying the directory is a mirror. `scripts/pin.sh <system> <commit>`
fetches the owning repo's `schemas/` and `api/openapi.yaml` at that
commit (`gh api` or a sparse clone), writes the copies and `SOURCE`,
and prints the diff. `scripts/check-mirrors.sh` re-fetches each mirror
at its pinned commit and fails on any byte difference (online; required
on `main`, best-effort on PRs, printed either way). `scripts/check-
layout.sh` (KT-3) confirms the skeleton directories of `docs/PLAN.md
§3.3` exist at the pin.

`api/index.md` is generated: one row per system with the OpenAPI
`info.version`, the pinned commit and the endpoint groups of `02 §3`.
`api/clients/go/<system>/` is generated with oapi-codegen v2 (client
only, pinned tool version in `go.mod` `tool` directive) and
`api/clients/ts/<system>.d.ts` with `openapi-typescript` (switch to the
kit's `uspace-ui-gen-api` when it ships, ui Q14). Generated files are
committed and CI fails if regeneration differs.

### As each skeleton publishes

One `build(contracts): pin <system> at <short commit>   [WP-L1 KT-2]`
PR per system, each with the regenerated index and clients. The first
pins are the CISP's (its skeleton is what the authority and the ANSP
copy, decision record §4.3), then the others in the order they land.
Until a mirror exists, the owning repo's own copy + `SOURCE` + CI diff
mechanism (M11) is the contract; this WP replaces it, it does not
precede it.

## Tests

- `schemas_test.go`: valid examples pass, invalid examples fail, every
  schema has both; enumerations equal core's at the pinned tag.
- `api_test.go`: every mirrored `openapi.yaml` parses as OpenAPI 3.1;
  every path in `02 §3` for that system exists in its file (a table in
  the test, not a guess: the row text of `02 §3` is the source).
- `scripts/` run in CI on an empty mirror set (day one) and print what
  they checked (E-04: "0 mirrors checked" is a visible line, not a pass
  in silence).

## Done when

- [ ] `make lint` and `go test -race -shuffle=on ./...` clean.
- [ ] The eight common schemas exist with valid and invalid examples;
  `make examples` passes; the enumeration check passes against core
  `v1.0.0` (and lists `provider` under `basis` as pending core v1.1.0,
  visibly).
- [ ] `scripts/pin.sh` run against `uspace-core` itself as a dry run
  (any file) proves the fetch and the `SOURCE` write; `check-mirrors`
  passes on `main` with the mirrors that exist and prints their count.
- [ ] The first system mirror (CISP) pinned in its own PR, index and
  clients regenerated and committed.
- [ ] `uspace-ui` told (PR comment or issue) that `schemas/common/`
  exists at commit X so WP-14 can wire the fixtures.

## Commits

`feat(contracts): define the envelope and the console frame schemas with examples   [WP-L1 KT-2]`,
`feat(contracts): define track/telemetry, source/status, zone/applicable and problem   [WP-L1 KT-2]`,
`test(contracts): validate every example both ways and pin enumerations to core   [WP-L1 KT-2]`,
`feat(contracts): add the mirror layout, pin.sh and the mirror and layout checks   [WP-L1 KT-3]`,
`ci: run the contracts job on every change   [WP-L1 KT-2]`,
`build(contracts): pin cisp at <commit>   [WP-L1 KT-2]` (one per system, later).

# WP-L9: `chaos` (L-M3)

Branch `feat/WP-L9-chaos`. Milestone **L-M3**. Owns `scripts/chaos/`
(one script per failure domain), `make chaos`, `docs/RUNBOOKS/chaos.md`.
Depends on WP-L6 (the stack and the demo scenario as the background
traffic). Consumers: `05 §6` evidence; the systems' runbooks (ussp
WP-19 `chaos.md` runs the USSP's rows with these scripts; this WP runs
the cross-system matrix); WP-L8 (reuses the scripts).

## Read first

1. `docs/PLAN.md §1` D8.
2. Spec `05 §6` (the failure-domain table: each row is a test with its
   "fails alone" and "everyone else" columns), `02` every failure
   column (the degraded behaviour each flow promises), `05 §5`
   (backpressure counters), `07` L-M3.
3. `knowledge/scenarios.md` SC-08 (switching sources), SC-14 (drain),
   SC-15 (stalled adapter), SC-16 (provider switched off), SC-18
   (storage down), SC-22 (missing inputs visible); LESSONS E-02 ("take
   the dependency away and watch what happens"), E-04, B-03..B-16,
   T-05.
4. The systems' `console/status/v1` extras (`degraded[]`, `sources[]`,
   `projection_age_s`, `cis_age_s`, `nats`, `dp_state`): the chaos
   assertions read these, because `05 §6` promises "with age shown".

## What to build

- `scripts/chaos/<domain>.sh`: pause or stop one container or one
  network for the stated time, then restore: an ingest adapter instance;
  one system's `api`; one hot-path process (`monitor`, `rid-sp`,
  `detect`, `deliver`, `manned-feed` in turn); one source instance
  disabled through the console API (an audited act, SC-08) and one
  dead; NATS of one system for 60 s; TimescaleDB of one system;
  PostgreSQL of one system; the CISP for 5 min; the authority for
  5 min; the DSS for 5 min; the ANSP feed; the token issuer; the whole
  stack (restart). Each script prints what it did and when (E-04).
- `make chaos`: runs `demo.yaml` as background traffic and each domain
  in turn, collecting before/during/after: the `console/status/v1`
  frames of every system (the promised `degraded`, `stale`, `age`
  values present within the promised time), the counters (`dropped_*`,
  `gap_*`, `degraded_*`, backlog replayed), the alerts (no duplicate
  raise after restart, no missed raise during the domain's "everyone
  else: unaffected" claim), and the recovery time (picture back within
  10 s after a process restart). Assertions are the `05 §6` and `02`
  claims, cited per row; a deviation is a finding, filed on the owning
  repo.
- `docs/RUNBOOKS/chaos.md`: the matrix with "observed" wording per
  cell (never "expected"), the run's digests and commits (E-05), and
  the findings.

## Done when

- [ ] Every row of `05 §6` run against the stack; the runbook filled
  with observed values; findings filed with counters.
- [ ] The three "nothing hidden" checks hold in every row: no track
  disappears without `stale` / `source_disabled`, every disable is an
  audited act, every age is shown (SC-22 wording).
- [ ] `make chaos` is repeatable on the droplet and on a laptop; the
  time and disk it needs are stated.

## Commits

`feat(chaos): script every failure domain of 05 section 6   [WP-L9 L-M3]`,
`feat(chaos): assert the promised degraded states, counters and recovery   [WP-L9 L-M3]`,
`docs(chaos): record the observed matrix   [WP-L9 L-M3]`.

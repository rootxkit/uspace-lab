# WP-L8: `load-test` (L-M2)

Branch `feat/WP-L8-load-test`. Milestone **L-M2**. Owns `cmd/loadgen`,
`internal/load`, `make load`, `load/` (tier definitions, the report
format, `docs/RUNBOOKS/load.md`). Depends on WP-L5 (the simulators are
the load sources), WP-L6 (the stack and the expected-event machinery).
Consumers: the Protobuf decision of `04 §1` (revisited at 5000), the
sizing rows of `05 §4` and `05 §7`, the systems' `docs/RUNBOOKS` (they
cite the lab's numbers), L-Q1.

## Read first

1. `docs/PLAN.md §1` D8, `§7.1` L-Q1.
2. Spec `05 §7` in full (the table is the report: every row a pass
   criterion with its figure), `05 §1` (volumes at 100 / 1000 / 5000),
   `05 §4` (capacity on the droplet; when a system needs its own
   database host), `05 §5` (every backpressure point; the counters it
   names are what "no silent loss" sums), `02` frequency columns,
   `04 §1` (the format decision), `07` L-M2.
3. `knowledge/scenarios.md` SC-14 (drain against intake: the predecessor
   measured; the new systems must beat 5× intake), SC-20 (soak and the
   missed-alert count), LESSONS B-01..B-16, C-15..C-19, E-05, E-10.
4. The systems' plans: their stated budgets (authority ≤ 1.2 GB, CISP
   ≤ 0.6 GB, ANSP ≤ 0.6 GB, ...) and their `/metrics` names for
   `dropped_*`, `gap_*`, `degraded_*`, `evaluation_period_s`, writer
   queue depth; the report reads them by name.

## What to build

- `cmd/loadgen`: drives N simulated operators (F5 telemetry at 1 Hz,
  intents at 2–3 per drone-day compressed into the run), M receivers
  hearing K aircraft each (F9, BT4 legacy and BT5 packs), the ANSP feed
  at tens of aircraft, and `sim-ussp` as the second USSP, from
  synthetic paths over the demo area (`load/paths.yaml`: random walks
  inside the designation with scripted conflicts so the expected-alert
  set is known) rather than SITL (SITL does not scale to 1000; the
  bridges' wire shapes are reused, so what the systems receive is the
  same). Tiers `load/tiers/{100,1000,5000}.yaml`: counts, duration
  (60 min; 2 h soak at the target tier), the expected-event rate.
- Measurement: ingest-to-picture latency from the generator's `ts` to
  the console frame's arrival (`captured_at` → frame), F3411 timings
  from the DP poller's view of `/uss/flights`, F3548 timings from the
  peer's notification receipt, alert latency from the triggering
  sample to the raise, every counter of `05 §5` before and after
  (accepted + dropped = sent per source), CPA `evaluation_period_s`,
  writer queue depth, disk growth against the `05 §4` model, memory
  over the soak, restart (each process killed once mid-run: picture
  back within 10 s, no duplicate alert, no lost backlog), NATS
  partition for 60 s, cross-system outages 5 min each (the chaos rows
  that `05 §7` lists; WP-L9 owns the full chaos matrix and this WP
  reuses its scripts).
- The report: `results/load/<run>/report.json` and a Markdown
  rendering with the `05 §7` table filled with observed p50/p95/p99 and
  pass/fail per row, the tier, the image digests, the host size, the
  commits (E-05); "not measured" is a visible value, never a blank.
- `make load TIER=100|1000|5000`; `docs/RUNBOOKS/load.md` (how to run,
  how long, how much disk, how to read the report).

## Done when

- [ ] 100 and 1000 run for 60 min with every `05 §7` row observed and
  passing, 2 h soak at 1000 with no monotonic memory growth; reports
  in the PR.
- [ ] 5000 attempted; the report says what broke first and where
  (CPU per partitioned consumer, writer, broker), and the `04 §1`
  Protobuf question is answered from the measured hot-path CPU share,
  as a proposal to the owner, not a change.
- [ ] Every failing criterion filed on the owning repo with the
  numbers and the counters.
- [ ] The host used is named (the droplet, resized or not, or a second
  host) so the numbers are comparable (L-Q1).

## Commits

`feat(load): generate operators, receivers, a manned feed and a peer at a tier   [WP-L8 L-M2]`,
`feat(load): measure every 05 section 7 row and the no-silent-loss identity   [WP-L8 L-M2]`,
`feat(load): render the report with observed percentiles and pass per row   [WP-L8 L-M2]`,
`docs(load): record the 100, 1000 and 5000 runs   [WP-L8 L-M2]`.

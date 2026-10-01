# WP-L6: `scenario-suite` (L-M1)

Branch `feat/WP-L6-scenario-suite`. Milestone **L-M1** (the lab's first
demo). Owns `scenarios/` (the full suite beyond WP-L5's first five),
`make demo`, the `systems` compose profile, `cmd/results` (the results
dashboard), `scenarios.yml` (nightly and on dispatch), `docs/RUNBOOKS/
demo.md`. Depends on WP-L5 and on the four systems' images from GHCR
(their first milestones: C-M1, A-M1, S-M1, N-M1; see decision record
§4.2 wave 6). Consumers: the owner and GCAA (the demo), every system's
safety-relevant WP (INV-02 evidence), WP-L8, WP-L9.

## Read first

1. `docs/PLAN.md §1` D6, D8, `§4` (`make demo`), `§7.1` L-Q1, L-Q4.
2. Spec `07` L-M1 done-when (every planned event: zone alert,
   conformance breach, proximity, unregistered broadcast, restriction
   activation, 120 m violation), the system milestones whose done-when
   name a lab scenario (A-M2, A-M3, S-M2, S-M3, S-M4, N-M1, N-M2),
   `05 §6` deployment paragraph, `02` failure columns.
3. `knowledge/scenarios.md` SC-04..SC-21 (SC-01..03 and SC-22 are
   WP-L5's); the system plans' scenario rows (authority `§9`
   "Scenarios", ussp `§10` "Scenarios (lab, SITL)", ansp WP-13) for
   what each expects the lab to run and record.
4. `docs/deploy/PLAN.md` WP-D1 (the same compose includes run on the
   droplet; `make demo` must not diverge from them).
5. LESSONS INV-02, E-02, E-04, E-05, D-02, C-03, C-10.

## What to build

- The suite: one YAML per scenario of `scenarios.md` (SC-04 with the
  Copernicus tile for N42E044 fetched by the scenario, SC-05..SC-21),
  plus the `07` done-when runs that are not SC scenarios: `s-m2-
  conformance.yaml` (leaving the volume, thresholds, height above the
  authorised upper, `nonconformance_nearby`, ANSP acknowledgement, peer
  `Nonconforming`, lost link), `s-m3-traffic.yaml` (SITL vs SITL, SITL
  vs manned track, e-conspicuity source), `s-m4-peers.yaml` (second
  USSP conflict within 1 s, `pending_dss`, peer purge at 24 h with a
  clock the scenario can move or a shortened retention the system
  exposes for the lab only, documented), `n-m1-restriction.yaml`
  (restriction over a SITL aircraft: CISP within 1 s, DSS constraint,
  `restriction_activated` within one tick, authority shows it),
  `a-m2-picture.yaml` (four identification statuses), `a-m3-
  violations.yaml` (120 m with DEM, zone incursion, unregistered,
  mismatch; raised and closed). Each expectation's window comes from
  the spec figure it tests, cited in the file.
- `demo.yaml`: the L-M1 script, every planned event in one 10-minute
  run, narrated in `docs/RUNBOOKS/demo.md` step by step with what the
  consoles show.
- `make demo`: `dss-up`, the `systems` profile (four systems from GHCR
  by digest from `deploy/images.env`, their `migrate` one-shots, the
  basemap volume from the WP-L3 release), the lab issuer as the issuer
  for every system (`*_ISSUERS` env), seed data through the public
  APIs only (an authority-role client publishes the demo zones and the
  U-space designation to the CISP; the registry rows through the
  authority API), SITL, the simulators, then `demo.yaml`. Prints the
  console URLs. `make demo-down`. Measures and prints the stack's
  memory and CPU at steady state (L-Q1 evidence).
- `cmd/results`: reads `results/**.json`, serves one page per run
  (scenario, pass/fail, latencies, counters, image digests), and the
  trend over runs; static HTML from the kit's theme when practical,
  otherwise plain; served at `uspace-lab.chikox.net` by WP-D1.
- `scenarios.yml`: nightly and on dispatch, runs the suite against
  the digests in `deploy/images.env` with SITL in a container, uploads
  `results/` as an artifact, and fails on any miss; a system's PR can
  dispatch it with its own image digest (the INV-02 evidence path).

## Done when

- [ ] `make demo` on a clean checkout with Docker brings up everything
  and `demo.yaml` passes: every planned event raised and cleared, the
  result file and the runbook in the PR; the measured footprint
  recorded (and compared with the droplet in `docs/PLAN.md §7.1` L-Q1,
  stated as measured, not assumed).
- [ ] Every SC scenario has a YAML and a result (pass, or a named
  failure filed as an issue on the owning repo with the result file);
  SC-15, SC-20, SC-21 (never passed or run in the predecessor) run and
  their outcome written in `scenarios.md`'s status column.
- [ ] `scenarios.yml` green nightly for a week before L-M1 is called.
- [ ] The results page shows the runs and is reachable on the droplet.

## Commits

`feat(scenarios): write the SC-04..SC-21 scenarios with cited windows   [WP-L6 L-M1]`,
`feat(scenarios): write the milestone scenarios of 07 for the four systems   [WP-L6 L-M1]`,
`feat(deploy): compose the four systems from GHCR digests with the lab issuer   [WP-L6 L-M1]`,
`feat(scenarios): make demo runs the whole stack and the demo scenario   [WP-L6 L-M1]`,
`feat(results): serve the scenario results and their trend   [WP-L6 L-M1]`,
`ci: run the scenario suite nightly and on dispatch with an image digest   [WP-L6 L-M1]`,
`docs(scenarios): narrate the demo and record the first full run   [WP-L6 L-M1]`.

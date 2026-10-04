# 20261005-systems-wp12: ussp-wp12-restriction on the planned-state fix

`ussp-wp12-restriction` after lab #18, against the ANSP's main image
and a USSP image built from the uspace-ussp branch
`fix/WP-12-planned-restriction` (a04638c), which is not merged. Same
stack (deploy/systems/, docs/RUNBOOKS/demo.md), seeded through the
public APIs (cmd/demo-seed: registry, uspace, ussp, ansp, sessions; the
U-space airspace about the origin), ArduCopter 4.5.7 SITL in WSL flown
by sim/fly.py. 2026-10-04, 20:59 to 21:09 UTC, Windows 11.

| System | Image | Commit |
|---|---|---|
| authority | `ghcr.io/rootxkit/uspace-authority@sha256:8663cac1…` | as in 20261004-systems-wp12-headers |
| CISP | `ghcr.io/rootxkit/uspace-cisp@sha256:a37ab317…` | as in 20261004-systems-wp12-headers |
| USSP | `uspace-ussp:a04638c` (local build, image id `sha256:a64fa812…`) | a04638c, `fix/WP-12-planned-restriction` |
| ANSP | `ghcr.io/rootxkit/uspace-ansp@sha256:774c15b9…` | 08731a3 (main) |
| DSS | `interuss/dss:v0.23.0@sha256:0781042b…` | deploy/dss/SOURCE |

The runner was built from fd375e8 (this branch: the scenario judges
the clear). `lab_dirty` is true: the build saw this directory's
untracked result files, nothing else.

## Why it was re-run

Re-run after lab #18 against ANSP main (USSP
`ghcr.io/rootxkit/uspace-ussp@sha256:4c2baccc…`), the scenario failed
with one missed alert and one false one. The ANSP answered the plan
step 201. The USSP raised `restriction_activated` at +30.2 s, the moment
of the plan step, with `cis_version` `restrictions:1`, the planned
version. The scenario expects it within 10 s after the activate step at
+40 s.

A planned restriction is not in force: spec 02 F2 and 03 §4 name the
states planned, active, ended and cancelled. The ANSP keeps a scheduled
activation planned until its ticker publishes the active version (ansp
PLAN Q31). The CISP leaves a planned restriction past its `starts_at`
planned (cross-plan cisp Q2). The ANSP and the scenario were right. The
USSP refused every restriction except an ended or a cancelled one, so
its standing re-check raised the alert on the planned version
(uspace-ussp PLAN §15.1 Q29).

## Verdicts

| Run | USSP | Verdict | What decided it |
|---|---|---|---|
| `../20261005-systems-wp12-planned` | the same branch's code, built before its commits (`uspace-ussp:wp12-planned`); scenario not yet committed | **PASS** | No alert at the plan step (+30 s). `restriction_activated` raised +40.1 s, cleared `resolved` +102.0 s. Missed 0, false 0. |
| this directory | `uspace-ussp:a04638c` | **PASS** | Plan +30.0 s: nothing. Activate +40.0 s: `restriction_activated` raised 0.11 s later (`restrictions:5`, decision marked, withdrawn true). End +100.0 s: cleared `resolved` 0.15 s later. `zone_incursion` on the restriction raised +40.4 s and cleared `not_reconfirmed` +100.4 s. Missed 0, false 0. 179 samples sent, 179 accepted. |

## Procedure, as run

1. `deploy/demo-up.sh` with deploy/demo.env set to the images above.
2. `demo-seed --steps registry,uspace,ussp,ansp`, then `--steps sessions`.
3. `sim/stop_sitl.sh; sim/run_sitl.sh -n 3` in WSL, then
   `deploy/labns.sh deploy/local-demo/bin/scenario run --targets
   targets/local-demo.yaml --vehicles sitl --lab sim/sitl.env --run
   <run> scenarios/ussp-wp12-restriction.yaml`.
4. For the second run: the USSP image swapped with demo-up.sh, fresh
   sessions, SITL restarted, at least 130 s after the first run ended.
5. `deploy/demo-down.sh --purge` and `sim/stop_sitl.sh` at the end.

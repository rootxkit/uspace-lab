# 20261005-chaos: the WP-L9 matrix, run 3

Every row of `scripts/chaos/matrix.yaml` against the systems stack
(deploy/systems/, the USSP of uspace-deploy 4cf1f68), under the
background scenario `scripts/chaos/background.yaml` flown by the
synthetic stand-in. 2026-10-05, 07:32 to 08:58 UTC, Windows 11, Docker
Desktop; `make chaos`'s steps run by hand from Git Bash (no make on the
host): `deploy/demo-down.sh`, `deploy/demo-up.sh`,
`scripts/chaos/prepare.sh --fresh`, then
`go run ./scripts/chaos run --run 20261005-chaos --sessions-cmd '<the
seed's sessions step>'`, then `deploy/demo-down.sh`.

Verdict: FAIL, 18 rows pass and 10 fail, every failure a finding of a
system (F1 to F8 in `docs/RUNBOOKS/chaos.md`, which has the observed
matrix). Every fault was observed in place, held and observed gone.

| File | What it is |
|---|---|
| `chaos.json` | `chaos-result/v1`: per row the scripts' output, the fault as observed, each expectation with when it was met, the alert findings; the background's every raise and clear; the images and commits |
| `chaos.txt` | what the harness printed at the end |
| `samples/<row>.json.gz` | every sample of the row: each endpoint's readiness, docker's view of the targets, the fault held or gone |
| `chaos-background.json` | the background run (`result/v1`); its own verdict is recorded, not judged (the runbook says why) |
| `background.yaml` | the copy of the background scenario the run loaded (hold 5040 s) |

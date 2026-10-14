# 20261014T215032Z-chaos: the WP-L9 matrix against the newest images

Every row of `scripts/chaos/matrix.yaml` (30 rows), run against the
systems stack with the systems' newest published images. The images are
listed by digest in `chaos.json`: authority `sha256:53d6f97c…`, CISP
`sha256:2e786148…`, USSP `sha256:854cca71…`, ANSP `sha256:1fcfb3c9…`.
The rows ran under the background scenario `scripts/chaos/background.yaml`.
The run was on 2026-10-14, 21:50 to 23:20 UTC, from Git Bash on the dev
workstation (Docker Desktop, 16 CPUs, 7.7 GB). Lab `b5c9b83` with a
clean tree, uspace-core v1.6.0. `make chaos`'s steps were run by hand
(there is no make on the host): `deploy/demo-down.sh`,
`deploy/demo-up.sh`, `scripts/chaos/prepare.sh --fresh`,
`go run ./scripts/chaos run`, `deploy/demo-down.sh`.

Verdict: FAIL, 16 rows pass and 14 fail. Thirteen failures are the
systems' (docs/RUNBOOKS/chaos.md has the matrix). One, the
`authority-rid-disabled` row's audit check, was the harness's own: it
read only `source_disabled` and the authority audits the switch back on
as `source_enabled`. That was fixed in `f54b2bf`, and the row alone
passed against the same images in `../20261014T232332Z-chaos/`. Every
fault was observed in place, held and observed gone.

| File | What it is |
|---|---|
| `chaos.json` | `chaos-result/v1`: per row the scripts' output, the fault as observed, each expectation with when it was met, the alert findings, the loss counters that rose, the disable row's audit; the background's every raise and clear; the images and commits |
| `chaos.txt` | what the harness printed at the end |
| `samples/<row>.json.gz` | every sample of the row |
| `chaos-background.json` | the background run (`result/v1`); recorded, not judged |
| `background.yaml` | the copy of the background scenario the run loaded |

An earlier run on the same day (lab `5a8dcc2`) is not kept, because two
of its failures were the lab's. The authority's fault state, left by a
load run killed during its authority outage, made `authority-down`
refuse to start. That is fixed: `demo-down.sh` now removes it. And the
disable row restored its switch after 8 ms, because the switch's end
time was lost in a deferred write; that is fixed too.

# results

`results/<run>/<scenario>.json` (`result/v1`, internal/runner): the lab
and uspace-core commits, the images as configured, the policy, the
vehicles' confirmed steps, every observed raise and clear, the ledgers,
the latencies and the verdict. `<scenario>.txt` is what the runner
printed.

| Run | Vehicles | Targets | What it is evidence of |
|---|---|---|---|
| `20261003-synthetic-reference` | synthetic | reference | the runner and the simulators, end to end, as CI runs them (includes the deliberately failing self-test) |
| `20261003-sitl-reference` | ArduCopter 4.5.7 SITL (WSL), flown by sim/fly.py | reference | the same scenarios flown by SITL: KT-4 (both bridges count every sample), SC-01, SC-02, SC-03, SC-22 |
| `20261004-systems` | ArduCopter 4.5.7 SITL (WSL) | systems | the owed runs against the four images, the DSS and the lab issuer: wp10 and sc-08 pass; the rest fail, each with its cause (its README) |
| `20261004-systems-rerun` | ArduCopter 4.5.7 SITL (WSL) | systems | the same owed runs on the main images of 2026-10-04 by digest, after fix/scenario-defects: wp10, SC-01, SC-02, SC-21 and sc-08 pass; SC-22, wp7, wp12, inv02 and SC-03 fail, each with its cause (its README); `-uspace-origin` and `-wp12-try1` are two of its runs kept beside it |
| `20261005-systems-wp12` | ArduCopter 4.5.7 SITL (WSL) | systems | `ussp-wp12-restriction` raised and cleared: passes against ANSP main and a USSP built from the unmerged fix/WP-12-planned-restriction (a04638c). Before that fix it raised at the plan step (its README); `../20261005-systems-wp12-planned` is the same run on the pre-commit build |
| `20261004-systems-try1..3` | ArduCopter 4.5.7 SITL (WSL) | systems | earlier attempts kept as evidence of lab findings fixed on WP-L6 (SC-22 of try2 is that scenario's result: it needs a fresh authority) |
| `20261005-chaos` | synthetic | systems | the WP-L9 chaos matrix (scripts/chaos): every failure domain of 05 §6 injected and observed, 18 rows pass, 10 fail with the system findings of docs/RUNBOOKS/chaos.md (F1 to F8) |

The two reference runs are not evidence for a system: the reference target is the lab's own
stand-in (`"evidence"` in each file says so). The owed system runs
(scenarios/README.md) write their results here with `mode.targets:
systems` and the image digests.

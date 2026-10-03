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

Neither is evidence for a system: the reference target is the lab's own
stand-in (`"evidence"` in each file says so). The owed system runs
(scenarios/README.md) write their results here with `mode.targets:
systems` and the image digests.

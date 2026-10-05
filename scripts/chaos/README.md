# scripts/chaos (WP-L9, L-M3)

The failure domains of spec `05 §6` injected into the lab's systems
stack, one at a time, under one standing alert, with every promised
degraded state checked and every fault checked to have happened. The
procedure and the observed matrix are in `docs/RUNBOOKS/chaos.md`.

| File | What it is |
|---|---|
| `<domain>.sh` | One script per failure domain: `inject` and `restore`, each printing what it did and when, each exiting 0 only when the act took effect and 1 when the fault was not (or could not be) put in place. Usable on their own (ussp WP-19, WP-L8). |
| `lib.sh` | The primitives the scripts share: kill, stop, start, pause, a database refusing connections, a system's containers stopped in order, a system partitioned by a packet filter, the whole project stopped and started. |
| `net/Dockerfile` | The packet filter a partition runs in a container's network namespace (Alpine by digest, built locally on first use). |
| `matrix.yaml` | The rows: domain, arguments, the spec's claim and citation, hold, recovery bound, the expectations, and what the row promises the standing alert. The figures pending GCAA are listed under `pending`. |
| `background.yaml` | The background scenario: one aircraft holding inside a zone, so the USSP and the authority each hold one open zone alert through every row. |
| `prepare.sh` | Seeds a stack started by `deploy/demo-up.sh` for the background (`--fresh` on new volumes). |
| `*.go` | The harness: `go run ./scripts/chaos check|run|watch|skew`. |

## What a row is judged on

1. **The fault happened.** From docker's or PostgreSQL's side, never
   from the script's word: a killed or stopped container not running,
   then running with a later `StartedAt` (both on docker's clock); a
   paused one paused; a blocked database refusing a connection with
   PostgreSQL's own words while its host runs; a partition's filter
   present in every container of the system and dropping packets; a
   skewed receiver's batch leaving with the configured offset, measured
   on the request itself. A row whose fault was never observed, did not
   hold to the end of the hold, or was never seen restored fails,
   whatever the systems said (`TestARowWhoseFaultNeverHappenedFails`,
   `TestASkewThatNeverLeftFailsWhateverTheAnswer`).
2. **The promised degraded state**, from each system's readiness
   endpoint (sampled every second) and the authority's and the ANSP's
   `console/status/v1` (`degraded[]`, `sources[]`, `nats`, the ages):
   the faulted system says so within the row's bound, everyone else
   stays ready, and after the restore every check that was ok before is
   ok again within the recovery bound.
3. **The standing alert**, from every raise and clear the background
   run recorded: in a `kept` row it stays open under its id; in a
   `stale_ok` row (the fault starves the system of the aircraft's data)
   it may clear as `stale` or `source_disabled` and must be open again
   within `realert_within_s`; never two open at once, never cleared for
   another reason while the aircraft is inside, and open again after
   every row. The background's own verdict (one raise and one clear per
   system, no false alert) is the run's.

Nothing commands an aircraft (INV-01): the faults are the lab's own
containers, networks and databases, and the background flies the
synthetic stand-in.

## Bounds

Every hold, wait, probe, script, output and buffer is bounded: holds at
30 min, waits at 30 min, probes at 30 s, a script at 3 min, a script's
output at 200 lines, docker output at 4 MiB, console status frames at
20 000 per system, alert events at 10 000. The harness validates the
whole matrix before it injects anything, checks every target exists and
runs before each row, and runs a row's restore even when its injection
failed.

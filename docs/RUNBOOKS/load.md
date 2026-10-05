# Runbook: the load test

How the lab loads a target at the volumes of spec `05 §1` and fills the
`05 §7` table with what it observed (docs/WORKPACKAGES/WP-L8.md). The
runs recorded so far are in `results/load/README.md`.

## What it is

`cmd/loadgen` drives one tier (`load/tiers/<tier>.yaml`) against the
target a targets file names:

- one operator client per aircraft (`internal/simop`): `telemetry/v1`
  at 1 Hz over the USSP's `WS /v1/telemetry`, under the tier's machine
  clients; one intent filed and activated per aircraft before the run;
- receivers (`internal/simrx`) hearing 40 % of the aircraft, K each,
  alternating BT5 / Wi-Fi packs and BT4 single messages, posting
  `rid/observation/v1` batches every 100 ms;
- consoles on the authority picture, each subscribed to the whole area
  (`console/subscribe/v1`), the first one timed;
- the USSP traffic stream (`WS /v1/traffic?intent_id=`) of every
  watched aircraft: the scripted pairs and a few walkers.

The aircraft fly synthetic paths from the lab origin
(`load/paths.yaml`, offsets in metres; the origin is `sim/sitl.env`
through the targets file, INV-03). Walkers random-walk inside a disc
per grid cell, on four altitude layers laid so that no two can ever
come within the policy's CPA minima; pairs fly head on along a line and
cross every 180 s at an altitude no walker uses. The expected alert set
is therefore exact: one proximity raise per pair member per crossing,
and its clear. `loadgen check` and every run refuse a layout whose
separation the policy does not guarantee, before anything starts.

SITL does not scale to 1000 aircraft; the bridges' wire shapes are the
same ones the scenario runner uses with SITL, so what the target
receives is the same.

## How it measures

- **Latency** is end to end on one clock, the generator's: from the
  moment a sample is handed to the client that sends it to the moment
  the frame that first shows it arrives on a console. A frame is tied
  to its sample by the aircraft and its `captured_at`: on the picture,
  where the authority places a Remote ID sample at its broadcast time,
  the nearest sample within 500 ms; on the traffic stream, where the
  USSP places operator telemetry at receipt, the newest sample handed
  out by then (50 ms allowed for the clocks), so a frame that arrived
  late is timed late rather than tied to the next sample. The target's
  clock is never subtracted from the lab's; a systems run needs the
  hosts' clocks within that 50 ms (NTP), and says so. The clients' own share is bounded by `poll_ms` (50 ms) and
  `batch_ms` (100 ms) and included: the figures are upper bounds.
- **Alert latency** is from the sample a raise names (its
  `captured_at`, either aircraft of the pair) to the raise's arrival.
- **Missed alerts**: every expected raise must arrive in its window
  (crossing - `t_cpa_max_s` - margin to crossing + margin) and its clear
  within `clear_within_s` of the crossing. Any other proximity raise on
  a watched aircraft is a false alarm.
- **No silent loss**: each operator client's ledger (sent = accepted +
  refused + dropped + duplicate, from the socket's own status frames),
  each receiver's (sent = accepted + duplicates + refused), and, for
  the picture, every heard sample either shown on the timed console or
  inside a counted loss (refused observations, `dropped_frames`, the
  generator's and the receivers' sheds).
- A frame or a raise that names no sample sent is counted untraceable
  and fails the run: a load run that cannot tie what it saw to what it
  sent measures nothing.

## The verdict

`load/criteria.yaml` holds the `05 §7` rows as checks, each with its
figure and where the figure comes from: `pending GCAA` (the spec's
default for a national choice; GCAA has not answered), `standard`
(F3411 / F3548), `spec` (a design target of `05`) or `lab` (the
harness's self-check). The run passes only when every check the tier
requires was measured on at least its minimum number of samples and
passed, no measured check failed, and at least one check was measured
at all. A check the harness cannot measure yet is written "not
measured" with the reason; it fails the run only when the tier
requires it.

What the harness does not measure yet, in every report:

| Row | Why |
|---|---|
| F3411, F3548 timing | needs the systems mode: the authority's DP poller and a peer USSP against the lab DSS |
| Zone alert tick, Art. 13(2) notices | no zone entry scripted in the load paths yet; no peer or ANSP driven |
| CPA pair checks per worker | read from a system's `/metrics`; the reference target has none |
| Storage | the reference target has no database |
| Restart, partition, cross-system outage | the kill and outage scripts are WP-L9's |
| Vectors | run in uspace-core's CI and per image by the conformance suite |

## Running it

```
make load TIER=ci                                    # 120 s, what CI runs
make load TIER=100 LOAD_FLAGS='--host "<name, size>"' # 60 min
make load TIER=1000 LOAD_FLAGS='--host "<name, size>"'
make load TIER=1000-soak LOAD_FLAGS='--host "<name, size>"'   # 2 h
make load TIER=5000 LOAD_FLAGS='--host "<name, size>" --duration-s 600'
make load-check                                      # every tier file
```

A run opens the consoles and the traffic streams first, then connects
the operator clients at the tier's `ramp_per_s` (250 a second: 20 s for
5000), then starts the generator; five thousand sockets dialled at once
were refused by the listener and took a console with them. The ramp is
not part of the measured period.

`--host` names the machine and its size (L-Q1: numbers are comparable
only on a named host). `--duration-s` shortens a tier; the report then
lists the override as a scale factor. `--run` names the run; a run never
overwrites an earlier record. Exit status: 0 pass, 1 fail, 2 nothing ran
(a file or the target refused).

| Tier | Aircraft | Heard / receivers | Consoles | Watched | Duration | Generator + reference target, one process |
|---|---|---|---|---|---|---|
| ci | 100 | 40 / 4 | 2 | 12 | 120 s | see results/load/README.md |
| 100 | 100 | 40 / 4 | 10 | 24 | 60 min | see results/load/README.md |
| 1000 | 1000 | 400 / 20 | 30 | 45 | 60 min | see results/load/README.md |
| 1000-soak | 1000 | 400 / 20 | 30 | 45 | 2 h | see results/load/README.md |
| 5000 | 5000 | 2000 / 80 | 60 | 45 | 60 min | see results/load/README.md |

Disk: a report is well under 1 MB (the memory samples are one number
per 10 s). Against the reference target nothing else is written. Run
only locally or in CI; never against the staging droplet.

## Scale factors

Each tier file lists what it changes from `05 §1` / `05 §7` and why
(`scale:`), and what `05 §7` asks the load to include that the harness
does not drive (`not_generated:`: the ANSP feed, the second USSP on a
DSS, the authority's DP polls, the optional national push). The CI tier
keeps the 100-drone volumes and scales the duration (3600 s to 120 s,
factor 0.033) and the consoles (10 to 2); every tier's consoles
subscribe to the whole area, which is heavier than `05 §1`'s viewports
of at most 200 tracks. The report repeats all of it.

## Reading the report

`results/load/<run>/report.json` (`load-report/v1`) and `report.md`:

1. The verdict, how many checks were measured, and whether the run
   covers every `05 §7` row (`complete`; against the reference target it
   never does).
2. The target and its evidence statement: the reference target proves
   the harness and is never evidence for a system (docs/PLAN.md L-D4).
3. The host, the commits (uspace-lab, uspace-core, Go), the digests of
   the tier, paths, criteria and policy files, the policy itself.
4. The offered load against the tier's rates: a latency measured at a
   lower load proves nothing about the tier, so the rate checks are
   required.
5. The `05 §7` table: p50 / p95 / p99 / max and n per latency, the value
   per count, pass / fail / not measured with the reason.
6. The loss accounting, the alerts (expected, missed, unexpected, each
   matched raise with its latency) and every stream's frame counts and
   `dropped_frames`.

## Systems mode (not built yet)

A run against the systems' images needs, before the generator starts:
the tier's serials registered and bound to the operator clients at the
USSP, its receivers registered at the authority with their keys, and a
console session for the picture. `cmd/demo-seed` does this for the
scenario fleet; the load fleet is 100 to 5000 aircraft. Until it is
built, `loadgen run` refuses a targets file in systems mode rather than
load a system with traffic it would refuse. The systems' `/metrics`
names (`dropped_*`, `gap_*`, `degraded_*`, `evaluation_period_s`,
writer queue depth) and the F3411 / F3548 timings come with it.

Storage for a systems run (`05 §1`, `05 §4`): at 1000 drones each system
with a telemetry hypertable writes about 52 GB a day uncompressed, about
2.2 GB per hour; a 60-minute run of the authority and the USSP needs
about 5 GB free beside the images, the 2-hour soak about 10 GB, and
5000 drones five times that. It is not to be run on the staging
droplet; the owner adds storage to the host that runs it.

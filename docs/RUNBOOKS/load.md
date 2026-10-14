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
at all. A check the harness cannot measure is written "not measured"
with the reason; it fails the run only when the tier requires it. A
tier's `required` checks are those the reference target can show too;
its `required_systems` are the further checks a systems run of it must
measure and pass (the rows only the systems' images can show), so a
systems run in which the SP, the DP display, F3548, the zone, the
probes, the vectors or the faults measured nothing fails.

How each row is observed, and what stays "not measured" and why (each
is written in every report, never a blank):

| Row | Against the reference target | In a systems run |
|---|---|---|
| F3411 SP `GET /uss/flights`, details | not measured: no SP | the lab polls the USSP's SP as a display provider (the tier's `dp`, the targets file's `f3411_dp`) |
| F3411 DP display | not measured: no DP | the authority's display provider polls the peer USSP over the oversight area the seed added; from the peer's answer to the picture frame (Systems mode, "The peer USSP") |
| F3411 DP cache 24 h | not measured: no DP | the probes read the authority's cache after the run; judged once its display provider has cached for 25 h, not measured before (a younger cache cannot hold anything older than 24 h) |
| F3548 intent change, details | not measured: no DSS or peer | the peer is notified of every change of the fleet's intents in the U-space square, and asks the USSP for each notified intent's details |
| F3548 conflicting intent, constraint | not measured | not measured: the peer files no intent, so no conflict between the two USSPs is made; no system of the lab stack publishes F3548 constraints |
| Zone alert tick | not measured: no zones | the zone aircraft fly through the zone the seed publishes; each entry's raise in monitor ticks |
| Art. 13(2) notices | not measured | not measured: no aircraft deviates from its authorisation, and the lab's U-space airspace is designated without conformance monitoring (2021/664 Art. 3(3)) |
| CPA pair checks, storage, the systems' memory | not measured: no `/metrics`, no database | probes (the targets file's `probes`, from `deploy/systems/probes.yaml`) |
| Restart, partition, cross-system outage | not measured: no containers | a fault plan (`--faults load/faults/<plan>.yaml`; the outages are judged on every system's readiness and the streams) |
| Vectors | not measured: no image | after the drain, each image's Go binaries are read for the uspace-core they were built with, and that version's vector tests are run, its vectors compared with `knowledge/vectors` |

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
only locally or in CI; never against the staging droplet. A run
against the systems is in "Systems mode" below.

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

## Systems mode

A systems run loads the four systems' images in the lab stack
(`deploy/demo-up.sh`, docs/RUNBOOKS/demo.md) instead of the reference
target. It is run locally, never against the staging droplet.

1. `deploy/demo-up.sh`. `deploy/demo.env` must hold the variables of the
   current `demo.env.example` (`DEMO_CADDY_IP`, `DEMO_LAB_SUBNET`,
   `DEMO_LAB_IP_RANGE`, `DEMO_EGRESS_SUBNET`, `DEMO_HOST_GATEWAY`,
   `DEMO_LOAD_RECEIVER_ADDRESSES`, `DEMO_LOAD_OPERATOR_ADDRESSES`,
   `LOAD_PEER_HOST`, `DEMO_LOAD_PEER_PORT`); `gen-secrets.sh` makes the
   Caddy certificate again when it does not name every host (an older
   state directory lacks `LOAD_PEER_HOST`). The lab Caddy has a fixed address
   that every system trusts for `X-Forwarded-For`, as the droplet's
   systems trust their edge Caddy. The lab Caddy and the authority also
   trust the host's address on the egress network, so each simulated
   receiver is judged under its own address. Without this, the
   authority's request budget per address (20 a second) refuses part of
   what the fleet's receivers send.
2. `go run ./cmd/loadgen seed --tier load/tiers/<tier>.yaml --lab sim/sitl.env --geoid <egm2008-2_5.pgm>`.
   It registers the tier's fleet through the public APIs: one operator
   per client (registered at the authority, active at the USSP, with one
   machine client and its aircraft bound, open A2 / C2), every aircraft
   in the registry, and every receiver at the authority. For a tier with
   a `peer` it also registers the peer's aircraft at the authority (under
   the first client's operator, never bound at the USSP), adds an
   oversight area over the fleet and the peer to the authority's display
   provider (`POST /v1/dp/views`), and moves the U-space airspace
   (`LABUSP1`, the demo's) over the tier's `peer.uspace` square. For a
   tier with a `zone` it imports, approves and publishes the zone
   (`LOADZ1`). The designations carry the day's dates, so a second seed
   on the same day publishes nothing again, and the seed waits until the
   USSP's own geo-awareness (`GET /v1/geo`, a load operator's token)
   puts the airspace and the zone where the fleet flies. Then it writes
   `targets/local-load-<tier>.yaml` and `secrets/load-<tier>/` (both
   git-ignored). Everything the systems issue is kept in the seed's
   state, so a second seed registers nothing again. Seed again before a
   run, because a console session ends after 30 minutes idle.
3. The first time receivers are registered, restart the authority's
   rid-ingest (`docker restart uspace-demo-authority-rid-ingest-1`). With
   no receiver keys it listens on loopback only (R-06), and every batch
   is answered 502 until it is restarted.
4. `go run ./cmd/loadgen run --tier load/tiers/<tier>.yaml --targets targets/local-load-<tier>.yaml --lab-env deploy/demo.env --host "<name, size>" [--faults load/faults/<plan>.yaml]`.
   `--lab-env` dials every lab host at 127.0.0.1 with the lab CA. Wait
   130 s between two runs: the USSP ends a flight after 120 s of silence.

The run refuses a targets file before it sends anything when:

- the file was seeded for another tier or fleet layout;
- a secret file is missing;
- a probe could not run;
- a fault plan does not fit the run;
- the pairs cross. The USSP authorises only one of two intents that share
  airspace, so pairs need `pairs.inner_gap_m`;
- the tier has a peer and the targets file names none (or other
  aircraft), or an outage in the fault plan and no readiness URLs.

Every intent must be activated, or the run stops before any stream
starts, naming the USSP's reasons. Intents are filed at the tier's
`intents_per_s`; one inside U-space airspace is answered `pending_dss`
until the USSP's DSS write lands, and is read (`GET /v1/intents/{id}`,
at most 3 minutes) until it moves on. Every intent is ended when the
run ends: after the drain in a run with a peer (the deletions owed to
it are timed too), else as the run is torn down.

What a systems run measures beyond the reference target:

- **Probes** (`deploy/systems/probes.yaml`). Every process's `/metrics`
  is read from inside the stack (`docker exec` into the lab Caddy and
  busybox `wget`) every `memory_sample_s`. Each telemetry database's size
  and compression lag are read with `psql` in its own container. From
  these come:
  - the memory row, which judges the systems' resident memory, never
    the harness's own heap;
  - the writer queue;
  - the CPA pair checks of the busiest process against 05 §1's budget;
  - disk growth against 05 §1's model (the fleet's samples × 600 B per
    system);
  - the systems' loss counters that moved, listed in the report.
  - the authority's display provider cache (`ussp_flights`, `dp_cache`
    in the probes file) after the run: entries older than 24 h, judged
    once the display provider has cached for 25 h (its first network
    track); a younger cache is "not measured" with its age.
- **F3411 SP**. The lab is the display provider of 05 §1. It tiles the
  fleet's area in 4 km views, polls the most populated `dp.views` of
  them once a second, and asks for the details of one flight per answer.
  The authority's own display provider polls the USSP's SP too, over
  the oversight area (05 §1's DP load); its network tracks of the fleet
  are counted in the report.
- **The peer USSP** (the tier's `peer`, the targets file's `peer`). A
  second USSP inside the harness (`internal/simussp`), served on
  `127.0.0.1:DEMO_LOAD_PEER_PORT` and reached by the systems through the
  lab Caddy as `https://LOAD_PEER_HOST`, so both ends of what it times
  are on the generator's clock:
  - it serves `peer.aircraft` flights of its own (circling
    `peer.centre`, at an altitude no other aircraft uses) as an F3411
    SP, with an ISA in the DSS whose subscribers it notifies. The first
    answer to the authority's display provider (the token's subject,
    `authority-01`) that holds a position is the SP response; the timed
    console's `network_rid` frame that first shows that position (tied
    by time, within 500 ms, and place, within 50 m) is the display;
  - it subscribes to F3548 operational intents over the U-space square.
    Each filing, activation and end of an intent there is timed from the
    lab's request to the USSP's notification (`intent_change_notify`);
    a change never notified (15 s after the end) counts against
    `notifications_complete`; each notified intent's details are asked
    of the USSP as the peer (`GET /uss/v1/operational_intents/{id}`,
    `details_answered`; a request not answered with 200 counts at its
    10 s bound). The peer files no intent, so no conflict is made.
  The peer's subscription and ISA are deleted when the run ends.
- **The zone** (the tier's `zone`). The fleet's last aircraft fly
  east-west lines through the zone the seed published, in and out at an
  altitude of their own. Each entry is due a `zone_incursion` raise on
  the aircraft's USSP traffic stream, timed from its first sample inside
  in monitor ticks (the largest `evaluation_period_s` the alerts carry,
  else 1 s; `zone_alert_tick`), and its clear after the exit. The
  entries join the expected set: a missing raise or clear is a missed
  alert, a raise outside every entry a false alarm.
- **Vectors**. After the drain each image of the targets file is
  exported (`docker create`, `docker export`) and the build information
  of every Go binary in it read: the uspace-core it was built with. Each
  version found runs uspace-core's vector tests from a module of its
  own (`go get`, `go test -run 'Vector|Manifest'`, from the network or
  the module cache), and its vendored vectors are compared with
  `knowledge/vectors` by behaviour (owners and words left out; a file
  that differs in those only is listed, not counted). About a minute.
- **Faults** (`load/faults/ci.yaml`, `load/faults/60min.yaml`). The
  WP-L9 scripts kill each hot-path process in turn, stop each system's
  NATS for 60 s, and take the CISP, the authority, the DSS and the ANSP
  down for 5 min each (the 60-minute plan). The run measures:
  - recovery: from the restore to the first sample newly shown;
  - alerts raised twice;
  - backlog lost;
  - other systems' streams that turned degraded during a partition;
  - each outage's claims (`expect` in the plan): the system lost (the
    fault, seen in place), every other system ready throughout, a
    consumer's check degraded within its bound, the age a consumer's
    check states growing with the hold, a stream's flag; and the stack
    ready again within the outage's `recover_within_s`. The readiness
    of every system is sampled every 2 s from the injection through the
    recovery (the targets file's `readiness`).
  A stream that breaks inside a fault's window, or on expired
  credentials and connects again on fresh ones, is excused; any other
  break is a run error.

Disk for a systems run (05 §1, 05 §4): at 1000 drones each system with
a telemetry hypertable writes about 52 GB a day uncompressed, which is
about 2.2 GB per hour. A 60-minute run of the authority and the USSP
needs about 5 GB free beside the images, the 2-hour soak about 10 GB,
and 5000 drones five times that; the authority stores more than that
model with its display provider on (the network tracks and the
`ussp_flights` cache beside the direct Remote ID tracks; the storage
row says how much). The vectors step needs the uspace-core versions in
the module cache or the network. Never run it on the staging droplet;
the owner adds storage to the host that runs it.

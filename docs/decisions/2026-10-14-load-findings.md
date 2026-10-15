# Decision record: what the systems-mode load runs found, 2026-10-14

Status: **open**, L1 to L10. These are defects and gaps that the WP-L8
load runs found against the systems' images in the lab stack
(`results/load/20261014-951fda2-*`, `results/load/README.md`). They
are recorded here, in the lab, and not as issues on the owning
repositories, because this work package files nothing on GitHub. Each
entry gives what was observed and where the evidence is, what the
specification asks for, who owns the fix and what the lab does in the
meantime. An entry is closed here, with the fixing commit, once a run
shows the failure gone.

Every run used the same images by digest: authority
`sha256:53d6f97c…`, CISP `sha256:2e786148…`, USSP `sha256:854cca71…`,
ANSP `sha256:1fcfb3c9…` (`deploy/demo.env.example`). They ran on one
dev workstation, with the generator, the consoles and the whole stack
sharing its 16 CPUs (Docker Desktop VM: 16 CPUs, 7.7 GB). The latency
figures are therefore an upper bound, not the droplet's, and none of
these runs counts as L-D4 evidence for a system until a run passes.

The run's figures that GCAA has not answered are the spec's defaults
and are marked pending GCAA in every report (the 1 s ingest-to-picture
bound, the 2 s raise bound). Nothing below relaxes or decides one.

| Id | Defect | Owner | Rows | Status |
|---|---|---|---|---|
| L1 | The USSP stops acknowledging operator telemetry, and its ingest grows, after a NATS outage | uspace-ussp | `operator_identity`, `restart_backlog`, `memory_monotonic`, `memory_growth`, the drain | open |
| L2 | Operator telemetry to the USSP console p99 is over 1 s | uspace-ussp | `operator_to_console_p99` | open |
| L3 | The zone alert is late by more than a monitor tick, and clears are lost across a NATS outage | uspace-ussp | `zone_alert_tick`, `missed_clears`, `no_false_alarm` | open |
| L4 | Duplicate raises after restarts | uspace-ussp, uspace-authority | `restart_duplicates` | open (F6, F7) |
| L5 | The ANSP is not ready while the authority is down | uspace-ansp | `outage_flagged` | open (F1) |
| L6 | The CISP briefly reports not ready, "migrate relational status: context deadline exceeded" | uspace-cisp | `outage_flagged`; chaos `authority-rid-ingest` | open |
| L7 | The authority stores 4.2 to 4.5 times the 05 §4 disk model with its display provider on | uspace-authority, spec owner | `disk_model` | open |
| L8 | Frames shown up to 64 s late after the authority's outage | uspace-authority | `receiver_to_picture_p99`, `picture_traceable` | open |
| L9 | The authority's display-provider rows shed at 1000 drones | uspace-authority | listed counters | open |
| L10 | At 5000 the authority sheds observations it answered as accepted | uspace-authority | `picture_identity` at 5000 | open |

## L1: the USSP stops acknowledging telemetry after a NATS outage

**Observed.** In both 60-minute runs every operator client ended with
samples the USSP accepted but never acknowledged: 216 062 at 100 drones
(all 100 clients) and 2 210 841 at 1000 (all 1000). The drain never
settled ("not settled within drain_s"). At 100 drones, 216 062 samples
are the last 2 161 s of the run, which places the stop near 1 440 s:
the end of the USSP's 60 s NATS outage (1 380 s to 1 440 s,
`load/faults/60min.yaml`). That timing is inferred from the count,
not observed per sample. `ussp-telemetry-ingest`'s resident memory
rose throughout and monotonically: 39 to 91 MB at 100 drones, 158 to
1 457 MB at 1000 (growth 4.10). At 1000 its own counter
`ussp_replay_window_put_dropped` rose by 9 859. The 2 h soak at 1000
on the same stack, which has no faults, balanced every ledger (7 200 000
sent, 7 200 000 accepted, 0 pending) and grew no memory (ingest growth
0.999). But the ingest started that soak at 1 055 MB: what the faulted
run had grown was never given back.

**Expected.** 05 §5 and 05 §7 "no silent loss": every sample is
accepted, refused or dropped and counted, and the ack follows the
accept. 05 §6 NATS: ingest spills to its disk queue and resumes. Memory
stays flat over the soak.

**Owner.** uspace-ussp (telemetry ingest: the acknowledgement path
after NATS reconnects).

**Meanwhile.** The rows fail, as they should. The lab does not count an
unacknowledged sample as accepted.

## L2: operator telemetry to the USSP console over 1 s at p99

**Observed.** `operator_to_ussp_console_s` p99 was 1.023 s at 100 drones
(p50 0.42) and 1.418 s at 1000 (p50 0.54, max 2.51). The authority's
picture from the receivers stayed near 0.09 s p50 on the same host.

**Expected.** 05 §7: under 1 s at p99 (pending GCAA).

**Owner.** uspace-ussp (telemetry to traffic stream). The shared host
inflates both paths alike, so the gap between them belongs to the USSP.

## L3: zone alert late, clears lost across a NATS outage

**Observed.** All 60 zone entries were raised in each run. The slowest
raise took 1.05 monitor ticks (1 s) at 100 drones and 2.44 at 1000.
Clears were missed for 2 entries at 100 and 4 at 1000. In both runs
the two zone aircraft's entries at 1 360 s went uncleared; their exit
(1 401 s) fell inside the USSP's NATS outage (1 380 s to 1 440 s). At
1000 the entries at 280 s (exit 321 s) also went uncleared; the
telemetry ingest was killed at 300 s. Both aircraft were then raised
again at 371.4 s, outside every entry: 2 false alarms.

**Expected.** 05 §7: the zone alert within one evaluation tick; every
raise cleared; no false alarm.

**Owner.** uspace-ussp (monitor). The lost clears are the same family as
F7 in `2026-10-05-chaos-findings.md`.

## L4: duplicate raises after restarts

**Observed.** Over the 60-minute runs, 36 raises (100 drones) and 164
raises (1000) came while another alert of the same kind was open for
the same aircraft. The plan kills each hot-path process once and stops
each NATS for 60 s.

**Expected.** 05 §7 restart: no duplicate alert.

**Owner.** uspace-ussp, uspace-authority: the same identity losses as F6
and F7 of the chaos record, seen here under load.

## L5: the ANSP is not ready while the authority is down

**Observed.** During the 300 s authority outage the ANSP's `/readyz`
answered 503 `jwks=down` in 117 of 120 samples at 100 drones and 81 of
124 at 1000.

**Expected and owner.** As F1 of the chaos record (uspace-ansp).

## L6: the CISP briefly not ready

**Observed.** The CISP answered 503 "migrations=unknown: migrate
relational status: failed to initialize: context deadline exceeded"
in 1 to 3 samples during the DSS and ANSP outages. This happened in
both 60-minute runs, and in the chaos matrix's `authority-rid-ingest`
row, while no fault touched the CISP.

**Expected.** 05 §6: nobody else is affected by an outage. The
readiness check should not run a migration-status query that can time
out under the host's load and turn the system not ready.

**Owner.** uspace-cisp.

## L7: the authority's storage is over the 05 §4 model

**Observed.** Growth over the run against 05 §1's model (each fleet
sample × 600 B per system): USSP 0.94 (100 drones) and 0.77 (1000);
authority 4.50 and 4.20. At 1000 drones the authority's database grew
from 986 MB to 10 060 MB in the hour. The authority stores network
tracks from its display provider and the `ussp_flights` cache beside
the direct Remote ID tracks.

**Expected.** 05 §4: the disk model holds within 20 %. Either the model
has to count the display provider's rows, or the authority stores
less.

**Owner.** uspace-authority (what it keeps), together with the spec's
owner (05 §4). The lab keeps its 600 B model and records the ratio.

## L8: frames shown late after the authority's outage

**Observed.** After the 300 s authority outage the picture came back
22 s after the restore. At 1000 drones, frames were shown up to 64 s
late (the receiver-to-picture max was 63.97 s; the DP display max was
64.2 s). 1 194 frames were too late for the lab to tie to a sample: its
ledger holds a sample for 64 s. At 100 drones the p99 was 4.95 s with a
max of 37 s. In the earlier 100 run on the same images (5a8dcc2, not
kept) the max was 2.5 s, so the size of this effect varies between
runs. The cause is not established.

**Expected.** 05 §7: under 1 s at p99. A sample replayed after an outage
is backlog and is flagged as such, not shown live late.

**Owner.** uspace-authority (picture after restore).

## L9: display-provider rows shed at 1000 drones

**Observed.** At 1000 drones the authority's counters
`uspace_authority_rows_shed_queue_full{component="dp_rows"}` rose by 79 494,
`rows_lost_handover_failed` by 1 646 and `gap_records_lost` by 124.
`rid_ingest` refused 8 704 batches (8 639 as `queue_unavailable`; the
plan stops the authority's NATS for 60 s, and the counters do not say
when they rose). The ANSP's `notices_sender_quota_refused`
rose by 2 692. The counters are listed and not judged (05 §7), but a
shed is a loss that the DP display figures do not show.

**Owner.** uspace-authority (DP row queue); uspace-ansp for the notice
quota.

## L10: at 5000 the authority sheds what it accepted

**Observed.** In the 5000 attempt (`20261014-951fda2-5000-systems`, 600
s) the authority's `rid-ingest` answered the receivers' batches
accepted (1 531 952 observations) and then shed 905 078 of them from
its queue (`uspace_authority_queue_shed_observations`, 212 707 full
batches), using 0.66 cores. The picture showed 40 392 of the 1 196 900
samples heard, 230 s late. The USSP's ingest also filled, but it
counted every drop against the sample it refused (1 772 520
`ussp_dropped_queue_full`), so its ledger stays honest.

**Expected.** 05 §5 and 05 §7: accepted plus dropped equals sent per
source, as the sender sees it. A batch answered accepted is either
shown or counted against that sender, so a receiver can buffer what was
not taken. 05 §1 sizes the authority for 5000 drones.

**Owner.** uspace-authority (rid-ingest: refuse when the queue is full,
not shed after accepting). The size of the queue that filled is the
owner's to find; this host saturated as well (generator late on 0.24 %
of samples), so the 5000 figures do not say how far the systems are
from 5000 on their own hosts (05 §4: a second host).

## What these runs could not answer

- **The DP cache 24 h bound**: the cache was 1 to 2 h old; a run of
  more than 25 h is needed (stays "not measured", by design).
- **F3548 conflict and constraint notifications**: the peer files no
  intent and no system publishes constraints.
- **Art. 13(2) notices**: no aircraft deviates, and the lab's U-space
  airspace has no conformance monitoring service.
- **The droplet**: nothing here ran on it (L-Q1).

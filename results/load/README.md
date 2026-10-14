# results/load

Load runs (`load-report/v1`, `internal/load`; docs/RUNBOOKS/load.md).
Each directory is one run: `report.json` and its rendering `report.md`.

## Systems runs (2026-10-14)

These runs drive the four systems' images in the lab stack
(`deploy/demo-up.sh`, docs/RUNBOOKS/load.md "Systems mode"), seeded by
`loadgen seed`. The images, by digest, are the same in every run:
authority `sha256:53d6f97c…`, CISP `sha256:2e786148…`, USSP
`sha256:854cca71…` and ANSP `sha256:1fcfb3c9…`, with harness `951fda2`
and uspace-core v1.6.0. The host is the dev workstation below, with the
whole stack in Docker Desktop on the same machine (16 CPUs, 7.7 GB to
the VM) and other projects' containers running beside it. So the
latencies are an upper bound, and no run passes, which means none of
them is yet L-D4 evidence for a system. Each failure is recorded with
its owner in `docs/decisions/2026-10-14-load-findings.md` (L1 to L10).

| Run | Tier | Verdict | What it shows |
|---|---|---|---|
| `20261014-951fda2-100-systems` | 100, 60 min, `load/faults/60min.yaml` | fail | 40 checks measured. F3411 SP p99 0.007 s, DP display p99 0.014 s, details p99 0.008 s; F3548 notification max 0.97 s with 48 of 48 changes notified and every detail answered; CPA pair checks 0.018 of budget; proximity raise p99 0.049 s, 320 of 320 raises and clears; vectors pass against v1.3.0 and v1.4.0 (the images' core). Fails: operator to USSP console p99 1.023 s (L2); zone 1.05 ticks and 2 clears lost in the USSP NATS outage (L3); 216 062 samples never acknowledged after that outage, so every operator ledger is open and the ingest's memory grows (L1); 36 duplicate raises (L4); the ANSP not ready while the authority is down (L5); the CISP not ready for a sample in two outages (L6); the authority's disk at 4.5 times the model (L7); picture p99 4.95 s, max 37 s, after the authority's outage (L8). |
| `20261014-951fda2-1000-systems` | 1000, 60 min, `load/faults/60min.yaml` | fail | 1000 msg/s operator, 400 Remote ID locations/s, 30 whole-area consoles (35 884 frames/s). F3411 SP p99 0.114 s, DP display p99 0.207 s; F3548 notification max 1.58 s, 48 of 48; proximity raise p99 1.21 s with 600 of 600; pair checks 0.237 of budget; partition replay and isolation pass. The same failures as at 100, larger: 2 210 841 samples unacknowledged and the ingest from 158 to 1 457 MB (L1), operator to console p99 1.418 s (L2), zone 2.44 ticks, 4 clears lost and 2 false alarms (L3), 164 duplicate raises (L4), the authority's database from 986 to 10 060 MB in the hour (L7), frames up to 64 s late and 1 194 untieable after the authority's outage (L8), 79 494 DP rows shed (L9). |
| `20261014-951fda2-1000-soak-systems` | 1000, 2 h, no faults | fail | **Memory: no monotonic growth** in any system process (largest growth 1.05). **No silent loss**: 7 200 000 operator samples sent and accepted, none pending; the picture balanced, nothing silent or untieable. Every raise and clear (1 200 of 1 200; zone 120 of 120), no false alarm; picture p99 0.457 s, raise p99 0.176 s, DP display p99 0.18 s. Fails: operator to console p99 1.046 s (L2), zone 1.14 ticks (L3), disk at 2.76 times the model (authority 4.74, L7). Without faults L1 does not appear, but the ingest began the soak at 1 055 MB, kept from the faulted run before it. |
| `20261014-951fda2-5000-systems` | 5000, 600 s (`--duration-s`), no faults | fail | The 5000 attempt (below). The offered load was held: 4 987 operator msg/s and 1 995 Remote ID locations/s. The F3411 SP held too (p99 1.13 s) and so did F3548 (max 1.48 s). The rest broke (L10). |

### The 5000 attempt

What broke first was **the ingest queues of both systems**. They shed
on the same host whose 16 CPUs the generator also used: the generator
was late on 0.24 % of samples, which is itself a failed check.

- USSP: `ussp-telemetry-ingest` was the busiest process, at 2.06 cores
  (container mean 269 % of a core, max 452 %). Its queues filled and
  it dropped 1 772 520 samples (`ussp_dropped_queue_full`, telemetry)
  and 2 803 201 outbox entries, all counted: of the 2 992 101 samples
  sent, 1 185 208 were accepted. The traffic streams showed 61 samples
  of the watched flights, and none of the 100 alerts due was seen on
  them (the monitor ran at 1.57 cores).
- Authority: `rid-ingest` answered the receivers' batches as accepted
  (1 531 952 observations, 216 089 of them as backlog after a refusal).
  It then shed 905 078 observations from its queue
  (`uspace_authority_queue_shed_observations`, in 212 707 full batches).
  The picture showed 40 392 of the 1 196 900 samples heard, 230 s late.
  The authority counts the shed, but the receivers were told the
  batches were accepted, so to the picture identity 940 375 samples
  are silent. The process used only 0.66 cores, so the queue, not its
  CPU, was the limit.
- The display provider kept up less well: DP display p95 2.18 s, p99
  6.27 s.

The run also lists 1 132 intents as "not ended", answered 409 "the
intent is ended": the USSP had ended those silent flights itself.
That was a harness defect, fixed after this run
(`fix(load): count an intent the USSP ended itself as ended`).

A first 5000 attempt, on the same stack right after the 2 h soak (the
authority's database at 30 GB), made the Docker Desktop VM stop
answering: the engine returned 500 to every call and the VM did not
answer `wsl` within its timeout. The run did not end on its own. It
was stopped after 70 minutes with no report and Docker Desktop was
restarted. Where the run waited is not known (it logged nothing below
WARN), so that hang is open in the harness. A run that cannot reach
its target should end with a report saying so.

The 04 §1 JSON share (an estimate, `protobuf` in each report): one
`encoding/json` decode and encode of a 1 KB track frame costs 8.9 to
9.1 µs on this host. Set against each hot-path process's CPU per
message, it comes to 16 to 21 % of the authority's picture fan-out
(42.7 to 56.7 µs a frame; 33 % at 5000, where the saturated host timed
the JSON at 18.8 µs), about 1 % of the USSP's telemetry ingest
(720 to 811 µs a sample) and under 1 % of its traffic stream. That is a
proposal to the owner, not a change: Protobuf would pay off only on
the picture fan-out, and only if the share holds in a profile, which
the systems do not expose. At 1000 the picture fan-out was the busiest
process, at 1.49 cores (container 169 % of a core mean, 271 % max),
ahead of the USSP's SP (0.74) and ingest (0.71). The lab Caddy, in
front of every system, averaged 109 % of a core.

Before these runs, three harness defects were found on the same images
and fixed; their reports are not kept. The first 1000 run lost every
console to the lab's 1 MiB frame bound on the authority's whole-area
snapshot (`fix(load): bound a whole-area console by its fleet's
snapshot`). The probes counted a scrape that the run itself cancelled
as a failure of every process. And a run with a peer reported 0
intents ended while the USSP held all of them ended.

## Reference runs

Every run below is against the **lab's reference target in process**,
not a system image. The reference target proves the harness: that it
drives the 05 §1 volumes, ties every frame to the sample it shows, times
it on one clock and closes the loss identities. It is never evidence
for a system (docs/PLAN.md L-D4). Generator, consoles and target share
one host's CPUs, so the figures are an upper bound of the harness plus
a stand-in, not the systems' latencies.

| Run | Tier | Harness | Verdict | What it shows |
|---|---|---|---|---|
| `20261005-c69c6c3-100` | 100, 60 min | c69c6c3 | pass | Every required check measured and passing (19 measured). Operator telemetry to USSP console p99 0.050 s (86 400 samples), receiver to authority picture p99 0.100 s (144 000), proximity raise p99 0.030 s; 320 of 320 expected raises and clears, no false alarm, every ledger balanced, nothing silent. Live heap flat at 21 MB after the warm-up (growth 1.025). |
| `20261005-c69c6c3-1000` | 1000, 60 min | c69c6c3 | pass | 1000 msg/s operator telemetry, 400 Remote ID locations/s, 30 whole-area consoles (12 015 frames/s). Operator to console p99 0.196 s, picture p99 0.217 s (1 440 000 samples), raise p99 0.152 s; 600 of 600 raises and clears; 96 samples dropped over rate by the target and counted (sent = accepted + dropped). Live heap 159 to 168 MB, growth 1.003. |
| `20261005-c69c6c3-1000-soak` | 1000, 2 h | c69c6c3 | pass | The soak. 7 200 s at the 1000 volumes. Operator to console p99 0.193 s (323 987 samples), picture p99 0.271 s (2 880 000), raise p99 0.048 s; 1200 of 1200 raises and clears; 244 samples dropped over rate and counted; no sample late, no frame untraceable. **Memory: no monotonic growth.** The live heap was flat from the end of the 900 s warm-up to 2 h: 167 MB at the end, 178 MB max, growth 1.008. |
| `20261005-c69c6c3-5000` | 5000, 600 s (`--duration-s`) | c69c6c3 | fail | The 5000 attempt. What broke first: **the picture fan-out**. Sixty consoles each subscribed to the whole area is 2000 tracks x 60 = 118 000 frames/s out of one process; every console's queue overflowed (15 729 to 23 042 `dropped_frames` each, all counted) and the timed console fell behind (picture p99 38 s, max 66 s). Frames later than the ledger holds a sample (64 s) could not be tied: 1532 untraceable frames and 1730 samples neither shown nor counted. The operator path held: p99 0.81 s, near the 1 s limit, with 6164 samples dropped over rate and counted. All 90 due raises came, with p99 0.26 s. The generator was late on 0.15 % of samples (the host was saturated). |
| `20261005-reference-1000-soak` | 1000, 2 h | e79b120 | fail | The first soak (superseded by the one above). Memory: live heap flat from 15 min to 2 h (growth 0.997). Every measured check passed (1200 of 1200 raises and clears, ledgers balanced). It failed on two harness defects, both fixed in c69c6c3. First, the drain counted status frames as traffic and could not settle after 245 console drops (counted). Second, a single host stall of about 3 s, with picture max 2.89 s, was a run error. It is now the bounded `generator_on_time` check. |
| `20261005-reference-5000` | 5000 | e79b120 | fail | The first 5000 attempt measured nothing, and said so ("the run measured nothing"). Five thousand operator sockets dialled the listener at once; the dials were refused, and a console could not open. Fixed in 855a977: consoles open first, then the clients ramp in at 250/s. |

Earlier runs found defects in the harness. Their reports are not kept; each commit message has the observation.

- The first 60-minute 100-drone run (f91669a) failed its memory row: `HeapAlloc` rose from 12 MB to about 130 MB. A heap profile pointed at the reference target's receiver nonces and observation keys. Two causes: the target swept its replay and dedupe windows only when a count cap was reached (fixed in 18aabce), and the row sampled `HeapAlloc`, which includes garbage (now the live heap, e79b120).
- A 420 s run reported crossings cut by the run's end as false alarms (fixed in e79b120).
- The e79b120 1000-drone run left 7 late traffic frames untied (fixed in 8d5da88).

## What is still open (WP-L8)

- **The 05 §7 rows the systems fail** (L1 to L10 in
  `docs/decisions/2026-10-14-load-findings.md`): each owner's to fix,
  then the runs again. The lab files nothing on other repositories.
- **The DP cache 24 h bound** needs a run of more than 25 h; until then
  it is "not measured" with the cache's age.
- **F3548 conflict and constraint notifications, Art. 13(2) notices**:
  no system of the lab stack makes the event (each report says why).
- **The droplet** (L-Q1): every run here shares one workstation with
  the stack, so the figures do not compare with the droplet's.

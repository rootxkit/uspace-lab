# results/load

Load runs (`load-report/v1`, `internal/load`; docs/RUNBOOKS/load.md).
Each directory is one run: `report.json` and its rendering `report.md`.

Every run here is against the **lab's reference target in process**,
not a system image. The reference target proves the harness: that it
drives the 05 §1 volumes, ties every frame to the sample it shows, times
it on one clock and closes the loss identities. It is never evidence
for a system (docs/PLAN.md L-D4). Generator, consoles and target share
one host's CPUs, so the figures are an upper bound of the harness plus
a stand-in, not the systems' latencies.

Host for every run: a dev workstation (Windows 11, 16 logical CPUs,
16 GB). Not the droplet: nothing here ran on it or wrote to it.

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

- **Systems mode.** None of these runs is against a system image. The F3411, F3548, CPA pair-budget, storage and chaos rows stay "not measured" until the harness can register the tier's fleet at the images and read their `/metrics` (docs/RUNBOOKS/load.md).
- **The 04 §1 Protobuf question is not answered.** It needs the hot-path CPU share of encoding inside the systems' ingest and fan-out. Here the generator, the consoles and the target encode and decode JSON in one process, so a share measured here would not be the systems' share.
- **5000.** The 5000 tier against the reference target stops at its picture fan-out. Against the systems it needs a second host (05 §4) and consoles with viewports (05 §1), not whole-area subscriptions.

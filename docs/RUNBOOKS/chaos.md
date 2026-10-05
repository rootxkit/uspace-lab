# Runbook: the chaos matrix (WP-L9, L-M3)

Every failure domain of spec `05 §6` injected into the lab's systems
stack, one at a time, while one aircraft holds a zone alert open in the
USSP and the authority; each row judged on the spec's own claims, and
on whether its fault actually happened. The harness and the scripts are
described in `scripts/chaos/README.md`; the rows and their citations are
`scripts/chaos/matrix.yaml`. What follows is what was **observed**.

## The observed matrix (run `20261005-chaos`)

2026-10-05, 07:32 to 08:58 UTC, Windows 11 with Docker Desktop (16
CPUs, 7.7 GiB to the Docker VM, other projects' containers running
beside it), project `uspace-chaos`, port 443. Lab at `eab0b1d` with
this branch's uncommitted CHANGELOG (`lab_dirty`), uspace-core v1.3.0,
the matrix `sha256:803a3036…`. Images, by digest (E-05; every container's
image and local id are in `chaos.json`):

| System | Image |
|---|---|
| authority | `ghcr.io/rootxkit/uspace-authority@sha256:8663cac1…` |
| CISP | `ghcr.io/rootxkit/uspace-cisp@sha256:63a02a8d…` |
| USSP | `ghcr.io/rootxkit/uspace-ussp@sha256:4d72301d…` (uspace-deploy 4cf1f68, ussp 4e2a664) |
| ANSP | `uspace-lab/uspace-ansp:d02b09a` (built locally by demo-up, image `sha256:01ed9677…`) |
| DSS | `interuss/dss:v0.23.0@sha256:0781042b…` |
| lab issuer | `uspace-lab/lab-issuer:local` (this commit) |

The background's alert was up in both systems 45 s after it started
(`07:33:19.9`); the console sessions were refreshed every 15 minutes
(two refreshes failed while the row of the moment had the authority,
then the whole stack, down; the next ones succeeded). Every fault was
observed in place, held to the end of its hold and observed gone: no
row was judged on a fault that did not happen. 18 rows pass, 10 fail;
every failure is a finding below, none is the harness's.

"seen" is when the harness first saw the fault in place after the
script started; "gone" when it saw it removed after the restore.
Readiness times count from the fault seen (during) or from the restore
(after); "others ready" is every probed endpoint but the faulted one
answering 200 in every sample of the hold.

| Row | Verdict | Fault, as observed | Promised state, as observed | Standing alert |
|---|---|---|---|---|
| `authority-rid-ingest` | PASS | seen +3.4 s, held 30/30, gone +0.6 s | others ready; authority ready; all recovered +0.6 s | authority cleared `stale` (allowed: no peer instance in the lab), raised again +38.1 s |
| `ussp-telemetry-ingest` | PASS | seen +3.2 s, held 30/30, gone +0.6 s | others ready; all recovered +0.6 s | ussp cleared `stale` (allowed), raised again +37.0 s |
| `authority-api` | FAIL | seen +5.5 s, held 23/23, gone +0.5 s | authority not ready +0 s; picture status `nats` connected throughout; **ANSP not ready (F1)**; **USSP `registry` degraded until +20 s (F3)** | kept |
| `ussp-api` | PASS | seen +5.5 s, held 22/22, gone +0.6 s | ussp not ready +0 s; others ready; all recovered | kept |
| `cisp-api` | FAIL | seen +5.5 s, held 22/22, gone +2.3 s | cisp not ready +0 s; subscribers ready; **ANSP `cisp` down until +17 s after the restore (F4)** | kept |
| `ansp-api` | PASS | seen +5.7 s, held 22/22, gone +0.6 s | ansp not ready +0 s; others ready; all recovered +1.6 s | kept |
| `ussp-monitor` | PASS | seen +3.3 s, held 30/30, gone +0.6 s | ussp and others ready; recovered +0.6 s | **kept under its id across the kill** |
| `ussp-monitor-stalled` | FAIL | seen +2.5 s, held 30/30, gone +0.6 s | ussp and others ready; recovered +0.6 s | **ussp cleared `stale` and raised under a new id at the resume (F5)** |
| `ussp-rid-sp` | PASS | seen +3.4 s, held 30/30, gone +0.6 s | ussp and others ready; recovered | kept |
| `authority-detect` | FAIL | seen +3.3 s, held 30/30, gone +0.6 s | authority and others ready; recovered | **authority raised a second violation, new id, at the restart (36 s into the row), the first still open (F6)** |
| `cisp-deliver` | PASS | seen +3.3 s, held 30/30, gone +0.6 s | cisp and others ready; recovered | kept |
| `ansp-manned-feed` | PASS | seen +2.8 s, held 60/60, gone +0.6 s | authority's source `ansp_feed/ansp` not live within 1 s, live again +11.2 s; consumers ready | kept |
| `ussp-nats` | FAIL | seen +2.8 s, held 60/60, gone +0.7 s | ussp `nats` down +0 s; others ready; recovered +1.6 s | **ussp held the alert through the outage, then cleared `stale` and raised a new id the moment NATS returned (F7)** |
| `authority-nats` | FAIL | seen +3.0 s, held 60/60, gone +0.6 s | picture status `nats` `unavailable` +0 s (the console says so), `connected` again; others ready | **authority the same as the USSP (F7), and it replayed one cleared frame 52 times in run 2** |
| `cisp-nats` | PASS | seen +2.9 s, held 60/60, gone +0.6 s | cisp `nats` "disconnected: reconnecting" +0 s; others ready; recovered +1.6 s | kept |
| `ansp-nats` | PASS | seen +2.9 s, held 60/60, gone +0.6 s | ansp `nats` not ok +0 s; others ready; recovered +1.6 s | kept |
| `ussp-timescaledb` | PASS | seen +2.9 s, held 60/60, gone +0.7 s | ussp `timescaledb` down +0 s; others ready; recovered +0.7 s | kept |
| `authority-timescaledb` | PASS | seen +2.9 s, held 60/60, gone +0.6 s | authority `telemetry` down +0 s; others ready; recovered | kept |
| `ussp-postgres` | PASS | seen +3.2 s, held 60/60, gone +0.7 s | ussp `postgres` down +0 s; others ready; recovered +18.6 s | kept |
| `authority-postgres` | PASS | seen +2.8 s, held 60/60, gone +0.6 s | authority `relational` down +0 s; others ready; recovered +28.5 s | kept |
| `ussp-dbhost` | PASS | seen +2.9 s, held 60/60, gone +0.5 s | ussp `postgres` and `timescaledb` down +0 s; others ready; recovered +7.5 s | kept |
| `ussp-partition` | PASS | seen +26.8 s (nine filters), held 17/17, gone +0.7 s, 107 packets dropped | ussp not ready from outside +0 s; others ready; recovered +38.6 s | ussp cleared `stale` (allowed), raised again 39 s after the heal |
| `cisp-down` | PASS | seen +15.6 s, held 110/110, gone +2.6 s | cisp not ready; subscribers ready for 300 s; ANSP `cisp` not ok +0 s; recovered +30.6 s | kept |
| `authority-down` | FAIL | seen +22.3 s, held 113/113, gone +0.6 s | authority not ready; **ANSP not ready (F1)**; CISP, USSP, DSS, issuer ready for 300 s; recovered +29.6 s | **authority raised a new id after its restart, the old one never cleared (F6)** |
| `dss-down` | PASS | seen +5.1 s, held 107/107, gone +0.6 s | DSS not ready; USSP `dss` down +13 s and ok again +8.5 s after; others ready; recovered +22.5 s | kept |
| `issuer-down` | FAIL | seen +5.0 s, held 45/45, gone +0.5 s | issuer not ready; **ANSP not ready (F1)**; authority, CISP, USSP, DSS ready for 120 s | kept |
| `receiver-clock` | PASS | −10 s: left −10.0 s, accepted 202; +45 s: left +45.0 s, refused 401; −45 s: left −45.0 s, refused 401 | every system ready | kept |
| `stack-restart` | FAIL | seen +12.6 s (31 containers), held 30/30, gone +8.7 s | all not ready; all recovered +54.6 s | **both systems raised a new id after the restart while the old one was still open (F6, F8)** |

The exit: the aircraft left the zone at the end of the hold; both
systems cleared their open alert as `resolved` and raised nothing after.
The runner's own verdict on the background (`chaos-background.json`:
missed 0, false 10) is recorded, not judged: it pairs each alert's first
raise with its first clear, so every allowed stale clear and re-raise
reads to it as a false alert.

Earlier runs on the same day, kept out of `results/` because their
judgement was wrong, not the systems: run 1 charged an allowed stale
clear to the row before the one that caused it and listed words the
systems do not use for a lost bus; run 2 lost both console streams to
expired sessions after the stack restart. Each was fixed in the harness
(`fix(chaos)` commits on this branch) before run 3; the systems'
behaviour behind F1 to F8 was the same in all three.

## What it needs

- Docker with Compose v2, bash, Go (the module's toolchain); no SITL:
  the background flies the synthetic stand-in (INV-01: nothing here
  commands an aircraft).
- GeographicLib's `egm2008-2_5.pgm` in `deploy/local-demo/ground/`
  (`docs/RUNBOOKS/demo.md`, "What it needs").
- Port 443 on 127.0.0.1 free (`DEMO_HTTPS_PORT`). The authority's
  picture accepts only the portless origin of its host, so a run on
  another port loses the authority's console stream (seen: 403 on the
  picture with port 9443).
- Memory: the stack measured 2.4 GiB over 31 containers (one `docker
  stats` sample after the seed), the harness a few tens of MB beside it;
  that fits the droplet's 7.6 GB, but the matrix was run on a laptop
  only (staging is not to be written to; the droplet run is the owner's
  call). Disk: no volume beyond the stack's own; a run's results are
  725 kB (the samples gzipped); the packet-filter image is 16 MB.
  No extra storage is needed.
- Time: about 1 h 35 min for the whole matrix (run 3: demo-up 137 s,
  the seed 38 s, the harness 86 min: the rows 61 min, then the
  background's hold ran out, the aircraft left the zone and landed). The
  hold is sized for the worst case of every row, so the last 22 minutes
  were the standing alert held with no fault: a soak in which it had to
  stay open, and did in both systems.

## Procedure

```
make chaos                      # every row; CHAOS_ROWS=id,id for some
make chaos CHAOS_KEEP=1         # leave the stack up afterwards
make chaos-check chaos-test     # offline: the matrix, the harness's tests
```

`make chaos` is `deploy/demo-down.sh`, `deploy/demo-up.sh`,
`scripts/chaos/prepare.sh --fresh` (receivers, rid-ingest restarted,
registry, the U-space airspace 60 km off, operators, sessions, the
background's zone), then `go run ./scripts/chaos run`, then
`deploy/demo-down.sh` again, keeping the run's exit status. Results go to
`results/<run>-chaos/`: `chaos.json` (every row: the fault as observed,
each expectation with when it was met, the alert findings, the scripts'
output), `chaos.txt`, `samples/<row>.json.gz` (every sample),
`chaos-background.json` (the background run, `result/v1`).

On Windows the run was made from Git Bash with the harness built as an
exe; the lab hosts are dialled at 127.0.0.1 by the harness itself, so
`deploy/labns.sh` is not needed. A single domain can be run by hand:

```
CHAOS_PROJECT=uspace-demo scripts/chaos/nats.sh inject ussp
CHAOS_PROJECT=uspace-demo scripts/chaos/nats.sh restore ussp
go run ./scripts/chaos watch --for 60s     # readiness and console status, every 2 s
go run ./scripts/chaos skew --skew-s 45     # one skewed receiver batch
```

## How a row is judged (short)

1. The fault was observed in place by the harness, held to the end of
   the hold, and observed gone after the restore; otherwise the row
   fails whatever the systems said.
2. The row's claims: the faulted system says so within the bound,
   everyone else stays ready, every check that was ok before is ok
   again within the recovery bound (10 s after a process restart, the
   brief's figure; the lab's own bounds elsewhere, in the matrix).
3. The standing alert: `kept` rows, open under its id throughout;
   `stale_ok` rows (the fault starves the system of the aircraft's
   data), a clear as `stale` allowed, open again within 45 s of the
   restore; never two open at once; never cleared for another reason
   while the aircraft is inside; at the exit, open, cleared as resolved,
   nothing raised after.
4. The streams: each absence in 3 counts only while the system's
   console stream is heard. A silence over `max_silence_s` (6 s) in a
   row fails it, unless the row's fault takes that stream down
   (`streams_down`) and it comes back re-sending the open alerts.

"Ready" is the system's readiness endpoint answering 200. A system may
answer 200 with a check degraded; the rows judge the checks separately.

## Findings

F1 to F8 are recorded, each with what was observed, what the spec asks
for, the owner and what the lab does meanwhile, in
`docs/decisions/2026-10-05-chaos-findings.md` (this work package files
nothing on GitHub). In short: the ANSP's readiness tied to other
systems' JWKS (F1), its CIS projection holding an unused version (F2)
and slow to see the CISP back (F4); the USSP's registry poll hanging
through an authority restart (F3) and the alert's identity lost to a
stalled monitor (F5) and a stack restart (F8); the authority's
violation identity lost to restarts (F6); and both systems' alert
identity lost to a NATS outage (F7).

### Lab (this repository)

- Fixed on this branch: `demo-up.sh` started the CISP before the ANSP
  whose JWKS it needs (failed on every fresh stack); the pinned USSP
  asked the issuer for its client in lower case (every outgoing token
  401); the scenario recorder ignored the alerts in console snapshots
  and read stream credentials once (both streams lost after a restart).
- Open: `deploy/systems/compose.yaml` leaves `USSP_TRUSTED_PROXIES`
  empty, so every client of the USSP shares the lab Caddy's address for
  rate limits and audit rows (`client_address` degraded at every
  baseline); the seed's state file outlives the volumes it describes
  (`prepare.sh --fresh`); a stack on a port other than 443 loses the
  authority's picture (origin check).

### The three "nothing hidden" checks (SC-22)

- No alert disappeared without `stale`: every clear while the aircraft
  was inside was `stale` (allowed or not), none `resolved` early.
- Every age shown: the authority's status carried `projection_age_s` and
  `cis_age_s` throughout, the readiness details carried each
  dependency's age; the CISP outage showed in the ANSP's `cisp` check
  with its serving age. Not every system's console stream is read (the
  CISP's and the USSP's console status are not), so this is observed
  for the authority and the ANSP only.
- Every disable an audited act: not exercised (no disable through a
  console API, see below).

## Not covered, and why

- **A source disabled through a console API** (05 §6 "disabled", SC-08:
  an audited act by a person): not scripted. The dead case is the
  `authority-dp-poller` row (the authority's network RID source killed
  while its direct RID receiver keeps hearing the aircraft) and the ANSP
  feed row. The disable needs an admin session and the receiver's
  disable call per system; it is left for WP-L9's next pass with SC-08's
  scenario, which already exercises the authority's disable.
- **The CIS 300 s bound** (02 F3): the CISP row holds 300 s and judges
  the cache, but files no intent in U-space airspace, so the refusal of
  new authorisations after the bound is not exercised (pending GCAA).
- **The 5 min disk spill of ingest** (05 §6 NATS): the NATS rows hold
  60 s, the brief's figure; the spill limit itself is not reached.
- **Counters** (`dropped_*`, `gap_*`, backlog replayed): the harness
  records the console status and readiness as the systems publish them;
  the systems' counters are in their own status logs, not on a public
  contract the lab reads, so they are not judged here. The receiver
  clock row reads the authority's answer per batch.
- **SITL**: the background is synthetic; a SITL background needs WSL
  and is a flag away (`runner.Options.Vehicles`), not done here.

# 20261004-systems: the owed runs against the system images

The first runs of the owed scenarios against the systems themselves
(`mode.targets: systems`): the four images, the WP-L2 DSS and lab
issuer in one compose project (deploy/systems/, docs/RUNBOOKS/demo.md),
seeded through their public APIs (cmd/demo-seed), with ArduCopter 4.5.7
SITL in WSL flown by sim/fly.py. 2026-10-04, Windows 11, Docker Desktop
28.5.1, 16 CPUs and 7.7 GiB given to Docker.

| System | Image | Commit |
|---|---|---|
| authority | `ghcr.io/rootxkit/uspace-authority@sha256:8663cac1…` | a2bfeaa |
| CISP | `ghcr.io/rootxkit/uspace-cisp@sha256:63a02a8d…` | e8e90a5 |
| USSP | `ghcr.io/rootxkit/uspace-ussp@sha256:07718be4…` | 6ec6238 |
| ANSP | `uspace-lab/uspace-ansp:d02b09a`, **built locally** (the ANSP publishes no image) | d02b09a |
| DSS | `interuss/dss:v0.23.0@sha256:0781042b…` | deploy/dss/SOURCE |

The digests in full are in every result file (`images`).

## Verdicts

| Scenario | Verdict | What decided it |
|---|---|---|
| `ussp-wp7-authorisation` | **FAIL** | Both intents `pending_dss` (`dss_unavailable`): the USSP image has no DSS writer yet (`noDSSWriter`, "until WP-13 brings dss-sync"), so inside U-space airspace no intent is ever authorised. Not a defect of the image's own scope; the scenario cannot pass until WP-13 is in an image. |
| `ussp-wp10-conformance` | **PASS** | Nonconformance raised and cleared three times (horizontal, vertical, long), lost link raised 15.0 s after the stream stopped and cleared on restore, the nearby operator told each time; 0 missed, 0 false. U-space airspace moved off the area (see the runbook). |
| `sc-01-hover-inside-minima` | **FAIL** | Both aircraft file the same volume; the USSP authorises A and rejects B (`intent_filed_first`). B still streams (outside U-space airspace no authorisation is needed) and the USSP raises proximity for A naming B's flight, but B has no intent, so no alert stream, and the runner cannot name the peer. |
| `sc-02-head-on-and-short-return` | **FAIL** | As SC-01. |
| `sc-21-slow-to-hover` | **FAIL** | As SC-01; B's landing also unconfirmed (lab timing, fixed after the run). |
| `ussp-wp12-restriction` | **FAIL** | The ANSP refused the plan 503 `cis_stale`: it holds every CIS version (finding 4 below), so it has no U-space airspace to place a restriction in. Beyond that the USSP image predates WP-12 (`restriction_activated`) and its intent was `pending_dss`. |
| `ansp-inv02-manned` | **FAIL** | The ANSP half passes (the manned track shown live at +20.9 s, shown stale 15.5 s after the feed froze). The USSP half cannot: the image reads no ANSP stream (`USSP_ANSP_STREAM_URL` is parsed and used by no code at 6ec6238). |
| `sc-03-zone-entry-exit` | **FAIL** | The authority half passes (two incursions into SC03Z, each raised and cleared resolved in its window). The USSP half cannot: zone alerts from the CIS come with WP-12, after the image. |
| `authority-sc08-rid-switch` | **PASS** | Incursion raised, cleared `source_disabled` when direct Remote ID was switched off, raised again when on. |
| `sc-22-missing-inputs-visible` | **FAIL** (`../20261004-systems-try2`) | Run first, on the authority with receivers only: `registry_projection_absent` raised and held; `source_control_unknown` never shown, because the authority publishes its switch state (version 0) at start, so the switches are read and "known" on any fresh deployment (finding 6). |

Lab commits: sc-08, SC-03 and ansp-inv02 record `commits.lab`
91e22e1 (dirty: the seed's zones option, not yet committed, which the
runner does not use). The others record "unknown": the runner was built
on Windows and run in WSL, where git cannot read the worktree (fixed in
b2b56d0). Their runner binaries were built from this branch at: sc-22
(try2) 65c5d01; wp7 and wp12 44684b3; wp10, SC-01, SC-02 and SC-21
e423b4a, with the scenario files as they stood at each run (wp10 at
11a399f). Every scenario fix is its own commit in the PR.

## Findings in the systems (not patched here; not yet filed as issues)

1. **USSP 6ec6238 has no DSS writer**: every intent inside U-space
   airspace is `pending_dss` (by design until its WP-13).
2. **USSP 6ec6238 predates WP-12**: no `restriction_activated`, no zone
   alerts from the CIS. The later main commits that add them failed CI
   and pushed no image (uspace-deploy images.env).
3. **USSP 6ec6238 reads no ANSP stream**: no manned traffic reaches its
   monitor, so no proximity with a manned aircraft.
4. **ANSP d02b09a holds every CIS version from CISP e8e90a5**: it
   requires the bytes at `/v1/{dataset}` and at
   `/v1/{dataset}/versions/{v}` to be equal (its audit B-1 fix,
   internal/cis/provenance.go), but the CISP's contract serves its own
   snapshot on the first path (`metadata.issued`, its `provider`, the
   `cis_*` members, its own signature) and the publisher's bytes on the
   second. Observed: `uspace_airspace` versions 1 and 2 held, sha256
   fe59ad4b… against 81c06420…; the USSP installed the same versions.
   Consequence: the ANSP can never place a restriction (`cis_stale`).
5. **Authority a2bfeaa: rid-ingest binds 127.0.0.1 when it starts with
   no receiver keys** and keeps that after keys arrive; every receiver
   batch was refused at the proxy until it was restarted.
6. **SC-22 against the authority**: `source_control_unknown` means "the
   switches were never read"; the authority's api publishes the state at
   start, so a fresh deployment never shows it. Either the scenario's
   premise ("started with no switch state") or the slug's trigger needs
   the owner's decision; the assertion was not changed.
7. **SC-01, SC-02, SC-21 against a USSP**: the scenarios give both
   aircraft one shared intent volume. A USSP that deconflicts
   strategically authorises only the first, so the second has no alert
   stream. Rewriting what these scenarios mean needs the owner's
   decision; the expectations were not changed.
8. Latent: the ANSP asks for the DSS audience with its port
   (`dss:8082`), which the lab issuer (M18: a bare host) refuses; and
   uspace-deploy's `env/ansp.env.example` lists `ansp` in
   `ANSP_AUDIENCES` while `ANSP_SYSTEM_ID=ansp`, which the ANSP refuses
   at start.

## Footprint

One `docker stats` sample at idle after `make demo` (31 containers):
about 2.8 GiB in all; the largest are CockroachDB (345 MiB), the ANSP
api (249 MiB), the USSP api (193 MiB) and the processes that load the
geoid grid (about 180 MiB each). Measured, not a load figure; the
droplet comparison of docs/PLAN.md L-Q1 is still owed.

## The attempts before (`../20261004-systems-try1..3`)

Kept as the evidence of lab findings fixed on this branch: try1 SC-22
(every receiver batch refused: rid-ingest on loopback), try2 wp7
(rejected `airspace_ceiling_not_judged`: a seed setting, the U-space
ceiling needs terrain), try3 wp12 (the landing not confirmed: the
landing-time fix).

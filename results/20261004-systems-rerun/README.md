# 20261004-systems-rerun: the owed runs again, on current main images

The runs of `../20261004-systems` again, after the scenario and seed
fixes of this branch (fix/scenario-defects), against the systems' main
images pulled from GHCR by digest. Same stack (deploy/systems/,
docs/RUNBOOKS/demo.md), seeded through the public APIs (cmd/demo-seed),
ArduCopter 4.5.7 SITL in WSL flown by sim/fly.py. 2026-10-04, 02:05 to
04:10 UTC, Windows 11, Docker Desktop 28.5.1.

| System | Image | Commit |
|---|---|---|
| authority | `ghcr.io/rootxkit/uspace-authority@sha256:3f8a821b…` | b67981b (main) |
| CISP | `ghcr.io/rootxkit/uspace-cisp@sha256:63a02a8d…` | e8e90a5 (main) |
| USSP | `ghcr.io/rootxkit/uspace-ussp@sha256:a5838e08…` | 6e78644 (main; **without** WP-13 / #19, still open) |
| ANSP | `ghcr.io/rootxkit/uspace-ansp@sha256:8e6a7e11…` | 7fc3ef1 (main; published, no longer built locally) |
| DSS | `interuss/dss:v0.23.0@sha256:0781042b…` | deploy/dss/SOURCE |

Each result file has the digests in full (`images`) and the commit of
the runner that produced it (`commits.lab`). The runner binaries were
built from this branch at 5fe1eb8 (SC-22), e3d5ab8 (wp7, wp10, SC-01,
SC-02, SC-21, inv02, SC-03, sc-08) and 98fb362 (wp12). `lab_dirty` is
true in every file: the build saw this directory's untracked result
files, nothing else.

## Verdicts

| Scenario | Verdict | What decided it |
|---|---|---|
| `sc-22-missing-inputs-visible` | **FAIL** (finding A) | Run before the registry was seeded, with the AGL zone SC22Z imported. `registry_projection_absent` raised from the first frame and held. The AGL zone raised a `zone_incursion` **warning** with `limit_not_judged: true`, `vertical_known: false`, `not_judged: [AGL]`, cleared `resolved` on exit (+54.6 s, +95.6 s). The failure: the authority also raised a **critical `unregistered`** violation (`serial_unknown`, `unknown_operator`) for the aircraft while its own status named the registry projection absent. A first run without the `never` on `unregistered` passed. That never was added after it (5fe1eb8). |
| `sc-01-hover-inside-minima` | **PASS** | Both intents authorised and activated (their own volumes). Proximity raised for a and b at +25.5 s, held through the hover, cleared `resolved` at +92.5 s. |
| `sc-02-head-on-and-short-return` | **PASS** | Both authorised. On the ground: raised +25.6 s, cleared +66.6 s. Head-on pass (40 m CPA): raised +95.6 s, cleared +130.6 s. Short return: raised +184.6 s, cleared +226.6 s. Missed 0, false 0. |
| `sc-21-slow-to-hover` | **PASS** | Both authorised. On the ground: raised +26.0 s, cleared +67.0 s. B slows to hover 40 m from A: raised +93.0 s, 8 s after the approach started, held through the 30 s hover, cleared `landed` +231.0 s. |
| `ussp-wp10-conformance` | **PASS** | Nonconformance raised and cleared three times (+51.1/+90.1 s, +130.1/+169.1 s, +194.1/+296.1 s), with the nearby operator told each time. Missed 0, false 0. |
| `authority-sc08-rid-switch` | **PASS** | Incursion raised +26.5 s, cleared `source_disabled` +40.0 s when direct Remote ID was switched off, raised again +66.5 s when it was switched on. |
| `ussp-wp7-authorisation` | **FAIL** (finding B) | Both intents `pending_dss`. Inside U-space airspace the USSP 6e78644 has no DSS writer: that is WP-13, #19 not merged. Both landings were confirmed once the duration was fixed (e3d5ab8). |
| `ussp-wp12-restriction` | **FAIL** (findings B, C) | Intent `pending_dss` (B). The ANSP answered **500** to the restriction plan, with no log line. The same request was sent by hand five times, between and after the two runs, and was answered 201 each time, also with `starts_at` at or 1 s before now and with the run's key format. The two 500s are this file and `../20261004-systems-rerun-wp12-try1`. Earlier attempts in this run were refused 400 and then 409; those were lab defects, fixed by 21ffcc5, 3d46c60 and 98fb362. |
| `ansp-inv02-manned` | **FAIL** (findings B, D) | With U-space airspace moved off the area (this file): the intent was authorised, but the ANSP showed no manned track. The ANSP 7fc3ef1 filters manned traffic to the U-space volumes (its manned-feed status line says "relevance: CIS version 3, 1 volumes"), and the track was 60 km away from them. With the airspace about the origin (`../20261004-systems-rerun-uspace-origin`), the ANSP half passed: the track was shown live at +20.4 s and stale at +104.5 s. The USSP half failed there: the intent was `pending_dss` and all 175 samples were `refused_intent_state`. Neither placement can pass both halves until #19. |
| `sc-03-zone-entry-exit` | **FAIL** (finding E, lab) | The authority half passed: two incursions into SC03Z, at +68.8/+82.8 s and +110.8/+124.8 s. The USSP half did not: the intent was `pending_authority`, because SC03Z is REQ_AUTHORIZATION and the intent volume overlaps it. So no flight was ever activated to alert on. |

## Findings

In the systems. These are not patched here and not filed as issues.

- **A. uspace-authority b67981b: `unregistered` without a registry.**
  internal/ridpipe/pipeline.go (`identify`) says that before the
  registry projection has loaded, identification answers
  `registry_unavailable` (SC-22). Observed: with
  `registry_projection_absent` on the status, a track was identified
  `serial_unknown` / `unknown_operator`, and detect raised a critical
  `unregistered` violation inside a PROHIBITED zone. One likely cause,
  not checked: internal/registry/reader.go `Lookup` may return a non-nil
  empty snapshot once the empty projection has been read.
- **B. uspace-ussp 6e78644: no DSS writer.** Every intent inside U-space
  airspace is `pending_dss`, which blocks wp7, wp12 and the USSP half of
  inv02. This is the open #19 (WP-13). It is expected, not a regression.
- **C. uspace-ansp 7fc3ef1: POST /v1/restrictions answered 500 twice**
  in runner executions, with no log line. Hand-made requests of the same
  shape succeeded. The cause is unknown.
- **D. uspace-ansp 7fc3ef1: manned traffic is limited to U-space
  volumes.** This may be by design. It means inv02 needs the airspace
  about the origin, which (B) makes `pending_dss`.

In the lab, fixed on this branch and found by this run:

- The lab issuer listed `ussp-ussp-dev-01`. Since uspace-ussp 8cce0e0
  the USSP asks for `ussp-USSP-DEV-01`, so every token request was
  refused 401 (168d5c2).
- A seed resumed after a failure panicked on an operator saved before
  any serial was bound (05bda9f).
- The 150 m AMSL ceiling was below wp12's 2000 m AMSL restriction, which
  the ANSP refused (21ffcc5).
- wp12's restriction end had no reason, so the end was refused and the
  restriction was left active. It was ended by hand before the next run
  (3d46c60).
- wp12's idempotency key was the same on every execution, so a second
  run got 409 (98fb362).
- wp7's landing did not fit in the run (e3d5ab8).
- The reference target could not judge `unregistered` (0c72518).

In the lab, **not fixed** (E): SC-03's intent overlaps its own
REQ_AUTHORIZATION zone. A USSP that waits for the zone authority
(`pending_authority`) never activates the flight, so the USSP half
cannot pass. Rewriting what SC-03 means needs the owner's decision
(scenarios/sc-03-zone-entry-exit.yaml).

## Procedure, as run

1. `deploy/demo-up.sh` (in WSL) with deploy/demo.env set to the digests
   above.
2. `demo-seed --steps receivers`, then restart rid-ingest. It still
   binds 127.0.0.1 when it starts with no receiver keys, and its log
   says to restart it.
3. `demo-seed --steps zones --zones sc-22-missing-inputs-visible`, run
   SC-22, then move the zone away with `--zones-away-north-m 60000`.
4. `demo-seed --steps registry,uspace,ussp,ansp` (U-space ceiling
   1500 m over the origin).
5. Run wp7 and wp12 with the airspace about the origin. Move it 60 km
   north, then run wp10, SC-01, SC-02, SC-21 and inv02. Run SC-03 and
   sc-08, each with its zone imported before the run and moved away
   after. Wait 130 s between USSP runs and restart SITL before each run.

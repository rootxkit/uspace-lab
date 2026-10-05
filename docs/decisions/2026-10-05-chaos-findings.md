# Decision record: defects the chaos matrix found, 2026-10-05

Status: **open**, F1 to F8. Defects the chaos matrix (WP-L9, L-M3)
observed in run `20261005-chaos` against the systems stack
(`results/20261005-chaos/`, `docs/RUNBOOKS/chaos.md`), each seen in
runs 2 and 3 of that day and F1, F5 and F7 also in run 1. They are
recorded here, in the lab, instead of as issues on the owning
repositories: the lab does not file issues on other repositories. Each
entry says what was observed and where the evidence is, what the
specification asks for, who owns the fix, and what the lab does
meanwhile. An entry is closed here, with the commit that fixed it, when
a run of its row shows the failure gone.

The run predates the stream-liveness judgement (a kept alert counts only
while its console stream is heard, `scripts/chaos/README.md` item 4):
the alert findings below are clears and raises that were received,
which that judgement does not change; a re-run judges the kept rows of
the same matrix again with it.

Nothing below decides a policy question. The matrix's figures that GCAA
has not answered are the spec's defaults, in `scripts/chaos/matrix.yaml`
under `pending`, each marked pending GCAA (`docs/PLAN.md` §7.1, L-Q4).

| Id | Defect | Owner | Rows | Status |
|---|---|---|---|---|
| F1 | ANSP readiness fails when another system's JWKS cannot be fetched | uspace-ansp | `authority-api`, `authority-down`, `issuer-down` | open |
| F2 | ANSP CIS projection holds a `uspace_airspace` version it does not use | uspace-ansp, uspace-cisp | every row (baseline) | open |
| F3 | USSP registry poll hangs through an authority restart | uspace-ussp | `authority-api` | open |
| F4 | ANSP slow to see the CISP back | uspace-ansp | `cisp-api` | open |
| F5 | A stalled USSP monitor costs the alert its identity | uspace-ussp | `ussp-monitor-stalled` | open |
| F6 | An authority restart loses the violation's identity | uspace-authority | `authority-detect`, `authority-down`, `stack-restart` | open |
| F7 | A NATS outage costs the alert its identity (USSP and authority) | uspace-ussp, uspace-authority | `ussp-nats`, `authority-nats` | open |
| F8 | After a whole-stack restart the USSP holds two alerts for one incursion | uspace-ussp | `stack-restart` | open |

## F1: ANSP readiness tied to other systems' JWKS

**Observed.** uspace-ansp image `d02b09a`. With the authority's api,
the whole authority or the lab issuer down, the ANSP's `/readyz`
alternates between 503 (`jwks` down: "check did not answer within 2s")
and 200 (`jwks` degraded: "stale (age 75 s: HTTP 502)") every few
seconds for the whole fault (run 3, `authority-down`: 08:17:40 to
08:22:36; samples in `results/20261005-chaos/samples/`).

**Expected.** 05 §6: JWKS are cached 24 h, no real-time service depends
on the authority, and the token service down leaves tokens valid for
their TTL; the ANSP stays ready on the keys it holds.

**Owner.** uspace-ansp: the `jwks` check is required and blocks on the
fetch instead of reporting on the cached keys.

**Meanwhile.** The rows' `others ready: always` claims fail on it, as
they should; nothing in the matrix is relaxed.

## F2: CIS projection holds a version it does not use

**Observed.** At every baseline and through the run the ANSP's `cisp`
check is degraded: "uspace_airspace version 1 held, not used: the bytes
at /v1/uspace_airspace and at /v1/uspace_airspace/versions/1 differ",
and "stale" once its age passes 300 s (it crossed 301 s during
`ussp-api` in run 2, which failed that row in run 2 and not in run 3).

**Expected.** One U-space airspace version, served the same at both
paths, used by the subscriber that holds it (02 F1/F2).

**Owner.** uspace-cisp and uspace-ansp together: either the CISP serves
different bytes for the current and the versioned document, or the ANSP
compares them in a way the CISP does not promise.

**Meanwhile.** `recovered` compares with the baseline, so a check
degraded at baseline does not fail a row by itself; the age crossing
300 s does, and is reported.

## F3: registry poll hangs through an authority restart

**Observed.** uspace-ussp `4e2a664`. After the authority's api came
back the USSP's `registry` check stayed degraded 20 s ("last poll
failed: ... context deadline exceeded, no answer within 90 s"): the
poll in flight when the api died waited out its 90 s timeout.

**Expected.** Recovery within the row's bound after the restore (05 §6
api row: everyone else unaffected); a reset connection fails the poll
at once.

**Owner.** uspace-ussp: the registry poll's client does not fail at the
connection reset.

**Meanwhile.** Reported by `authority-api`'s `recovered` claim.

## F4: slow to see the CISP back

**Observed.** After the CISP's api restarted the ANSP's `cisp` check
stayed down 17 s (`cisp_publisher` down 16 s), past the 10 s
process-restart bound; the USSP's `ansp_coordination` showed 2
escalated deliveries from the same outage.

**Expected.** The brief's "back within 10 s after a process restart".

**Owner.** uspace-ansp: its CISP check and publisher poll too slowly to
see the CISP back.

**Meanwhile.** Reported by `cisp-api`'s `recovered` claim.

## F5: a stalled monitor costs the alert its identity

**Observed.** Frozen for 30 s (`docker pause`, SC-15) and resumed, the
USSP's monitor cleared the standing zone alert as `stale` and raised it
again under a new id in the same instant; killed and restarted
(`ussp-monitor`) it kept the alert under its id.

**Expected.** 05 §6 treats a stalled and a dead hot-path process as one
domain, and the USSP's own design carries an alert across a restart
under its id.

**Owner.** uspace-ussp (monitor).

**Meanwhile.** `ussp-monitor-stalled` stays `kept`; it fails until the
alert keeps its id.

## F6: a restart loses the violation's identity

**Observed.** uspace-authority `8663cac1`. When `detect` restarted (at
once), 21 s after the whole authority came back from 300 s down, and
30 s after the stack restart, the authority raised the standing zone
violation under a new id while the old one was still open, and never
cleared the old one: three open violations for one incursion by the end
of the hold, only the last cleared at the exit.

**Expected.** 05 §2/§6: state survives a process restart.

**Owner.** uspace-authority (detect).

**Meanwhile.** The rows fail on `duplicate_raise`; nothing is relaxed.

## F7: a NATS outage costs the alert its identity

**Observed.** In both the USSP and the authority: through 60 s without
NATS the alert stayed open (consumers held last state, as 05 §6 says);
the moment NATS returned it was cleared as `stale` and raised again
under a new id. In run 2 the authority also sent the same cleared
violation 52 times in that instant.

**Expected.** 05 §6: ingest spills to disk and is replayed, which would
leave no gap for the monitor to judge stale.

**Owner.** uspace-ussp and uspace-authority.

**Meanwhile.** The NATS rows stay `kept`. The 5 min spill limit itself
is not reached (the rows hold 60 s, pending GCAA).

## F8: two alerts for one incursion after a stack restart

**Observed.** After the whole-stack restart the USSP raised the alert
under a new id while the old one was still open, and cleared the old
one as `stale` 1.3 s later.

**Expected.** 05 §2 restart persistence: the standing alert open again
once, never twice.

**Owner.** uspace-ussp.

**Meanwhile.** `stack-restart` allows a stale clear and one re-raise
(`stale_ok`), never two open at once; it fails on the overlap.

## Observed, not judged

The operator client's ledger in the background run does not balance
(sent 7617, accepted 1509, 3700 resent after 1292 dials) although every
produced sample was acknowledged by sequence (`acked_seq` = `last_seq` =
5143). The cause is not established; it is not a finding until it is.

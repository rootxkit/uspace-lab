# WP-L4: `spec-errata`

Branch `docs/WP-L4-spec-errata`. One agent, one PR, documentation only;
nothing blocks on it (decision record §3 L4). Owns the `## Errata`
tables of `docs/spec/*.md` and the `owners` header of
`knowledge/vectors/alert_lifecycle.json`. Depends on `docs/PLAN.md §8`
(what WP-L0 already applied). Consumers: every reader of the spec; the
`09` conformance table.

## Read first

1. `docs/PLAN.md §8` (applied vs. left), `docs/WORKPACKAGES/WP-L0.md`
   (the errata tables are append-only).
2. `docs/decisions/2026-10-02-cross-plan.md`: §3 L4 (the list), and
   each decision it cites: M10, M14, M15, M18, M20, M23, M28, M35, M36,
   M37; §2.2 cisp Q18, Q19, authority Q-A4; Appendix A, B, C.
3. `knowledge/README.md` "Regenerating" (a header-only change to a
   vector file: the generator writes `owners`; a hand edit is refused by
   the determinism check, so the change goes through
   `knowledge/tools/gen_vectors.py` and a rerun), `uspace-core/docs/
   RELEASING.md §1` (an `owners` change moves no expected value: no
   major).
4. LESSONS E-04 (write what was decided, cite where).

## What to build

Append one dated row per change to the errata table of each touched
file (the tables exist in `00`, `02`, `03`, `05`; add one to `04`,
`06`, `09` as needed) and apply the change in the text above it:

| File | Change | Decision |
|---|---|---|
| `03` conventions | goose, two embedded trees, version tables `goose_db_version_relational` / `goose_db_version_timeseries`, a `migrate` subcommand and one-shot compose service; long-running processes never migrate and refuse to start on a lower version. | M36 |
| `03 §4` identifier table | Restriction identifier `DAR` + 4 base-36 (no hyphen; ED-318 caps `identifier` at 7); the CISP enforces length and uniqueness only. | M10 |
| `02 F1`, `04 §3.4` | ED-318 collection metadata per core's `ed318.Metadata` (`issued`, `provider`, `validFrom`, `validTo`, `description`); the CISP adds `cis_dataset`, `cis_version`, `cis_updated_at` and `ETag`. `09` row for `Metadata` updated. | M15 |
| `00 §6.1` cisp row | No `ed318` package in the CISP's `internal/`; it imports core's. | cisp Q19 |
| `05 §3` | Partition key = core `geodesy/cell` (`cell5` 0.1° × 0.1°, `cell3` 1° × 1°, names `c5:<lat_idx>:<lon_idx>` / `c3:...`, ring-1 neighbours), not H3; `09` H3 row updated. | M35 |
| `00 §6.2`, `03` | The authority's projection tables (registry, zones, restrictions) live in its telemetry database; KV keeps switches, policy, cells. One `timescaledb-ha` container per system on the droplet holding both databases (`05 §6` deployment paragraph). | Q-A4, M37 |
| `06 §3` | Scope catalogue = Appendix B: add `ansp.coordination`, `ansp.requests`, `dp.observe` (lab only), reserved `cis.publish:ats_data`; remove `rid.observe` from the JWT catalogue (receivers use key + HMAC). | M23 |
| `02 §1` conventions | Error body `problem/v1` (M28); `aud` = host of the target's base URL with `*_AUDIENCES` lists (M18); console session claims (`scope = "session"`, `roles[]`, `realm`, M20); cookie names `uspace_session` / `uspace_csrf` (M21); WS auth = cookie on same-origin upgrade + `Origin` (M22); `X-JWS-Signature` detached JWS (M26); `*_MTLS_MODE = required | off` (M25). | M18–M28 |
| `02` F2, F3 rows | Heartbeat `POST /v1/publishers/heartbeat {sent_at, active_refs?}` every 15 s (M3); one notification path `POST /v1/cis/notifications` with two allowed issuers (M1, M5); `?applies_at=` beside `?at=` (M17); coordination intake `POST /v1/coordination/notices` (M2). | M1–M5, M17 |
| `04 §1` | The schema-ownership rule of M14 (request/response by the API owner, pushed streams by the producer, shared shapes in `uspace-lab/schemas/common/`); every WS frame carries the envelope (M29). | M14, M29 |
| `09` | Rows citing `creationDateTime`, H3, `golang-migrate` or `manned_track.v1` updated to the decided names; nothing else in `09` changes status. | — |
| `knowledge/vectors/alert_lifecycle.json` | `owners` without `cisp` (the CISP has no monitor); through the generator, with the determinism check; `SHA256SUMS` consumers (core) told it is a header-only change. | cisp Q18 |

Do not touch: anything the owner-only questions of `08` and
`docs/PLAN.md §7.1` depend on (retention, formats, hosting). Do not
restate decisions in prose that is already right.

## Done when

- [ ] Every row above applied; every errata row cites the decision.
- [ ] `grep` for the retired names (`golang-migrate`, `DAR-`,
  `creationDateTime`, `H3`, `rid.observe`, `ansp_session`,
  `manned_track.v1`) across `docs/spec/` finds only errata rows.
- [ ] The vector header change regenerated, deterministic (two runs
  identical), `utm_commit` unchanged; the PR says "no expected value
  moved" and core is pinged to re-sync (`scripts/sync-vectors.sh`) as a
  minor.
- [ ] The decision record is not edited.

## Commits

`docs(spec): apply the migration, identifier and metadata errata   [WP-L4]`,
`docs(spec): apply the grid, projection and container errata   [WP-L4]`,
`docs(spec): apply the security and interface convention errata   [WP-L4]`,
`docs(spec): apply the schema ownership rule and update the conformance rows   [WP-L4]`,
`chore(knowledge): drop cisp from the alert_lifecycle owners   [WP-L4]`.

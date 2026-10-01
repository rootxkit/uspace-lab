# WP-L0: plan (done on `plan/lab-workpackages`)

Status: complete. This brief records what WP-L0 delivered so that every
other lab work package knows what it may rely on.

## Delivered

| Item | Where | Frozen? |
|---|---|---|
| The accepted decision record | `docs/decisions/2026-10-02-cross-plan.md` | Yes. A change to a decision is a new dated record, never an edit. |
| Three spec errata (ANSP processes, `track/manned/v1` and `alt_pressure_m`, M30 subjects) | `docs/spec/00`, `02`, `03`, `05`, each with an `## Errata` table | WP-L4 adds rows to the same tables; nobody rewrites history above them. |
| The lab plan | `docs/PLAN.md` | §1 decisions and §5 ownership need a plan change (edit the plan in the same PR and say so in the title). |
| Briefs WP-L1..WP-L9 | `docs/WORKPACKAGES/` | Each WP may refine its own brief in its first commit. |
| The deployment plan | `docs/deploy/PLAN.md` | Moves to `uspace-deploy` when the owner creates it; until then edits land here. |

## What every WP inherits

- The hard rules of `docs/PLAN.md §1`: INV-01 (no send path towards a
  vehicle), INV-02 (a scenario is the proof), INV-03 (configuration,
  not code), `trust: simulated` never in a production image.
- The owner-only questions of `docs/PLAN.md §7.1` are open. A WP uses
  the stated demo default, prints it where a result depends on it, and
  never writes the policy answer into a schema, a spec or a scenario as
  if it were decided.
- The engineering rules E-01 to E-04 of `knowledge/LESSONS.md`.
- Commit format `type(scope): subject   [WP-Lk]`; no AI attribution.
- Linters at the versions `uspace-core` pins; `make tools`, `make lint`
  before every push (the `Makefile` lands with WP-L1 and WP-L5; until
  then the core commands are the reference).

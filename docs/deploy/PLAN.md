# Deployment plan (`uspace-deploy`)

Status: plan, held in `uspace-lab/docs/deploy/` until the owner creates
the **private** repository `rootxkit/uspace-deploy`; this file and the
briefs below move there unchanged, and the lab keeps a pointer. It is
private because it holds host names bound to accounts, the staging mTLS
CA, env files and backup locations; nothing in it is a secret itself
(secrets live on the droplet, never in a repo). Inputs: the decision
record `docs/decisions/2026-10-02-cross-plan.md` (§3 D1, D2; M25, M36,
M37, M38; §2.1 droplet-sizing row), spec `05 §6` (deployment
paragraph), `06 §4`, `07` KT-3 (Caddy entries), each system's `deploy/`
(its own compose and Caddy snippet, which this repo composes).

Sections: 1 scope and decisions; 2 layout; 3 work packages; 4 open
questions.

---

## 1. Scope and decisions

One DigitalOcean droplet runs staging for every system. Each system
repo ships its own compose project and a Caddy *snippet*; this repo
composes them: one Caddyfile, one set of includes, one shared basemap
volume, one `deploy.sh`, backups, the mTLS CA for staging, and the
cutover runbook. A system's image is built in its own CI, pushed to
GHCR, signed with cosign, and pulled here by digest; this repo builds
nothing.

| # | Decision | Why |
|---|---|---|
| D1 | Hosts: `uspace-authority.chikox.net`, `uspace-cisp.chikox.net`, `uspace-ussp.chikox.net`, `uspace-ansp.chikox.net`, `uspace-lab.chikox.net` (`05 §6`); `courier.chikox.net` is the operator; `utm.chikox.net` and `ingest.chikox.net` stay with the predecessor until A-M5. Nothing in any repo hardcodes a hostname; this repo's env files are the only place they appear. | Spec `05 §6`; production domains are state-owned (Q16). |
| D2 | Caddy is the only shared component: TLS termination, routing by path to each system's processes (`02 §3`), `client_auth { mode verify_if_given, trusted_ca_cert_file }` on the CISP and ANSP hosts with `X-Client-Cert-Subject` forwarded on the mTLS routes and stripped everywhere else, `/basemap/*` as `file_server` with range requests and long cache headers, public cache headers on the CISP `/public/*`, rate limits, and `/metrics` never routed. | Decision record M25, M38; `06 §4`. |
| D3 | Database: one `timescale/timescaledb-ha:pg16` container per system holding both of its databases (relational with PostGIS, time series); migrations as one-shot `migrate` services (`<system> migrate`, version tables `goose_db_version_relational` / `_timeseries`) that run before the long-running processes, which never migrate and refuse to start on a lower version. Two hosts only when a system outgrows one (`05 §4`). | M36, M37. |
| D4 | Images from GHCR by digest, recorded in `images.env` per system; `deploy.sh` verifies the cosign signature against the repo's published identity before pulling, and refuses a tag. Rollback = the previous `images.env` (kept in git here). | Spec KT-3 platform baseline (SHA tags, rollback); supply-chain hygiene. |
| D5 | The lab's DSS and issuer compose (`uspace-lab/deploy/`, WP-L2) is included as-is; the authority's token service becomes the issuer of record at A-M4 and the lab issuer stays allow-listed for the lab client. | Decision record §4 preamble, L-Q2. |
| D6 | mTLS on staging: `*_MTLS_MODE=off` until the staging CA exists (WP-D1 ships the CA tooling; switching to `required` on the CISP and ANSP hosts is a WP-D1 done-when, with the lab's simulated USSP given a client certificate from the same CA). `off` is printed at error level every status period by every system (the cisp WP-2 rule). | M25. |
| D7 | Backups: nightly `pg_dump` of every database to object storage with 30 days retention, plus a weekly **restore check** that restores into a scratch container and runs each system's `migrate status`; the check's success is a verified restore, not a completed upload. | Predecessor S-23; LESSONS E-02 (the success path must run). |
| D8 | Sizing: the droplet is resized to ≥ 4 vCPU / 8 GB, or the DSS and the lab stack move to a second host, **before L-M1**; which one is the owner's call (§4). The compose is split so either works. | Decision record §2.1 (droplet sizing). |

---

## 2. Layout

```
uspace-deploy (private)
├── Caddyfile                 the five hosts; imports each system's snippet
├── caddy/
│   ├── snippets/<system>.caddy   copied from <system>/deploy/caddy/ at a pinned commit (SOURCE)
│   ├── mtls.caddy            client_auth block for the CISP and ANSP hosts
│   ├── basemap.caddy         /basemap/* file_server, range requests, cache headers
│   └── public-cache.caddy    the CISP /public/* cache
├── compose/
│   ├── compose.yaml          top level: network, Caddy, the shared basemap volume
│   ├── <system>.yaml         include of <system>/deploy/compose.yaml at a pinned commit (SOURCE)
│   ├── lab.yaml              include of uspace-lab/deploy/compose.yaml (DSS, issuer, simulators profile)
│   └── images.env            one digest per image; the only file deploy.sh changes
├── env/
│   ├── <system>.env.example  copied from each repo's .env.example; the real .env lives on the host only
│   └── README.md             every variable, who sets it, and the host-only ones
├── mtls/                     staging CA tooling: make-ca.sh, issue-client.sh (never the keys)
├── backup/                   nightly dump, restore-check.sh, retention
├── deploy.sh                 verify cosign, pull by digest, run migrate one-shots, up, health check, print what changed
├── basemap/pull.sh           fetch the lab's basemap release asset into the shared volume, verify the hash
└── docs/
    ├── RUNBOOKS/deploy.md    first deploy, update, rollback, what to look at
    ├── RUNBOOKS/cutover.md   WP-D2
    └── RUNBOOKS/restore.md   the restore check and a real restore
```

---

## 3. Work packages

Branch `feat/WP-D<k>-<slug>`, commit suffix `[WP-Dk]`. Done-when always
includes: `caddy validate`, `docker compose config` clean for every
include, `deploy.sh --dry-run` printing exactly what it would change,
and the runbook updated with what was observed (E-04).

| WP | Slug | Owns | Depends on | Needed by |
|---|---|---|---|---|
| WP-D1 | `droplet-compose` | everything in §2 except `cutover.md` | lab WP-L2 (DSS include), WP-L3 (basemap asset); each system's `deploy/` as it lands | the first staging deploy of any system (cisp C-M1 demo); L-M1 |
| WP-D2 | `cutover` | `docs/RUNBOOKS/cutover.md`, the DNS and retirement steps | authority A-M4, A-M5 readiness; WP-D1 | A-M5 |
| WP-D3 | `production-handover` | what state hosting needs: the production domain variables, key custody (HSM/KMS, cisp Q16), the state CA, the security assessment checklist (Q16) | GCAA's answers to Q16 and cisp Q16 | production |

### WP-D1: `droplet-compose`

Read first: `05 §6` deployment paragraph; `06 §4`; decision record M25,
M36, M37, M38, §3 D1, §2.1 droplet-sizing row; each system plan's
`§10`/`§11` deployment section and its `deploy/` directory; the cisp
WP-2 rule on printing `off`; `uspace-lab/deploy/README.md` (WP-L2);
`uspace-lab/basemap/README.md` (WP-L3); the predecessor's `infra/`
(compose and backup patterns, S-20..S-23, S-31) as reference only.

Build:

- The Caddyfile with the five hosts and the four snippets (D2); the
  mTLS block on the CISP and ANSP hosts only, applied to `/v1/
  restrictions*`, `/v1/publishers/heartbeat`, `/v1/manned-traffic/*`,
  `/v1/coordination/*` (the Go middleware enforces presence and
  subject binding; Caddy forwards `X-Client-Cert-Subject` there and
  strips it on every other route, proven by a test request that sets
  the header from outside and sees it dropped); `/basemap/*` from the
  shared read-only volume with `Accept-Ranges` and `Cache-Control:
  public, max-age=31536000, immutable` (the bundle is versioned by its
  release; `SOURCE.json` short-cached); `/public/*` on the CISP with
  the cache headers its plan sets; rate limits as each system's snippet declares them; `/metrics`
  unrouted (a request to it from outside gets 404; proven).
- Compose includes per system with one `timescaledb-ha` container
  holding both databases (D3), the `migrate` one-shot as a dependency
  of every long-running service (`depends_on: condition:
  service_completed_successfully`), NATS, the Go processes from one
  image, `web`; the lab include; the shared `basemap` volume; one
  isolated network per system plus the Caddy network; `images.env`.
- `env/`: one `.env.example` per system copied from its repo with the
  host-specific values marked; the real files on the host under
  `/srv/uspace/env/`, mode 600, never in git.
- `deploy.sh [system|all]`: cosign verify every digest in `images.env`
  (fail closed), pull, run the `migrate` one-shots and print their
  version output, `up -d`, wait for each `/healthz` and `/readyz`,
  print the before/after digests. `--dry-run` prints the plan.
  Rollback = `git checkout <previous images.env> && deploy.sh`.
- `mtls/`: `make-ca.sh` (staging CA, key on the host only),
  `issue-client.sh <system>` (client certificates for the authority,
  the ANSP, the USSP and the lab's `sim-ussp`, subjects matching
  `oauth_clients.mtls_subject`); switching `CISP_MTLS_MODE` and
  `ANSP_MTLS_MODE` to `required` on staging once every caller has a
  certificate, with the proof that an uncertified call to a mTLS
  route is refused and a certified one accepted (E-01).
- `backup/`: nightly dumps of every database (both per system) to
  object storage, 30 days; `restore-check.sh` weekly: restore the
  latest dump of each database into a scratch `timescaledb-ha`, run
  `<system> migrate status`, print the version and the row count of
  one table per database, exit non-zero on any failure, and send the
  result somewhere a person reads (an issue, a mail; the owner picks).
- `basemap/pull.sh`: fetch the lab's `basemap-v<date>` release asset,
  verify the SHA-256, place it in the shared volume atomically,
  print `SOURCE.json`.
- `docs/RUNBOOKS/deploy.md` and `restore.md` with the first real run
  recorded.

Done when:

- [ ] `caddy validate` and `docker compose config` clean; `deploy.sh
  --dry-run` prints the plan for all systems.
- [ ] The CISP (the first system with an image) deployed to
  `uspace-cisp.chikox.net` through `deploy.sh`: cosign verified,
  `migrate` output shown, health green, the public map serving the
  basemap from the shared volume with 206 responses; the C-M1 demo
  runs against it.
- [ ] The header-strip, `/metrics`-unrouted and mTLS refuse/accept
  proofs pasted into the PR.
- [ ] One restore check run end to end with its output; the nightly
  job observed to run on two consecutive nights.
- [ ] The droplet sizing decision (D8) recorded with the measured
  footprint after the lab's `make demo` (lab WP-L6) and the owner's
  choice.

Commits: `feat(caddy): route the five hosts with mTLS, basemap and
public-cache snippets [WP-D1]`, `feat(compose): include each system
with one timescaledb-ha and a migrate one-shot [WP-D1]`,
`feat(deploy): verify, pull by digest, migrate and health-check
[WP-D1]`, `feat(mtls): staging CA and client certificate tooling
[WP-D1]`, `feat(backup): nightly dumps and a weekly verified restore
[WP-D1]`, `docs(runbooks): record the first deploy and restore [WP-D1]`.

### WP-D2: `cutover` (A-M5)

Prepare in wave 5 of the decision record, execute at A-M5. Read first:
spec `07` A-M5, `05 §6` (the old names stay with the predecessor until
then), the predecessor's `infra/` and its backups (`_utm-final-backup`
holds its last dumps), the authority's WP-20 (registry import) and
WP-24 (deploy-staging). Build: `docs/RUNBOOKS/cutover.md`: the
pre-checks (the authority holds the imported registry and the picture
has run for a week; the receivers' keys re-issued at the authority;
the lab's scenarios green against the staging images), the DNS steps
for `utm.chikox.net` and `ingest.chikox.net` (retire, or point at the
authority's public pages, the owner's call), the receiver
reconfiguration order (one receiver first, watched, then the rest),
the rollback (the predecessor's compose kept stopped but intact for 30
days), and the verification after cutover (every receiver seen at the
authority within its heartbeat; no frame reaches the old ingest).
Done when the runbook has been rehearsed once on a scratch host with
the predecessor's final backup restored, and the rehearsal's
observations are in it.

### WP-D3: `production-handover`

Not scheduled. Written when GCAA answers Q16 (hosting, assessment) and
cisp Q16 (key custody). Scope: the production domain and issuer
variables, the state CA replacing the staging one, key custody (file
PEM → HSM/KMS, two-key JWKS overlap for rotation), the security
assessment checklist, and the handover of this repo.

---

## 4. Open questions (owner-only; not decided here)

| # | Question | Default used by WP-D1 | Who answers |
|---|---|---|---|
| D-Q1 | Droplet sizing (decision record §2.1): resize to ≥ 4 vCPU / 8 GB, or a second host for the DSS and the lab stack. | The compose is split for either; WP-D1 deploys the systems on the current droplet and the lab's `make demo` measures the footprint first. | owner (money) |
| D-Q2 | Where the restore check reports (issue, mail, chat). | A GitHub issue in this repo on failure, a comment on success. | owner |
| D-Q3 | Object storage for backups and the basemap mirror (which account, region, retention beyond 30 days). | DigitalOcean Spaces in the droplet's region, 30 days; longer retention per the `05 §4` national-choice rows once answered (spec Q8). | owner; DPO for retention |
| D-Q4 | When production domains and state hosting take over (spec Q16). | Staging on the droplet until A-M5; WP-D3 after GCAA's answer. | GCAA |
| D-Q5 | Signing-key custody for the CISP and the authority (cisp Q16, decision record §2.1). | File-mounted PEM on the host, two-key JWKS overlap; the code reads a PEM path only. | the state |
| D-Q6 | Whether the DSS stays on this host in production (authority Q-A16, spec Q7). | Beside the CISP compose on the droplet. | GCAA |

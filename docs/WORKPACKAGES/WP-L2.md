# WP-L2: `dss-compose`

Branch `feat/WP-L2-dss-compose`. No spec milestone of its own; it is the
prerequisite of every standards work package (decision record §3 L2).
Owns `deploy/` (the lab stack compose and its env), `cmd/lab-issuer`,
`internal/issuer`, `make dss-up`, `make dss-down`. Depends on nothing in
this repo. Consumers: ussp WP-9, WP-13, WP-19; ansp WP-9; authority
WP-14; WP-L5 (`sim-ussp`), WP-L7 (the qualifier runs against this DSS);
the staging droplet (`docs/deploy/PLAN.md` WP-D1 includes this compose).

## Read first

1. `docs/PLAN.md §1` D4, D5, `§4` (issuer and DSS rows), `§7.1` L-Q2.
2. `docs/decisions/2026-10-02-cross-plan.md`: M18 (audience = host of
   the target's base URL; `audience` and `resource` parameters; national
   scopes bound per client, `utm.*`/`rid.*` free), M23 and Appendix B
   (the scope catalogue; `dp.observe` to the lab client only), M24
   (client ids), Appendix A (the JWT contract), §4 preamble (the lab
   issuer replaces the authority's token service until A-M4).
3. Spec `00 §6.2` (JWT rules), `02 F6` (DSS API, `uss_base_url`,
   subscriptions, `TimeSyncMaxDifferentialSeconds`), `02 §1` Auth row,
   `06 §3` (scopes), `06 §4` (no test keys committed), `05 §6` DSS and
   token-service rows, `01 §5`.
4. `uspace-core/auth`: `Issuer`, `NewIssuer`, `Issue`, `JWKS`,
   `Verifier`, `Config` (the verifier the systems run; the issuer must
   produce what it accepts: RS256, `kid`, `jti`, `exp` ≤ 1 h, `scope` as
   a space-separated string).
5. InterUSS `dss` repository at the commit you pin: its deployment docs
   for a local instance (compose or the documented single-node setup),
   the datastore it expects at that commit, the flags for accepted
   audiences and the trusted public key or JWKS, and its `dummy-oauth`
   (reference for the token endpoint shape; not used). Record the
   commit and the image digest in `deploy/dss/SOURCE`. Do not assume
   the flag names from memory: read them at the pinned commit (E-04).
6. LESSONS E-02, E-05, B-14.

## What to build

### `cmd/lab-issuer`

- `POST /oauth/token`: client credentials (HTTP Basic or body), `scope`,
  `audience` (and RFC 8707 `resource`; either names the target host).
  Clients from `deploy/issuer/clients.yaml`: `lab-01` (every national
  scope plus `dp.observe`), `authority-01`, `cisp-01`, `ansp-01`,
  `ussp-<code>-01` (`DEV01` in the lab), `sim-ussp-01` (the peer),
  each with its allowed national scopes and audiences; any audience for
  `utm.*` and `rid.*` scopes (M18). Secrets are generated at first start
  into a git-ignored file and printed once; keys with `rsa.GenerateKey`
  at start, `kid` = date + counter; a `--key-file` for a stable staging
  key (PEM path only, never a committed key).
- `GET /.well-known/jwks.json` (`use: sig`), `GET /healthz`.
- Refusals as `problem/v1` with the slug (`unauthenticated`,
  `forbidden_scope`, `forbidden_audience`), counted.
- The issued token must verify with `core/auth.Verifier` configured
  with this issuer: one test proves it for every client and scope
  combination in `clients.yaml`, and one proves each refusal (E-01).

### `deploy/compose.yaml`

Profiles `dss` (the DSS and its datastore, image by digest, accepted
audiences = the DSS service name and `DSS_PUBLIC_HOST` from env, trusted
key = the lab issuer's JWKS or public key as the pinned commit's docs
describe), `issuer` (the lab issuer), and later `sim` (WP-L5) and
`systems` (WP-L6: the four systems from GHCR). One isolated network;
nothing published except Caddy's ports on the droplet. `deploy/.env.
example` lists every variable with a comment; `deploy/README.md` says
what `make dss-up` starts, how long it takes, and how to read the DSS
logs.

`make dss-up` brings up `dss` + `issuer` and then **proves** them: it
requests a token for `sim-ussp-01` with `utm.strategic_coordination`
and audience = the DSS host, calls `GET /dss/v1/operational_intent_references`
on an empty area with it, and prints the response code; it also proves
the refusal (no token → 401) so the success line is known to be a
success (E-02). `make dss-down` removes the volumes.

## Done when

- [ ] `make lint`, `go test -race -shuffle=on ./...` clean.
- [ ] `make dss-up` on a clean checkout with Docker: both proofs
  printed; `deploy/dss/SOURCE` names the commit and the digest; the
  README states the time and memory it took on the authoring machine
  (measured, E-05).
- [ ] The issuer's tokens verify with `core/auth.Verifier` for every
  client in `clients.yaml`; every refusal tested beside its acceptance.
- [ ] No key, secret or token in the repository (gitleaks passes; the
  generated files are git-ignored and named in `.gitignore`).
- [ ] The compose file is consumable as an include by
  `docs/deploy/PLAN.md` WP-D1 (no absolute paths, env-driven hosts).

## Commits

`feat(deploy): compose the InterUSS DSS by digest with the lab issuer   [WP-L2]`,
`feat(deploy): issue ecosystem tokens from the lab issuer with the host audience rule   [WP-L2]`,
`test(deploy): verify every issued token with core's verifier and refuse the rest   [WP-L2]`,
`docs(deploy): describe make dss-up and what it proves   [WP-L2]`.

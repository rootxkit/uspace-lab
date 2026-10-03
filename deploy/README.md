# The lab stack: DSS and lab issuer

`deploy/compose.yaml` runs the InterUSS DSS (pinned by digest, never
patched; `dss/SOURCE` names the commit) and the lab token service
`cmd/lab-issuer`, which stands in for the authority's token service until
A-M4 (decision record §4). Brief: `docs/WORKPACKAGES/WP-L2.md`.

## `make dss-up`

Needs Docker with Compose v2. On a clean checkout:

```
make dss-up        # or deploy/dss-up.sh
```

It:

1. copies `deploy/.env.example` to `deploy/.env` if there is none;
2. builds the issuer image and starts the profiles `dss` and `issuer`:
   CockroachDB (single node, insecure, on the internal network only), the
   three DSS schema migrations (`rid`, `scd`, `aux_`), the DSS
   core-service, and the issuer; waits until every service is healthy;
3. **proves** them, with `lab-issuer probe` run inside the issuer
   container (nothing is published, so the proof runs on the network):
   - `POST /dss/v1/operational_intent_references/query` **without a
     token** must answer 401, so the next 200 is known to come from the
     token;
   - a token for `sim-ussp-01` with `utm.strategic_coordination` and
     audience = the DSS host (`dss`), then the same search over the
     empty area of `DSS_PROBE_*`: must answer 200 with an
     `operational_intent_references` list (the count is printed);
   - a token for `DSS_PROBE_WRONG_AUDIENCE` must be refused by the DSS
     with 401, so the accepted-audience list is in force;
4. prints one `docker stats` sample per container.

The script exits non-zero if any proof answers otherwise. Output of a
run (2026-10-03, see the measurements below):

```
ok     POST /dss/v1/operational_intent_references/query without a token -> HTTP 401 (want 401)
ok     POST /dss/v1/operational_intent_references/query as sim-ussp-01, aud dss, scope utm.strategic_coordination -> HTTP 200 (want 200), 0 operational intent references
ok     POST /dss/v1/operational_intent_references/query with aud not-the-dss.invalid -> HTTP 401 (want 401)
dss-up: all proofs passed
```

The brief names `GET /dss/v1/operational_intent_references`; the pinned
DSS has no such route. The search is
`POST /dss/v1/operational_intent_references/query` (read at the pinned
commit, `dss/SOURCE`).

`make dss-down` (`deploy/dss-down.sh`) removes the containers, the
network and the volumes (the DSS datastore), then checks that nothing of
the project is left. It keeps `deploy/local/`; delete that directory to
start over with a new key and new secrets.

## Time and memory (measured)

Measured on the authoring machine, 2026-10-03: Windows 11, Docker Desktop
28.5.1 (Compose v2.40.3), 16 CPUs and 7.7 GiB given to Docker; this
repository at the commit of this change, the images of `dss/SOURCE`,
`CRDB_CACHE=64MiB`, `CRDB_MAX_SQL_MEMORY=128MiB`.

| Run | Time to "all proofs passed" |
|---|---|
| first run (CockroachDB image pulled, issuer image built; DSS image already local) | 71 s |
| second run (images cached, datastore volume new) | 25 s (stack healthy after 20 s) |

| Container | Memory, idle after the proof (`docker stats`, 5 samples over the two runs) |
|---|---|
| `dss-crdb` (CockroachDB v24.1.3) | 314-333 MiB |
| `dss` (core-service) | 10-11 MiB |
| `lab-issuer` | 7-8 MiB |
| total | about 350 MiB |

These are idle figures with an empty datastore, not a load measurement;
WP-L6 measures the demo footprint (docs/PLAN.md L-Q1). CockroachDB's
pools are bounded by the two `CRDB_*` variables; its resident size above
them is its own runtime.

## Logs

```
docker compose -p uspace-lab -f deploy/compose.yaml --env-file deploy/.env logs -f dss
docker compose -p uspace-lab -f deploy/compose.yaml --env-file deploy/.env logs dss-migrate-scd
docker compose -p uspace-lab -f deploy/compose.yaml --env-file deploy/.env logs lab-issuer
```

The DSS logs in the console format; a refused token is logged with its
reason (`Invalid access token audience`, `Access token validation
failed`, `missing scopes`). `-dump_requests` is deliberately off: it
would write every bearer token to the log.

## The lab issuer

- `POST /oauth/token`: client credentials (HTTP Basic or `client_id` and
  `client_secret` in the form body, never both), `scope`
  (space-separated), and `audience` (a bare host) or RFC 8707 `resource`
  (an absolute URI; its host). Answer: RFC 6749 §5.1 JSON
  (`access_token`, `token_type`, `expires_in`, `scope`). The token is a
  `uspace-core/auth.Issuer` RS256 JWT: `iss` = `LAB_ISSUER_URL`, `sub` =
  the client id, `aud` = the host, `scope`, `iat`, `exp`, `jti`, `kid`.
- Clients: `issuer/clients.yaml`. National scopes only for a client's
  audiences (hosts from `LAB_*_AUDIENCES`); `utm.*` and `rid.*` for any
  audience (decision record M18); `dp.observe` to `lab-01` only (M23).
- Refusals: `problem/v1` bodies with the slug `unauthenticated` (401),
  `forbidden_scope` (403), `forbidden_audience` (403), `invalid_request`
  (400), counted as `refused_<slug>` and shown with `issued` on
  `GET /healthz`.
- `GET /.well-known/jwks.json`: the public key, `use: sig`, `alg: RS256`.

State, all under `LAB_STATE_DIR` (`deploy/local/`, git-ignored):

| File | What |
|---|---|
| `signing-key.pem` | the signing key, generated at first start (RSA 3072, mode 0600), kept across restarts; its `Kid` header is the kid, date plus counter (`20261003-1`) |
| `kids.log` | one line per generated key (the counter) |
| `client-secrets.json` | one secret per client, generated at first start and printed once in the issuer's log; a client added to `clients.yaml` later gets one at the next start |
| `public/issuer-public.pem` | the public key in the PKIX PEM form the DSS's `-public_key_files` reads |
| `public/jwks.json` | the JWKS, for verifiers given static keys |

The DSS reads the public key file **once** at start. The issuer keeps its
key across restarts for that reason; if `deploy/local/` is deleted while
the stack runs, restart the DSS too (`make dss-down && make dss-up`).

For a stable staging key, set `LAB_ISSUER_KEY_FILE` to a PEM path (and
`LAB_ISSUER_KID` if the PEM has no `Kid` header). Never commit a key.

## Using it from a system or the droplet

- Token URL `http://lab-issuer:8080/oauth/token`, issuer
  `LAB_ISSUER_URL`. `uspace-core/auth.Verifier` fetches a JWKS over
  HTTPS only (plain HTTP to localhost only): inside compose, give the
  verifier the static keys of `deploy/local/public/jwks.json`, or reach
  the JWKS through Caddy over HTTPS.
- DSS base URL `http://dss:8082`; token audience `dss` inside the
  network, `DSS_PUBLIC_HOST` from outside.
- The compose file has no absolute path and no host in it; include it
  from the droplet compose (docs/deploy/PLAN.md WP-D1) and set the
  variables of `.env.example` there.

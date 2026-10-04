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
would write every bearer token to the log. So is the default log level:
at `info` core-service logs every request with its headers, the bearer
token included, `-dump_requests` or not (387 such lines in one WP-L6
systems run), so it runs at `-log_level=warn`, where the refusals and
errors still appear.

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

## `make sim-ussp-up`: the peer USSP against this DSS

WP-L5's `cmd/sim-ussp` runs in the profile `sim` (image
`deploy/sim-ussp/Dockerfile`) on the lab network, as the issuer client
`sim-ussp-01`:

- DSS-bound tokens: `utm.strategic_coordination` and
  `rid.service_provider`, audience `dss`, from
  `http://lab-issuer:8080/oauth/token`; the secret is read from
  `client-secrets.json` in the state directory (mounted read only).
- Incoming tokens: issuer `LAB_ISSUER_URL`, audience `sim-ussp`, keys
  from `public/jwks.json` (static, for the HTTPS-only JWKS fetch rule
  above).
- Writes: an ISA over `SIM_USSP_ISA`
  (`PUT /rid/v2/dss/identification_service_areas/{id}`, answered 200)
  and one operational intent reference over `SIM_USSP_INTENT`
  (`PUT /dss/v1/operational_intent_references/{id}`, answered 201),
  `uss_base_url` `http://sim-ussp:8093`. Its key holds the OVNs the DSS
  returns for the references already in that volume, and the ids are
  new on each start, so a restart is not refused with 409 (both were
  found running against this DSS; the WP-L5 double had answered 200
  and modelled neither). Then it serves `/uss/flights` and
  `/uss/v1/operational_intents/{id}`, vehicles on `sim-ussp:15562/udp`.

`deploy/sim-ussp-up.sh` starts the stack with the profile (and the DSS
and issuer, if they are not up), then succeeds only if sim-ussp logged
both writes with the id, version and OVN the DSS returned, is still
serving, and a search of the DSS by the issuer's probe over the
`SIM_USSP_INTENT` area finds at least one reference there. It adds the
`SIM_USSP_*` and `LAB_SIM_USSP_IMAGE` variables to an existing
`deploy/.env` from `.env.example` when they are missing. Run of
2026-10-03 (Docker Desktop 28.5.1, Windows 11), on a fresh datastore,
then again with sim-ussp restarted; a deliberately invalid intent
(lower altitude above upper) made it exit 1 with the DSS's 400:

```
sim-ussp-up: {"msg":"ISA written","id":"f31f6e42-2040-464b-9c9c-cf5139ef0d18","version":"1hmoi44ls6jr8",...}
sim-ussp-up: {"msg":"operational intent reference written","id":"6ae4bb08-35a4-4dce-9830-a83f9686ce2c","ovn":"bdvt7nUXcZOd7W2DaJHcWfj5YolMQT12z8FpZA1CLPU_",...}
ok     POST /dss/v1/operational_intent_references/query as sim-ussp-01, aud dss, scope utm.strategic_coordination -> HTTP 200 (want 200), 1 operational intent references
sim-ussp-up: the DSS holds 1 operational intent reference(s) in the SIM_USSP_INTENT area; ...
(restarted) ... 200 (want 200), 2 operational intent references
```

A reference of another USS in the same volume is still refused (409,
`missing_operational_intents`): the DSS gives its OVN only to its
manager, and sim-ussp does not fetch it from that USS.

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
- Tokens from the authority's token service: the DSS trusts only the
  files of `DSS_PUBLIC_KEY_FILES` (default: the lab issuer's key). Put
  the public half of the authority's token key under
  `LAB_STATE_DIR/public/` and list it beside the lab issuer's key; the
  DSS still checks the audience (`dss`, `DSS_PUBLIC_HOST`) and the scopes
  of every request. The files are read once at start, so a rotation of
  the authority's key needs the new public key listed and a DSS restart.

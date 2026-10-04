# Conformance targets

One file per system the suite runs against. Every host, secret and
identifier of a deployment is a `${VAR}` (`${VAR:-default}` for an
optional one): the files are the same for every deployment (INV-03,
spec `06 §4`). A run with a required variable unset stops and names
every unset variable. Values come from the environment, or from a
`KEY=VALUE` file given as `CONFORMANCE_ENV` (`--env-file`).

| Target | The system's hook | Required | Useful |
|---|---|---|---|
| `ansp.yaml` | uspace-ansp `make conformance-target` (`testdata/conformance/`) prints the values | `ANSP_CONFORMANCE_BASE_URL`, `ANSP_CONFORMANCE_AUDIENCE`, `LAB_TOKEN_URL` | `LAB_SECRETS_JSON` (default `deploy/local/client-secrets.json`), `ANSP_CONFORMANCE_SESSION_FILE`, `ANSP_CONFORMANCE_ACK_ID`, `CONFORMANCE_ANSP_OPENAPI` |
| `authority.yaml` | uspace-authority `docs/PLAN.md §9` conformance row; dp-poller's observation hook (Q-A7) | `AUTHORITY_CONFORMANCE_BASE_URL`, `LAB_TOKEN_URL` | `AUTHORITY_CONFORMANCE_AUDIENCE`, `AUTHORITY_CONFORMANCE_SESSION_FILE`, `AUTHORITY_CONFORMANCE_OPERATOR` (a registration for REG-NOPII), `AUTHORITY_CONFORMANCE_OBSERVATION_URL`, `CONFORMANCE_AUTHORITY_OPENAPI` |
| `cisp.yaml` | uspace-cisp `make conformance` calls `conformance/cisp/run` with `CISP_BASE_URL` and `CONFORMANCE_ENV` (its Q47) | `CISP_BASE_URL` | `CISP_READER_TOKEN`, `CISP_AUTHORITY_TOKEN`, `CISP_ANSP_TOKEN`, `CISP_AUTHORITY_SIGNING_KEY_FILE`/`_KID`, `CISP_JWKS_URL`, `CISP_ISSUER_URL`, `CONFORMANCE_PUBLISH`, `CONFORMANCE_WEBHOOK_LISTEN`/`_URL` |
| `ussp.yaml` | uspace-ussp `make conformance` (its WP-19, not yet built) | `USSP_CONFORMANCE_BASE_URL`, `LAB_TOKEN_URL` | `USSP_CONFORMANCE_OPERATOR_TOKEN_URL`/`_CLIENT_ID`/`_SECRET_FILE` (an operator client of the USSP), `USSP_CONFORMANCE_SESSION_FILE`, `USSP_CONFORMANCE_RID_INJECTION_URL`, `USSP_CONFORMANCE_FLIGHT_PLANNING_URL` |
| `sim-ussp.yaml` | the onboarding candidate (`make sim-ussp-up`) | `SIM_USSP_CONFORMANCE_BASE_URL` | `SIM_USSP_IMAGE` |

Every target also takes `<SYSTEM>_IMAGE` (the image under test by
digest, copied into the report) and `CONFORMANCE_TARGET_NAME` (the run's
name and the baseline it is judged against).

A target file may also name `ca_file` (the roots the target's TLS
chains to, the lab CA of the systems stack), `resolve` (host to address,
as curl's `--resolve`, so the stack's hosts reach 127.0.0.1 without
touching the machine's resolver), `client_cert` (an operation bound to
mTLS needs one), `fixtures` (existing resources by path parameter name:
the stale-`If-Match` and success checks use them; the suite never
guesses a resource) and `only` (restrict the national checks to these
operation ids).

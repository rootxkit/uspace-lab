# uss_qualifier configurations

InterUSS `uss_qualifier` at the commit `SOURCE` pins (v0.36.0,
`0fabe238`, image by digest; never patched, plan D5), against the lab
DSS and issuer (WP-L2). The suite reports what uss_qualifier checks and
nothing more (spec `09 §3`).

| File | Decides | Drives on the target | The other side |
|---|---|---|---|
| `f3411-sp.yaml` | F3411-SP (gate, L-Q3) | RID injection (`rid_injection`) | the mock USS as Display Provider |
| `f3411-dp.yaml` | F3411-DP (informative, L-Q3) | RID observation (`rid_observation`; the authority's `/v1/dp/observations`, Q-A7) | the mock USS as Service Provider |
| `f3548-scd.yaml` | F3548-SCD (gate, L-Q3) | flight_planning v1 (`flight_planning`) | the mock USS as the second USS |
| — | F3548-CP, F3548-CM | — | not applicable: v0.36.0 has no constraint processing or constraint management requirement set (`coverage.yaml`) |

`library/resources.yaml` holds the test's own resources (the Tbilisi
areas and flights around `SITL_HOME`, the evaluation settings),
`library/environment.yaml` the systems under test as `${QUALIFIER_*}`
placeholders that `conformance qualifier-render` fills from the
environment (an unset one is an error). Participant ids are client ids
(M24); audiences are hosts (M18).

## Running one

```
make dss-up                                        # the lab DSS and issuer
export MOCK_USS_AUTH_SPEC="ClientIdClientSecret(token_endpoint=http://lab-issuer:8080/oauth/token,client_id=lab-01,client_secret=<lab-01's secret>,send_request_as_data=true)"
docker compose -p uspace-lab-qualifier -f conformance/uss_qualifier/compose.yaml up -d --wait
export QUALIFIER_PARTICIPANT=authority-01 \
  QUALIFIER_TARGET_INTERFACE_URL=<the target's interface as the containers reach it> \
  QUALIFIER_TARGET_URL_REGEX='<a regex of the target's URLs>' \
  QUALIFIER_DSS_URL=http://dss:8082 QUALIFIER_DSS_HOST=dss \
  QUALIFIER_MOCK_RIDSP_URL=http://mock-ridsp QUALIFIER_MOCK_RIDDP_URL=http://mock-riddp \
  QUALIFIER_MOCK_SCD_URL=http://mock-scd QUALIFIER_MOCK_URL_REGEX='http://mock-.*' \
  QUALIFIER_TOKEN_URL=http://lab-issuer:8080/oauth/token
conformance/uss_qualifier/run-qualifier.sh f3411-dp.yaml local/qualifier/dp
CONFORMANCE_QUALIFIER_REPORTS="F3411-DP=local/qualifier/dp/qualifier/report.json" make conformance TARGET=authority
docker compose -p uspace-lab-qualifier -f conformance/uss_qualifier/compose.yaml down
make dss-down
```

`run-qualifier.sh` reads lab-01's secret from the issuer's
`client-secrets.json` and hands it to the container as `AUTH_SPEC` only.
uss_qualifier exits non-zero when a check fails; the script says so and
leaves the judgement to `cmd/conformance`, which reads the report's
checks for the target's participant.

The lab issuer issues the InterUSS automated-testing scopes the
qualifier and the mock USS request (`rid.inject_test_data`,
`dss.read.identification_service_areas`,
`interuss.flight_planning.direct_automated_test`,
`interuss.flight_planning.plan`) to `lab-01` only.

## What was observed (2026-10-05)

- `check.sh`: the image embeds `GIT_COMMIT_HASH=0fabe238...`, compose
  uses `SOURCE`'s digest, and uss_qualifier validated all three
  configurations (`--exit-before-execution`; baselines TB-6817edf,
  TB-cd980e3, TB-02c851b).
- A full `f3411-sp.yaml` run with the InterUSS mock SP standing in as
  the candidate (participant `mock-ridsp-candidate`), the lab DSS and the
  lab issuer: the run completed and wrote its report; 3464 checks passed
  for the lab DSS; the 7 checks of the candidate failed because the mock
  SP answered 401 to every injection. The cause, read in
  `monitorlib/auth_validation.py` at the pinned commit: mock_uss accepts
  only a string `aud`, and the lab issuer (uspace-core `auth.Issuer`)
  writes `aud` as a one-element array. The DSS accepts both. A system
  under test verifies with uspace-core and is not affected; running the
  mock USS against the lab issuer needs a single-string audience (a
  uspace-core change, open).
- The InterUSS observation interface's scope at the pinned commit is
  `dss.read.identification_service_areas`
  (`uas_standards.interuss.automated_testing.rid.v1.constants.Scope.Observe`),
  not the `dp.observe` decision record Q-A7 names for the authority's
  hook: uss_qualifier will be refused by an authority that admits only
  `dp.observe` (open, for the authority and the decision record).

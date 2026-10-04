# Runbook: the systems stack and the owed scenario runs

How the lab runs the four systems' images together with the DSS and the
lab issuer, seeds them through their public APIs, and runs the owed
scenarios against them with ArduCopter SITL (docs/WORKPACKAGES/WP-L6.md).
Everything here was done for the first time on 2026-10-04; what was
observed is in `results/20261004-systems/README.md`.

`demo.yaml` (the ten-minute L-M1 script) is not written yet; this
runbook covers the stack, the seed and the per-scenario runs it will
build on.

## What it needs

- Docker with Compose v2, about 3 GB of memory for the stack (31
  containers; one `docker stats` sample is printed by `make demo`).
- Port 443 free on 127.0.0.1 (`DEMO_HTTPS_PORT` moves it).
- GeographicLib's `egm2008-2_5.pgm` in `DEMO_GROUND_DIR` (deploy/
  demo.env): the systems' geoid, and `targets.geoid` (R-16).
- Linux or WSL for SITL and the runner: ArduPilot built (`sim/sitl.env`
  names `SIM_VEHICLE` and `MAVPROXY`), `unshare` (util-linux).
- Go for the seed and the runner.

## 1. The stack: `deploy/demo-up.sh` (first half of `make demo`)

1. Copies `deploy/demo.env.example` to `deploy/demo.env` (git-ignored).
2. `deploy/systems/gen-secrets.sh`: the lab CA and Caddy's certificate
   for the five hosts, every system's keys, the database passwords
   (`deploy/local-demo/`, git-ignored).
3. Builds the ANSP image from `ANSP_SOURCE_COMMIT` if it is not present
   (the ANSP publishes none); every other image is pulled by digest.
4. Starts the DSS and the lab issuer, then writes each issuer client's
   secret to `local-demo/clients/<id>.secret`.
5. Starts the authority first and waits for its JWKS through Caddy (the
   CISP's api refuses to start without it), then every other service;
   waits for every health check and checks each migration exited 0.
6. `deploy/systems/prove.sh`: TLS is refused without the lab CA; each
   system's and the issuer's JWKS answers; a DSS query through the lab
   host is 401 without a token and 200 with a lab-01 token.
7. Prints one `docker stats` sample.

`deploy/demo-down.sh` (`make demo-down`) removes the project and checks
nothing of it is left; `--purge` also removes `local-demo/`.

The hosts are `*.uspace.test`. Inside the stack they are aliases of the
lab Caddy. From outside, `deploy/labns.sh <command>` runs a command with
those names resolving to 127.0.0.1 and the lab CA trusted, in its own
user and mount namespace (nothing on the machine changes); the seed
dials 127.0.0.1 itself.

## 2. The seed: `cmd/demo-seed`

Through the public APIs only. Order matters for SC-22:

```
# Receivers only: SC-22 needs an authority with no registry projection.
go run ./cmd/demo-seed --steps receivers scenarios/*.yaml
docker restart uspace-demo-authority-rid-ingest-1   # see "rid-ingest" below
# ... run SC-22 now (step 4) ...
go run ./cmd/demo-seed --steps registry,uspace,ussp,ansp scenarios/*.yaml
```

Before every run, open fresh sessions and write the targets file (a
console session ends after 30 minutes idle):

```
go run ./cmd/demo-seed --steps sessions --geoid <path to egm2008-2_5.pgm> \
  --sitl-reader 'bash -c "exec ~/ardupilot-venv/bin/python sim/mav_reader.py --sysid {sysid} --out udp:127.0.0.1:{out_port} --max-seconds {max_s}"' \
  --sitl-fly 'bash -c "exec ~/ardupilot-venv/bin/python sim/fly.py --link udpin:127.0.0.1:{fly_port} --plan -"' \
  scenarios/*.yaml
```

(`make demo-sessions` with `SITL_READER` and `SITL_FLY`.) It writes
`targets/local-demo.yaml` and `secrets/demo/`, both git-ignored.

Zones are published per scenario, just before it runs, because a
scenario's zone is near the origin every other scenario flies from:

```
go run ./cmd/demo-seed --steps zones --zones sc-03-zone-entry-exit scenarios/*.yaml
```

### Where the U-space airspace goes

The USSP image of the suite (6ec6238) has no DSS writer yet (its
WP-13): inside U-space airspace every intent is `pending_dss`, and its
telemetry is refused. So:

- `ussp-wp7-authorisation` and `ussp-wp12-restriction` run with the
  airspace about the origin (the default), as their notes require;
- the runs that need an authorised intent (`ussp-wp10-conformance`,
  SC-01, SC-02, SC-21, `ansp-inv02-manned`, SC-03) run with it moved off
  the area: `--steps uspace --uspace-north-m 60000`.

## 3. SITL

In WSL, with a fleet of three on its own ports (another fleet may hold
the defaults): in `sim/sitl.env` `SITL_INSTANCE_BASE=10`,
`SITL_OUT_PORT_BASE=14760`, `SITL_FLY_PORT_BASE=14860`. Restart the fleet
before every run (the vehicles must start on the ground at their homes):

```
sim/stop_sitl.sh; PATH=$HOME/ardupilot-venv/bin:$PATH sim/run_sitl.sh -n 3
```

## 4. A run

```
GOOS=linux go build -o deploy/local-demo/bin/scenario ./cmd/scenario   # once
deploy/labns.sh deploy/local-demo/bin/scenario run --targets targets/local-demo.yaml \
  --vehicles sitl --lab sim/sitl.env --run <run id> scenarios/<file>.yaml
```

Between two runs on one USSP, wait at least 130 s after the last one
ended: the USSP ends a flight after 120 s of silence
(`flight_end_after_s`), and a run started sooner continues the previous
run's flight, which is not bound to the new intent (its conformance
stays `unknown` for the whole run; seen once).

## What was learned standing it up (2026-10-04)

- **rid-ingest binds 127.0.0.1 when it starts with no receiver keys**
  (its R-06) and stays there when keys are added later: register the
  receivers, then restart rid-ingest. Until then every batch failed
  "connection refused" at Caddy.
- **The CISP's api refuses to start until the authority's JWKS answers**
  (or its cache has a copy): demo-up starts the authority first.
- **The ANSP refuses its system id as an audience**: `ANSP_SYSTEM_ID`
  is `uspace-ansp`, and `ansp` stays the lab alias in `ANSP_AUDIENCES`.
- **The ANSP asks for the DSS audience with the port** (`dss:8082`),
  which the lab issuer refuses: the systems reach the DSS at
  `https://lab.uspace.test`, as on the droplet.
- **A U-space height ceiling needs terrain**: with
  `max_height_agl_m` set and no terrain, the USSP rejects every intent
  (`airspace_ceiling_not_judged`); the seed sets none.
- The authority's and the ANSP's console APIs read the session as a
  bearer (`session_bearer: true` in the targets file).
- SITL's own UTC runs 1.5 to 1.7 s behind the host: `mav_reader`
  stamps on the host clock by default (`--clock host`).

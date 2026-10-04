# Scenarios

A scenario is the steps of a `knowledge/scenarios.md` entry (or a `07`
done-when) written as data the runner executes and judges
(docs/PLAN.md D6). `go run ./cmd/scenario check scenarios/*.yaml`
validates them; `go run ./cmd/scenario run --targets <file> <scenario>`
runs one and writes `results/<run>/<scenario>.json`.

## The format (`format: scenario/v1`)

| Member | Meaning |
|---|---|
| `id`, `title`, `source`, `owners` | What it is and where it comes from (`source` names the SC, the runbook or the done-when). |
| `policy` | The policy file the expectations assume (`policy/demo.yaml`, L-Q4); printed with its `policy_version` in every result. |
| `systems` | The systems whose streams are collected (`ussp`, `authority`, `ansp`). |
| `reference` | `true`: runnable against the lab's reference target (CI); `false`: only against the systems (the kind it expects is a system's own judgement). The runner refuses, before starting, a scenario the targets cannot judge. |
| `country` | ED-269 country of the scenario's zones. |
| `duration_s`, `tail_s` | Hard bound after t0; time kept after the last vehicle finishes. |
| `aircraft[]` | `name`, `sysid` (the SITL instance), `serial`, `operator_reg`, `operator_id`; `operator` (sim-operator streams it to a USSP: `system`, `client`, `transport`, an `intent` filed and activated before t0 (a `center` and `radius_m`, or `boxes` of `south_m`, `north_m`, `west_m`, `east_m`, one polygon volume each), `drop_rate`, `latency_s`); `receivers` (the receivers that hear its Remote ID); `mark_alt_invalid` (S-36). |
| `receivers[]` | sim-receiver: `id` (registered at the authority), `at`, `transport` (`pack` or `single`), `hae` (`geoid` or `gps`), `drop_rate`, `latency_s`, `seed`, `rssi_dbm`. |
| `feeds[]` | Manned traffic: `kind` `ansp_stream` (sim-ansp-feed) or `adsb_file` (sim-adsb), with `tracks` (straight legs) or a `recording`. |
| `zones[]` | `id`, `type`, a `square` or `circle`, `lower`/`upper` with their reference (`AGL`, `AMSL`, `WGS84`), an optional `window`. The reference target loads them; for a systems run they are written beside the result as `<scenario>.zones.ed269.json` for import. |
| `steps[]` | `do`: `takeoff` (arm, then climb to `alt_rel_m`), `goto` (`to`, `speed_ms`, `tolerance_m`), `hold` (`for_s`), `land`, `knob`, `request` (`system`, `method`, `path`, `body`, `headers`, `expect`, `capture`). `at_s` delays a step to t0 + `at_s`; a flight step without it follows the aircraft's previous step. An `id` makes the step a mark. |
| `expect[]` | `system`, `kind` (the system's own kind: `proximity`, `zone_incursion`, `violation/v1` kinds, `degraded`, `manned_track`), `aircraft`, `peer`, `subject`, `detail` (members the raise's detail must carry with equal values, e.g. `limit_not_judged: true`); `raise` and `clear` windows (`after` a mark, `min_s`, `max_s`, a clear `reason`); `hold_until` (no clear before a mark). |
| `never[]` | Matchers that must raise nothing. |
| `expect_intents[]` | The decision (and state) the USSP must give an aircraft's intent. |
| `judged_kinds` | `system:kind` pairs whose unexpected raises count as false alerts (besides the kinds `expect` and `never` name). |

No coordinate is written in a scenario (INV-03): every position is an
offset in metres north and east of the origin, `SITL_HOME` in
`sim/sitl.env`, and the homes are `run_sitl.sh`'s (25 m apart east).
Request bodies take `"${lat:N,E}"` and `"${lng:N,E}"` (numbers),
`${time:S}` (t0 + S, RFC 3339), `${run}`, a value an earlier request
captured, and `${extra:key}` from the targets file.

### Marks

`t0`; a step `id` is the moment every vehicle it names **confirmed** it by
its own telemetry (sim/fly.py, E-08; the synthetic vehicle likewise);
`<id>.start` when the first one started it. A knob or request step's mark
is when it was executed. A window measured from a mark that never
happened fails, naming the mark.

### Knobs

`operator_link: down|up` (close the telemetry socket and queue; replay as
backlog), `operator_stream: stop|start` (connected, silent: the USSP's
lost link), `receiver: down|up` for `receivers`, `feed: live|stale|outage`
for `feeds`, and `serial` / `address` for an aircraft's transmitter
(SC-10, SC-11).

In a scenario a USSP owes (`owners` names `ussp`), two aircraft may not
file overlapping intents unless `expect_intents` expects one of them
`rejected`: a USSP that deconflicts strategically authorises only the
first, and the second has no flight to alert on.

## The verdict

Every expected raise and clear observed inside its window; nothing on the
never-list; missed alerts and false alerts both zero; every simulator's
ledger balanced (accepted + refused + dropped + duplicate = sent, per
source: no silent loss); a sample from every vehicle; every step the
vehicles were asked to fly confirmed. The runner prints what it saw,
including, for a miss, the alerts it saw instead (E-04), and exits
non-zero on any failure.

## Targets

`targets/reference.yaml` runs against the lab's reference target
(uspace-core's alerting monitor under the scenario's policy, in process):
the runner's self-check and CI target, **never evidence for a system**.
`targets/systems.example.yaml` is the template for the systems' images:
base URLs, the operator clients, the receivers' keys, sessions for
request steps, the SITL commands, and the image digests that every result
records.

## The scenarios here

| File | Source | Reference | Owed by |
|---|---|---|---|
| `kt4-baseline.yaml` | 07 KT-4 | yes | lab (KT-4) |
| `sc-01-hover-inside-minima.yaml` | SC-01 | yes | ussp WP-11 |
| `sc-02-head-on-and-short-return.yaml` | SC-02 | yes | ussp WP-11 |
| `sc-03-zone-entry-exit.yaml` | SC-03 | yes | authority, ussp WP-12 (zone path) |
| `sc-21-slow-to-hover.yaml` | SC-21 | yes | ussp WP-11 |
| `sc-22-missing-inputs-visible.yaml` | SC-22 | yes | authority (each deployment) |
| `selftest-wrong-expectation.yaml` | WP-L5 tests | yes (fails by design) | lab |
| `ussp-wp7-authorisation.yaml` | ussp RUNBOOKS/WP-7 | no | ussp WP-7 |
| `ussp-wp10-conformance.yaml` | ussp RUNBOOKS/WP-10 | no | ussp WP-10 |
| `ussp-wp12-restriction.yaml` | ussp RUNBOOKS/WP-12 | no | ussp WP-12 |
| `ansp-inv02-manned.yaml` | ansp INV-02, SC-15 | no | ansp WP-4, WP-6 |
| `authority-sc08-rid-switch.yaml` | SC-08 1-3 | no | authority |

## The owed system runs

The system repositories' runbooks list lab runs still owed. Each maps to
scenarios here:

| Owed | Scenarios |
|---|---|
| uspace-ussp WP-7 (S-M1) | `ussp-wp7-authorisation.yaml` |
| uspace-ussp WP-10 (S-M2) | `ussp-wp10-conformance.yaml` |
| uspace-ussp WP-11 (S-M3) | `sc-01-hover-inside-minima.yaml`, `sc-02-head-on-and-short-return.yaml`, `sc-21-slow-to-hover.yaml` |
| uspace-ussp WP-12 (N-M1, S-M4) | `ussp-wp12-restriction.yaml`, `sc-03-zone-entry-exit.yaml` (zone path) |
| uspace-ansp INV-02 (WP-4, WP-6) | `ansp-inv02-manned.yaml` |
| uspace-authority SC-* | `sc-03-zone-entry-exit.yaml`, `sc-22-missing-inputs-visible.yaml` (before the registry is seeded, its AGL zone imported), `authority-sc08-rid-switch.yaml` |

To run one:

1. Bring up the systems from their images (digests), with the same geoid
   as `targets.geoid` and the lab issuer or the authority's token
   service.
2. Copy `targets/systems.example.yaml` to `targets/<name>.yaml` and fill
   it: base URLs, the image digests, and the secret files under
   `secrets/` (gitignored): at the USSP an operator account with machine
   clients (`op-a`, `op-b`, `default`) and the scenario's serials bound to
   them; at the authority a receiver registered for each scenario
   receiver (`POST /v1/rid/receivers`, keys as shown once), a console
   session; at the ANSP a supervisor session and CSRF token, and a
   client granted `ansp.traffic`; `extra.uspace_airspace_id`.
3. Put `SITL_HOME` (sim/sitl.env) inside the deployment's U-space
   airspace and start the fleet: `make sim N=3` (Linux or WSL).
4. Import the scenario's zones when it has some: the runner writes
   `results/<run>/<scenario>.zones.ed269.json` placed by the run's
   origin (a dry run with `--vehicles synthetic` writes it too).
5. Run it: `go run ./cmd/scenario run --targets targets/<name>.yaml
   --vehicles sitl --lab sim/sitl.env scenarios/<file>.yaml`.
6. Record the result file's path, its verdict and the observed numbers
   in the system's runbook ("Lab run (owed)"): the result names the lab
   and core commits, the image digests as configured, the policy and
   every raise and clear with its time.

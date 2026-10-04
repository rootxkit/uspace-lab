# uspace-lab implementation plan

Status: plan for parallel implementation by independent agents. Branch
`plan/lab-workpackages`. Inputs: the spec in `docs/spec/` (`00 §6.1` lab
row, `00 §7`, `01 §5`, `02 F12`, `04 §1`–`§2`, `05 §6`–`§7`, `07` Phase 0
and Phase 6, `09 §3`), the knowledge base in `knowledge/` (`LESSONS.md`,
`scenarios.md`, the 18 vector files), the accepted cross-plan decision
record `docs/decisions/2026-10-02-cross-plan.md` (its §3 L1–L5, §4 and
§5.6 are the lab's work; its Appendix C is the console frame the lab
owns), `uspace-core` v1.0.0 (`vectors`, `odid`, `auth`, `geoid`,
`ed318`, `f3411`, `f3548`), the five system plans on `plan/initial`, and
the predecessor `rootxkit/utm` (read-only reference: `sim/`,
`tools/sitl_remote_id.py`, `tools/remote_id_sim.py`,
`tools/fake_rid_sp.py`, `infra/basemap/fetch_basemap.sh`).

Sections: 1 scope and decisions; 2 repository layout; 3 what the lab
publishes (KT-2); 4 the lab stack; 5 work packages and waves; 6
engineering standards and CI; 7 open questions; 8 what this plan applied
from the decision record.

---

## 1. Scope and decisions

`uspace-lab` is the sixth repository: it holds the specification, the
knowledge base, the contracts aggregate, the lab stack (DSS, issuer,
simulators, SITL), the scenario suite, the load and chaos tests, the
conformance suite and the demo (`00 §6.1`, `01 §5`, `07` Phase 0 and
Phase 6). It speaks only the public contracts of the other systems
(`02 F12`), ships nothing into their images, and holds no production
data.

Hard rules, inherited and permanent:

- **INV-01.** Nothing in the lab commands an aircraft. The SITL bridges
  read MAVLink and never write to the connection; the scenario runner
  drives SITL through a separate harness whose only job is to fly the
  simulated vehicle, and even that harness checks every step against
  the vehicle's own telemetry (LESSONS E-08). No bridge, simulator or
  generator has a send path towards a vehicle.
- **INV-02.** A safety-relevant work package in any system is done only
  when a lab scenario has raised and cleared its alert. The lab's
  scenario suite is therefore prerequisite work, not a final phase
  (decision record §4.3).
- **INV-03.** Thresholds and coordinates are configuration. No scenario,
  simulator or compose file hardcodes a home position, a policy figure or
  a hostname; the demo policy of §7 is a file the scenario loads and
  prints.
- **`trust: simulated` never reaches production.** Every simulator is
  built as its own binary and image under `cmd/sim-*`, never copied into
  a system image; the lab's keys and clients exist only in the lab
  issuer.

Decisions taken in this plan:

| # | Decision | Why |
|---|---|---|
| D1 | The lab is a Go module (`github.com/rootxkit/uspace-lab`, Go 1.27, `uspace-core` pinned by tag) for everything that speaks a system contract: the simulators, the bridges' encoding half, the lab issuer, the scenario runner, the national conformance tests, the load generator. Python stays where the spec puts it (`00 §6`): `sim/` (SITL launch, the pymavlink reader) and `knowledge/tools/`. | The encoders and signatures a simulator needs (`odid.Encode`, `auth.SignReport`, `geoid`, the `f3411`/`f3548` types, `ed318.Export`) exist once, in core, pinned by vectors. A Python re-implementation would be a second judgement. pymavlink is the only mature MAVLink reader and is receive-only in our use. |
| D2 | The MAVLink boundary is one process, `sim/mav_reader.py`: it reads each SITL vehicle (receive-only) and emits a neutral `sim/vehicle/v1` NDJSON line per sample on a local UDP port. Every bridge (`cmd/sim-operator`, `cmd/sim-receiver`) consumes that stream and never opens MAVLink itself. | One place for E-03 (wire offsets from pymavlink's definitions, never from memory); one place to prove receive-only; Go code that is testable without a simulator. |
| D3 | `schemas/common/` is owned here and consumed by everyone; every other schema under `schemas/` is a read-only mirror of the producing repo's file, pinned by `SOURCE`. The same for `api/<system>/openapi.yaml`. | Decision record M14 (shapes produced by several systems live in the lab), M11/M31 (one pinning mechanism), `04 §1`. |
| D4 | The lab issuer is `core/auth.Issuer` behind `POST /oauth/token` and `/.well-known/jwks.json`, with the client ids and audience rule of the decision record (M18, M24). It stands in for the authority's token service until A-M4 and is one allow-listed issuer among others afterwards. | §4.3 of the decision record: the authority's WP-2 must not gate anybody. |
| D5 | The InterUSS DSS runs from the published image pinned by digest with the datastore its pinned commit's compose uses; the lab records the commit in `SOURCE`. The lab never patches the DSS. | A patched reference implementation proves nothing about third-party conformance. |
| D6 | Scenarios are YAML (`scenarios/*.yaml`): aircraft, paths, receivers, feeds, zones, policy file, expected events with their timing. The runner is Go. The Python harness only flies. | Scenarios are data the owner and GCAA can read; the expected set is what the missed-alert count is measured against (`05 §7`). |
| D7 | The basemap bundle is a release artefact of this repo (not committed): a Georgia extract with a size budget, Protomaps glyphs with the Georgian ranges, sprites, `SOURCE.json`; a Tbilisi-only extract for the kit's Storybook is committed in `uspace-ui`, built by the same script. | Decision record M38, ui Q4. |
| D8 | Results are JSON files with the commit of this repo, of core and of every image digest under test (E-05); the results dashboard reads them. Nothing is reported that was not observed (E-04). | The lab produces the evidence decisions rest on. |

---

## 2. Repository layout

```
uspace-lab
├── docs/
│   ├── spec/                 the specification (00..09), with errata tables
│   ├── decisions/            accepted decision records
│   ├── deploy/PLAN.md        the deployment plan (moves to uspace-deploy)
│   ├── PLAN.md               this plan
│   └── WORKPACKAGES/         WP-L*.md briefs
├── knowledge/                LESSONS.md, scenarios.md, vectors/, tools/ (unchanged)
├── schemas/                  KT-2 aggregate (WP-L1)
│   ├── common/               owned here: envelope, track/telemetry, source/status,
│   │                         zone/applicable, console/{status,snapshot,subscribe}, problem
│   └── <system>/             read-only mirrors + SOURCE
├── api/                      KT-2 aggregate (WP-L1): <system>/openapi.yaml + SOURCE,
│                             index.md (generated), clients/{go,ts}/ (generated)
├── deploy/                   the lab stack (WP-L2): compose.yaml, dss/, issuer/, env
├── sim/                      SITL launch, mav_reader.py, sitl.env.example (WP-L5)
├── scenarios/                YAML scenarios (WP-L5, WP-L6)
├── cmd/                      Go binaries (WP-L2, WP-L5, WP-L6..L9)
│   ├── lab-issuer/           token service stand-in
│   ├── sim-operator/         F5: operator telemetry over WS /v1/telemetry
│   ├── sim-receiver/         F9: ODID datagrams to POST /v1/rid/observations
│   ├── sim-ansp-feed/        F4: a recorded ADS-B file as track/manned/v1
│   ├── sim-ussp/             F6: a second USSP against the lab DSS
│   ├── sim-adsb/             an e-conspicuity receiver file (aircraft.json / SBS)
│   ├── scenario/             the scenario runner
│   ├── loadgen/              the load generator (WP-L8)
│   └── results/              the results dashboard
├── internal/                 vehicle stream decoding, scenario model, results, assertions
├── conformance/              WP-L7: uss_qualifier configs, national tests, ED-318 tests
├── basemap/                  WP-L3: build script, size budget, verification
├── scripts/                  check-mirrors.sh, validate-examples.sh, pin.sh
├── Makefile                  dss-up, sim, demo, scenarios, load, chaos, conformance
└── .github/workflows/        ci.yml (lint, test, examples, mirrors), scenarios.yml, release.yml
```

Exclusive directory ownership per work package avoids merge conflicts;
the only shared files are `Makefile` (one target block per WP) and
`CHANGELOG.md`.

---

## 3. What the lab publishes (KT-2)

### 3.1 `schemas/common/` (owned here)

JSON Schema 2020-12, `$id = https://schemas.uspace.ge/<family>/<name>/v1.json`,
every schema with at least two examples under `examples/` and one
counter-example under `examples/invalid/` (E-01: a validator that only
sees valid documents proves nothing). Contents, from decision record M14,
M28, M29 and Appendix C:

| Schema | Content | Consumers |
|---|---|---|
| `envelope/v1` | `schema`, `msg_id` (ULID), `producer`, `ts`, `rx_ts`, `captured_at`, `time_source`, `backlog` (`04 §2`), plus `body` for stream frames | every WS frame, every `04` subject |
| `track/telemetry/v1` | the picture track of `04 §3` with `trust`, `source`, `source_instance`, `identification`, `cell` | authority, USSP, consoles |
| `source/status/v1` | adapter status every 2 s: type, instance, state (`live`, `stale`, `disabled`, `down`), `since`, `age_s`, counters | every system |
| `zone/applicable/v1` | a zone as judged applicable now, with `cis_applicability` | consoles, geo-awareness |
| `console/status/v1` | `connection_id`, `server_ts`, `policy_version`, `stale_after_s`, `live_max_age_s`, `dropped_frames`, `degraded[]`, `sources[]`; optional `datasets{}`, `projection_age_s`, `cis_version`, `cis_age_s`, `nats`, `resync_since`, `dp_state` | every browser-facing and machine-facing WS |
| `console/snapshot/v1` | `tracks[]`, `alerts[]`, `manned[]`, `zones_version` on connect and re-subscribe | consoles |
| `console/subscribe/v1` | client → server `{bbox, layers[]}` | consoles |
| `problem/v1` | RFC 9457 body with `errors: [{field, reason}]`, `truncated`, `type = https://schemas.uspace.ge/problems/<slug>` | every API, `uspace-ui` forms |

The envelope's `producer` and `time_source` enumerations are pinned to
`uspace-core/core` (`TimeSource`, `Trust`) by a check that reads the Go
source at the pinned tag, as `uspace-ui` does for its enums.

### 3.2 Mirrors (`schemas/<system>/`, `api/<system>/`)

Each producing repo owns its schemas and its `api/openapi.yaml`
(`04 §1`, `00 §7`). The lab holds a byte-for-byte copy with a `SOURCE`
file (`repo`, `commit`, `path`, `fetched_at`) and a CI job that re-fetches
each file at its pinned commit and fails on any difference. Bumping a pin
is its own `build:` commit (decision record §4.3). Ownership, from M14:

| Owner | Request/response schemas (carried by its `api/openapi.yaml`) | Pushed stream messages |
|---|---|---|
| cisp | `cis/change/v1`, `cis/restriction/v1`, `cis/ussp_list/v1`, `cis/uspace_requirements/v1` | — |
| authority | `occurrence/v1`, `rid/observation/v1` | `violation/v1` |
| ussp | `telemetry/v1`, `intent/request/v1`, `intent/decision/v1`, `intent/state/v1` | `alert/v1`, `traffic/product/v1` |
| ansp | `coordination/annex_v/v1` | `track/manned/v1` |

The aggregate index (`api/index.md`) and the generated clients
(`api/clients/go/<system>/` with oapi-codegen v2, `api/clients/ts/` with
`openapi-typescript`, through the kit's `uspace-ui-gen-api` once it
ships) are regenerated by CI and committed, so a consumer sees a diff
when a contract moves.

### 3.3 KT-3, the skeleton layout

The skeletons themselves are each repo's WP-0. The lab's share is the
check that the aggregate relies on: `scripts/check-layout.sh` confirms,
for every system repo at its pinned commit, that `api/openapi.yaml`,
`schemas/`, `migrations/{relational,timeseries}/`, `deploy/` and `web/`
exist and that `api/openapi.yaml` parses as OpenAPI 3.1. The Caddy
entries of KT-3 are deployment (`docs/deploy/PLAN.md` WP-D1).

---

## 4. The lab stack

| Component | What it is | WP |
|---|---|---|
| Lab issuer | `cmd/lab-issuer`: `POST /oauth/token` (client credentials; `audience` and RFC 8707 `resource`; national scopes bound per client, `utm.*`/`rid.*` free as in M18), `/.well-known/jwks.json`; clients `lab-01` (`dp.observe` included), `authority-01`, `cisp-01`, `ansp-01`, `ussp-<code>-01`, the simulated peer; keys generated at start and never committed (`06 §4`) | WP-L2 |
| DSS | InterUSS DSS by digest with its datastore; `accepted_jwt_audiences` = the DSS host and the compose alias; trusts the lab issuer's public key as its pinned commit's docs describe | WP-L2 |
| SITL | `sim/run_sitl.sh -n N` (from the predecessor; home from `sim/sitl.env`), `sim/mav_reader.py` → `sim/vehicle/v1` | WP-L5 |
| Operator simulator | `cmd/sim-operator`: one F5 client per aircraft, `telemetry/v1` at 1 Hz over `WS /v1/telemetry`, intents through `POST /v1/intents`, queue and `backlog` on reconnect (`02 F5` failure column) | WP-L5 |
| Receiver simulator | `cmd/sim-receiver`: ODID Basic ID, Location, System, Operator ID through `odid.Encode` (HAE = AMSL + N, R-16), signed datagrams to `POST /v1/rid/observations`, drop rate, latency, backlog, address and serial changes (SC-10, SC-11) | WP-L5 |
| ANSP feed simulator | `cmd/sim-ansp-feed`: a recorded ADS-B file as `track/manned/v1` frames in the envelope on `WS /v1/manned-traffic/stream`, outage control | WP-L5 |
| e-conspicuity file | `cmd/sim-adsb`: readsb `aircraft.json` or an SBS stream from a file (ussp Q11 default) | WP-L5 |
| Peer USSP | `cmd/sim-ussp`: F3411 SP (`/uss/flights`, details, ISA upkeep) and F3548 USS endpoints (operational intent references with `ovn`, notifications) against the lab DSS, enough to be the second USSP of S-M4 and the onboarding candidate of L-M4 | WP-L5 |
| Scenario runner | `cmd/scenario`: loads a YAML, starts the simulators, flies through the harness, collects frames, alerts and status, asserts the expected set and the counters, writes JSON results | WP-L5 (runner), WP-L6 (suite) |
| Basemap | `basemap/build.sh` → release artefact | WP-L3 |
| Conformance | `conformance/`: `uss_qualifier` configurations, national contract tests, ED-318 tests, onboarding procedure | WP-L7 |
| Load, chaos | `cmd/loadgen`, `scripts/chaos/` | WP-L8, WP-L9 |

`make demo` (L-M1) brings up the four systems from their images, the
DSS, the issuer, N SITL aircraft, a receiver, an ANSP feed and the peer
USSP, and runs the demo scenario end to end.

---

## 5. Work packages and waves

Each WP has a brief in `docs/WORKPACKAGES/WP-L<k>.md` that is complete
on its own. Branch `feat/WP-L<k>-<slug>`. Commit suffix `[WP-L<k>]`
(with the milestone where one applies: `[WP-L1 KT-2]`). Done-when always
includes: gofmt, vet, staticcheck and golangci-lint clean at the pinned
versions; `go test -race -shuffle=on` green; `ruff` clean on `sim/` and
`knowledge/tools/`; every example validated; CHANGELOG entry; results
and tool versions recorded (E-04, E-05).

| WP | Slug | Owns (exclusively) | Depends on | Milestone | Needed by |
|---|---|---|---|---|---|
| WP-L0 | `plan` | `docs/PLAN.md`, `docs/WORKPACKAGES/`, `docs/decisions/`, `docs/deploy/PLAN.md` | — | done on `plan/lab-workpackages` | everyone |
| WP-L1 | `contracts-aggregate` | `schemas/`, `api/`, `scripts/check-mirrors.sh`, `check-layout.sh`, `validate-examples.sh`, the `contracts` CI job | this plan | KT-2, KT-3 (check) | every repo (M11, M14, M29, M31); ui WP-14 |
| WP-L2 | `dss-compose` | `deploy/`, `cmd/lab-issuer`, `make dss-up` | — | — | ussp WP-9, WP-13; ansp WP-9; authority WP-14; L-M4 |
| WP-L3 | `basemap-bundle` | `basemap/`, the `release.yml` basemap job | — | — | ui WP-3, cisp WP-10, every console (M38) |
| WP-L4 | `spec-errata` | `docs/spec/` errata of decision record §3 L4 not yet applied; `knowledge/vectors/alert_lifecycle.json` owners | this plan | — | documentation; nothing blocks on it |
| WP-L5 | `sitl-and-simulators` | `sim/`, `cmd/sim-*`, `cmd/scenario`, `internal/`, `scenarios/` (format and the first scenarios), `make sim` | WP-L2 (issuer, DSS for `sim-ussp`) | KT-4 | INV-02 of ussp WP-10..12, authority WP-12, ansp WP-5/6; must merge before wave 4 of the decision record |
| WP-L6 | `scenario-suite` | `scenarios/` (the full suite), `make demo`, `cmd/results` | WP-L5; the four systems' images | L-M1 | the demo |
| WP-L7 | `conformance-suite` | `conformance/`, `make conformance`, `conformance.yml`, the onboarding procedure | WP-L1, WP-L2, WP-L5 (`sim-ussp` as candidate) | L-M4 | GCAA (Q7); every release of every system |
| WP-L8 | `load-test` | `cmd/loadgen`, `make load`, the `05 §7` report format | WP-L5, WP-L6 | L-M2 | the Protobuf decision of `04 §1` |
| WP-L9 | `chaos` | `scripts/chaos/`, `make chaos`, `docs/RUNBOOKS/chaos.md` | WP-L6 | L-M3 | `05 §6` evidence |

Waves, aligned with §4 of the decision record:

```
wave 0 (now, 4 agents):      WP-L1 (common schemas first)   WP-L2   WP-L3   WP-L5 (sim/ and the bridges)
wave 1 (day 2-3):            WP-L4
after the system WP-0s:      WP-L1 lands each mirror as its skeleton publishes
before system wave 4:        WP-L5 merged (INV-02 gate)
after system wave 4:         WP-L6 -> L-M1,  then WP-L8 -> L-M2,  WP-L9 -> L-M3,  WP-L7 -> L-M4
```

WP-L1 is on every repo's path and starts with `schemas/common/` on day
one; the mirrors cannot land before the skeletons exist, so its brief
splits the work into "day one" and "as each skeleton publishes". WP-L5
is long-running and starts early; its runner is what every
safety-relevant WP elsewhere is gated on.

---

## 6. Engineering standards and CI

- Go: `gofmt`, `go vet`, `staticcheck` and `golangci-lint` at the same
  pinned versions as `uspace-core` (`make tools`, `make lint`); `go test
  -race -shuffle=on`; no cgo; dependencies justified in the commit body
  (expected: `uspace-core`, `nhooyr.io/websocket` or `gorilla/websocket`,
  `goccy/go-yaml`, the JSON Schema validator, `oapi-codegen` as a tool).
- Python (`sim/`, `knowledge/tools/`): `ruff` lint and format, `mypy
  --strict`, `pytest`. pymavlink pinned; every wire offset derived from
  its definitions and pinned by a test (E-03).
- The four engineering rules of `knowledge/LESSONS.md` E-01 to E-04 are
  this repo's rules: test presence, run the branch that says nothing is
  wrong, never write a wire offset from memory, never report an inference
  as an observation.
- Commits: Conventional Commits, `type(scope): subject   [WP-Lk]`,
  scopes `contracts`, `deploy`, `sim`, `scenarios`, `basemap`,
  `conformance`, `load`, `chaos`, `spec`, `knowledge`, `docs`. No AI
  attribution of any kind. Never force-push.
- CI (`ci.yml`): lint, test, `validate-examples`, `check-mirrors`
  (online, required on `main`, best-effort on PRs as core's vector check
  is), `check-layout`; gitleaks. `scenarios.yml`: SITL in a container
  against the systems' images, on `workflow_dispatch`, nightly and on
  tags (it pulls images). `release.yml`: basemap bundle and results on
  tags. CI stays lean: nothing here runs the 1000-drone load in CI.
- Line endings LF everywhere (`.gitattributes`); the vectors keep their
  SHA-256 pins.

---

## 7. Open questions

### 7.1 Owner-only (open; the default is what the lab builds until answered)

These come from §2.1 of the decision record and from the lab's own
needs. None is decided here. Each row says what the lab does meanwhile
so that no work waits on an answer, and who answers.

| # | Question | Demo default the lab uses | Who answers |
|---|---|---|---|
| L-Q1 | Droplet sizing: the demo adds the DSS (plus its datastore) and the lab stack to five systems on 2 vCPU / 3.8 GB; the plans' memory budgets already exceed it (decision record §2.1, last-but-one row). | `make demo` is written for one host but `deploy/compose.yaml` is split so the DSS and the lab stack can run on a second host; L-M1 is attempted on the resized droplet first. The lab measures and reports the footprint in WP-L6 rather than assuming it. | owner (money) |
| L-Q2 | Who hosts the DSS and what the issuer URL is in production (authority Q-A16; spec Q7). | The lab DSS beside the CISP compose on the droplet; issuer URL = the authority's host once A-M4 lands, the lab issuer before. | GCAA (spec Q7) |
| L-Q3 | Which `uss_qualifier` configurations certification requires (ussp Q13; spec Q7). | F3411 v22a SP + F3548 strategic coordination and constraint processing are the gate; DP tests run and are reported as informative. | GCAA |
| L-Q4 | The Art. 3(4) demo figures: deviation thresholds, `lost_link_s`, `telemetry_lost_s`, nearby radius, CPA minima (ussp Q6; spec Q17). | `scenarios/policy/demo.yaml`: `h_m 50`, `v_m 15`, `t_s 60`, `telemetry_lost_s 5`, `lost_link_s 15`, `nonconformance_nearby_radius_m 2000`, CPA `t_cpa_max_s 60`, `d_horizontal_min_m 60`, `d_vertical_min_m 20`, neighbour radius 800 m, height limit 120 m AGL; printed with `policy_version` in every result. | GCAA |
| L-Q5 | The e-conspicuity receiver hardware and feed format (ussp Q11; spec Q14, Q18). | A readsb `aircraft.json` or SBS file replayed by `sim-adsb`; ADS-L deferred. | GCAA |
| L-Q6 | Which surveillance formats the ANSP can hand over (ansp 12; spec Q3, Q14). | The lab replays a recorded ADS-B file; no ASTERIX fixture until the agreement names it. | Sakaeronavigatsia |
| L-Q7 | Terrain and geoid sources and their licence (authority Q-A13; spec Q10). | Copernicus GLO-30 tiles and EGM2008 2.5′ fetched by the scenario that needs them (SC-04, SC-13), attribution in the result; nothing committed. | GCAA (licence) |
| L-Q8 | Basemap: is a Georgia-wide extract under about 1 GB acceptable for the demo, and does the droplet have the disk (ui Q4). | City-level zooms for Tbilisi, Kutaisi, Batumi and Poti, z ≤ 12 elsewhere, budget 1 GB enforced by the build; Storybook extract Tbilisi-only. | owner |
| L-Q9 | Visual regression without a hosted service (ui Q7). | None in the lab; the kit's stories are the test. | owner (money) |
| L-Q10 | Accessibility obligations for public interfaces (ui Q11; spec Q15). | The conformance suite runs `axe` on the public pages it already loads and reports; WCAG 2.2 AA is the assumed bar. | ministry |
| L-Q11 | Hosting: when production domains and state hosting take over from `*.chikox.net` (spec Q16). | Staging on the droplet until A-M5; `docs/deploy/PLAN.md` WP-D2. | GCAA |
| L-Q12 | Whether GCAA adopts the conformance suite as a certification condition and the signed report format (spec Q7). | The report is the `uss_qualifier` report plus the national test report, both JSON, hashed and signed by the lab issuer's key; "proposed" wording in the onboarding procedure. | GCAA |

### 7.2 Decided here (lab-internal)

| # | Question | Decision |
|---|---|---|
| L-D1 | Go or Python for the simulators. | D1: Go for everything that speaks a contract; Python only reads MAVLink and generates vectors. |
| L-D2 | Where the ODID encoder for the receiver simulator comes from. | `uspace-core/odid.Encode` (pinned by `odid_decode.json`); the predecessor's Python encoder is reference only. |
| L-D3 | Scenario format. | D6: YAML, one file per scenario, the `scenarios.md` steps rewritten as expected events with timing. |
| L-D4 | Do the lab's results count as evidence for a system's done-when. | Yes, when the result file names the image digest, the scenario, the commit of this repo and of core, and the observed numbers; a system WP pastes that file's path and summary into its PR. |
| L-D5 | Vectors proposed by systems (`conformance.json`, `deconfliction.json` from the USSP). | Accepted into `knowledge/vectors/` by a lab PR after the S-M2/S-M4 scenarios have run them; the file shape of `knowledge/README.md`; `utm_commit` set to the generator's rule for non-utm files. |

---

## 8. What this plan applied from the decision record

Applied on this branch: the decision record itself
(`docs/decisions/2026-10-02-cross-plan.md`) and the three spec-only
errata (the ANSP's three processes in `00 §6.1`; `track/manned/v1` and
`alt_pressure_m` in `02 F4` and `03`; the accepted subject deviations of
M30 in `05 §3`), each with an errata table in the touched file.

Left to WP-L4 (decision record §3 L4): `03` migration tool (goose, the
version tables of M36) and the `DAR-` prefix (M10); `02 F1`/`04 §3.4`
ED-318 metadata names (M15); `00 §6.1` the CISP `ed318` package row
(cisp Q19); `05 §3` the grid instead of H3 (M35); `00 §6.2`/`03`
projection tables (Q-A4); `06 §3` scope catalogue additions (M23); `02
§1` error body, audience rule and session claims (M18, M20, M28); `04
§1` the schema-ownership rule (M14); `09` rows that cite the old names;
`knowledge/vectors/alert_lifecycle.json` owners without `cisp` (cisp
Q18; a header change, no expected value moves, so no major).

Applied by WP-L4 (branch `docs/WP-L4-spec-errata`), together with the
contract errata the merged system PRs found; each change has a dated
row in the touched file's errata table. The vector change turned out to
be two cases' `owner` lists: the file header already omitted `cisp`.

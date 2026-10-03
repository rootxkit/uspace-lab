# sim

SITL launch, the lab's only MAVLink boundary, and the harness
(docs/PLAN.md D2, INV-01; docs/WORKPACKAGES/WP-L5.md).

| File | What it does |
|---|---|
| `run_sitl.sh -n N` (`make sim N=N`) | N ArduCopter SITL vehicles, each its own process group, SYSID `SITL_SYSID_BASE + i`, home `SITL_SPACING_M * i` east of `SITL_HOME`, two local outputs: the reader's (`SITL_OUT_PORT_BASE + i`) and the harness's (`SITL_FLY_PORT_BASE + i`). Writes `out/instances.tsv`. Refuses a TCP port another fleet holds (`SITL_INSTANCE_BASE` moves this one). |
| `stop_sitl.sh` (`make sim-down`) | Stops this fleet's groups, says which, and checks that nothing of this fleet (its instance directories) is left: exit 0 when the teardown worked, 1 when a simulator survived. |
| `mav_reader.py --sysid K --out udp:127.0.0.1:PORT` | Receive only. One `sim/vehicle/v1` line (`schema/vehicle-v1.json`) per `GLOBAL_POSITION_INT` on stdout (and `--emit udp:...`). Field table and units checked against pymavlink at import. |
| `fly.py --link udpin:127.0.0.1:PORT --plan FILE\|-` | **The only send path in the repository, towards SITL only.** Arms, takes off, flies to points, holds, lands; every step confirmed by the vehicle's own telemetry (E-08). Nothing imports it; the scenario runner starts it in SITL mode only. |
| `sitl.env.example` | Copy to `sitl.env` (gitignored). No script defaults a coordinate (INV-03). |

```sh
make sim-venv                 # pymavlink, ruff, mypy, pytest pinned
cp sim/sitl.env.example sim/sitl.env   # set SITL_HOME, SIM_VEHICLE, MAVPROXY
make sim N=3                  # Linux or WSL with ArduPilot built
python sim/mav_reader.py --sysid 1 --out udp:127.0.0.1:14560
make sim-down
make sim-lint sim-test
```

## Measured on 2026-10-03 (ArduCopter 4.5.7, WSL Ubuntu 24.04)

- Three vehicles started from one working directory overwrote each
  other's `eeprom.bin` and only one came up; each instance now starts in
  its own directory.
- A connect to a closed local TCP port under WSL waited for a timeout
  instead of being refused; the port checks use `ss`.
- The reader's first lines carry `ts: null`: the vehicle had not yet sent
  `SYSTEM_TIME` (R-16 in practice).
- SITL simulates the ground. Around the example home (605 m) the terrain
  is 578 m to the north and 624 m 100 m to the east; a vehicle sent east
  at 20 m above home flew into the rise and held there at full lean,
  its goto never confirmed. Scenario heights clear it, and the runner
  fails a run in which any vehicle did not confirm every step.

// Package vehicle is the lab's vehicle stream (docs/PLAN.md D2): the
// sim/vehicle/v1 line that sim/mav_reader.py writes for every SITL sample,
// its parser, the bus that fans it out to the simulators, the plan
// sim/fly.py flies, and Synth, the kinematic stand-in for SITL that lets
// the whole chain run in CI without ArduPilot.
//
// Nothing here speaks MAVLink or can reach a vehicle: the stream is read,
// never written back (INV-01).
package vehicle

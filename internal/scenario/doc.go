// Package scenario is the lab's scenario format (scenarios/README.md,
// docs/PLAN.md D6): the YAML model and its checks, the demo policy file,
// sim/sitl.env (the origin every offset is measured from: no coordinate
// is written in a scenario, INV-03), and the compiler that turns a
// scenario into one flight plan per vehicle, a timeline of knobs and
// requests, and the zones as uspace-core judges them.
package scenario

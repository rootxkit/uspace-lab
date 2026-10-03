// Package simop is the operator simulator (cmd/sim-operator, 02 F5): per
// aircraft, a client of the USSP's operator telemetry with a machine
// token from the USSP's issuer, telemetry/v1 at 1 Hz over WS
// /v1/telemetry (or POST /v1/telemetry/batch), ts the vehicle's own time,
// a queue of up to ten minutes while disconnected replayed as backlog,
// frames unacknowledged at a break resent (B-05), intents filed and
// activated through POST and PATCH /v1/intents, and drop and latency
// knobs. Its ledger compares what it sent with what the USSP's status
// frames say happened to it.
package simop

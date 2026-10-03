// Package simrx is the receiver simulator (cmd/sim-receiver, 02 F9): it
// plays each SITL vehicle's Remote ID module and a ground receiver. Basic
// ID, Location, System and Operator ID are encoded by uspace-core's odid
// (HAE = AMSL + N from the configured geoid, R-16, or the vehicle's own
// alt_hae_m when the scenario says gps), as message packs for Bluetooth 5
// and Wi-Fi or separate messages for Bluetooth 4 legacy, one MAC per
// transmitter, and reported in batches of at most one second to POST
// /v1/rid/observations: the bearer key names the receiver, the body's
// HMAC-SHA256 under its secret is X-Report-Signature (auth.SignReport).
// Knobs: drop rate, latency, outage with backlog replay, address change,
// serial change (SC-10, SC-11), and the unknown timestamp and no System
// message until the vehicle has sent UTC (R-16).
package simrx

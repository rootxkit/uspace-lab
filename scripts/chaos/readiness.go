package main

import (
	"context"
	"net/http"
	"time"

	"github.com/rootxkit/uspace-lab/internal/readiness"
)

// The readiness probe and its parser are shared with the load harness's
// outage faults (internal/readiness).

// Readiness is one probe of one system's readiness endpoint.
type Readiness = readiness.Readiness

const (
	maxProbeBody = readiness.MaxBody
	stateOK      = readiness.StateOK
)

func probeReadiness(ctx context.Context, hc *http.Client, url string, timeout time.Duration) Readiness {
	return readiness.Probe(ctx, hc, url, timeout)
}

func parseReadiness(r *Readiness, b []byte) { readiness.Parse(r, b) }

func bounded(s string) string { return readiness.Bounded(s) }

func shortErr(err error) string { return readiness.ShortErr(err) }

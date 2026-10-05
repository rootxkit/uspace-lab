package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The bodies are what the images under test answered on 2026-10-05
// (docs/RUNBOOKS/chaos.md), trimmed.
var readinessBodies = map[string]struct {
	body   string
	status string
	checks map[string]string
}{
	"authority": {`{"checks":{"relational":{"ok":true},"telemetry":{"ok":false,"error":"connection refused"}},"status":"not_ready"}`,
		"not_ready", map[string]string{"relational": "ok", "telemetry": "down"}},
	"cisp": {`{"checks":{"database":"ok","migrations":"ok","nats":"down"},"status":"not_ready"}`,
		"not_ready", map[string]string{"database": "ok", "migrations": "ok", "nats": "down"}},
	"ussp": {`{"checked_at":"2026-10-05T02:58:33Z","degraded":["dss"],"dependencies":{"dss":{"detail":"down since 02:57:17Z","required":false,"state":"down"},"nats":{"required":true,"state":"up"},"cis":{"age_s":3,"state":"up"}}}`,
		"answered", map[string]string{"dss": "down", "nats": "ok", "cis": "ok"}},
	"ansp": {`{"process":"api","status":"ready","checks":[{"name":"nats","state":"ok","required":true},{"name":"cisp","state":"degraded","required":false,"reason":"no answer for 40 s"}]}`,
		"ready", map[string]string{"nats": "ok", "cisp": "degraded"}},
	"issuer": {`{"clients":6,"issuer":"https://lab.example","status":"ok"}`, "ok", nil},
	"dss":    {`ok`, "ok", nil},
}

func TestParseReadinessOfEverySystem(t *testing.T) {
	for name, c := range readinessBodies {
		t.Run(name, func(t *testing.T) {
			var r Readiness
			parseReadiness(&r, []byte(c.body))
			if r.Status != c.status {
				t.Fatalf("status %q, want %q", r.Status, c.status)
			}
			if len(r.Checks) != len(c.checks) {
				t.Fatalf("checks %v, want %v", r.Checks, c.checks)
			}
			for k, v := range c.checks {
				if r.Checks[k] != v {
					t.Fatalf("check %s %q, want %q (all: %v)", k, r.Checks[k], v, r.Checks)
				}
			}
		})
	}
}

func TestParseReadinessKeepsDetailsAndRefusesToGuess(t *testing.T) {
	var r Readiness
	parseReadiness(&r, []byte(readinessBodies["ansp"].body))
	if r.Details["cisp"] != "no answer for 40 s" {
		t.Fatalf("details %v", r.Details)
	}
	var u Readiness
	parseReadiness(&u, []byte(`{"checks":42}`))
	if u.Note == "" || len(u.Checks) != 0 {
		t.Fatalf("an unknown shape must be noted, not read: %+v", u)
	}
}

func TestProbeReadinessBothWays(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			_, _ = w.Write([]byte(readinessBodies["cisp"].body))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", maxProbeBody+10)))
		default:
			http.Error(w, `{"status":"not_ready"}`, http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ok := probeReadiness(context.Background(), srv.Client(), srv.URL+"/ready", time.Second)
	if !ok.Ready() || ok.Checks["nats"] != "down" {
		t.Fatalf("%+v", ok)
	}
	bad := probeReadiness(context.Background(), srv.Client(), srv.URL+"/other", time.Second)
	if bad.Ready() || !bad.Answered() || bad.Code != http.StatusServiceUnavailable {
		t.Fatalf("%+v", bad)
	}
	big := probeReadiness(context.Background(), srv.Client(), srv.URL+"/big", time.Second)
	if big.Note == "" {
		t.Fatalf("an oversized body must be noted: %+v", big)
	}
	srv.Close()
	gone := probeReadiness(context.Background(), srv.Client(), srv.URL+"/ready", time.Second)
	if gone.Answered() || gone.Err == "" {
		t.Fatalf("a closed server answered: %+v", gone)
	}
}

func TestRecoveredNeedsEveryBaselineCheckBack(t *testing.T) {
	base := Readiness{Code: 200, Checks: map[string]string{"nats": "ok", "client_address": "degraded"}}
	if !recovered(base, Readiness{Code: 200, Checks: map[string]string{"nats": "ok", "client_address": "degraded"}}) {
		t.Fatal("the baseline itself is recovered")
	}
	if recovered(base, Readiness{Code: 200, Checks: map[string]string{"nats": "down"}}) {
		t.Fatal("a check ok before and down now is not recovered")
	}
	if recovered(base, Readiness{Code: 503, Checks: map[string]string{"nats": "ok"}}) {
		t.Fatal("not 200 is not recovered")
	}
	// A check degraded before the fault does not have to heal.
	if !recovered(base, Readiness{Code: 200, Checks: map[string]string{"nats": "ok"}}) {
		t.Fatal("a check that was not ok before is not required")
	}
}

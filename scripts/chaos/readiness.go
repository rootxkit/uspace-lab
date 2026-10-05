package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxProbeBody bounds what a readiness probe reads (bounded everything).
const maxProbeBody = 64 << 10

// Readiness is one probe of one system's readiness endpoint, normalised:
// the HTTP status (0 when nothing answered), the overall status word and
// each named check's state as the system wrote it, with "ok" for the
// systems' several spellings of a healthy check ("ok", "up", true).
// Nothing is inferred: a body that is not one of the shapes below keeps
// only its HTTP status and a note (E-04).
type Readiness struct {
	At     time.Time         `json:"at"`
	Code   int               `json:"code"`
	Err    string            `json:"error,omitempty"`
	Status string            `json:"status,omitempty"`
	Checks map[string]string `json:"checks,omitempty"`
	// Details holds a check's own words when it gave any (an age, a
	// reason), bounded.
	Details map[string]string `json:"details,omitempty"`
	Note    string            `json:"note,omitempty"`
	TookMS  int64             `json:"took_ms"`
}

// Answered is true when the endpoint answered with an HTTP status.
func (r Readiness) Answered() bool { return r.Code != 0 }

// Ready is the endpoint answering 200.
func (r Readiness) Ready() bool { return r.Code == http.StatusOK }

// NotOK lists the checks whose state is not ok, sorted.
func (r Readiness) NotOK() []string {
	var out []string
	for k, v := range r.Checks {
		if v != stateOK {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

const stateOK = "ok"

// probeReadiness GETs url with its own timeout.
func probeReadiness(ctx context.Context, hc *http.Client, url string, timeout time.Duration) Readiness {
	start := time.Now()
	r := Readiness{At: start.UTC()}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	r.TookMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Err = shortErr(err)
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	r.Code = resp.StatusCode
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody+1))
	if err != nil {
		r.Note = "body not read: " + shortErr(err)
		return r
	}
	if len(b) > maxProbeBody {
		r.Note = fmt.Sprintf("body larger than %d bytes, not parsed", maxProbeBody)
		return r
	}
	parseReadiness(&r, b)
	return r
}

// parseReadiness reads the four systems' readiness bodies (as served by
// the images under test; each system's api/openapi.yaml declares its
// own) and the lab issuer's and the DSS's health answers:
//
//	authority  {"status":"ready","checks":{"relational":{"ok":true},...}}
//	cisp       {"status":"ready","checks":{"database":"ok",...}}
//	ussp       {"degraded":[...],"dependencies":{"nats":{"state":"up","detail":...},...}}
//	ansp       {"status":"ready","checks":[{"name":"nats","state":"ok","reason":...},...]}
//	issuer     {"status":"ok",...}
//	dss        ok (text)
func parseReadiness(r *Readiness, b []byte) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		t := strings.TrimSpace(string(b))
		if len(t) > 80 {
			t = t[:80]
		}
		r.Status = t
		return
	}
	if s, ok := raw["status"]; ok {
		_ = json.Unmarshal(s, &r.Status)
	}
	checks := map[string]string{}
	details := map[string]string{}
	if c, ok := raw["checks"]; ok {
		var asMap map[string]json.RawMessage
		var asList []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		switch {
		case json.Unmarshal(c, &asList) == nil:
			for _, e := range asList {
				checks[e.Name] = normState(e.State)
				if e.Reason != "" && e.Reason != "ok" {
					details[e.Name] = bounded(e.Reason)
				}
			}
		case json.Unmarshal(c, &asMap) == nil:
			for name, v := range asMap {
				var s string
				var o struct {
					OK     *bool  `json:"ok"`
					Error  string `json:"error"`
					Detail string `json:"detail"`
				}
				switch {
				case json.Unmarshal(v, &s) == nil:
					checks[name] = normState(s)
				case json.Unmarshal(v, &o) == nil && o.OK != nil:
					if *o.OK {
						checks[name] = stateOK
					} else {
						checks[name] = "down"
					}
					if d := firstNonEmpty(o.Error, o.Detail); d != "" {
						details[name] = bounded(d)
					}
				default:
					checks[name] = "unreadable"
				}
			}
		default:
			r.Note = "checks in an unknown shape"
		}
	}
	if d, ok := raw["dependencies"]; ok {
		var deps map[string]struct {
			State  string   `json:"state"`
			Detail string   `json:"detail"`
			AgeS   *float64 `json:"age_s"`
		}
		if json.Unmarshal(d, &deps) == nil {
			for name, dep := range deps {
				checks[name] = normState(dep.State)
				switch {
				case dep.Detail != "":
					details[name] = bounded(dep.Detail)
				case dep.AgeS != nil:
					details[name] = fmt.Sprintf("age_s=%g", *dep.AgeS)
				}
			}
		} else {
			r.Note = "dependencies in an unknown shape"
		}
		if r.Status == "" {
			r.Status = "answered"
		}
	}
	if len(checks) > 0 {
		r.Checks = checks
	}
	if len(details) > 0 {
		r.Details = details
	}
}

func normState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok", "up", "ready", "healthy", "pass":
		return stateOK
	case "":
		return "unknown"
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func bounded(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// shortErr keeps a transport error readable: the last two clauses.
func shortErr(err error) string {
	s := err.Error()
	parts := strings.Split(s, ": ")
	if len(parts) > 2 {
		s = strings.Join(parts[len(parts)-2:], ": ")
	}
	return bounded(s)
}

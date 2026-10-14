package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuthority is the authority's sources and audit API as the disable
// row uses it: a switch per type, an audit event per changed switch
// (actor as given), the requests' credentials recorded.
type fakeAuthority struct {
	mu      sync.Mutex
	enabled map[string]bool
	actor   string // "" audits nothing
	events  []map[string]any
	auths   []string
	refuse  bool
}

func (f *fakeAuthority) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auths = append(f.auths, r.Header.Get("Authorization")+"|"+r.Header.Get("X-CSRF-Token"))
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/sources/"):
		if f.refuse {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"source_control_unavailable"}`))
			return
		}
		typ := strings.TrimPrefix(r.URL.Path, "/v1/sources/")
		var in struct {
			Enabled bool   `json:"enabled"`
			Reason  string `json:"reason"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &in)
		changed := f.enabled[typ] != in.Enabled
		f.enabled[typ] = in.Enabled
		if changed && f.actor != "" {
			f.events = append(f.events, map[string]any{"actor_type": "user", "actor_id": f.actor, "entity_id": typ, "event_type": "source_disabled",
				"payload": map[string]any{"enabled": in.Enabled, "reason": in.Reason}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"changed": changed, "control": map[string]any{"enabled": in.Enabled, "actor": f.actor}})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sources":
		var cs []map[string]any
		for typ, on := range f.enabled {
			cs = append(cs, map[string]any{"source_type": typ, "enabled": on})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"default_deny": false, "controls": cs})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/audit/events":
		if r.URL.Query().Get("event_type") != "source_disabled" {
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": f.events})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func adminOf(srv *httptest.Server) *adminAPI {
	return &adminAPI{base: srv.URL, session: "admin-session", csrf: "csrf-1", hc: srv.Client(), run: "t1"}
}

// A switch off and on is seen from the authority's side and audited as
// a person's act: the row's fault held, then restored, and the audit
// passes. Without audit events, or with them by no person, the audit
// fails; a refused switch exits 1 and the fault is never held.
func TestDisableRowSwitchesAndAudits(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, actor string
		pass        bool
	}{{"audited", "lab-admin", true}, {"not audited", "", false}} {
		fa := &fakeAuthority{enabled: map[string]bool{"direct_rid": true}, actor: c.actor}
		srv := httptest.NewServer(fa)
		a := adminOf(srv)
		spec := FaultSpec{Kind: kindDisabled, Source: "direct_rid", enabled: func(ctx context.Context) (bool, error) { return a.enabled(ctx, "direct_rid") }}
		if held, _ := spec.heldNow(ctx, nil, nil); held {
			t.Fatalf("%s: held before the switch", c.name)
		}
		t0 := time.Now()
		if sr := a.switchRun(ctx, "inject", []string{"direct_rid"}); sr.ExitCode != 0 || !strings.Contains(sr.Output[0], "changed true, enabled false") {
			t.Fatalf("%s: inject %+v", c.name, sr)
		} else if sr.End.Before(sr.Start) || sr.Start.Before(t0.Add(-time.Second)) {
			// The hold runs from End: a zero End ended it at once (the
			// first matrix run, "no sample was taken during the hold").
			t.Fatalf("%s: the switch's times %v..%v", c.name, sr.Start, sr.End)
		}
		if held, note := spec.heldNow(ctx, nil, nil); !held {
			t.Fatalf("%s: not held after the switch: %s", c.name, note)
		}
		if ok, _ := spec.restoredNow(ctx, nil, nil, nil); ok {
			t.Fatalf("%s: restored while off", c.name)
		}
		if sr := a.switchRun(ctx, "restore", []string{"direct_rid"}); sr.ExitCode != 0 {
			t.Fatalf("%s: restore %+v", c.name, sr)
		}
		if ok, note := spec.restoredNow(ctx, nil, nil, nil); !ok {
			t.Fatalf("%s: not restored: %s", c.name, note)
		}
		res := a.audited(ctx, "direct_rid", t0, time.Now())
		if res.Pass != c.pass || (c.pass && res.Events != 2) {
			t.Errorf("%s: audit %+v", c.name, res)
		}
		if fa.auths[0] != "Bearer admin-session|csrf-1" {
			t.Errorf("%s: credentials %q", c.name, fa.auths[0])
		}
		srv.Close()
	}
	fa := &fakeAuthority{enabled: map[string]bool{"direct_rid": true}, refuse: true}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	a := adminOf(srv)
	if sr := a.switchRun(ctx, "inject", []string{"direct_rid"}); sr.ExitCode != 1 || !strings.Contains(strings.Join(sr.Output, " "), "answered 503") || sr.End.IsZero() {
		t.Fatalf("a refused switch: %+v", sr)
	}
	spec := FaultSpec{Kind: kindDisabled, Source: "direct_rid", enabled: func(ctx context.Context) (bool, error) { return a.enabled(ctx, "direct_rid") }}
	if held, _ := spec.heldNow(ctx, nil, nil); held {
		t.Fatal("a refused switch was held")
	}
	none := FaultSpec{Kind: kindDisabled, Source: "direct_rid"}
	if held, note := none.heldNow(ctx, nil, nil); held || !strings.Contains(note, "no admin session") {
		t.Fatalf("no admin session: %v %s", held, note)
	}
}

// The harness runs the disable domain itself, through the admin API,
// and every other domain's script; with no admin session the act fails
// rather than passing.
func TestDispatch(t *testing.T) {
	h := &harness{}
	if sr := h.dispatch(context.Background(), domainDisable, "inject", []string{"direct_rid"}); sr.ExitCode == 0 {
		t.Fatalf("no admin session: %+v", sr)
	}
	fa := &fakeAuthority{enabled: map[string]bool{"direct_rid": true}, actor: "lab-admin"}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	h.admin = adminOf(srv)
	if sr := h.dispatch(context.Background(), domainDisable, "inject", []string{"direct_rid"}); sr.ExitCode != 0 {
		t.Fatalf("through the admin API: %+v", sr)
	}
	if !scripted("adapter") || scripted(domainDisable) || scripted("clock") {
		t.Fatal("scripted")
	}

}

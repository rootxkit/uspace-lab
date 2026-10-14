package load

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeUSSP answers the token endpoint, files every intent with the
// state decide gives it, activates on request, ends on request, and
// counts what else it is asked (a WebSocket upgrade, telemetry).
type fakeUSSP struct {
	decide  func(n int) string
	mu      sync.Mutex
	filed   int
	ended   map[string]bool
	streams atomic.Int64
}

func (f *fakeUSSP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/oauth/token":
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "token_type": "Bearer", "expires_in": 3600})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/intents":
		f.mu.Lock()
		f.filed++
		n := f.filed
		f.mu.Unlock()
		st := f.decide(n)
		d := map[string]any{"intent_id": fmt.Sprintf("i%04d", n), "state": st, "decision": st}
		if st == "rejected" {
			d["conflicts"] = []map[string]string{{"kind": "intent", "reason": "intent_filed_first"}}
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(d)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/intents/"):
		var b struct{ Action string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		id := strings.TrimPrefix(r.URL.Path, "/v1/intents/")
		st := map[string]string{"activate": "activated", "end": "ended"}[b.Action]
		if b.Action == "end" {
			f.mu.Lock()
			f.ended[id] = true
			f.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"intent_id": id, "state": st})
	default:
		f.streams.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// An intent the USSP did not authorise stops the run before any stream
// or client starts, naming the aircraft, the state and the USSP's
// reason: the run used to keep the rejected intent's id, open its
// traffic stream and wait a minute for a 404 that said nothing of why.
// The presence half: every intent authorised, the run goes on to the
// streams.
func TestAnIntentNotAuthorisedStopsTheRun(t *testing.T) {
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 30
	fake := &fakeUSSP{ended: map[string]bool{}, decide: func(n int) string {
		if n == 2 {
			return "rejected"
		}
		return "accepted"
	}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	p := systemsTargets(t, f, srv.URL, nil)
	rep, err := Run(context.Background(), Options{Files: f, TargetsPath: p, Run: "x", RepoRoot: repo("."), ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(rep.Errors, "; ")
	if rep.Verdict != VerdictFail || !strings.Contains(errs, "rejected (intent_filed_first)") || !strings.Contains(errs, "not authorised") {
		t.Fatalf("verdict %s, errors %s", rep.Verdict, errs)
	}
	if n := fake.streams.Load(); n != 0 {
		t.Fatalf("%d requests after the refusal", n)
	}
	fake2 := &fakeUSSP{ended: map[string]bool{}, decide: func(int) string { return "accepted" }}
	srv2 := httptest.NewServer(fake2)
	t.Cleanup(srv2.Close)
	p2 := systemsTargets(t, f, srv2.URL, nil)
	rep2, err := Run(context.Background(), Options{Files: f, TargetsPath: p2, Run: "x", RepoRoot: repo("."), ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rep2.Errors, "; "), "not authorised") || fake2.streams.Load() == 0 {
		t.Fatalf("every intent authorised: errors %v, %d stream requests", rep2.Errors, fake2.streams.Load())
	}
}

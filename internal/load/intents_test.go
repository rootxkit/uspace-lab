package load

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
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

// Every intent a run activated is ended when the run ends, whether it
// ran to the end or stopped early: an intent left authorised for its
// window (the run plus its drain and ten minutes) held the airspace, and
// the next run's intents were all rejected, intent_filed_first. The
// report counts the intents ended; an intent the USSP would not end is
// an error of the run.
func TestARunEndsItsIntents(t *testing.T) {
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 30
	fake := &fakeUSSP{ended: map[string]bool{}, decide: func(int) string { return "accepted" }}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	rep, err := Run(context.Background(), Options{Files: f, TargetsPath: systemsTargets(t, f, srv.URL, nil), Run: "x", RepoRoot: repo("."), ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	filed, ended := fake.filed, len(fake.ended)
	fake.mu.Unlock()
	if filed != f.Tier.Operators.Aircraft || ended != filed || rep.Volumes.IntentsEnded != filed {
		t.Fatalf("filed %d, ended %d, report says %d", filed, ended, rep.Volumes.IntentsEnded)
	}
	// Against the reference target, after a whole run.
	f2 := loadSmall(t)
	f2.Tier.DurationS = 1
	rep2, err := Run(context.Background(), Options{Files: f2, TargetsPath: repo("targets/reference.yaml"), Run: "e", RepoRoot: repo(".")})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Volumes.IntentsFiled == 0 || rep2.Volumes.IntentsEnded != rep2.Volumes.IntentsFiled {
		t.Fatalf("reference: filed %d, ended %d (%v)", rep2.Volumes.IntentsFiled, rep2.Volumes.IntentsEnded, rep2.Errors)
	}
	// A USSP that will not end them: the run says so.
	stuck := &fakeUSSP{ended: map[string]bool{}, decide: func(int) string { return "accepted" }}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if strings.Contains(string(body), `"end"`) {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		stuck.ServeHTTP(w, r)
	})
	srv3 := httptest.NewServer(h)
	t.Cleanup(srv3.Close)
	rep3, err := Run(context.Background(), Options{Files: f, TargetsPath: systemsTargets(t, f, srv3.URL, nil), Run: "x", RepoRoot: repo("."), ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Volumes.IntentsEnded != 0 || !strings.Contains(strings.Join(rep3.Errors, "; "), "not ended") {
		t.Fatalf("ended %d, errors %v", rep3.Volumes.IntentsEnded, rep3.Errors)
	}
}

// A run with a peer ends its intents after the drain and before the
// report is collected (the deletions owed to the peer are timed): the
// count it set is the report's, where collecting used to overwrite it
// with 0 (the first recorded 100 run said "Intents ended after the run
// 0" with all 100 ended at the USSP). Ended after collecting, as
// without a peer, the count set then stands too.
func TestIntentsEndedBeforeTheReportAreCounted(t *testing.T) {
	f := loadSmall(t)
	r := &run{tier: f.Tier, files: f, fleet: &Fleet{}, picture: NewLedger(), traffic: NewReceiptLedger(), readers: &readers{},
		rec: observe.NewRecorder(nil), memory: make([]uint64, 12)}
	rep := &Report{Volumes: Volumes{IntentsEnded: 7}}
	r.collect(rep, time.Now(), 1)
	if rep.Volumes.IntentsEnded != 7 {
		t.Fatalf("ended before collecting: the report says %d", rep.Volumes.IntentsEnded)
	}
	rep = &Report{}
	r.collect(rep, time.Now(), 1)
	if rep.Volumes.IntentsEnded != 0 {
		t.Fatalf("none ended: the report says %d", rep.Volumes.IntentsEnded)
	}
}

// An intent the USSP ended itself (the 5000 run's flights went silent
// past the USSP's bound) is refused an end, 409 "the intent is ended":
// it is ended, read back from the USSP, and counted so, not a run error
// (the first 5000 report listed 1132 of them). An intent refused an end
// that the USSP still holds activated stays an error.
func TestAnIntentTheUSSPEndedItselfIsEnded(t *testing.T) {
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 30
	for _, c := range []struct {
		held  string
		ended bool
	}{{"ended", true}, {"activated", false}} {
		inner := &fakeUSSP{ended: map[string]bool{}, decide: func(int) string { return "accepted" }}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/intents/"):
				_ = json.NewEncoder(w).Encode(map[string]any{"intent_id": strings.TrimPrefix(r.URL.Path, "/v1/intents/"), "state": c.held})
				return
			case r.Method == http.MethodPatch:
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				if strings.Contains(string(body), `"end"`) {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"type":"https://schemas.uspace.ge/problems/end_refused","status":409,"detail":"the intent is ended and cannot be ended"}`))
					return
				}
			}
			inner.ServeHTTP(w, r)
		})
		srv := httptest.NewServer(h)
		rep, err := Run(context.Background(), Options{Files: f, TargetsPath: systemsTargets(t, f, srv.URL, nil), Run: "x", RepoRoot: repo("."), ReadyTimeout: time.Second})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		notEnded := strings.Contains(strings.Join(rep.Errors, "; "), "not ended")
		inner.mu.Lock()
		filed := inner.filed
		inner.mu.Unlock()
		switch {
		case c.ended && (rep.Volumes.IntentsEnded != filed || filed == 0 || notEnded):
			t.Errorf("held ended: filed %d, ended %d, errors %v", filed, rep.Volumes.IntentsEnded, rep.Errors)
		case !c.ended && (rep.Volumes.IntentsEnded != 0 || !notEnded):
			t.Errorf("held activated: ended %d, errors %v", rep.Volumes.IntentsEnded, rep.Errors)
		}
	}
}

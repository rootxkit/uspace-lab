package load

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// fakeSP is an F3411 SP: GET /uss/flights answers flights (or none),
// details answers 200 or status; it checks the bearer and the view.
type fakeSP struct {
	flights  []string
	details  int
	views    atomic.Int64
	badView  atomic.Int64
	requests atomic.Int64
}

func (f *fakeSP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	switch {
	case r.URL.Path == "/oauth/token":
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "dp", "token_type": "Bearer", "expires_in": 3600})
	case r.Header.Get("Authorization") != "Bearer dp":
		w.WriteHeader(http.StatusUnauthorized)
	case r.URL.Path == "/uss/flights":
		f.views.Add(1)
		if len(strings.Split(r.URL.Query().Get("view"), ",")) != 4 {
			f.badView.Add(1)
		}
		var fl []map[string]string
		for _, id := range f.flights {
			fl = append(fl, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"flights": fl})
	case strings.HasSuffix(r.URL.Path, "/details"):
		if f.details != 0 {
			w.WriteHeader(f.details)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"id": "x"}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func pollFor(t *testing.T, f *fakeSP, n int64) *spPoller {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	tok := &oauth.ClientCredentials{TokenURL: srv.URL + "/oauth/token", ClientID: "lab-01", ClientSecret: "s", Audience: "ussp", Scopes: []string{"rid.display_provider"}}
	p := newSPPoller(srv.URL, tok, []view{{41.70, 44.80, 41.73, 44.84}, {41.73, 44.80, 41.76, 44.84}}, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.run(ctx) }()
	tk := time.NewTicker(5 * time.Millisecond)
	defer tk.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for f.views.Load() < n {
		select {
		case <-tk.C:
		case <-deadline.C:
			t.Fatalf("only %d polls", f.views.Load())
		}
	}
	cancel()
	<-done
	return p
}

// The lab polls every view as a display provider and times the SP's
// answers and the details of a flight it returned (presence); answers
// with no flight time nothing the load is about, so the row is not
// measured (absence); a details request the SP refuses is a failure of
// the run, said with its status.
func TestTheLabPollsTheSPAsADisplayProvider(t *testing.T) {
	f := &fakeSP{flights: []string{"f1", "f2"}}
	p := pollFor(t, f, 8)
	obs := map[string]Observation{}
	st, fail := p.observe(obs, 1, nil)
	if fail != "" || f.badView.Load() != 0 || st.Flights == 0 || st.DetailsOK == 0 {
		t.Fatalf("fail %q, bad views %d, %+v", fail, f.badView.Load(), st)
	}
	if !obs["f3411_sp_flights_s"].Measured || !obs["f3411_details_s"].Measured || obs["f3411_sp_flights_s"].N < 8 {
		t.Fatalf("observations %+v %+v", obs["f3411_sp_flights_s"], obs["f3411_details_s"])
	}
	empty := pollFor(t, &fakeSP{}, 4)
	obs = map[string]Observation{}
	if _, fail := empty.observe(obs, 1, nil); fail != "" || obs["f3411_sp_flights_s"].Measured || !strings.Contains(obs["f3411_sp_flights_s"].Reason, "none returned a flight") || obs["f3411_details_s"].Measured {
		t.Fatalf("empty answers: %q %+v", fail, obs)
	}
	refused := pollFor(t, &fakeSP{flights: []string{"f1"}, details: http.StatusServiceUnavailable}, 4)
	obs = map[string]Observation{}
	if _, fail := refused.observe(obs, 1, nil); !strings.Contains(fail, "503") || obs["f3411_details_s"].Measured {
		t.Fatalf("refused details: %q %+v", fail, obs["f3411_details_s"])
	}
}

// The views cover the fleet's most populated tiles, at most as many as
// the tier asks for, each a square whose diagonal F3411 admits (7 km).
func TestDPViewsCoverTheFleet(t *testing.T) {
	f := loadSmall(t)
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	vs := fl.dpViews(4000, 2)
	if len(vs) != 2 {
		t.Fatalf("%d views", len(vs))
	}
	for _, v := range vs {
		d, err := geodesy.DistanceM(core.LatLon{LatDeg: v[0], LonDeg: v[1]}, core.LatLon{LatDeg: v[2], LonDeg: v[3]})
		if err != nil {
			t.Fatal(err)
		}
		if !(d > 5000 && d < 7000) || v[0] >= v[2] || v[1] >= v[3] {
			t.Fatalf("view %v, diagonal %v m", v, d)
		}
	}
	if all := fl.dpViews(4000, 1000); len(all) < 2 || len(all) > len(fl.Aircraft) {
		t.Fatalf("%d views for the whole fleet", len(all))
	}
	if math.IsNaN(vs[0][0]) {
		t.Fatal("NaN view")
	}
	bad := &DPPolls{Views: 10, ViewSideM: 6000, PollMS: 1000}
	if err := bad.validate(); err == nil {
		t.Fatal("a view wider than F3411 admits was taken")
	}
}

// A failure inside a fault the run injected (a killed rid-sp answers
// 502) is counted apart and is not a run error; the same failure
// outside every fault is.
func TestSPFailuresDuringAFaultAreExcused(t *testing.T) {
	refused := pollFor(t, &fakeSP{flights: []string{"f1"}, details: http.StatusBadGateway}, 4)
	inj, res := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	fr := &faultRunner{records: []FaultRecord{{InjectedAt: &inj, RestoredAt: &res}}}
	st, fail := refused.observe(map[string]Observation{}, 1, fr.during)
	if fail != "" || st.FailuresDuringFaults == 0 || st.FailuresDuringFaults != st.Failures {
		t.Fatalf("inside the fault: %q %+v", fail, st)
	}
	past := time.Now().Add(-2 * time.Hour)
	before := &faultRunner{records: []FaultRecord{{InjectedAt: &past, RestoredAt: &past}}}
	if _, fail := refused.observe(map[string]Observation{}, 1, before.during); !strings.Contains(fail, "outside any fault") {
		t.Fatalf("outside every fault: %q", fail)
	}
}

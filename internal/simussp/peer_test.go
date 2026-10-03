package simussp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/f3411"
	"github.com/rootxkit/uspace-core/f3548"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

type staticTokens string

func (s staticTokens) Token(context.Context) (string, error) { return string(s), nil }

func verify(_ context.Context, tok string) ([]string, error) {
	switch tok {
	case "dp":
		return []string{string(f3411.ScopeDisplayProvider)}, nil
	case "sc":
		return []string{ScopeStrategicCoordination}, nil
	}
	return nil, errors.New("unknown token")
}

// dss is a DSS double for the two writes: it records each body and
// answers with the standard response shape and the status the pinned
// InterUSS DSS answers a new entity with, observed against it (make
// sim-ussp-up): 200 for an ISA, 201 for an operational intent reference
// (an E-01 pair: the refusal path is TestDSSRefusalIsReported). Like the
// DSS, it refuses with 409 a PUT to a reference id it already holds,
// since this USS never sends the OVN key to replace one, and a PUT whose
// key lacks the OVN of a reference it holds (every reference in the
// double is in the one test volume, so every one intersects); its query
// returns them with their OVNs, as the DSS does to their manager.
type dss struct {
	mu     sync.Mutex
	isa    []f3411.CreateIdentificationServiceAreaParameters
	oir    []f3548.PutOperationalIntentReferenceParameters
	oirIDs []string
	ovns   []string
	paths  []string
	status int
}

func (d *dss) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paths = append(d.paths, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer dss-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if d.status != 0 {
		w.WriteHeader(d.status)
		_, _ = w.Write([]byte(`{"message":"refused by the double"}`))
		return
	}
	b, _ := io.ReadAll(r.Body)
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/dss/v1/operational_intent_references/query":
		refs := []f3548.OperationalIntentReference{}
		for i, id := range d.oirIDs {
			ovn := d.ovns[i]
			refs = append(refs, f3548.OperationalIntentReference{Id: id, Manager: "lab-peer", Ovn: &ovn, State: f3548.Accepted,
				SubscriptionId: "00000000-0000-4000-8000-000000000001", UssAvailability: "Normal", UssBaseUrl: "https://peer.lab.test", Version: 1})
		}
		_ = json.NewEncoder(w).Encode(f3548.QueryOperationalIntentReferenceResponse{OperationalIntentReferences: refs})
	case strings.HasPrefix(r.URL.Path, "/rid/v2/dss/identification_service_areas/"):
		var p f3411.CreateIdentificationServiceAreaParameters
		if json.Unmarshal(b, &p) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		d.isa = append(d.isa, p)
		_ = json.NewEncoder(w).Encode(f3411.PutIdentificationServiceAreaResponse{ServiceArea: f3411.IdentificationServiceArea{
			Id: id, Owner: "lab-peer", UssBaseUrl: p.UssBaseUrl, Version: "v1", TimeStart: *p.Extents.TimeStart, TimeEnd: *p.Extents.TimeEnd}})
	case strings.HasPrefix(r.URL.Path, "/dss/v1/operational_intent_references/"):
		var p f3548.PutOperationalIntentReferenceParameters
		if json.Unmarshal(b, &p) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, seen := range d.oirIDs {
			if seen == id {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"Current version is x but client specified version "}`))
				return
			}
		}
		for _, want := range d.ovns {
			if p.Key == nil || !slices.Contains(*p.Key, want) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"Current OVNs not provided for one or more OperationalIntents or Constraints"}`))
				return
			}
		}
		ovn := "ovn-" + id[:8]
		d.oir = append(d.oir, p)
		d.oirIDs = append(d.oirIDs, id)
		d.ovns = append(d.ovns, ovn)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(f3548.ChangeOperationalIntentReferenceResponse{
			OperationalIntentReference: f3548.OperationalIntentReference{Id: id, Manager: "lab-peer", Ovn: &ovn, State: p.State,
				SubscriptionId: "00000000-0000-4000-8000-000000000001", TimeStart: *p.Extents[0].TimeStart, TimeEnd: *p.Extents[0].TimeEnd,
				UssAvailability: "Normal", UssBaseUrl: p.UssBaseUrl, Version: 1},
			Subscribers: []f3548.SubscriberToNotify{}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newPeer(t *testing.T, d *dss) (*Peer, *httptest.Server) {
	t.Helper()
	dssSrv := httptest.NewServer(d)
	t.Cleanup(dssSrv.Close)
	now := time.Now().UTC()
	p, err := New(Config{
		BaseURL: "https://peer.lab.test", DSSRIDBase: dssSrv.URL + "/rid/v2", DSSUTMBase: dssSrv.URL,
		Tokens: staticTokens("dss-token"), Verify: verify,
		ISACenter: core.LatLon{LatDeg: 41.7151, LonDeg: 44.8271}, ISARadiusM: 2000, ISAAltLow: 500, ISAAltHigh: 900,
		Aircraft: []Aircraft{{Sysid: 3, Serial: "LABPEER0003", OperatorID: "GEOLAB000009"}},
		Intents:  []Intent{{Center: core.LatLon{LatDeg: 41.7151, LonDeg: 44.8271}, RadiusM: 500, AltLowerW84: 600, AltUpperW84: 760, Start: now, End: now.Add(time.Hour)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return p, srv
}

func get(t *testing.T, url, tok string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestWritesTheISAAndTheIntentReferenceAndServesFlights(t *testing.T) {
	d := &dss{}
	p, srv := newPeer(t, d)
	ctx := context.Background()
	if err := p.PutISA(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.PutIntents(ctx); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	if len(d.isa) != 1 || d.isa[0].UssBaseUrl != "https://peer.lab.test" || d.isa[0].Extents.Volume.OutlineCircle == nil {
		t.Fatalf("ISA %+v", d.isa)
	}
	if len(d.oir) != 1 || d.oir[0].State != f3548.Accepted || d.oir[0].NewSubscription == nil || d.oir[0].UssBaseUrl != "https://peer.lab.test" {
		t.Fatalf("intent reference %+v", d.oir)
	}
	d.mu.Unlock()
	if id, v := p.ISA(); id == "" || v != "v1" {
		t.Fatalf("ISA id %q version %q", id, v)
	}
	// A SITL aircraft's samples become a flight served at /uss/flights.
	t0 := time.Now().UTC()
	for i := range 5 {
		ts := vehicle.FormatTime(t0.Add(time.Duration(i) * time.Second))
		p.Observe(&vehicle.Sample{Schema: vehicle.Schema, Sysid: 3, TS: &ts, LatDeg: 41.7152, LonDeg: 44.8272, AltAMSLM: 635,
			AltHAEM: vehicle.F(650.9), HeightTakeoffM: 30, SpeedMS: 3, TrackDeg: vehicle.F(45), Armed: true, Status: vehicle.StatusAirborne, FixOK: true})
	}
	status, body := get(t, srv.URL+"/uss/flights?view=41.71,44.82,41.72,44.83&recent_positions_duration=60", "dp")
	if status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	resp, err := f3411.UnmarshalGetFlightsResponse(body)
	if err != nil {
		t.Fatalf("uspace-core refuses the response: %v\n%s", err, body)
	}
	if resp.Flights == nil || len(*resp.Flights) != 1 || len(*(*resp.Flights)[0].RecentPositions) != 5 {
		t.Fatalf("%s", body)
	}
	// Details name the serial.
	id := (*resp.Flights)[0].Id
	status, body = get(t, srv.URL+"/uss/flights/"+id+"/details", "dp")
	var det f3411.GetFlightDetailsResponse
	if status != http.StatusOK || json.Unmarshal(body, &det) != nil || *det.Details.UasId.SerialNumber != "LABPEER0003" {
		t.Fatalf("%d %s", status, body)
	}
	// The intent with its OVN, as uspace-core reads it.
	for oid := range p.Intents() {
		status, body = get(t, srv.URL+"/uss/v1/operational_intents/"+oid, "sc")
		var wrap struct {
			OperationalIntent json.RawMessage `json:"operational_intent"`
		}
		_ = json.Unmarshal(body, &wrap)
		oi, err := f3548.UnmarshalOperationalIntent(wrap.OperationalIntent)
		if status != http.StatusOK || err != nil || oi.Reference.Ovn == nil {
			t.Fatalf("%d %v %s", status, err, body)
		}
	}
}

func TestViewsAndTokensAreChecked(t *testing.T) {
	_, srv := newPeer(t, &dss{})
	cases := []struct {
		url, tok string
		want     int
	}{
		{"/uss/flights?view=41.71,44.82,41.72,44.83", "", http.StatusUnauthorized},
		{"/uss/flights?view=41.71,44.82,41.72,44.83", "bad", http.StatusUnauthorized},
		{"/uss/flights?view=41.71,44.82,41.72,44.83", "sc", http.StatusForbidden},
		{"/uss/flights?view=41.0,44.0,41.5,44.5", "dp", http.StatusRequestEntityTooLarge},
		{"/uss/flights?view=x", "dp", http.StatusBadRequest},
		{"/uss/flights?view=41.71,44.82,41.72,44.83", "dp", http.StatusOK},
		{"/uss/v1/operational_intents/00000000-0000-4000-8000-000000000000", "sc", http.StatusNotFound},
		{"/nowhere", "dp", http.StatusNotFound},
	}
	for _, c := range cases {
		if status, body := get(t, srv.URL+c.url, c.tok); status != c.want {
			t.Errorf("%s with %q: %d, want %d (%s)", c.url, c.tok, status, c.want, body)
		}
	}
}

func TestDSSRefusalIsReported(t *testing.T) {
	p, _ := newPeer(t, &dss{status: http.StatusConflict})
	err := p.PutISA(context.Background())
	var de *DSSError
	if !errors.As(err, &de) || de.Status != http.StatusConflict || p.Counters().Get("isa_write_failures") != 1 {
		t.Fatalf("got %v", err)
	}
}

// A restarted peer (a second process, same configuration, later) writes
// its references again and the DSS takes them: the ids are per run, so
// the second PUT is a creation, not a replacement without the OVN, and
// its key carries the OVN of the first run's reference in the same
// volume. Both refusals the real DSS gave (409) are shown to happen in
// the double without them.
func TestRestartedPeerWritesNewReferences(t *testing.T) {
	d := &dss{}
	first, _ := newPeer(t, d)
	second, _ := newPeer(t, d)
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	first.cfg.Now = func() time.Time { return t0 }
	second.cfg.Now = func() time.Time { return t0.Add(time.Minute) }
	for _, p := range []*Peer{first, second} {
		if err := p.PutIntents(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.oirIDs) != 2 || d.oirIDs[0] == d.oirIDs[1] {
		t.Fatalf("reference ids %v", d.oirIDs)
	}
	if k := d.oir[0].Key; k == nil || len(*k) != 0 {
		t.Fatalf("first key %v, want empty", k)
	}
	if k := d.oir[1].Key; k == nil || !slices.Equal(*k, []string{d.ovns[0]}) {
		t.Fatalf("second key %v, want [%s]", k, d.ovns[0])
	}
	// The double does refuse a key without an intersecting OVN: the
	// second run's reference is left out.
	var de *DSSError
	var out f3548.ChangeOperationalIntentReferenceResponse
	body := f3548.PutOperationalIntentReferenceParameters{Key: &f3548.Key{d.ovns[0]}, Extents: []f3548.Volume4D{volume4D(second.cfg.Intents[0])},
		State: f3548.Accepted, UssBaseUrl: second.cfg.BaseURL}
	err := second.put(context.Background(), second.cfg.DSSUTMBase+"/dss/v1/operational_intent_references/00000000-0000-4000-8000-0000000000aa", body, &out, http.StatusCreated)
	if !errors.As(err, &de) || de.Status != http.StatusConflict {
		t.Fatalf("a key without an intersecting OVN: got %v, want a 409", err)
	}
	// The double does refuse a reused id: the second run again.
	err = second.PutIntents(context.Background())
	if !errors.As(err, &de) || de.Status != http.StatusConflict {
		t.Fatalf("a reused id: got %v, want a 409", err)
	}
}

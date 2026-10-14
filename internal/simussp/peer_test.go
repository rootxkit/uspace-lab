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
	subs   []f3548.PutSubscriptionParameters
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
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/rid/v2/dss/identification_service_areas/"):
		// DELETE .../{id}/{version}: the id is the second-last element.
		parts := strings.Split(r.URL.Path, "/")
		_ = json.NewEncoder(w).Encode(f3411.DeleteIdentificationServiceAreaResponse{ServiceArea: f3411.IdentificationServiceArea{
			Id: parts[len(parts)-2], Owner: "lab-peer", Version: id}})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/dss/v1/subscriptions/"):
		var p f3548.PutSubscriptionParameters
		if json.Unmarshal(b, &p) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		d.subs = append(d.subs, p)
		_ = json.NewEncoder(w).Encode(f3548.PutSubscriptionResponse{Subscription: f3548.Subscription{Id: id, Version: "s1", UssBaseUrl: p.UssBaseUrl}})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/dss/v1/subscriptions/"):
		parts := strings.Split(r.URL.Path, "/")
		_ = json.NewEncoder(w).Encode(f3548.DeleteSubscriptionResponse{Subscription: f3548.Subscription{Id: parts[len(parts)-2], Version: id}})
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

// The served flights are reported with the time of the answer and the
// position time of each flight served; a view that holds none of them
// reports an empty answer (the pair E-01 asks for).
func TestFlightsAnswersAreReported(t *testing.T) {
	type report struct {
		at     time.Time
		served map[string]time.Time
	}
	var mu sync.Mutex
	var got []report
	p, srv := newPeer(t, &dss{})
	p.cfg.OnFlights = func(at time.Time, served map[string]time.Time) {
		mu.Lock()
		got = append(got, report{at, served})
		mu.Unlock()
	}
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	ts := vehicle.FormatTime(t0)
	p.Observe(&vehicle.Sample{Schema: vehicle.Schema, Sysid: 3, TS: &ts, LatDeg: 41.7152, LonDeg: 44.8272, AltAMSLM: 635,
		AltHAEM: vehicle.F(650.9), HeightTakeoffM: 30, Armed: true, Status: vehicle.StatusAirborne, FixOK: true})
	before := time.Now()
	if status, body := get(t, srv.URL+"/uss/flights?view=41.71,44.82,41.72,44.83", "dp"); status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	if status, body := get(t, srv.URL+"/uss/flights?view=41.60,44.70,41.61,44.71", "dp"); status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d reports, want 2", len(got))
	}
	want := FlightID("LABPEER0003")
	if pos, ok := got[0].served[want]; !ok || !pos.Equal(t0) || len(got[0].served) != 1 || got[0].at.Before(before) {
		t.Fatalf("first answer reported %+v, want flight %s at %s", got[0], want, t0)
	}
	if len(got[1].served) != 0 {
		t.Fatalf("an answer for an empty view reported %v", got[1].served)
	}
}

// An F3548 notification is reported with the intent, its state and the
// subscriptions; a deletion carries no state; a body that names no
// intent is accepted and counted, never reported.
func TestIntentNotificationsAreReported(t *testing.T) {
	var mu sync.Mutex
	var got []Notification
	p, srv := newPeer(t, &dss{})
	p.cfg.OnNotification = func(n Notification) {
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	}
	post := func(body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/uss/v1/operational_intents", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sc")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	id := "2f8343be-6482-4d1b-a474-16847e01af1e"
	for _, b := range []string{
		`{"operational_intent_id":"` + id + `","operational_intent":{"reference":{"id":"` + id + `","state":"Activated"}},"subscriptions":[{"subscription_id":"sub-1","notification_index":3}]}`,
		`{"operational_intent_id":"` + id + `","subscriptions":[{"subscription_id":"sub-1","notification_index":4}]}`,
		`{"subscriptions":[]}`,
		`not json`,
	} {
		if s := post(b); s != http.StatusNoContent {
			t.Fatalf("POST %s: %d", b, s)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("reported %+v, want two", got)
	}
	if got[0].Standard != "f3548" || got[0].EntityID != id || got[0].State != "Activated" || !slices.Equal(got[0].Subscriptions, []string{"sub-1"}) || got[0].At.IsZero() {
		t.Fatalf("first %+v", got[0])
	}
	if got[1].EntityID != id || got[1].State != "" {
		t.Fatalf("deletion %+v", got[1])
	}
	if p.Counters().Get("notifications") != 4 || p.Counters().Get("notifications_unreadable") != 2 {
		t.Fatalf("counters %d notifications, %d unreadable", p.Counters().Get("notifications"), p.Counters().Get("notifications_unreadable"))
	}
}

// The subscription and the ISA are written and deleted with the ids and
// versions the DSS gave; with nothing written, a delete calls nothing.
func TestSubscriptionAndISAAreDeleted(t *testing.T) {
	d := &dss{}
	p, _ := newPeer(t, d)
	ctx := context.Background()
	if err := p.DeleteSubscription(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteISA(ctx); err != nil {
		t.Fatal(err)
	}
	if len(d.paths) != 0 {
		t.Fatalf("deletes with nothing written called %v", d.paths)
	}
	now := time.Now().UTC()
	v := volume4D(Intent{Center: core.LatLon{LatDeg: 41.7151, LonDeg: 44.8271}, RadiusM: 3000, AltLowerW84: 0, AltUpperW84: 3000, Start: now, End: now.Add(time.Hour)})
	if err := p.PutSubscription(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := p.PutISA(ctx); err != nil {
		t.Fatal(err)
	}
	if len(d.subs) != 1 || d.subs[0].NotifyForOperationalIntents == nil || !*d.subs[0].NotifyForOperationalIntents || d.subs[0].UssBaseUrl != "https://peer.lab.test" {
		t.Fatalf("subscription %+v", d.subs)
	}
	sid, _ := p.Subscription()
	iid, _ := p.ISA()
	if err := p.DeleteSubscription(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteISA(ctx); err != nil {
		t.Fatal(err)
	}
	wantSub := "DELETE /dss/v1/subscriptions/" + sid + "/s1"
	wantISA := "DELETE /rid/v2/dss/identification_service_areas/" + iid + "/v1"
	if !slices.Contains(d.paths, wantSub) || !slices.Contains(d.paths, wantISA) {
		t.Fatalf("paths %v, want %s and %s", d.paths, wantSub, wantISA)
	}
	if id, _ := p.Subscription(); id != "" {
		t.Fatalf("subscription %q kept after its delete", id)
	}
}

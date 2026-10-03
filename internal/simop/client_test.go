package simop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/reftarget"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/wire"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

func testPolicy(t *testing.T) *scenario.Policy {
	t.Helper()
	p, err := scenario.LoadPolicy(wiretest.Root() + "/scenarios/policy/demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func sample(sysid int, boot uint32, at time.Time) vehicle.Sample {
	ts := vehicle.FormatTime(at)
	return vehicle.Sample{
		Schema: vehicle.Schema, Sysid: sysid, TS: &ts, TimeBootMS: boot, LatDeg: 41.7151, LonDeg: 44.8271,
		AltAMSLM: 635, AltHAEM: vehicle.F(635), AltPressureM: vehicle.F(634), HeightTakeoffM: 30,
		SpeedMS: 5, TrackDeg: vehicle.F(90), VNMS: 0, VEMS: 5, Armed: true, Status: vehicle.StatusAirborne, FixOK: true,
	}
}

func TestFrameMatchesTheUSSPSchema(t *testing.T) {
	g := geoidx.Constant(15.9)
	s := sample(1, 1000, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	op := core.LatLon{LatDeg: 41.7150, LonDeg: 44.8270}
	f, skip := BuildFrame(&s, "LABSC01A0001", "lab-1", 7, "6f1c1b7e-5c3a-4d2e-9f0a-1b2c3d4e5f60", &op, g)
	if skip != "" {
		t.Fatal(skip)
	}
	wiretest.Validate(t, "ussp/telemetry-v1.json", f)
	if *f.AltWGS84M != 635+15.9 {
		t.Fatalf("HAE %v, want AMSL + N", *f.AltWGS84M)
	}
	// Presence of the refusal: the pinned schema refuses what the USSP
	// refuses (a client claiming a trust class, 06 T11).
	var m map[string]any
	b, _ := json.Marshal(f)
	_ = json.Unmarshal(b, &m)
	m["trust"] = "simulated"
	if err := wiretest.Check(t, "ussp/telemetry-v1.json", m); err == nil {
		t.Fatal("the schema accepted a frame with a trust class")
	}
	// No UTC yet and no fix: no frame.
	s.TS = nil
	if _, skip := BuildFrame(&s, "X", "e", 0, "", nil, g); skip != skipNoUTC {
		t.Fatalf("skip %q", skip)
	}
	s = sample(1, 1, time.Now())
	s.FixOK = false
	if _, skip := BuildFrame(&s, "X", "e", 0, "", nil, g); skip != skipNoFix {
		t.Fatalf("skip %q", skip)
	}
	// S-36: alt invalid sends no HAE.
	s = sample(1, 1, time.Now())
	s.AltInvalid = true
	f, _ = BuildFrame(&s, "X", "e", 0, "", nil, g)
	if f.AltWGS84M != nil || f.AccuracyV != accuracyVUnknown {
		t.Fatal("an invalid altitude was sent")
	}
}

type rig struct {
	target *reftarget.Target
	srv    *httptest.Server
	tokens *oauth.ClientCredentials
}

func newRig(t *testing.T, serials ...string) *rig { return newRigHz(t, 1000, serials...) }

func newRigHz(t *testing.T, hz float64, serials ...string) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tg, err := reftarget.New(ctx, reftarget.Config{
		Policy: testPolicy(t), Audience: "ref.lab.test", Geoid: geoidx.Constant(15.9), ConsoleToken: "console", MaxLiveHz: hz,
		Clients: []reftarget.Client{{ID: "op-a", Secret: "secret-a", Serials: serials,
			Scopes: []string{reftarget.ScopeTelemetry, reftarget.ScopeIntents, reftarget.ScopeTraffic}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	go tg.Run(ctx)
	srv := httptest.NewServer(tg.Handler())
	t.Cleanup(srv.Close)
	return &rig{target: tg, srv: srv, tokens: &oauth.ClientCredentials{TokenURL: srv.URL + "/oauth/token", ClientID: "op-a", ClientSecret: "secret-a",
		Scopes: []string{reftarget.ScopeTelemetry, reftarget.ScopeIntents, reftarget.ScopeTraffic}, Audience: "ref.lab.test"}}
}

// feed pushes n samples at 4 Hz worth of boot time, one per call period.
func feed(ctx context.Context, ch chan<- vehicle.Sample, sysid, n int, every time.Duration) {
	t0 := time.Now()
	for i := range n {
		select {
		case <-ctx.Done():
			return
		case ch <- sample(sysid, uint32(1000+i*250), t0.Add(time.Duration(i)*250*time.Millisecond)):
		}
		time.Sleep(every) // pacing the producer, not waiting for a result
	}
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamsToTheReferenceUSSPAndBalances(t *testing.T) {
	r := newRig(t, "LABOP0001")
	c, err := New(Config{BaseURL: r.srv.URL, Tokens: r.tokens, Serial: "LABOP0001", Period: 20 * time.Millisecond, Geoid: geoidx.Constant(15.9)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = c.Run(ctx, ch) }()
	feed(ctx, ch, 1, 40, 25*time.Millisecond)
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	l := c.Drain(dctx)
	if !l.Exact || l.Sent == 0 || l.Accepted != l.Sent || l.Refused != 0 {
		t.Fatalf("ledger %+v", l)
	}
	if got := r.target.Counters()["operator_ws"]["accepted"]; got != l.Accepted {
		t.Fatalf("the target counted %d accepted, the client's ledger %d", got, l.Accepted)
	}
}

// The presence twin of the test above: streamed faster than the USSP's
// 2 Hz, the excess is dropped by the target and the ledger still adds up.
func TestAboveTheLiveRateIsDroppedAndBalanced(t *testing.T) {
	r := newRigHz(t, 2, "LABOP0001")
	c, _ := New(Config{BaseURL: r.srv.URL, Tokens: r.tokens, Serial: "LABOP0001", Period: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = c.Run(ctx, ch) }()
	feed(ctx, ch, 1, 20, 25*time.Millisecond)
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	l := c.Drain(dctx)
	if l.Dropped == 0 || l.Outcomes["dropped_rate"] != l.Dropped || !l.Exact {
		t.Fatalf("ledger %+v", l)
	}
}

func TestUnboundSerialIsRefusedAndStillBalanced(t *testing.T) {
	r := newRig(t, "LABOP0001")
	c, _ := New(Config{BaseURL: r.srv.URL, Tokens: r.tokens, Serial: "NOT-BOUND", Period: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = c.Run(ctx, ch) }()
	feed(ctx, ch, 1, 10, 25*time.Millisecond)
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	l := c.Drain(dctx)
	if l.Refused == 0 || l.Accepted != 0 || !l.Exact || l.Outcomes["refused_unbound"] != l.Refused {
		t.Fatalf("ledger %+v", l)
	}
}

func TestAWrongSecretNeverConnects(t *testing.T) {
	r := newRig(t, "LABOP0001")
	bad := &oauth.ClientCredentials{TokenURL: r.srv.URL + "/oauth/token", ClientID: "op-a", ClientSecret: "wrong"}
	c, _ := New(Config{BaseURL: r.srv.URL, Tokens: bad, Serial: "LABOP0001", Period: 20 * time.Millisecond, RetryAfter: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx, make(chan vehicle.Sample)) }()
	waitFor(t, 5*time.Second, "dial failures", func() bool { return c.Counters().Get(CounterDialFailures) >= 2 })
	if c.Counters().Get(CounterSentLive) != 0 {
		t.Fatal("sent without a token")
	}
}

// recorder is a WS /v1/telemetry double that records every frame and
// acknowledges up to the last seq (an E-01 pair with the queue test).
type recorder struct {
	mu     sync.Mutex
	frames []Frame
	conns  int
}

func (rc *recorder) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		rc.mu.Lock()
		rc.conns++
		id := rc.conns
		rc.mu.Unlock()
		var acc uint64
		outcomes := map[string]uint64{}
		for {
			_, b, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var m Message
			if err := json.Unmarshal(b, &m); err != nil {
				t.Errorf("bad frame: %v", err)
				return
			}
			rc.mu.Lock()
			rc.frames = append(rc.frames, m.Body)
			rc.mu.Unlock()
			acc++
			outcomes["accepted"]++
			st, _ := wire.New(wire.SchemaStatus, "ussp/telemetry-ingest", time.Now(), time.Now(), nil, "system", false, map[string]any{
				"connection_id": "c" + string(rune('0'+id)), "accepted": acc, "refused": 0, "dropped": 0, "outcomes": outcomes,
				"acked_seq": map[string]int64{m.Body.Serial: m.Body.Seq},
			})
			_ = conn.Write(r.Context(), websocket.MessageText, st)
		}
	})
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }
func (staticToken) Invalidate()                             {}

func TestQueueWhileDownReplaysAsBacklogWithItsOwnTime(t *testing.T) {
	rc := &recorder{}
	srv := httptest.NewServer(rc.handler(t))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Tokens: staticToken("tok"), Serial: "S1", Period: 20 * time.Millisecond, RetryAfter: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = c.Run(ctx, ch) }()
	feed(ctx, ch, 1, 5, 25*time.Millisecond)
	waitFor(t, 5*time.Second, "live frames", func() bool { rc.mu.Lock(); defer rc.mu.Unlock(); return len(rc.frames) >= 3 })
	c.LinkDown()
	waitFor(t, 5*time.Second, "disconnect", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.connected })
	feed(ctx, ch, 1, 8, 25*time.Millisecond)
	waitFor(t, 5*time.Second, "a queue", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) >= 4 })
	rc.mu.Lock()
	before := len(rc.frames)
	rc.mu.Unlock()
	c.LinkUp()
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	l := c.Drain(dctx)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.conns != 2 {
		t.Fatalf("%d connections, want 2", rc.conns)
	}
	backlog := 0
	var lastTS string
	for _, f := range rc.frames[before:] {
		if f.Backlog {
			backlog++
			if f.TS <= lastTS {
				t.Fatalf("backlog out of order or without its own ts: %s after %s", f.TS, lastTS)
			}
			lastTS = f.TS
		}
	}
	if backlog < 4 {
		t.Fatalf("%d backlog frames after the outage", backlog)
	}
	// The absence twin: live frames before the outage carried no backlog.
	for _, f := range rc.frames[:3] {
		if f.Backlog {
			t.Fatal("a live frame was marked backlog")
		}
	}
	if !l.Balanced || l.SentBacklog == 0 {
		t.Fatalf("ledger %+v", l)
	}
}

func TestQueueIsBoundedAndShedsCounted(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1", Tokens: staticToken("tok"), Serial: "S1", QueueMax: 3})
	c.connected = false
	for i := range 10 {
		s := sample(1, uint32(i+1), time.Now())
		c.latest = &s
		c.tick()
	}
	if len(c.queue) != 3 || c.Counters().Get(CounterQueueShed) != 7 {
		t.Fatalf("queue %d, shed %d", len(c.queue), c.Counters().Get(CounterQueueShed))
	}
}

func TestDropKnob(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1", Tokens: staticToken("tok"), Serial: "S1", DropRate: 0.5, Seed: 7})
	for i := range 200 {
		s := sample(1, uint32(i+1), time.Now())
		c.latest = &s
		c.tick()
	}
	d := c.Counters().Get(CounterDroppedByKnob)
	if d < 60 || d > 140 || c.Counters().Get(CounterProduced) != 200 {
		t.Fatalf("dropped %d of 200", d)
	}
}

func TestBatchTransport(t *testing.T) {
	r := newRig(t, "LABOP0002")
	c, _ := New(Config{BaseURL: r.srv.URL, Tokens: r.tokens, Serial: "LABOP0002", Transport: TransportBatch, Period: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = c.Run(ctx, ch) }()
	feed(ctx, ch, 1, 20, 25*time.Millisecond)
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	l := c.Drain(dctx)
	if l.Accepted == 0 || !l.Exact {
		t.Fatalf("ledger %+v", l)
	}
}

func TestIntentsFiledAndActivated(t *testing.T) {
	r := newRig(t, "LABOP0003")
	in := &Intents{BaseURL: r.srv.URL, Tokens: r.tokens}
	req := sampleIntent("LABOP0003")
	wiretest.Validate(t, "ussp/intent-request-v1.json", req)
	d, err := in.File(context.Background(), req)
	if err != nil || d.Decision != "authorised" || d.State != "accepted" {
		t.Fatalf("%+v %v", d, err)
	}
	d, err = in.Change(context.Background(), d.IntentID, "activate")
	if err != nil || d.State != "activated" {
		t.Fatalf("%+v %v", d, err)
	}
	// Refusal: a serial bound to nobody.
	if _, err := in.File(context.Background(), sampleIntent("OTHER")); err == nil {
		t.Fatal("an unbound serial was authorised")
	}
}

func sampleIntent(serial string) IntentRequest {
	now := time.Now().UTC()
	return IntentRequest{
		ClientRef: "lab-" + serial, UASSerial: serial, Mode: "VLOS", FlightType: "normal", Category: "open", Subcategory: "A2", ClassLabel: "C2",
		Volumes: []Volume4D{{
			Volume: Volume3D{
				OutlineCircle: &Circle{Center: Point{Lat: 41.7151, Lng: 44.8271}, Radius: Radius{Value: 500, Units: "M"}},
				AltitudeLower: IntentAltitude{Value: 600, Reference: "W84", Units: "M"},
				AltitudeUpper: IntentAltitude{Value: 760, Reference: "W84", Units: "M"},
			},
			TimeStart: IntentTime{Value: now.Format(time.RFC3339), Format: "RFC3339"},
			TimeEnd:   IntentTime{Value: now.Add(time.Hour).Format(time.RFC3339), Format: "RFC3339"},
		}},
		IdentificationTechnology: "network", ConnectivityMethods: []string{"lte"}, EnduranceS: 3600,
		LossOfC2Procedure: "return to home", OperatorReg: "GEOLAB000001", Contingency: IntentContingency{Procedure: "land at take-off"},
		EmergencyContactRef: "lab-contact-1",
	}
}

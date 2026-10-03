package simrx

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/odid"
	"github.com/rootxkit/uspace-core/timeplace"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/reftarget"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

var hmacKey = bytes.Repeat([]byte{0x42}, 32)

func sample(sysid int, boot uint32, at time.Time, armed bool) vehicle.Sample {
	ts := vehicle.FormatTime(at)
	st := vehicle.StatusGround
	if armed {
		st = vehicle.StatusAirborne
	}
	return vehicle.Sample{
		Schema: vehicle.Schema, Sysid: sysid, TS: &ts, TimeBootMS: boot, LatDeg: 41.7151, LonDeg: 44.8271,
		AltAMSLM: 635, AltHAEM: vehicle.F(635), AltPressureM: vehicle.F(634), HeightTakeoffM: 30,
		SpeedMS: 5, TrackDeg: vehicle.F(90), VEMS: 5, Armed: armed, Status: st, FixOK: true,
	}
}

func TestEncodingConstantsAreTheDecodersOwn(t *testing.T) {
	// MAV_ODID_TIME_ACC_0_1_SECOND is 0.1 s by uspace-core's reading.
	if timeplace.AccuracyS(tsAccuracy0p1s) != 0.1 {
		t.Fatal("ts accuracy code is not 0.1 s")
	}
}

func TestBroadcastDecodesToTheInputs(t *testing.T) {
	g := geoidx.Constant(15.9)
	at := time.Date(2026, 10, 3, 12, 34, 56, 700e6, time.UTC)
	s := sample(1, 1000, at, true)
	loc := LocationOf(&s, HAEGeoid, g)
	sys, ok := SystemOf(&s, core.LatLon{LatDeg: 41.7150, LonDeg: 44.8270})
	if !ok {
		t.Fatal("no System with UTC known")
	}
	pack, err := odid.EncodePack([]odid.Message{loc, BasicIDOf("LABSER0001"), sys, OperatorIDOf("GEOLAB000001")})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := odid.Decode(pack, odid.DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got odid.Location
	var serial, operator string
	var gotSys odid.System
	for _, m := range msgs {
		switch v := m.(type) {
		case odid.Location:
			got = v
		case odid.BasicID:
			serial = v.UAID
		case odid.OperatorID:
			operator = v.OperatorID
		case odid.System:
			gotSys = v
		}
	}
	if serial != "LABSER0001" || operator != "GEOLAB000001" {
		t.Fatalf("serial %q operator %q", serial, operator)
	}
	if math.Abs(*got.LatDeg-41.7151) > 1e-7 || math.Abs(*got.LonDeg-44.8271) > 1e-7 {
		t.Fatal("position")
	}
	// HAE = AMSL + N (R-16), to the broadcast's 0.5 m step.
	if math.Abs(*got.AltHAEM-(635+15.9)) > 0.5 {
		t.Fatalf("HAE %v, want %v", *got.AltHAEM, 635+15.9)
	}
	if *got.SecondsAfterHour != 34*60+56.7 || got.Status != odid.StatusAirborne {
		t.Fatalf("timestamp %v status %v", *got.SecondsAfterHour, got.Status)
	}
	if gotSys.TimestampS == 0 || *gotSys.OperatorLatDeg == 0 {
		t.Fatal("System without time or operator location")
	}
	// gps: the vehicle's own alt_hae_m, which SITL reports equal to AMSL.
	loc = LocationOf(&s, HAEGPS, g)
	if *loc.AltHAEM != 635 {
		t.Fatalf("gps HAE %v", *loc.AltHAEM)
	}
}

func TestUnknownTimeAndNoSystemUntilUTC(t *testing.T) {
	s := sample(1, 1000, time.Now(), true)
	s.TS = nil
	loc := LocationOf(&s, HAEGeoid, geoidx.Constant(10))
	if loc.SecondsAfterHour != nil || loc.TSAccuracy != 0 {
		t.Fatal("a timestamp before UTC is known")
	}
	if _, ok := SystemOf(&s, core.LatLon{}); ok {
		t.Fatal("a System message before UTC is known")
	}
	b, err := odid.Encode(loc)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := odid.DecodeMessage(b, odid.DecodeOptions{})
	if m.(odid.Location).SecondsAfterHour != nil {
		t.Fatal("unknown time did not survive the wire")
	}
}

func TestInvalidAltitudeIsNotBroadcast(t *testing.T) {
	s := sample(1, 1000, time.Now(), true)
	s.AltInvalid = true
	if loc := LocationOf(&s, HAEGeoid, geoidx.Constant(10)); loc.AltHAEM != nil || loc.VertAccuracy != 0 {
		t.Fatal("S-36: an invalid altitude was broadcast")
	}
}

// capture is an F9 double that checks the signature and records batches.
type capture struct {
	mu      sync.Mutex
	batches []Batch
	status  int
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m := hmac.New(sha256.New, hmacKey)
	m.Write(body)
	if r.Header.Get("Authorization") != "Bearer rx-key" || r.Header.Get("X-Report-Signature") != hex.EncodeToString(m.Sum(nil)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var b Batch
	_ = json.Unmarshal(body, &b)
	c.mu.Lock()
	status := c.status
	if status == 0 || status == http.StatusAccepted {
		c.batches = append(c.batches, b)
	}
	c.mu.Unlock()
	if status != 0 && status != http.StatusAccepted {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"batch_id":"x","accepted":` + itoa(len(b.Observations)) + `,"duplicates":0}`))
}

func itoa(i int) string { return strings.TrimSpace(strings.Repeat(" ", 0)) + jsonInt(i) }

func jsonInt(i int) string { b, _ := json.Marshal(i); return string(b) }

func newRx(t *testing.T, url string, transport string, extra func(*Config)) *Receiver {
	t.Helper()
	cfg := Config{
		BaseURL: url, ReceiverID: "lab-rx-1", BearerKey: "rx-key", HMACKey: hmacKey, Transport: transport,
		Geoid: geoidx.Constant(15.9), LocationPeriod: 10 * time.Millisecond, StaticPeriod: 30 * time.Millisecond,
		BatchPeriod: 50 * time.Millisecond, Position: &core.LatLon{LatDeg: 41.7, LonDeg: 44.8}, RSSIDBm: -60,
		Transmitters: []Transmitter{{Sysid: 1, MAC: "02:00:00:00:00:01", Serial: "LABSER0001", OperatorID: "GEOLAB000001"}},
	}
	if extra != nil {
		extra(&cfg)
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func feed(ctx context.Context, ch chan<- vehicle.Sample, n int) {
	t0 := time.Now()
	for i := range n {
		select {
		case <-ctx.Done():
			return
		case ch <- sample(1, uint32(1000+i*250), t0.Add(time.Duration(i)*20*time.Millisecond), i > 2):
		}
		time.Sleep(15 * time.Millisecond) // pacing the producer
	}
}

func TestBatchesAreSignedValidAndPacked(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	for _, tr := range []string{TransportPack, TransportSingle} {
		c.mu.Lock()
		c.batches = nil
		c.mu.Unlock()
		r := newRx(t, srv.URL, tr, nil)
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan vehicle.Sample)
		go func() { _ = r.Run(ctx, ch) }()
		feed(ctx, ch, 20)
		dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
		l := r.Drain(dctx)
		dcancel()
		cancel()
		if !l.Balanced || l.Accepted == 0 || l.Refused != 0 {
			t.Fatalf("%s: ledger %+v", tr, l)
		}
		c.mu.Lock()
		packs, singles := 0, 0
		for _, b := range c.batches {
			wiretest.Validate(t, "authority/rid-observation-v1.json", b)
			if len(b.Observations) > MaxObservations {
				t.Fatal("batch over 64 observations")
			}
			for _, o := range b.Observations {
				raw, _ := hex.DecodeString(o.PayloadHex)
				if typ, _ := odid.TypeOf(raw); typ == odid.TypeMessagePack {
					packs++
				} else {
					singles++
				}
			}
		}
		c.mu.Unlock()
		if tr == TransportPack && (packs == 0 || singles != 0) || tr == TransportSingle && (singles == 0 || packs != 0) {
			t.Fatalf("%s: %d packs, %d singles", tr, packs, singles)
		}
	}
}

func TestOutageBuffersAndReplaysAsBacklog(t *testing.T) {
	c := &capture{status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(c)
	defer srv.Close()
	r := newRx(t, srv.URL, TransportPack, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan vehicle.Sample)
	go func() { _ = r.Run(ctx, ch) }()
	feed(ctx, ch, 15)
	deadline := time.Now().Add(5 * time.Second)
	for r.Ledger().Pending == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.Counters().Get(CounterBatchFailures) == 0 || r.Ledger().Pending == 0 {
		t.Fatalf("no backlog during the outage: %+v", r.Ledger())
	}
	c.mu.Lock()
	c.status = http.StatusAccepted
	c.mu.Unlock()
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	l := r.Drain(dctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	backlog := 0
	for _, b := range c.batches {
		if b.Backlog {
			backlog++
		}
	}
	if backlog == 0 || !l.Balanced || l.Pending != 0 {
		t.Fatalf("backlog batches %d, ledger %+v", backlog, l)
	}
}

func TestAgainstTheReferenceAuthority(t *testing.T) {
	pol, err := scenario.LoadPolicy(wiretest.Root() + "/scenarios/policy/demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tg, err := reftarget.New(ctx, reftarget.Config{Policy: pol, Audience: "ref.lab.test", Geoid: geoidx.Constant(15.9),
		Receivers: []reftarget.Receiver{{ID: "lab-rx-1", BearerKey: "rx-key", HMACKey: hmacKey}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(tg.Handler())
	defer srv.Close()
	r := newRx(t, srv.URL, TransportPack, nil)
	ch := make(chan vehicle.Sample)
	go func() { _ = r.Run(ctx, ch) }()
	feed(ctx, ch, 20)
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	l := r.Drain(dctx)
	if l.Accepted == 0 || !l.Balanced || tg.Counters()["direct_rid"]["accepted"] != l.Accepted {
		t.Fatalf("ledger %+v target %v", l, tg.Counters()["direct_rid"])
	}
	// The refused path: a wrong HMAC key is refused, counted on both sides.
	bad := newRx(t, srv.URL, TransportPack, func(c *Config) { c.HMACKey = bytes.Repeat([]byte{1}, 32) })
	ch2 := make(chan vehicle.Sample)
	go func() { _ = bad.Run(ctx, ch2) }()
	feed(ctx, ch2, 8)
	dctx2, dcancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel2()
	lb := bad.Drain(dctx2)
	if lb.Refused == 0 || lb.Accepted != 0 || tg.Counters()["direct_rid"]["refused_signature"] == 0 {
		t.Fatalf("ledger %+v target %v", lb, tg.Counters()["direct_rid"])
	}
}

func TestDropKnobAndSerialChange(t *testing.T) {
	c := &capture{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	r := newRx(t, srv.URL, TransportSingle, func(c *Config) { c.DropRate = 0.3; c.Seed = 9 })
	for i := range 50 {
		s := sample(1, uint32(i), time.Now(), true)
		r.mu.Lock()
		for _, tx := range r.tx[1] {
			tx.lastLocation = time.Time{}
		}
		r.mu.Unlock()
		r.hear(&s)
		if i == 25 {
			r.SetSerial(1, "LABSER0002")
		}
	}
	d := r.Counters().Get(CounterDroppedByKnob)
	if d == 0 || d >= r.Counters().Get(CounterObserved) {
		t.Fatalf("dropped %d of %d", d, r.Counters().Get(CounterObserved))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tx[1][0].Serial != "LABSER0002" {
		t.Fatal("serial not changed")
	}
}

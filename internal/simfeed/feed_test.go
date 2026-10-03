package simfeed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"

	"github.com/rootxkit/uspace-lab/internal/manned"
	"github.com/rootxkit/uspace-lab/internal/wire"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

func verifier(_ context.Context, tok string) ([]string, error) {
	switch tok {
	case "good":
		return []string{ScopeTraffic}, nil
	case "other":
		return []string{"cis.read"}, nil
	}
	return nil, errors.New("unknown token")
}

func newFeed(t *testing.T) (*Feed, *httptest.Server) {
	t.Helper()
	from := core.LatLon{LatDeg: 41.7, LonDeg: 44.8}
	src := &manned.Legs{T0: time.Now(), Legs: []manned.Leg{{ICAO24: "f0a001", Callsign: "LAB1", From: from,
		To: geodesy.Destination(from, 90, 50_000), SpeedMS: 60, AltPressureM: 900}}}
	f, err := New(Config{Source: src, Instance: "lab-adsb-1", Verify: verifier, StaleAfterS: 0.3, LiveMaxAgeS: 0.2,
		PolicyVersion: "1", Period: 50 * time.Millisecond, StatusEvery: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f.Handler())
	t.Cleanup(srv.Close)
	return f, srv
}

func dial(t *testing.T, srv *httptest.Server, tok string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/manned-traffic/stream?bbox=44,41,46,42"
	return websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
}

func read(t *testing.T, c *websocket.Conn, want string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		e, err := wire.ParseEnvelope(b)
		if err != nil {
			t.Fatal(err)
		}
		if e.Schema != want {
			continue
		}
		var full map[string]any
		_ = json.Unmarshal(b, &full)
		body, _ := full["body"].(map[string]any)
		if ok == nil || ok(body) {
			return full
		}
	}
}

func TestStreamFramesMatchTheANSPSchema(t *testing.T) {
	_, srv := newFeed(t)
	c, resp, err := dial(t, srv, "good")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer c.CloseNow()
	read(t, c, wire.SchemaStatus, nil)
	read(t, c, wire.SchemaSnapshot, nil)
	frame := read(t, c, wire.SchemaManned, nil)
	wiretest.Validate(t, "ansp/track-manned-v1.json", frame)
	body := frame["body"].(map[string]any)
	if body["state"] != StateLive || body["trust"] != "surveillance" || body["source"] != "ansp_feed" {
		t.Fatalf("%v", body)
	}
}

func TestStaleKnobAgesAircraftIntoStale(t *testing.T) {
	f, srv := newFeed(t)
	c, resp, err := dial(t, srv, "good")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer c.CloseNow()
	read(t, c, wire.SchemaManned, func(b map[string]any) bool { return b["state"] == StateLive })
	if err := f.SetState(StateStale); err != nil {
		t.Fatal(err)
	}
	frame := read(t, c, wire.SchemaManned, func(b map[string]any) bool { return b["state"] == StateStale })
	if age := frame["body"].(map[string]any)["age_s"].(float64); age <= 0.3 {
		t.Fatalf("stale at age %v", age)
	}
	st := read(t, c, wire.SchemaStatus, func(b map[string]any) bool {
		d, _ := b["degraded"].([]any)
		return len(d) == 1 && d[0] == "manned_feed_stale"
	})
	_ = st
	// Back to live: live frames again.
	_ = f.SetState(StateLive)
	read(t, c, wire.SchemaManned, func(b map[string]any) bool { return b["state"] == StateLive })
}

func TestRefusalsAndOutage(t *testing.T) {
	f, srv := newFeed(t)
	for tok, want := range map[string]int{"": http.StatusUnauthorized, "bad": http.StatusUnauthorized, "other": http.StatusForbidden} {
		_, resp, err := dial(t, srv, tok)
		if err == nil || resp == nil || resp.StatusCode != want {
			t.Fatalf("token %q: %v %v", tok, resp, err)
		}
	}
	c, resp, err := dial(t, srv, "good")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	read(t, c, wire.SchemaStatus, nil)
	_ = f.SetState(StateOutage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
				t.Fatalf("closed with %v, want 1013", err)
			}
			break
		}
	}
	_, resp, err = dial(t, srv, "good")
	if err == nil || resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("during the outage: %v", resp)
	}
}

func TestSnapshotFiltersByBox(t *testing.T) {
	_, srv := newFeed(t)
	get := func(bbox string) map[string]any {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/manned-traffic/snapshot?bbox="+bbox, nil)
		req.Header.Set("Authorization", "Bearer good")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	if n := len(get("44,41,46,42")["manned"].([]any)); n != 1 {
		t.Fatalf("%d in the box", n)
	}
	if n := len(get("10,10,11,11")["manned"].([]any)); n != 0 {
		t.Fatalf("%d outside the box", n)
	}
}

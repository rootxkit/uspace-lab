package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/wire"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

func frame(t *testing.T, schema string, body map[string]any) []byte {
	t.Helper()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	b, err := wire.New(schema, "ussp/traffic-ws", now, now, nil, "system", false, body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func alertBody(state string, reason any) map[string]any {
	return map[string]any{
		"alert_id": "6f1c1b7e-5c3a-4d2e-9f0a-1b2c3d4e5f60", "kind": "proximity", "severity": "critical", "state": state,
		"clear_reason": reason, "flight_id": "0b6d3a3e-2f4c-4a4e-8b1e-2c3d4e5f6a7b", "intent_id": "1b6d3a3e-2f4c-4a4e-8b1e-2c3d4e5f6a7b",
		"authorisation_number": nil, "captured_at": "2026-10-03T11:59:59.900Z", "raised_at": "2026-10-03T12:00:00.000Z",
		"updated_at": "2026-10-03T12:00:00.000Z", "policy_version": 1,
		"detail": map[string]any{"t_cpa_s": 0.0, "d_cpa_h_m": 25.0, "d_alt_m": 0.0, "evaluation_period_s": 1,
			"peer": map[string]any{"track_id": "9b6d3a3e-2f4c-4a4e-8b1e-2c3d4e5f6a7b", "trust": "authenticated"}},
	}
}

func TestAlertFramesBecomeRaiseAndClear(t *testing.T) {
	// The frames are what uspace-ussp's alert/v1 says they are.
	for _, st := range []string{"raised", "updated"} {
		var m map[string]any
		_ = jsonRoundTrip(frame(t, wire.SchemaAlert, alertBody(st, nil)), &m)
		wiretest.Validate(t, "ussp/alert-v1.json", m)
	}
	r := NewRecorder(nil)
	s := Stream{Name: "ussp-alerts-a", System: "ussp", Aircraft: "a"}
	st := NewStreamState()
	now := time.Now()
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("raised", nil)), now)
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("updated", nil)), now)
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("cleared", "resolved")), now)
	ev := r.Events()
	if len(ev) != 2 || ev[0].Phase != PhaseRaised || ev[1].Phase != PhaseCleared || ev[1].Reason != "resolved" || ev[0].Aircraft != "a" {
		t.Fatalf("%+v", ev)
	}
	if ev[0].CapturedAt == nil || ev[0].Peer == "" {
		t.Fatalf("captured_at or peer lost: %+v", ev[0])
	}
	if r.Frames()["ussp-alerts-a"]["alert/v1"] != 3 {
		t.Fatal("frames not counted")
	}
}

func TestAnAlertActiveOnConnectIsARaise(t *testing.T) {
	r := NewRecorder(nil)
	st := NewStreamState()
	r.Handle(Stream{Name: "x", System: "ussp"}, st, frame(t, wire.SchemaAlert, alertBody("updated", nil)), time.Now())
	if ev := r.Events(); len(ev) != 1 || ev[0].Phase != PhaseRaised {
		t.Fatalf("%+v", ev)
	}
}

func TestPeerResolvesToTheAircraftWhoseStreamNamedItsFlight(t *testing.T) {
	r := NewRecorder(nil)
	a := alertBody("raised", nil)
	b := alertBody("raised", nil)
	b["alert_id"] = "7f1c1b7e-5c3a-4d2e-9f0a-1b2c3d4e5f60"
	b["flight_id"] = "9b6d3a3e-2f4c-4a4e-8b1e-2c3d4e5f6a7b"
	r.Handle(Stream{Name: "sa", System: "ussp", Aircraft: "a"}, NewStreamState(), frame(t, wire.SchemaAlert, a), time.Now())
	r.Handle(Stream{Name: "sb", System: "ussp", Aircraft: "b"}, NewStreamState(), frame(t, wire.SchemaAlert, b), time.Now())
	ev := r.Events()
	if ev[0].Peer != "b" {
		t.Fatalf("peer %q, want b", ev[0].Peer)
	}
}

func TestViolationMannedAndStatus(t *testing.T) {
	r := NewRecorder(map[string]string{"LABSER1": "c"})
	st := NewStreamState()
	s := Stream{Name: "pic", System: "authority"}
	vb := map[string]any{"violation_id": "01J00000000000000000000000", "kind": "zone_incursion", "state": "raised", "severity": "critical",
		"track_ref": "LABSER1", "serial": "LABSER1", "zone_id": "GEO/SC08Z", "captured_at": "2026-10-03T12:00:00.000Z", "opened_at": "2026-10-03T12:00:00.000Z", "clear_reason": nil}
	r.Handle(s, st, frame(t, wire.SchemaViolation, vb), time.Now())
	vb["state"], vb["clear_reason"] = "cleared", "source_disabled"
	r.Handle(s, st, frame(t, wire.SchemaViolation, vb), time.Now())
	m := Stream{Name: "man", System: "ansp"}
	ms := NewStreamState()
	for _, state := range []string{"live", "live", "stale", "live"} {
		r.Handle(m, ms, frame(t, wire.SchemaManned, map[string]any{"icao24": "f0c001", "state": state}), time.Now())
	}
	r.Handle(s, st, frame(t, wire.SchemaStatus, map[string]any{"degraded": []string{"cis_absent"}}), time.Now())
	r.Handle(s, st, frame(t, wire.SchemaStatus, map[string]any{"degraded": []string{}}), time.Now())
	var kinds []string
	for _, e := range r.Events() {
		kinds = append(kinds, e.Kind+":"+e.Phase+":"+e.Subject+":"+e.Reason+":"+e.Aircraft)
	}
	want := []string{
		"zone_incursion:raised:SC08Z::c", "zone_incursion:cleared:SC08Z:source_disabled:c",
		"manned_track:raised:f0c001::", "manned_track:cleared:f0c001:stale:", "manned_track:raised:f0c001::",
		"degraded:raised:cis_absent::", "degraded:cleared:cis_absent:not_degraded:",
	}
	if len(kinds) != len(want) {
		t.Fatalf("%v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event %d: %s, want %s", i, kinds[i], want[i])
		}
	}
}

func jsonRoundTrip(b []byte, v any) error { return json.Unmarshal(b, v) }

// A stream with OnOpen sends it first on every connection (the picture's
// console/subscribe/v1), and one without sends nothing.
func TestOnOpenIsSentOnConnect(t *testing.T) {
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
		defer cancel()
		_, b, err := c.Read(ctx)
		if err != nil {
			got <- "nothing"
			return
		}
		got <- string(b)
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, on := range []string{`{"schema":"console/subscribe/v1"}`, ""} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		r := NewRecorder(nil)
		go r.Run(ctx, Stream{Name: "s", URL: url, OnOpen: []byte(on), Retry: time.Hour})
		want := on
		if on == "" {
			want = "nothing"
		}
		if g := <-got; g != want {
			t.Fatalf("got %q, want %q", g, want)
		}
		cancel()
	}
}

func TestOnAddSeesEveryRecordedEventAndNothingElse(t *testing.T) {
	r := NewRecorder(nil)
	var seen []Event
	r.OnAdd(func(e Event) { seen = append(seen, e) })
	s := Stream{Name: "ussp-alerts-a", System: "ussp", Aircraft: "a"}
	st := NewStreamState()
	now := time.Now()
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("raised", nil)), now)
	// An update is a frame, not an event: the hook must not see it.
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("updated", nil)), now)
	r.Handle(s, st, frame(t, wire.SchemaAlert, alertBody("cleared", "resolved")), now)
	if len(seen) != 2 || seen[0].Phase != PhaseRaised || seen[1].Phase != PhaseCleared {
		t.Fatalf("hook saw %+v", seen)
	}
	if got := r.Events(); len(got) != len(seen) {
		t.Fatalf("recorded %d events, hook saw %d", len(got), len(seen))
	}
}

func TestWithoutOnAddRecordingIsUnchanged(t *testing.T) {
	r := NewRecorder(nil)
	r.Handle(Stream{Name: "x", System: "ussp"}, NewStreamState(), frame(t, wire.SchemaAlert, alertBody("raised", nil)), time.Now())
	if ev := r.Events(); len(ev) != 1 {
		t.Fatalf("%+v", ev)
	}
}

// An alert first seen in the snapshot a system sends on (re)connect is
// a raise; the same alert in a later snapshot, or as an update, is not
// recorded again; a new id in a snapshot is a second raise (what a
// restart that loses an alert's identity looks like).
func TestSnapshotAlertsAreSeenOnce(t *testing.T) {
	r := NewRecorder(map[string]string{"LABSER1": "c"})
	st := NewStreamState()
	s := Stream{Name: "pic", System: "authority"}
	vb := func(id, state string) map[string]any {
		return map[string]any{"violation_id": id, "kind": "zone_incursion", "state": state, "severity": "warning",
			"track_ref": "LABSER1", "serial": "LABSER1", "zone_id": "GEO/Z", "captured_at": "2026-10-03T12:00:00.000Z",
			"opened_at": "2026-10-03T12:00:00.000Z", "clear_reason": nil}
	}
	snap := func(items ...map[string]any) []byte {
		var alerts []json.RawMessage
		for _, it := range items {
			alerts = append(alerts, frame(t, wire.SchemaViolation, it))
		}
		return frame(t, wire.SchemaSnapshot, map[string]any{"tracks": []any{}, "alerts": alerts, "manned": []any{}, "zones_version": "1"})
	}
	r.Handle(s, st, snap(vb("01J00000000000000000000001", "updated")), time.Now())
	r.Handle(s, st, snap(vb("01J00000000000000000000001", "updated")), time.Now())
	r.Handle(s, st, frame(t, wire.SchemaViolation, vb("01J00000000000000000000001", "updated")), time.Now())
	if ev := r.Events(); len(ev) != 1 || ev[0].Phase != PhaseRaised || ev[0].Aircraft != "c" {
		t.Fatalf("one raise expected: %+v", ev)
	}
	r.Handle(s, st, snap(vb("01J00000000000000000000002", "updated")), time.Now())
	if ev := r.Events(); len(ev) != 2 || ev[1].AlertID != "01J00000000000000000000002" || ev[1].Phase != PhaseRaised {
		t.Fatalf("a new id in a snapshot is a raise: %+v", ev)
	}
	// A snapshot without alerts records nothing.
	r.Handle(s, st, snap(), time.Now())
	if len(r.Events()) != 2 {
		t.Fatal("an empty snapshot recorded an event")
	}
}

// HeaderFunc is asked on every connection, so credentials renewed
// between two connections are the ones sent; Header alone sends the
// same every time.
func TestHeaderFuncIsAskedOnEveryConnection(t *testing.T) {
	got := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Close(4401, "sign in again")
	}))
	defer srv.Close()
	n := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := NewRecorder(nil)
	go r.Run(ctx, Stream{Name: "s", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Retry: 10 * time.Millisecond,
		HeaderFunc: func(context.Context) (http.Header, error) {
			n++
			return http.Header{"Authorization": {fmt.Sprintf("Bearer t%d", n)}}, nil
		}})
	first, second := <-got, <-got
	cancel()
	if first != "Bearer t1" || second != "Bearer t2" {
		t.Fatalf("headers %q, %q: renewed credentials were not sent", first, second)
	}
	// A HeaderFunc that fails is a stream error, and nothing is dialled.
	r2 := NewRecorder(nil)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	r2.Run(ctx2, Stream{Name: "f", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Retry: 50 * time.Millisecond,
		HeaderFunc: func(context.Context) (http.Header, error) { return nil, fmt.Errorf("no session") }})
	if e := r2.Errors()["f"]; !strings.Contains(e, "no session") {
		t.Fatalf("error %q", e)
	}
	select {
	case h := <-got:
		t.Fatalf("dialled without credentials: %q", h)
	default:
	}
}

package observe

import (
	"encoding/json"
	"testing"
	"time"

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

package simadsb

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"

	"github.com/rootxkit/uspace-lab/internal/manned"
)

// documentedKeys is the key list uspace-ansp's dump1090 SOURCE quotes
// from README-json.md, plus "messages" (in the same document and in the
// ansp's own aircraft.json fixture).
var documentedKeys = map[string]bool{
	"now": true, "aircraft": true, "hex": true, "type": true, "flight": true, "alt_baro": true, "alt_geom": true,
	"gs": true, "track": true, "baro_rate": true, "geom_rate": true, "squawk": true, "emergency": true, "lat": true,
	"lon": true, "nic": true, "rc": true, "seen_pos": true, "version": true, "nic_baro": true, "nac_p": true,
	"nac_v": true, "sil": true, "sil_type": true, "gva": true, "sda": true, "mlat": true, "tisb": true, "seen": true,
	"messages": true,
}

func TestDocumentUsesOnlyDocumentedKeysAndUnits(t *testing.T) {
	t0 := time.Unix(1_790_000_000, 0)
	from := core.LatLon{LatDeg: 41.7, LonDeg: 44.8}
	alt := 1000.0
	s := &Server{Source: &manned.Legs{T0: t0, Legs: []manned.Leg{{ICAO24: "f0a001", Callsign: "LAB1", From: from,
		To: geodesy.Destination(from, 0, 10_000), SpeedMS: manned.KnotToMS * 120, AltPressureM: alt}}},
		Now: func() time.Time { return t0.Add(10 * time.Second) }}
	srv := httptest.NewServer(s)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/data/aircraft.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for k := range raw {
		if !documentedKeys[k] {
			t.Errorf("undocumented top-level key %q", k)
		}
	}
	items := raw["aircraft"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d aircraft", len(items))
	}
	it := items[0].(map[string]any)
	for k := range it {
		if !documentedKeys[k] {
			t.Errorf("undocumented key %q", k)
		}
	}
	// 1000 m is 3280.8 ft; 120 kt; the callsign padded to 8.
	if math.Abs(it["alt_baro"].(float64)-3280.8) > 0.05 || math.Abs(it["gs"].(float64)-120) > 0.05 || it["flight"] != "LAB1    " {
		t.Fatalf("%v", it)
	}
}

func TestFreezeAgesAndDownRefuses(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	from := core.LatLon{LatDeg: 41.7, LonDeg: 44.8}
	s := &Server{Source: &manned.Legs{T0: now, Legs: []manned.Leg{{ICAO24: "f0a001", From: from, To: geodesy.Destination(from, 0, 10_000), SpeedMS: 50, AltPressureM: 500}}},
		Now: func() time.Time { return now }}
	if d := s.Document(now); len(d.Aircraft) != 1 || d.Aircraft[0].SeenPos != 0 {
		t.Fatalf("%+v", d)
	}
	s.Freeze()
	now = now.Add(20 * time.Second)
	if d := s.Document(now); d.Aircraft[0].SeenPos != 20 {
		t.Fatalf("frozen: seen_pos %v, want 20", d.Aircraft[0].SeenPos)
	}
	s.SetDown(true)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/data/aircraft.json", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("down: %d", rr.Code)
	}
}

package seed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// RFC 6238 Appendix B (SHA-1, secret "12345678901234567890"), the last
// six digits of each eight-digit vector.
func TestTOTPMatchesRFC6238(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, err := TOTP(secret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("T=%d: %s %v, want %s", unix, got, err, want)
		}
	}
	if _, err := TOTP("not base32!", time.Unix(59, 0)); err == nil {
		t.Error("a bad secret was accepted")
	}
}

// A code is used once: within one 30 s step the second code waits for
// the next step (each system accepts a code once).
func TestCodeWaitsForTheNextStep(t *testing.T) {
	a := &Account{TOTP: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"}
	now := time.Unix(1_790_000_010, 0)
	if _, err := a.Code(context.Background(), func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.Code(ctx, func() time.Time { return now }); err == nil {
		t.Fatal("a second code in the same step was given")
	}
}

func scenarios(t *testing.T) ([]*scenario.Scenario, *scenario.Lab) {
	t.Helper()
	var ss []*scenario.Scenario
	for _, f := range []string{"ussp-wp10-conformance.yaml", "sc-03-zone-entry-exit.yaml", "authority-sc08-rid-switch.yaml"} {
		s, err := scenario.Load("../../scenarios/" + f)
		if err != nil {
			t.Fatal(err)
		}
		ss = append(ss, s)
	}
	lab, err := scenario.LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	return ss, lab
}

// The serials each operator client binds come from the scenarios ("default"
// is op-a), with their classes; every aircraft is registered once.
func TestSerialsAndAircraftFromTheScenarios(t *testing.T) {
	ss, _ := scenarios(t)
	a, b := serialsOf(ss, "op-a"), serialsOf(ss, "op-b")
	if a["LABX9WP10A0001"] != "C2" || a["LABX9SC03A0001"] != "C2" || b["LABX9WP10B0002"] != "C2" {
		t.Fatalf("op-a %v, op-b %v", a, b)
	}
	if _, ok := a["LABX9SC08C0003"]; ok {
		t.Fatal("a Remote ID-only aircraft is bound to an operator client")
	}
	seen := map[string]bool{}
	for _, c := range aircraft(ss) {
		if seen[c.serial] {
			t.Fatalf("%s registered twice", c.serial)
		}
		seen[c.serial] = true
	}
	if !seen["LABX9SC08C0003"] || len(receivers(ss)) != 1 {
		t.Fatalf("aircraft %v receivers %v", seen, receivers(ss))
	}
}

// The zones file holds the named scenarios' zones only, and naming a
// scenario that is not loaded or has no zones is refused.
func TestZonesDoc(t *testing.T) {
	ss, lab := scenarios(t)
	b, err := zonesDoc(ss, []string{"sc-03-zone-entry-exit"}, lab)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "SC03Z") || strings.Contains(string(b), "SC08Z") {
		t.Fatalf("%s", b)
	}
	if _, err := zonesDoc(ss, []string{"nope"}, lab); err == nil {
		t.Fatal("an unknown scenario was accepted")
	}
	if _, err := zonesDoc(ss, []string{"ussp-wp10-conformance"}, lab); err == nil {
		t.Fatal("a scenario without zones was accepted")
	}
}

// --zones-away-north-m takes a scenario's zones off the area (bf8229e):
// the same zones, under the same identifiers and limits (so each import
// is a newer version that supersedes the one in force), placed that far
// north, and the lab the run goes on with is left where it was.
func TestZonesAwayAreTheSameZonesFarNorth(t *testing.T) {
	ss, lab := scenarios(t)
	origin := lab.Origin
	type zone struct {
		Identifier string `json:"identifier"`
		Geometry   []struct {
			Lower      float64 `json:"lowerLimit"`
			LowerRef   string  `json:"lowerVerticalReference"`
			Upper      float64 `json:"upperLimit"`
			UpperRef   string  `json:"upperVerticalReference"`
			Projection struct {
				Coordinates [][][2]float64 `json:"coordinates"`
			} `json:"horizontalProjection"`
		} `json:"geometry"`
	}
	read := func(lab *scenario.Lab) []zone {
		t.Helper()
		b, err := zonesDoc(ss, []string{"sc-03-zone-entry-exit", "authority-sc08-rid-switch"}, lab)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Features []zone `json:"features"`
		}
		if err := json.Unmarshal(b, &doc); err != nil || len(doc.Features) != 2 {
			t.Fatalf("%v %s", err, b)
		}
		return doc.Features
	}
	if zonesLab(lab, 0) != lab {
		t.Fatal("no distance moved the zones")
	}
	here := read(lab)
	const awayM = 60000
	away := read(zonesLab(lab, awayM))
	if lab.Origin != origin {
		t.Fatalf("the lab's origin moved: %+v, was %+v", lab.Origin, origin)
	}
	for i := range here {
		h, a := here[i], away[i]
		if len(h.Geometry) != 1 || len(a.Geometry) != 1 {
			t.Fatalf("zone %d: %d volumes away, %d here", i, len(a.Geometry), len(h.Geometry))
		}
		hv, av := h.Geometry[0], a.Geometry[0]
		if h.Identifier != a.Identifier || hv.Lower != av.Lower || hv.LowerRef != av.LowerRef || hv.Upper != av.Upper || hv.UpperRef != av.UpperRef {
			t.Fatalf("zone %d changed more than its place: %+v against %+v", i, a, h)
		}
		ring, ringAway := hv.Projection.Coordinates[0], av.Projection.Coordinates[0]
		if len(ring) != len(ringAway) {
			t.Fatalf("zone %s: %d vertices away, %d here", h.Identifier, len(ringAway), len(ring))
		}
		for k := range ring {
			// 60 km north is about 0.54 degrees of latitude; the
			// longitude stays within the metres a meridian converges.
			dLat, dLon := ringAway[k][1]-ring[k][1], ringAway[k][0]-ring[k][0]
			if dLat < 0.53 || dLat > 0.55 || dLon < -0.001 || dLon > 0.001 {
				t.Fatalf("zone %s vertex %d moved %.5f deg north, %.5f deg east", h.Identifier, k, dLat, dLon)
			}
		}
	}
}

// The U-space airspace is a closed square about its centre, USPACE,
// in the deployment's country.
func TestUSpaceFeature(t *testing.T) {
	_, lab := scenarios(t)
	f, err := uspaceFeature(lab, "LABUSP1", "GEO", 1000, scenario.Offset{NorthM: 60000})
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Geometry struct {
			Coordinates [][][2]float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			Identifier, Country, Type string
		} `json:"properties"`
	}
	if err := json.Unmarshal(f, &v); err != nil {
		t.Fatal(err)
	}
	ring := v.Geometry.Coordinates[0]
	if len(ring) != 5 || ring[0] != ring[4] || v.Properties.Type != "USPACE" || v.Properties.Country != "GEO" {
		t.Fatalf("%s", f)
	}
	if ring[0][1] < lab.Origin.LatDeg+0.5 {
		t.Fatalf("the square is not 60 km north: %v", ring[0])
	}
	if _, err := uspaceFeature(lab, "X", "", 1000, scenario.Offset{}); err == nil {
		t.Fatal("no country was accepted")
	}
}

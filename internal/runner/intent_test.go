package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// An intent of boxes is filed as one outline_polygon volume per box,
// four corners placed by the lab's origin and not closed (F3548
// Polygon), each with the band; a circle intent stays one circle.
func TestIntentRequestFilesBoxesAsPolygons(t *testing.T) {
	lab, err := scenario.LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	s, err := scenario.Load("../../scenarios/sc-02-head-on-and-short-return.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r := &run{sc: s, lab: lab, geo: geoidx.Constant(15.9), t0: time.Unix(1_790_000_000, 0)}
	req, err := r.intentRequest(s.AircraftByName("a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Volumes) != 2 {
		t.Fatalf("%d volumes, want one per box", len(req.Volumes))
	}
	box := s.AircraftByName("a").Operator.Intent.Boxes[0]
	v := req.Volumes[0].Volume
	if v.OutlineCircle != nil || v.OutlinePolygon == nil || len(v.OutlinePolygon.Vertices) != 4 {
		t.Fatalf("volume 0: %+v", v)
	}
	sw := lab.At(scenario.Offset{NorthM: box.SouthM, EastM: box.WestM})
	ne := lab.At(scenario.Offset{NorthM: box.NorthM, EastM: box.EastM})
	p := v.OutlinePolygon.Vertices
	if p[0].Lat != sw.LatDeg || p[0].Lng != sw.LonDeg || p[2].Lat != ne.LatDeg || p[2].Lng != ne.LonDeg || p[3] == p[0] {
		t.Fatalf("corners %+v, want %v first and %v third", p, sw, ne)
	}
	home := lab.Home(1).AltAMSLM
	if v.AltitudeLower.Value != home-20+15.9 || v.AltitudeUpper.Value != home+120+15.9 || v.AltitudeLower.Reference != "W84" {
		t.Fatalf("band %+v %+v", v.AltitudeLower, v.AltitudeUpper)
	}
	b, err := json.Marshal(req.Volumes)
	if err != nil || strings.Contains(string(b), "outline_circle") {
		t.Fatalf("%v %s", err, b)
	}
	s1, err := scenario.Load("../../scenarios/sc-03-zone-entry-exit.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r.sc = s1
	req, err = r.intentRequest(s1.AircraftByName("a"))
	if err != nil || len(req.Volumes) != 1 || req.Volumes[0].Volume.OutlineCircle == nil || req.Volumes[0].Volume.OutlinePolygon != nil {
		t.Fatalf("circle intent: %v %+v", err, req.Volumes)
	}
}

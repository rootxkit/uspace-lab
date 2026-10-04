package basemap

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestRepositoryRegionsPlan(t *testing.T) {
	r, err := LoadRegions("../../basemap/regions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Cities) != 4 {
		t.Fatalf("%d cities, ui Q4 names four", len(r.Cities))
	}
	plan := r.PlanBundle()
	if plan[0].Name != "country" || plan[0].MinZoom != 0 || plan[0].MaxZoom != r.Country.MaxZoom || *plan[0].BBox != r.Country.Bounds {
		t.Errorf("first extract %+v", plan[0])
	}
	if got, want := len(plan), 1+r.MaxZoom()-r.Country.MaxZoom; got != want {
		t.Fatalf("%d extracts, want %d (the country, then one per zoom above it)", got, want)
	}
	for i, e := range plan[1:] {
		z := r.Country.MaxZoom + 1 + i
		if e.MinZoom != z || e.MaxZoom != z || e.BBox != nil || len(e.Region) != 4 {
			t.Errorf("extract %d: %+v, want z%d over the four city boxes", i+1, e, z)
		}
	}
	sb := r.PlanStorybook()
	if len(sb) != 1 || sb[0].MinZoom != 0 || sb[0].MaxZoom != r.Storybook.MaxZoom || *sb[0].BBox != r.Storybook.Bounds {
		t.Errorf("storybook plan %+v", sb)
	}
}

// Cities with different max zooms: each zoom above the country gets the
// boxes that reach it, so the extracts stay disjoint by zoom.
func TestPlanPerCityMaxZoom(t *testing.T) {
	r := &Regions{
		Country: Area{Name: "c", Bounds: Box{40, 41, 47, 44}, MaxZoom: 10},
		Cities: []Area{
			{Name: "a", Bounds: Box{44, 41.5, 45, 42}, MaxZoom: 13},
			{Name: "b", Bounds: Box{41, 41.5, 42, 42}, MaxZoom: 11},
		},
		Storybook: Area{Name: "s", Bounds: Box{44.7, 41.6, 44.8, 41.7}, MaxZoom: 14},
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
	plan := r.PlanBundle()
	got := []int{}
	for _, e := range plan[1:] {
		got = append(got, len(e.Region))
	}
	if !slices.Equal(got, []int{2, 1, 1}) {
		t.Errorf("boxes per zoom 11..13 = %v, want [2 1 1]", got)
	}
}

func TestRegionsCheckRefuses(t *testing.T) {
	base := func() *Regions {
		return &Regions{
			Country:   Area{Name: "c", Bounds: Box{40, 41, 47, 44}, MaxZoom: 12},
			Cities:    []Area{{Name: "a", Bounds: Box{44, 41.5, 45, 42}, MaxZoom: 15}},
			Storybook: Area{Name: "s", Bounds: Box{44.7, 41.6, 44.8, 41.7}, MaxZoom: 14},
		}
	}
	if err := base().Check(); err != nil {
		t.Fatalf("the base policy is refused: %v", err)
	}
	cases := map[string]struct {
		f    func(r *Regions)
		want string
	}{
		"city outside":      {func(r *Regions) { r.Cities[0].Bounds = Box{30, 41.5, 31, 42} }, "not inside country"},
		"city not deeper":   {func(r *Regions) { r.Cities[0].MaxZoom = 12 }, "not above country.max_zoom"},
		"inverted box":      {func(r *Regions) { r.Country.Bounds = Box{47, 41, 40, 44} }, "min must be below max"},
		"latitude":          {func(r *Regions) { r.Storybook.Bounds = Box{0, 80, 1, 89} }, "Web Mercator"},
		"no cities":         {func(r *Regions) { r.Cities = nil }, "cities: none"},
		"duplicate":         {func(r *Regions) { r.Cities = append(r.Cities, r.Cities[0]) }, "twice"},
		"zoom out of range": {func(r *Regions) { r.Storybook.MaxZoom = 30 }, "outside 0..24"},
		"unnamed":           {func(r *Regions) { r.Country.Name = "" }, "country.name"},
	}
	for name, c := range cases {
		r := base()
		c.f(r)
		err := r.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestLoadRegionsRefusesUnknownKeys(t *testing.T) {
	b, err := os.ReadFile("testdata/regions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p := t.TempDir() + "/r.yaml"
	if err := os.WriteFile(p, append(b, []byte("extra: 1\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegions(p); err == nil {
		t.Error("an unknown key was accepted")
	}
	if _, err := LoadRegions("testdata/regions.yaml"); err != nil {
		t.Errorf("the fixture policy: %v", err)
	}
}

func TestRegionGeoJSON(t *testing.T) {
	b, err := RegionGeoJSON([]Box{{1, 2, 3, 4}, {5, 6, 7, 8}})
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Type        string
		Coordinates [][][][2]float64
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.Type != "MultiPolygon" || len(g.Coordinates) != 2 {
		t.Fatalf("%s", b)
	}
	ring := g.Coordinates[0][0]
	if len(ring) != 5 || ring[0] != ring[4] || ring[0] != [2]float64{1, 2} || ring[2] != [2]float64{3, 4} {
		t.Errorf("ring %v", ring)
	}
	// Counter-clockwise (RFC 7946): positive shoelace area.
	area := 0.0
	for i := 0; i < 4; i++ {
		area += ring[i][0]*ring[i+1][1] - ring[i+1][0]*ring[i][1]
	}
	if area <= 0 {
		t.Errorf("ring is clockwise: %v", ring)
	}
}

func TestBoxString(t *testing.T) {
	if s := (Box{39.9, 41, 46.8, 43.6}).String(); s != "39.9,41,46.8,43.6" {
		t.Errorf("%q", s)
	}
	a := Box{0, 0, 1, 1}
	if a.Overlaps(Box{1, 0, 2, 1}) {
		t.Error("boxes sharing only an edge overlap")
	}
	if !a.Overlaps(Box{0.5, 0.5, 2, 2}) {
		t.Error("overlapping boxes do not overlap")
	}
}

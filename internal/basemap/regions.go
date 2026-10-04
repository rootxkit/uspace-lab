package basemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// Box is a WGS84 bounding box in the TileJSON order of SOURCE.json and
// of `pmtiles extract --bbox`: min_lon, min_lat, max_lon, max_lat.
type Box [4]float64

// MinLon and the other accessors name the corners.
func (b Box) MinLon() float64 { return b[0] }

// MinLat is the southern edge.
func (b Box) MinLat() float64 { return b[1] }

// MaxLon is the eastern edge.
func (b Box) MaxLon() float64 { return b[2] }

// MaxLat is the northern edge.
func (b Box) MaxLat() float64 { return b[3] }

// String is the --bbox argument of pmtiles extract.
func (b Box) String() string {
	parts := make([]string, 4)
	for i, v := range b {
		parts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.Join(parts, ",")
}

// Contains reports whether o lies inside b (edges included).
func (b Box) Contains(o Box) bool {
	return o.MinLon() >= b.MinLon() && o.MaxLon() <= b.MaxLon() &&
		o.MinLat() >= b.MinLat() && o.MaxLat() <= b.MaxLat()
}

// Overlaps reports whether b and o share an area of positive size:
// boxes that only touch along an edge do not overlap.
func (b Box) Overlaps(o Box) bool {
	return b.MinLon() < o.MaxLon() && o.MinLon() < b.MaxLon() &&
		b.MinLat() < o.MaxLat() && o.MinLat() < b.MaxLat()
}

// Centre is the middle of the box.
func (b Box) Centre() (lon, lat float64) {
	return (b.MinLon() + b.MaxLon()) / 2, (b.MinLat() + b.MaxLat()) / 2
}

// maxMercatorLat is where the Web Mercator tile grid ends.
var maxMercatorLat = math.Atan(math.Sinh(math.Pi)) * 180 / math.Pi

func (b Box) check(field string) error {
	for i, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s[%d]: not a number", field, i)
		}
	}
	if b.MinLon() < -180 || b.MaxLon() > 180 {
		return fmt.Errorf("%s: longitude outside -180..180", field)
	}
	if b.MinLat() < -maxMercatorLat || b.MaxLat() > maxMercatorLat {
		return fmt.Errorf("%s: latitude outside the Web Mercator grid", field)
	}
	if b.MinLon() >= b.MaxLon() || b.MinLat() >= b.MaxLat() {
		return fmt.Errorf("%s: min must be below max (min_lon, min_lat, max_lon, max_lat)", field)
	}
	return nil
}

// Area is one named box with the highest zoom kept inside it.
type Area struct {
	Name    string `yaml:"name" json:"name"`
	Bounds  Box    `yaml:"bounds" json:"bounds"`
	MaxZoom int    `yaml:"max_zoom" json:"max_zoom"`
}

// Regions is basemap/regions.yaml: the countrywide extract, the city
// boxes kept at higher zooms, and the Storybook extract.
type Regions struct {
	Country   Area   `yaml:"country" json:"country"`
	Cities    []Area `yaml:"cities" json:"cities"`
	Storybook Area   `yaml:"storybook" json:"storybook"`
}

// MaxSourceZoom bounds every max_zoom: MapLibre renders no zoom above
// 24. It is a sanity bound, not the policy (regions.yaml is); whether
// the source has tiles that deep is the extract's business.
const MaxSourceZoom = 24

// LoadRegions reads and checks a regions file. Unknown keys are refused.
func LoadRegions(path string) (*Regions, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var r Regions
	if err := yaml.UnmarshalWithOptions(b, &r, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := r.Check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// Check validates the policy: boxes well formed, every city inside the
// country with a higher max zoom, names unique.
func (r *Regions) Check() error {
	var errs []error
	checkArea := func(field string, a *Area) {
		if a.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name: missing", field))
		}
		if err := a.Bounds.check(field + ".bounds"); err != nil {
			errs = append(errs, err)
		}
		if a.MaxZoom < 0 || a.MaxZoom > MaxSourceZoom {
			errs = append(errs, fmt.Errorf("%s.max_zoom: %d outside 0..%d", field, a.MaxZoom, MaxSourceZoom))
		}
	}
	checkArea("country", &r.Country)
	checkArea("storybook", &r.Storybook)
	if len(r.Cities) == 0 {
		errs = append(errs, errors.New("cities: none"))
	}
	seen := map[string]bool{}
	for i := range r.Cities {
		c := &r.Cities[i]
		field := fmt.Sprintf("cities[%d]", i)
		checkArea(field, c)
		if seen[c.Name] {
			errs = append(errs, fmt.Errorf("%s.name: %q twice", field, c.Name))
		}
		seen[c.Name] = true
		if !r.Country.Bounds.Contains(c.Bounds) {
			errs = append(errs, fmt.Errorf("%s.bounds: not inside country.bounds", field))
		}
		if c.MaxZoom <= r.Country.MaxZoom {
			errs = append(errs, fmt.Errorf("%s.max_zoom: %d is not above country.max_zoom %d", field, c.MaxZoom, r.Country.MaxZoom))
		}
	}
	return errors.Join(errs...)
}

// MaxZoom is the highest zoom of the bundle.
func (r *Regions) MaxZoom() int {
	z := r.Country.MaxZoom
	for _, c := range r.Cities {
		z = max(z, c.MaxZoom)
	}
	return z
}

// Extract is one `pmtiles extract` run: a zoom range over a bbox or a
// GeoJSON region file. The extracts of a plan are disjoint (no tile in
// two of them), which is what `pmtiles merge` requires.
type Extract struct {
	Name    string
	MinZoom int
	MaxZoom int
	// Exactly one of BBox and Region is set.
	BBox   *Box
	Region []Box // written as a GeoJSON MultiPolygon
}

// PlanBundle is the deployment bundle: the country from zoom 0 to its
// max zoom over its bounds, then one extract per zoom above it over the
// union of the city boxes that reach that zoom. go-pmtiles extract has
// one max zoom per run, so the per-region policy is several extracts
// merged; one extract per zoom keeps them disjoint even where two city
// boxes share a tile.
func (r *Regions) PlanBundle() []Extract {
	b := r.Country.Bounds
	plan := []Extract{{Name: "country", MinZoom: 0, MaxZoom: r.Country.MaxZoom, BBox: &b}}
	for z := r.Country.MaxZoom + 1; z <= r.MaxZoom(); z++ {
		var boxes []Box
		for _, c := range r.Cities {
			if c.MaxZoom >= z {
				boxes = append(boxes, c.Bounds)
			}
		}
		plan = append(plan, Extract{Name: fmt.Sprintf("cities-z%d", z), MinZoom: z, MaxZoom: z, Region: boxes})
	}
	return plan
}

// PlanStorybook is the Tbilisi-only extract: one run, zoom 0 to its max.
func (r *Regions) PlanStorybook() []Extract {
	b := r.Storybook.Bounds
	return []Extract{{Name: "storybook", MinZoom: 0, MaxZoom: r.Storybook.MaxZoom, BBox: &b}}
}

// RegionGeoJSON is the MultiPolygon of the boxes, each ring closed and
// counter-clockwise (RFC 7946 §3.1.6).
func RegionGeoJSON(boxes []Box) ([]byte, error) {
	polys := make([][][][2]float64, 0, len(boxes))
	for _, b := range boxes {
		ring := [][2]float64{
			{b.MinLon(), b.MinLat()}, {b.MaxLon(), b.MinLat()},
			{b.MaxLon(), b.MaxLat()}, {b.MinLon(), b.MaxLat()},
			{b.MinLon(), b.MinLat()},
		}
		polys = append(polys, [][][2]float64{ring})
	}
	return json.Marshal(map[string]any{"type": "MultiPolygon", "coordinates": polys})
}

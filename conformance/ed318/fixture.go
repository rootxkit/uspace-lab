package ed318

import (
	"encoding/json"
	"math"
	"time"
)

// FixtureArea is where the publication fixture lies and how high its
// zones reach (conformance/policy.yaml ed318_fixture).
type FixtureArea struct {
	CentreLatDeg float64
	CentreLonDeg float64
	HalfSideM    float64
	UpperAGLM    float64
	UpperAMSLFt  float64
}

// metresPerDegLat is the length of one degree of latitude near the
// fixture (WGS84 mean); the fixture only needs two squares that do not
// overlap, not a survey.
const metresPerDegLat = 111320.0

// Fixture is the ED-318 zones collection the publication tests publish:
// two PROHIBITED test zones side by side, active applying from an hour
// ago to tomorrow (AGL, metres) and expired having ended yesterday (AMSL,
// feet), so the applicability filters have one of each to keep, drop or
// annotate. ids are the two identifiers (at most 7 characters, ED-318).
func Fixture(a FixtureArea, now time.Time, active, expired string) ([]byte, error) {
	now = now.UTC().Truncate(time.Second)
	dLat := a.HalfSideM / metresPerDegLat
	dLon := a.HalfSideM / (metresPerDegLat * math.Cos(a.CentreLatDeg*math.Pi/180))
	square := func(lonOffset float64) [][][2]float64 {
		lon0 := a.CentreLonDeg + lonOffset - dLon
		lon1 := a.CentreLonDeg + lonOffset + dLon
		lat0, lat1 := a.CentreLatDeg-dLat, a.CentreLatDeg+dLat
		return [][][2]float64{{{lon0, lat0}, {lon1, lat0}, {lon1, lat1}, {lon0, lat1}, {lon0, lat0}}}
	}
	zone := func(id, name string, ring [][][2]float64, layer map[string]any, from, to time.Time) map[string]any {
		return map[string]any{
			"type": "Feature",
			"id":   id,
			"geometry": map[string]any{
				"type":        "Polygon",
				"coordinates": ring,
				"layer":       layer,
			},
			"properties": map[string]any{
				"identifier": id,
				"country":    "GEO",
				"name":       []map[string]string{{"text": name, "lang": "en-GB"}},
				"type":       "PROHIBITED",
				"variant":    "COMMON",
				"reason":     []string{"SENSITIVE"},
				"message":    []map[string]string{{"text": "Conformance test zone; not a real restriction", "lang": "en-GB"}},
				"limitedApplicability": []map[string]string{{
					"startDateTime": from.Format(time.RFC3339),
					"endDateTime":   to.Format(time.RFC3339),
				}},
				"zoneAuthority": []map[string]any{{
					"name":    []map[string]string{{"text": "uspace-lab conformance suite", "lang": "en-GB"}},
					"purpose": "INFORMATION",
				}},
			},
		}
	}
	fc := map[string]any{
		"type": "FeatureCollection",
		"name": "uspace-lab conformance fixture",
		"metadata": map[string]any{
			"issued":   now.Format(time.RFC3339),
			"provider": []map[string]string{{"text": "uspace-lab conformance suite", "lang": "en-GB"}},
		},
		"features": []any{
			zone(active, "Conformance zone applying now", square(+2*dLon),
				map[string]any{"upper": a.UpperAGLM, "upperReference": "AGL", "lower": 0, "lowerReference": "AGL", "uom": "m"},
				now.Add(-time.Hour), now.Add(24*time.Hour)),
			zone(expired, "Conformance zone that ended yesterday", square(-2*dLon),
				map[string]any{"upper": a.UpperAMSLFt, "upperReference": "AMSL", "lower": 0, "lowerReference": "AGL", "uom": "ft"},
				now.Add(-48*time.Hour), now.Add(-24*time.Hour)),
		},
	}
	return json.Marshal(fc)
}

// InvalidFixture is Fixture with the first zone's type removed: a body
// the CISP must refuse whole, naming the field.
func InvalidFixture(a FixtureArea, now time.Time, active, expired string) ([]byte, error) {
	b, err := Fixture(a, now, active, expired)
	if err != nil {
		return nil, err
	}
	var fc map[string]any
	if err := json.Unmarshal(b, &fc); err != nil {
		return nil, err
	}
	f0 := fc["features"].([]any)[0].(map[string]any)
	delete(f0["properties"].(map[string]any), "type")
	return json.Marshal(fc)
}

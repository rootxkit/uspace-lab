// Package geoidx gives the simulators and the reference target the geoid
// undulation N they need for HAE = AMSL + N (LESSONS R-16): a
// GeographicLib grid loaded through uspace-core's geoid package, or a
// constant N for a test area where it is flat enough (SC-12 used 15.9 m).
// The receiver simulator and the ingest it feeds must use the same one,
// so it is configuration, never a literal.
package geoidx

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"
)

// Constant is a geoid with the same undulation everywhere.
type Constant float64

// UndulationM returns N.
func (c Constant) UndulationM(core.LatLon) (float64, error) { return float64(c), nil }

// Load reads a spec: "constant:<metres>" or the path of a GeographicLib
// .pgm grid. An empty spec is refused: no geoid is a degraded state the
// caller must name, not a default.
func Load(spec string) (geoid.Undulator, string, error) {
	if spec == "" {
		return nil, "", core.Fieldf("geoid", "not configured")
	}
	if v, ok := strings.CutPrefix(spec, "constant:"); ok {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || !core.IsFinite(n) || n < -120 || n > 120 {
			return nil, "", core.Fieldf("geoid", "%q is not an undulation in metres", v)
		}
		return Constant(n), fmt.Sprintf("constant N = %g m", n), nil
	}
	g, err := geoid.Load(spec)
	if err != nil {
		return nil, "", fmt.Errorf("geoid %s: %w", spec, err)
	}
	desc := g.Description()
	return g, desc, nil
}

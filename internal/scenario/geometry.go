package scenario

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// metresPerDegLonEquator is run_sitl.sh's constant (2 pi a / 360 for the
// WGS84 semi-major axis). Homes are computed with the script's own
// spherical formula so the synthetic fleet and the SITL fleet start at the
// same points to the bit; nothing else uses it.
const metresPerDegLonEquator = 111319.49

// Lab is sim/sitl.env: where the fleet starts and how it is numbered.
type Lab struct {
	Origin       vehicle.Home
	HeadingDeg   float64
	SpacingM     float64
	SysidBase    int
	OutPortBase  int
	FlyPortBase  int
	InstanceBase int
	Path         string
}

// LoadLab reads a sitl.env file (KEY=VALUE lines, # comments), the same
// file run_sitl.sh sources. SITL_HOME is required; it has no default
// anywhere (INV-03).
func LoadLab(path string) (*Lab, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own configuration file
	if err != nil {
		return nil, fmt.Errorf("sitl.env: %w (copy sim/sitl.env.example)", err)
	}
	defer func() { _ = f.Close() }()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("sitl.env: %w", err)
	}
	return labFrom(vals, path)
}

func labFrom(vals map[string]string, path string) (*Lab, error) {
	home := vals["SITL_HOME"]
	if home == "" {
		return nil, core.Fieldf("SITL_HOME", "not set in %s; coordinates are never defaulted", path)
	}
	parts := strings.Split(home, ",")
	if len(parts) != 4 {
		return nil, core.Fieldf("SITL_HOME", "want lat,lon,alt_amsl_m,heading_deg")
	}
	var nums [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || !core.IsFinite(v) {
			return nil, core.Fieldf("SITL_HOME", "%q is not a number", p)
		}
		nums[i] = v
	}
	if nums[0] < -89 || nums[0] > 89 || nums[1] < -180 || nums[1] > 180 {
		return nil, core.Fieldf("SITL_HOME", "position out of range")
	}
	l := &Lab{
		Origin:     vehicle.Home{LatDeg: nums[0], LonDeg: nums[1], AltAMSLM: nums[2]},
		HeadingDeg: nums[3],
		Path:       path,
	}
	num := func(key string, def float64) (float64, error) {
		s, ok := vals[key]
		if !ok || s == "" {
			return def, nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || !core.IsFinite(v) || v < 0 {
			return 0, core.Fieldf(key, "%q is not a non-negative number", s)
		}
		return v, nil
	}
	// The defaults are run_sitl.sh's (ports and numbering, not places).
	var err error
	if l.SpacingM, err = num("SITL_SPACING_M", 25); err != nil {
		return nil, err
	}
	ints := []struct {
		key string
		def float64
		dst *int
	}{
		{"SITL_SYSID_BASE", 1, &l.SysidBase},
		{"SITL_OUT_PORT_BASE", 14560, &l.OutPortBase},
		{"SITL_FLY_PORT_BASE", 14660, &l.FlyPortBase},
		{"SITL_INSTANCE_BASE", 0, &l.InstanceBase},
	}
	for _, it := range ints {
		v, err := num(it.key, it.def)
		if err != nil {
			return nil, err
		}
		*it.dst = int(v)
	}
	return l, nil
}

// Index is the SITL instance index of a sysid (run_sitl.sh: sysid =
// SITL_SYSID_BASE + i).
func (l *Lab) Index(sysid int) int { return sysid - l.SysidBase }

// Home is where the vehicle with sysid starts: run_sitl.sh's spacing due
// east of the origin, with the script's formula.
func (l *Lab) Home(sysid int) vehicle.Home {
	i := float64(l.Index(sysid))
	lon := l.Origin.LonDeg + (i*l.SpacingM)/(metresPerDegLonEquator*math.Cos(l.Origin.LatDeg*math.Pi/180))
	// run_sitl.sh prints the longitude with %.7f; so does SITL's home.
	lon = math.Round(lon*1e7) / 1e7
	return vehicle.Home{LatDeg: l.Origin.LatDeg, LonDeg: lon, AltAMSLM: l.Origin.AltAMSLM}
}

// OutPort and FlyPort are the vehicle's reader and harness ports.
func (l *Lab) OutPort(sysid int) int { return l.OutPortBase + l.Index(sysid) }

// FlyPort is the harness port of the vehicle with sysid.
func (l *Lab) FlyPort(sysid int) int { return l.FlyPortBase + l.Index(sysid) }

// At is the position of an offset from the origin (geodesic, uspace-core).
func (l *Lab) At(o Offset) core.LatLon {
	p := core.LatLon{LatDeg: l.Origin.LatDeg, LonDeg: l.Origin.LonDeg}
	return Move(p, o)
}

// Move is p moved north then east by the offset's metres.
func Move(p core.LatLon, o Offset) core.LatLon {
	switch {
	case o.NorthM > 0:
		p = geodesy.Destination(p, 0, o.NorthM)
	case o.NorthM < 0:
		p = geodesy.Destination(p, 180, -o.NorthM)
	}
	switch {
	case o.EastM > 0:
		p = geodesy.Destination(p, 90, o.EastM)
	case o.EastM < 0:
		p = geodesy.Destination(p, 270, -o.EastM)
	}
	return p
}

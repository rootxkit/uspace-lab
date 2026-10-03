package vehicle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/rootxkit/uspace-core/core"
)

// Schema is the name every line carries.
const Schema = "sim/vehicle/v1"

// TimeLayout is the line's ts: RFC 3339 UTC with milliseconds and Z.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// MaxLineBytes bounds one line (E-10). A line is about 450 bytes.
const MaxLineBytes = 4096

// Status values (HEARTBEAT, sim/mav_reader.py).
const (
	StatusGround    = "ground"
	StatusAirborne  = "airborne"
	StatusEmergency = "emergency"
)

// Sample is one sim/vehicle/v1 line (sim/schema/vehicle-v1.json). The
// MAVLink field behind each member is the table in sim/mav_reader.py.
type Sample struct {
	Schema         string   `json:"schema"`
	Sysid          int      `json:"sysid"`
	TS             *string  `json:"ts"`
	TimeBootMS     uint32   `json:"time_boot_ms"`
	LatDeg         float64  `json:"lat_deg"`
	LonDeg         float64  `json:"lon_deg"`
	AltAMSLM       float64  `json:"alt_amsl_m"`
	AltHAEM        *float64 `json:"alt_hae_m"`
	AltPressureM   *float64 `json:"alt_pressure_m"`
	HeightTakeoffM float64  `json:"height_takeoff_m"`
	SpeedMS        float64  `json:"speed_ms"`
	TrackDeg       *float64 `json:"track_deg"`
	VSpeedMS       float64  `json:"vspeed_ms"`
	VNMS           float64  `json:"vn_ms"`
	VEMS           float64  `json:"ve_ms"`
	Armed          bool     `json:"armed"`
	Status         string   `json:"status"`
	FixOK          bool     `json:"fix_ok"`
	AltInvalid     bool     `json:"alt_invalid"`
}

// requiredKeys are the members a line must carry, null or not.
var requiredKeys = []string{
	"schema", "sysid", "ts", "time_boot_ms", "lat_deg", "lon_deg", "alt_amsl_m", "alt_hae_m",
	"alt_pressure_m", "height_takeoff_m", "speed_ms", "track_deg", "vspeed_ms", "vn_ms", "ve_ms",
	"armed", "status", "fix_ok", "alt_invalid",
}

// Time is the vehicle's UTC for the sample; ok false when the vehicle had
// sent no UTC yet (ts null, R-16).
func (s *Sample) Time() (t time.Time, ok bool) {
	if s.TS == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(TimeLayout, *s.TS)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Flying reports whether the vehicle says it is flying: armed, or an
// emergency while armed.
func (s *Sample) Flying() bool { return s.Armed }

// Pos is the sample's position.
func (s *Sample) Pos() core.LatLon { return core.LatLon{LatDeg: s.LatDeg, LonDeg: s.LonDeg} }

// FormatTime formats t as a line's ts.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// MarshalLine is the sample as one NDJSON line with its newline.
func (s *Sample) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("vehicle: marshal sysid %d: %w", s.Sysid, err)
	}
	return append(b, '\n'), nil
}

// ParseLine decodes and checks one line. It refuses an oversized line,
// unknown or missing members and every value the schema refuses, with a
// *core.FieldError; it never panics (FuzzParseLine).
func ParseLine(line []byte) (Sample, error) {
	line = bytes.TrimSpace(line)
	if len(line) > MaxLineBytes {
		return Sample{}, core.Fieldf("line", "longer than %d bytes", MaxLineBytes)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(line, &keys); err != nil {
		return Sample{}, core.Fieldf("line", "not a JSON object")
	}
	for _, k := range requiredKeys {
		if _, ok := keys[k]; !ok {
			return Sample{}, core.Fieldf(k, "missing")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var s Sample
	if err := dec.Decode(&s); err != nil {
		return Sample{}, core.Fieldf("line", "not sim/vehicle/v1: %s", short(err.Error()))
	}
	if err := s.Validate(); err != nil {
		return Sample{}, err
	}
	return s, nil
}

// Validate checks the values against sim/schema/vehicle-v1.json.
func (s *Sample) Validate() error {
	if s.Schema != Schema {
		return core.Fieldf("schema", "is %q, want %q", short(s.Schema), Schema)
	}
	if s.Sysid < 1 || s.Sysid > 254 {
		return core.Fieldf("sysid", "%d is not 1..254", s.Sysid)
	}
	if s.TS != nil {
		if _, err := time.Parse(TimeLayout, *s.TS); err != nil || len(*s.TS) != len(TimeLayout) {
			return core.Fieldf("ts", "not RFC 3339 UTC with milliseconds and Z")
		}
	}
	checks := []struct {
		name     string
		v        float64
		min, max float64
	}{
		{"lat_deg", s.LatDeg, -90, 90},
		{"lon_deg", s.LonDeg, -180, 180},
		{"alt_amsl_m", s.AltAMSLM, -1000, 20000},
		{"height_takeoff_m", s.HeightTakeoffM, -1000, 20000},
		{"speed_ms", s.SpeedMS, 0, 400},
		{"vspeed_ms", s.VSpeedMS, -400, 400},
		{"vn_ms", s.VNMS, -400, 400},
		{"ve_ms", s.VEMS, -400, 400},
	}
	for _, c := range checks {
		if !core.IsFinite(c.v) || c.v < c.min || c.v > c.max {
			return core.Fieldf(c.name, "%v is not a number in [%v, %v]", c.v, c.min, c.max)
		}
	}
	for name, p := range map[string]*float64{"alt_hae_m": s.AltHAEM, "alt_pressure_m": s.AltPressureM} {
		if p != nil && (!core.IsFinite(*p) || *p < -1000 || *p > 20000) {
			return core.Fieldf(name, "%v is not a number in [-1000, 20000]", *p)
		}
	}
	if s.TrackDeg != nil && (!core.IsFinite(*s.TrackDeg) || *s.TrackDeg < 0 || *s.TrackDeg >= 360) {
		return core.Fieldf("track_deg", "%v is not in [0, 360)", *s.TrackDeg)
	}
	switch s.Status {
	case StatusGround, StatusAirborne, StatusEmergency:
	default:
		return core.Fieldf("status", "%q is not ground, airborne or emergency", short(s.Status))
	}
	return nil
}

func short(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// F returns a pointer to v (for the nullable members).
func F(v float64) *float64 { return &v }

// TrackFrom is the direction of travel of a NED velocity in degrees true,
// or nil below minSpeedMS (as the reader: the direction says nothing
// there).
func TrackFrom(vnMS, veMS, minSpeedMS float64) *float64 {
	if math.Hypot(vnMS, veMS) < minSpeedMS {
		return nil
	}
	d := math.Mod(math.Atan2(veMS, vnMS)*180/math.Pi+360, 360)
	if d >= 360 {
		d = 0
	}
	return &d
}

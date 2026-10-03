package scenario

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/serial"
)

// Format is the scenario format version this package reads.
const Format = "scenario/v1"

// Systems a scenario can address. "peer" is cmd/sim-ussp.
const (
	SystemUSSP      = "ussp"
	SystemAuthority = "authority"
	SystemANSP      = "ansp"
	SystemCISP      = "cisp"
	SystemPeer      = "peer"
)

// Step verbs.
const (
	DoTakeoff = "takeoff" // arm, then take off to alt_rel_m
	DoGoto    = "goto"
	DoHold    = "hold"
	DoLand    = "land"
	DoKnob    = "knob"    // change a simulator's behaviour at at_s
	DoRequest = "request" // one HTTP request to a system (e.g. the ANSP activating a restriction)
)

// Scenario is one scenarios/<id>.yaml (the format: scenarios/README.md).
type Scenario struct {
	Format    string     `yaml:"format" json:"format"`
	ID        string     `yaml:"id" json:"id"`
	Title     string     `yaml:"title" json:"title"`
	Source    string     `yaml:"source" json:"source"`
	Owners    []string   `yaml:"owners" json:"owners"`
	Country   string     `yaml:"country" json:"country,omitempty"`
	Policy    string     `yaml:"policy" json:"policy"`
	Systems   []string   `yaml:"systems" json:"systems"`
	Reference bool       `yaml:"reference" json:"reference"`
	Note      string     `yaml:"note" json:"note,omitempty"`
	DurationS float64    `yaml:"duration_s" json:"duration_s"`
	TailS     float64    `yaml:"tail_s" json:"tail_s"`
	Aircraft  []Aircraft `yaml:"aircraft" json:"aircraft"`
	Receivers []Receiver `yaml:"receivers" json:"receivers,omitempty"`
	Feeds     []Feed     `yaml:"feeds" json:"feeds,omitempty"`
	Zones     []Zone     `yaml:"zones" json:"zones,omitempty"`
	Steps     []Step     `yaml:"steps" json:"steps"`
	Expect    []Expect   `yaml:"expect" json:"expect,omitempty"`
	Never     []Matcher  `yaml:"never" json:"never,omitempty"`
	// ExpectIntents are the decisions the USSP must give the intents the
	// runner files (WP-7: flight authorisation has no alert path).
	ExpectIntents []IntentExpect `yaml:"expect_intents" json:"expect_intents,omitempty"`
	Measure       []string       `yaml:"measure" json:"measure,omitempty"`
	JudgedKinds   []string       `yaml:"judged_kinds" json:"judged_kinds,omitempty"`

	// Dir is the scenario file's directory (for the policy path).
	Dir string `yaml:"-" json:"-"`
	// PolicyDoc is the loaded policy.
	PolicyDoc *Policy `yaml:"-" json:"-"`
}

// Aircraft is one simulated vehicle and how the systems hear it.
type Aircraft struct {
	Name        string    `yaml:"name" json:"name"`
	Sysid       int       `yaml:"sysid" json:"sysid"`
	Serial      string    `yaml:"serial" json:"serial"`
	OperatorReg string    `yaml:"operator_reg" json:"operator_reg"`
	OperatorID  string    `yaml:"operator_id" json:"operator_id,omitempty"`
	Operator    *Operator `yaml:"operator" json:"operator,omitempty"`
	Receivers   []string  `yaml:"receivers" json:"receivers,omitempty"`
	// MarkAltInvalid is S-36 for this vehicle.
	MarkAltInvalid bool `yaml:"mark_alt_invalid" json:"mark_alt_invalid,omitempty"`
}

// Operator says that sim-operator streams the aircraft to a USSP.
type Operator struct {
	System string `yaml:"system" json:"system"`
	// Client names the operator client in the targets file (its token
	// endpoint, id and secret); default "default".
	Client string `yaml:"client" json:"client,omitempty"`
	// Intent files and activates an intent before t0 when set.
	Intent *Intent `yaml:"intent" json:"intent,omitempty"`
	// Transport is ws (default) or batch.
	Transport string `yaml:"transport" json:"transport,omitempty"`
	// OperatorAt is the remote pilot's position (Art. 8(2)(e)); default
	// the aircraft's home.
	OperatorAt *Offset `yaml:"operator_at" json:"operator_at,omitempty"`
	DropRate   float64 `yaml:"drop_rate" json:"drop_rate,omitempty"`
	LatencyS   float64 `yaml:"latency_s" json:"latency_s,omitempty"`
}

// Intent is the operational intent filed for an aircraft: a circle about
// an offset, an AMSL band relative to the aircraft's home ground, and a
// window around the run.
type Intent struct {
	Center         Offset  `yaml:"center" json:"center"`
	RadiusM        float64 `yaml:"radius_m" json:"radius_m"`
	AltLowerRelM   float64 `yaml:"alt_lower_rel_m" json:"alt_lower_rel_m"`
	AltUpperRelM   float64 `yaml:"alt_upper_rel_m" json:"alt_upper_rel_m"`
	StartsBeforeS  float64 `yaml:"starts_before_s" json:"starts_before_s"`
	LastsS         float64 `yaml:"lasts_s" json:"lasts_s"`
	Category       string  `yaml:"category" json:"category"`
	Subcategory    string  `yaml:"subcategory" json:"subcategory,omitempty"`
	ClassLabel     string  `yaml:"class_label" json:"class_label,omitempty"`
	Mode           string  `yaml:"mode" json:"mode"`
	Identification string  `yaml:"identification" json:"identification"`
}

// Receiver is one simulated Remote ID receiver (sim-receiver).
type Receiver struct {
	ID        string  `yaml:"id" json:"id"`
	System    string  `yaml:"system" json:"system"`
	At        Offset  `yaml:"at" json:"at"`
	Transport string  `yaml:"transport" json:"transport"` // pack (BT5, Wi-Fi) or single (BT4)
	HAE       string  `yaml:"hae" json:"hae"`             // geoid (AMSL + N, R-16) or gps (the vehicle's alt_hae_m)
	DropRate  float64 `yaml:"drop_rate" json:"drop_rate,omitempty"`
	LatencyS  float64 `yaml:"latency_s" json:"latency_s,omitempty"`
	Seed      uint64  `yaml:"seed" json:"seed,omitempty"`
	RSSIDBm   float64 `yaml:"rssi_dbm" json:"rssi_dbm,omitempty"`
}

// Feed is a manned-traffic source: sim-ansp-feed or sim-adsb.
type Feed struct {
	ID   string `yaml:"id" json:"id"`
	Kind string `yaml:"kind" json:"kind"` // ansp_stream (sim-ansp-feed) or adsb_file (sim-adsb)
	// Tracks are synthetic manned aircraft flown along offsets; a
	// recording is the alternative (Recording).
	Tracks    []MannedTrack `yaml:"tracks" json:"tracks,omitempty"`
	Recording string        `yaml:"recording" json:"recording,omitempty"`
}

// MannedTrack is one manned aircraft of a feed: a straight leg at a
// pressure altitude.
type MannedTrack struct {
	ICAO24       string  `yaml:"icao24" json:"icao24"`
	Callsign     string  `yaml:"callsign" json:"callsign,omitempty"`
	From         Offset  `yaml:"from" json:"from"`
	To           Offset  `yaml:"to" json:"to"`
	SpeedMS      float64 `yaml:"speed_ms" json:"speed_ms"`
	AltPressureM float64 `yaml:"alt_pressure_m" json:"alt_pressure_m"`
	AltWGS84M    float64 `yaml:"alt_wgs84_m" json:"alt_wgs84_m,omitempty"`
	StartS       float64 `yaml:"start_s" json:"start_s"`
}

// Zone is a zone the scenario needs, as offsets from the origin.
type Zone struct {
	ID     string   `yaml:"id" json:"id"`
	Type   string   `yaml:"type" json:"type"`
	Square *Square  `yaml:"square" json:"square,omitempty"`
	Circle *Circle  `yaml:"circle" json:"circle,omitempty"`
	Lower  Limit    `yaml:"lower" json:"lower"`
	Upper  Limit    `yaml:"upper" json:"upper"`
	Window []string `yaml:"window" json:"window,omitempty"` // [start, end] RFC 3339; empty: permanent
}

// Square is an axis-aligned square of side SideM centred on an offset.
type Square struct {
	Center Offset  `yaml:"center" json:"center"`
	SideM  float64 `yaml:"side_m" json:"side_m"`
}

// Circle is a circle about an offset.
type Circle struct {
	Center  Offset  `yaml:"center" json:"center"`
	RadiusM float64 `yaml:"radius_m" json:"radius_m"`
}

// Limit is a vertical limit with its reference (AGL, AMSL, WGS84).
type Limit struct {
	M   float64 `yaml:"m" json:"m"`
	Ref string  `yaml:"ref" json:"ref"`
}

// Offset is north and east of the origin in metres: no coordinate is
// written in a scenario (INV-03); the origin is sim/sitl.env's SITL_HOME.
type Offset struct {
	NorthM float64 `yaml:"north_m" json:"north_m"`
	EastM  float64 `yaml:"east_m" json:"east_m"`
}

// Step is one thing that happens: a flight step for some aircraft, a
// knob, or a request.
type Step struct {
	ID         string   `yaml:"id" json:"id,omitempty"`
	AtS        *float64 `yaml:"at_s" json:"at_s,omitempty"`
	Aircraft   []string `yaml:"aircraft" json:"aircraft,omitempty"`
	Do         string   `yaml:"do" json:"do"`
	AltRelM    float64  `yaml:"alt_rel_m" json:"alt_rel_m,omitempty"`
	To         *Offset  `yaml:"to" json:"to,omitempty"`
	SpeedMS    float64  `yaml:"speed_ms" json:"speed_ms,omitempty"`
	ToleranceM float64  `yaml:"tolerance_m" json:"tolerance_m,omitempty"`
	ForS       float64  `yaml:"for_s" json:"for_s,omitempty"`
	Knob       *Knob    `yaml:"knob" json:"knob,omitempty"`
	Request    *Request `yaml:"request" json:"request,omitempty"`
}

// Knob changes a simulator at a step's at_s.
type Knob struct {
	// OperatorLink is down (close the WebSocket and queue, 02 F5) or up.
	OperatorLink string `yaml:"operator_link" json:"operator_link,omitempty"`
	// OperatorStream is stop (connected, sending nothing: the USSP's
	// lost link) or start.
	OperatorStream string `yaml:"operator_stream" json:"operator_stream,omitempty"`
	// Receiver is down (buffer, replay as backlog) or up, for Receivers.
	Receiver  string   `yaml:"receiver" json:"receiver,omitempty"`
	Receivers []string `yaml:"receivers" json:"receivers,omitempty"`
	// Feed is live, stale (connected, no samples) or outage (refuse) for
	// Feeds.
	Feed  string   `yaml:"feed" json:"feed,omitempty"`
	Feeds []string `yaml:"feeds" json:"feeds,omitempty"`
	// Serial and Address change a transmitter (SC-10, SC-11).
	Serial  string `yaml:"serial" json:"serial,omitempty"`
	Address string `yaml:"address" json:"address,omitempty"`
}

// Request is one HTTP request to a system, with the targets file's
// credentials for it.
type Request struct {
	System string         `yaml:"system" json:"system"`
	Method string         `yaml:"method" json:"method"`
	Path   string         `yaml:"path" json:"path"`
	Body   map[string]any `yaml:"body" json:"body,omitempty"`
	// Expect is the status the system must answer; default any 2xx.
	Expect int `yaml:"expect" json:"expect,omitempty"`
	// Capture names a JSON member of the answer to keep as ${name} for
	// later requests.
	Capture map[string]string `yaml:"capture" json:"capture,omitempty"`
	// Headers are sent with the request, with the body's substitutions
	// (the ANSP's Idempotency-Key, for one).
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
}

// Matcher selects observed events.
type Matcher struct {
	System   string `yaml:"system" json:"system"`
	Kind     string `yaml:"kind" json:"kind"`
	Aircraft string `yaml:"aircraft" json:"aircraft,omitempty"`
	Peer     string `yaml:"peer" json:"peer,omitempty"`
	// Subject matches a non-aircraft subject (a zone id, a source slug).
	Subject string `yaml:"subject" json:"subject,omitempty"`
}

// Expect is one expected alert: its raise and, when given, its clear.
type Expect struct {
	Name    string `yaml:"name" json:"name"`
	Matcher `yaml:",inline" json:",inline"`
	Raise   Window  `yaml:"raise" json:"raise"`
	Clear   *Window `yaml:"clear" json:"clear,omitempty"`
	// HoldUntil: the alert must not clear before this window opens (SC-01
	// step 2: no clear while the hover lasts).
	HoldUntil *Window `yaml:"hold_until" json:"hold_until,omitempty"`
}

// IntentExpect is the decision and state an aircraft's intent must get.
type IntentExpect struct {
	Aircraft string `yaml:"aircraft" json:"aircraft"`
	Decision string `yaml:"decision" json:"decision"`
	State    string `yaml:"state" json:"state,omitempty"`
}

// Window is [mark + min_s, mark + max_s]. The mark is t0, a step id (the
// moment the vehicle confirmed it), or <id>.start.
type Window struct {
	After  string  `yaml:"after" json:"after"`
	MinS   float64 `yaml:"min_s" json:"min_s"`
	MaxS   float64 `yaml:"max_s" json:"max_s"`
	Reason string  `yaml:"reason" json:"reason,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ArduCopter 4.5.7's landing defaults (read from its source, the SITL
// the lab flies): WPNAV_SPEED_DN 150 cm/s down to LAND_ALT_LOW 1000 cm,
// then LAND_SPEED 50 cm/s; landingMarginS covers the touchdown, the
// disarm and its confirmation by sim/fly.py (measured: 30.6 s from 20 m,
// 37.5 s from 30 m, 57.5 s from 60 m; results/20261003-sitl-reference,
// results/20261004-systems).
const (
	landFastMS     = 1.5
	landSlowMS     = 0.5
	landSlowBelowM = 10.0
	landingMarginS = 6.0
)

// LandS is about how long a SITL vehicle takes to land from alt_rel_m.
func LandS(altRelM float64) float64 {
	slow := math.Min(altRelM, landSlowBelowM)
	fast := math.Max(altRelM-landSlowBelowM, 0)
	return fast/landFastMS + slow/landSlowMS + landingMarginS
}

// Load reads, checks and resolves a scenario and its policy.
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the scenario
	if err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	var s Scenario
	if err := yaml.UnmarshalWithOptions(b, &s, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("scenario %s: %w", path, err)
	}
	s.Dir = filepath.Dir(path)
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("scenario %s: %w", path, err)
	}
	pol, err := LoadPolicy(filepath.Join(s.Dir, s.Policy))
	if err != nil {
		return nil, fmt.Errorf("scenario %s: %w", path, err)
	}
	s.PolicyDoc = pol
	return &s, nil
}

// Validate checks the scenario on its own (the policy is loaded by Load).
//
//nolint:gocyclo // one flat list of checks reads better than helpers
func (s *Scenario) Validate() error {
	if s.Format != Format {
		return core.Fieldf("format", "is %q, want %q", s.Format, Format)
	}
	if !idPattern.MatchString(s.ID) {
		return core.Fieldf("id", "%q is not a slug", s.ID)
	}
	if s.Source == "" || s.Policy == "" || len(s.Owners) == 0 {
		return core.Fieldf("source", "source, owners and policy are required")
	}
	if len(s.Systems) == 0 {
		return core.Fieldf("systems", "name the systems the scenario drives")
	}
	for _, sys := range s.Systems {
		if !knownSystem(sys) {
			return core.Fieldf("systems", "%q is not a system", sys)
		}
	}
	if !(s.DurationS > 0) || s.DurationS > 4*3600 || s.TailS < 0 {
		return core.Fieldf("duration_s", "must be in (0, 4 h]; tail_s >= 0")
	}
	names := map[string]*Aircraft{}
	sysids := map[int]bool{}
	for i := range s.Aircraft {
		a := &s.Aircraft[i]
		f := fmt.Sprintf("aircraft[%d]", i)
		if !idPattern.MatchString(a.Name) || names[a.Name] != nil {
			return core.Fieldf(f+".name", "%q is not a unique slug", a.Name)
		}
		if a.Sysid < 1 || a.Sysid > 254 || sysids[a.Sysid] {
			return core.Fieldf(f+".sysid", "%d is not a unique 1..254", a.Sysid)
		}
		if a.Serial == "" || len(a.Serial) > 20 {
			return core.Fieldf(f+".serial", "1..20 characters (the ODID Basic ID holds 20)")
		}
		if a.Operator != nil {
			if a.Operator.System != SystemUSSP && a.Operator.System != SystemPeer {
				return core.Fieldf(f+".operator.system", "%q: an operator streams to a USSP", a.Operator.System)
			}
			switch a.Operator.Transport {
			case "", "ws", "batch":
			default:
				return core.Fieldf(f+".operator.transport", "ws or batch")
			}
			if a.Operator.DropRate < 0 || a.Operator.DropRate >= 1 || a.Operator.LatencyS < 0 {
				return core.Fieldf(f+".operator", "drop_rate in [0, 1), latency_s >= 0")
			}
			if in := a.Operator.Intent; in != nil {
				if !(in.RadiusM > 0) || in.AltUpperRelM <= in.AltLowerRelM || !(in.LastsS > 0) || in.StartsBeforeS < 0 {
					return core.Fieldf(f+".operator.intent", "radius_m > 0, alt_upper_rel_m > alt_lower_rel_m, lasts_s > 0")
				}
				// The USSP refuses an intent whose serial is not valid for
				// its class (uspace-ussp internal/intent/validate.go,
				// serial.ValidateForClass: CTA-2063-A for C1, C2, C3, C5
				// and C6), and the authority's registry refuses the UAS.
				if err := serial.ValidateForClass(a.Serial, in.ClassLabel); err != nil {
					return core.Fieldf(f+".serial", "%q for class %q: %v", a.Serial, in.ClassLabel, err)
				}
			}
		}
		names[a.Name] = a
		sysids[a.Sysid] = true
	}
	rx := map[string]bool{}
	for i, r := range s.Receivers {
		f := fmt.Sprintf("receivers[%d]", i)
		if !idPattern.MatchString(r.ID) || rx[r.ID] {
			return core.Fieldf(f+".id", "%q is not a unique slug", r.ID)
		}
		if r.System != SystemAuthority {
			return core.Fieldf(f+".system", "a receiver reports to the authority")
		}
		if r.Transport != "pack" && r.Transport != "single" {
			return core.Fieldf(f+".transport", "pack or single")
		}
		if r.HAE != "geoid" && r.HAE != "gps" {
			return core.Fieldf(f+".hae", "geoid or gps")
		}
		if r.DropRate < 0 || r.DropRate >= 1 || r.LatencyS < 0 {
			return core.Fieldf(f, "drop_rate in [0, 1), latency_s >= 0")
		}
		rx[r.ID] = true
	}
	for _, a := range s.Aircraft {
		for _, r := range a.Receivers {
			if !rx[r] {
				return core.Fieldf("aircraft."+a.Name+".receivers", "%q is not a receiver", r)
			}
		}
	}
	feeds := map[string]bool{}
	for i, fd := range s.Feeds {
		f := fmt.Sprintf("feeds[%d]", i)
		if !idPattern.MatchString(fd.ID) || feeds[fd.ID] {
			return core.Fieldf(f+".id", "%q is not a unique slug", fd.ID)
		}
		if fd.Kind != "ansp_stream" && fd.Kind != "adsb_file" {
			return core.Fieldf(f+".kind", "ansp_stream or adsb_file")
		}
		if len(fd.Tracks) == 0 && fd.Recording == "" {
			return core.Fieldf(f, "tracks or a recording")
		}
		for j, t := range fd.Tracks {
			if !regexp.MustCompile(`^[0-9a-f]{6}$`).MatchString(t.ICAO24) || !(t.SpeedMS > 0) {
				return core.Fieldf(fmt.Sprintf("%s.tracks[%d]", f, j), "icao24 six lower-case hex digits, speed_ms > 0")
			}
		}
		feeds[fd.ID] = true
	}
	zones := map[string]bool{}
	for i, z := range s.Zones {
		f := fmt.Sprintf("zones[%d]", i)
		if z.ID == "" || zones[z.ID] {
			return core.Fieldf(f+".id", "missing or repeated")
		}
		switch core.ZoneType(z.Type) {
		case core.ZoneProhibited, core.ZoneReqAuthorization, core.ZoneConditional:
		case core.ZoneNoRestriction, core.ZoneUSpace:
			return core.Fieldf(f+".type", "%q raises nothing; not a scenario zone", z.Type)
		default:
			return core.Fieldf(f+".type", "%q is not PROHIBITED, REQ_AUTHORIZATION or CONDITIONAL", z.Type)
		}
		if (z.Square == nil) == (z.Circle == nil) {
			return core.Fieldf(f, "exactly one of square, circle")
		}
		for _, l := range []Limit{z.Lower, z.Upper} {
			switch core.VerticalRef(l.Ref) {
			case core.RefAGL, core.RefAMSL, core.RefWGS84:
			default:
				return core.Fieldf(f, "limit reference %q is not AGL, AMSL or WGS84", l.Ref)
			}
		}
		if len(z.Window) != 0 && len(z.Window) != 2 {
			return core.Fieldf(f+".window", "[start, end] or empty")
		}
		zones[z.ID] = true
	}
	marks := map[string]bool{"t0": true}
	altOf := map[string]float64{}
	for i := range s.Steps {
		st := &s.Steps[i]
		f := fmt.Sprintf("steps[%d]", i)
		if st.ID != "" {
			if !idPattern.MatchString(st.ID) || marks[st.ID] {
				return core.Fieldf(f+".id", "%q is not a unique slug", st.ID)
			}
			marks[st.ID] = true
			marks[st.ID+".start"] = true
		}
		if st.AtS != nil && (*st.AtS < 0 || *st.AtS > s.DurationS) {
			return core.Fieldf(f+".at_s", "outside [0, duration_s]")
		}
		switch st.Do {
		case DoTakeoff, DoGoto, DoHold, DoLand:
			if len(st.Aircraft) == 0 {
				return core.Fieldf(f+".aircraft", "a flight step names its aircraft")
			}
			for _, n := range st.Aircraft {
				if names[n] == nil {
					return core.Fieldf(f+".aircraft", "%q is not an aircraft", n)
				}
			}
			if st.Do == DoTakeoff && !(st.AltRelM > 0) {
				return core.Fieldf(f+".alt_rel_m", "takeoff needs alt_rel_m > 0")
			}
			if st.Do == DoGoto && (st.To == nil || st.SpeedMS < 0) {
				return core.Fieldf(f+".to", "goto needs to")
			}
			if st.Do == DoHold && !(st.ForS > 0) {
				return core.Fieldf(f+".for_s", "hold needs for_s > 0")
			}
			if (st.Do == DoTakeoff || st.Do == DoGoto) && st.AltRelM > 0 {
				for _, n := range st.Aircraft {
					altOf[n] = st.AltRelM
				}
			}
			// A timed landing must be able to finish inside the run: the
			// runner fails a run in which a vehicle did not confirm a
			// step, and SITL lands at its own pace.
			if st.Do == DoLand && st.AtS != nil {
				for _, n := range st.Aircraft {
					if need := LandS(altOf[n]); *st.AtS+need > s.DurationS {
						return core.Fieldf(f+".at_s", "%s lands from %.0f m at %.0f s, which takes about %.0f s, after duration_s %.0f",
							n, altOf[n], *st.AtS, need, s.DurationS)
					}
				}
			}
		case DoKnob:
			if st.AtS == nil || st.Knob == nil {
				return core.Fieldf(f, "a knob needs at_s and knob")
			}
		case DoRequest:
			if st.AtS == nil || st.Request == nil || st.Request.Method == "" || !strings.HasPrefix(st.Request.Path, "/") {
				return core.Fieldf(f, "a request needs at_s, method and an absolute path")
			}
		default:
			return core.Fieldf(f+".do", "%q is not takeoff, goto, hold, land, knob or request", st.Do)
		}
	}
	checkWindow := func(f string, w *Window) error {
		if w == nil {
			return nil
		}
		if !marks[w.After] {
			return core.Fieldf(f+".after", "%q is not t0 or a step id", w.After)
		}
		if w.MaxS < w.MinS {
			return core.Fieldf(f, "max_s < min_s")
		}
		return nil
	}
	icaos := map[string]bool{}
	for _, fd := range s.Feeds {
		for _, t := range fd.Tracks {
			icaos[t.ICAO24] = true
		}
	}
	checkMatcher := func(f string, m Matcher) error {
		if !knownSystem(m.System) || m.Kind == "" {
			return core.Fieldf(f, "system and kind are required")
		}
		if m.Aircraft != "" && names[m.Aircraft] == nil {
			return core.Fieldf(f, "%q is not an aircraft", m.Aircraft)
		}
		if m.Peer != "" && names[m.Peer] == nil && !icaos[m.Peer] {
			return core.Fieldf(f, "%q is neither an aircraft nor a feed track's icao24", m.Peer)
		}
		return nil
	}
	expNames := map[string]bool{}
	for i := range s.Expect {
		e := &s.Expect[i]
		f := fmt.Sprintf("expect[%d]", i)
		if e.Name == "" || expNames[e.Name] {
			return core.Fieldf(f+".name", "missing or repeated")
		}
		expNames[e.Name] = true
		if err := checkMatcher(f, e.Matcher); err != nil {
			return err
		}
		for k, w := range map[string]*Window{"raise": &e.Raise, "clear": e.Clear, "hold_until": e.HoldUntil} {
			if err := checkWindow(f+"."+k, w); err != nil {
				return err
			}
		}
	}
	for i, m := range s.Never {
		if err := checkMatcher(fmt.Sprintf("never[%d]", i), m); err != nil {
			return err
		}
	}
	for i, ie := range s.ExpectIntents {
		a := names[ie.Aircraft]
		if a == nil || a.Operator == nil || a.Operator.Intent == nil || ie.Decision == "" {
			return core.Fieldf(fmt.Sprintf("expect_intents[%d]", i), "an aircraft with an operator intent, and a decision")
		}
	}
	return nil
}

func knownSystem(s string) bool {
	switch s {
	case SystemUSSP, SystemAuthority, SystemANSP, SystemCISP, SystemPeer:
		return true
	}
	return false
}

// AircraftByName returns the aircraft called name.
func (s *Scenario) AircraftByName(name string) *Aircraft {
	for i := range s.Aircraft {
		if s.Aircraft[i].Name == name {
			return &s.Aircraft[i]
		}
	}
	return nil
}

// Kinds are the alert kinds the scenario judges: judged_kinds, or every
// kind its expectations and never-list name, per system. A raise of one
// of these that no expectation explains is a false alert.
func (s *Scenario) Kinds() map[string][]string {
	set := map[string]map[string]bool{}
	add := func(sys, k string) {
		if set[sys] == nil {
			set[sys] = map[string]bool{}
		}
		set[sys][k] = true
	}
	for i := range s.Expect {
		add(s.Expect[i].System, s.Expect[i].Kind)
	}
	for _, m := range s.Never {
		add(m.System, m.Kind)
	}
	for _, jk := range s.JudgedKinds {
		if sys, k, ok := strings.Cut(jk, ":"); ok {
			add(sys, k)
		}
	}
	out := map[string][]string{}
	for sys, ks := range set {
		for k := range ks {
			out[sys] = append(out[sys], k)
		}
		sort.Strings(out[sys])
	}
	return out
}

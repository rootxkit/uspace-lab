package load

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// Roles of an aircraft.
const (
	RoleWalker = "walker"
	RolePair   = "pair"
)

// Aircraft is one synthetic aircraft of the fleet.
type Aircraft struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Serial string `json:"serial"`
	MAC    string `json:"mac"`
	Role   string `json:"role"`
	// Sysid is the in-process vehicle number the bridges key on (it is
	// never written as a sim/vehicle/v1 line, so 05 §1's 5000 aircraft
	// are not held to MAVLink's 1..254).
	Sysid   int     `json:"-"`
	Pair    int     `json:"pair,omitempty"`
	Side    float64 `json:"-"`
	AltRelM float64 `json:"alt_rel_m"`
	Client  int     `json:"client"`
	// OperatorReg is the registration number of the aircraft's client
	// (ClientRegistration): the operator a systems run registers and
	// binds the aircraft under, and the one its intent and its Remote ID
	// Operator ID name.
	OperatorReg string `json:"operator_reg"`
	Receiver    int    `json:"receiver"` // -1: not heard
	Watched     bool   `json:"watched"`

	centre scenario.Offset
	// walker state, advanced one nominal period per sample
	posN, posE, headingDeg float64
	rng                    *rand.Rand
}

// Expected is one alert a scripted crossing makes due (05 §7 "the
// scenario's expected set"): a raise in [RaiseFromS, RaiseToS] and a
// clear in [CrossingS, ClearToS], seconds after the generator's start.
// A crossing near the end of the run is listed when its raise window
// opens inside the run; RaiseDue and ClearDue say whether the run lasts
// long enough to count the raise or the clear as missed, so a raise
// that arrives for it is matched (never a false alarm) without the run
// demanding what it ended too early to see.
type Expected struct {
	Aircraft   string  `json:"aircraft"`
	Peer       string  `json:"peer"`
	Pair       int     `json:"pair"`
	CrossingS  float64 `json:"crossing_s"`
	RaiseFromS float64 `json:"raise_from_s"`
	RaiseToS   float64 `json:"raise_to_s"`
	ClearToS   float64 `json:"clear_to_s"`
	RaiseDue   bool    `json:"raise_due"`
	ClearDue   bool    `json:"clear_due"`
}

// Fleet is the tier's aircraft on the paths.
type Fleet struct {
	Aircraft  []*Aircraft
	Receivers [][]int // aircraft indices heard by receiver i
	Expected  []Expected
	// BBox is [west, south, east, north] around every path (the
	// consoles' subscription).
	BBox   [4]float64
	origin vehicle.Home
	paths  *Paths
	period time.Duration
	geo    geoid.Undulator
	digest string
}

// NewFleet lays the tier's aircraft on the paths from origin.
func NewFleet(f *Files, origin vehicle.Home, geo geoid.Undulator) (*Fleet, error) {
	t, p := f.Tier, f.Paths
	n := t.Operators.Aircraft
	pairs := t.Watch.Pairs
	walkers := n - 2*pairs
	period := time.Duration(float64(time.Second) / t.Operators.TelemetryHz)
	if p.Walkers.RadiusM <= 2*p.Walkers.SpeedMS*period.Seconds() {
		return nil, fmt.Errorf("load paths: walkers.radius_m %v is not above two periods of travel", p.Walkers.RadiusM)
	}
	fl := &Fleet{origin: origin, paths: p, period: period, geo: geo}
	cols, _ := gridOf(walkers)
	tile := p.Walkers.LayerTile
	for i := range n {
		a := &Aircraft{Index: i, Name: fmt.Sprintf("ac-%05d", i), Serial: AircraftSerial(i, t.Operators.Clients), MAC: macOfIndex(i),
			Sysid: i + 1, Client: i % t.Operators.Clients, Receiver: -1}
		a.OperatorReg = ClientRegistration(a.Client, t.Operators.Clients)
		if i < 2*pairs {
			a.Role, a.Pair, a.Watched = RolePair, i/2, true
			a.Side = 1
			if i%2 == 1 {
				a.Side = -1
			}
			a.AltRelM = p.Pairs.AltRelM
			a.centre = scenario.Offset{NorthM: p.Pairs.Origin.NorthM, EastM: p.Pairs.Origin.EastM + float64(a.Pair)*p.Pairs.SpacingM}
		} else {
			w := i - 2*pairs
			row, col := w/cols, w%cols
			a.Role = RoleWalker
			a.Watched = w < t.Watch.Walkers
			a.AltRelM = p.Walkers.LayersRelM[(row%tile)*tile+col%tile]
			a.centre = scenario.Offset{NorthM: p.Walkers.Origin.NorthM + float64(row)*p.Walkers.SpacingM, EastM: p.Walkers.Origin.EastM + float64(col)*p.Walkers.SpacingM}
			a.rng = rand.New(rand.NewPCG(p.Seed, uint64(i))) //nolint:gosec // a path, not a secret
			a.headingDeg = a.rng.Float64() * 360
			a.posN, a.posE = a.centre.NorthM, a.centre.EastM
		}
		fl.Aircraft = append(fl.Aircraft, a)
	}
	// Heard aircraft: an even spread of round(n * fraction), grouped by
	// aircraft_per_receiver.
	frac := t.Receivers.HeardFraction
	var heard []int
	for i := range n {
		if math.Floor(float64(i+1)*frac) > math.Floor(float64(i)*frac) {
			heard = append(heard, i)
		}
	}
	k := max(1, t.Receivers.AircraftPerReceiver)
	for j := 0; j < len(heard); j += k {
		rx := heard[j:min(j+k, len(heard))]
		for _, i := range rx {
			fl.Aircraft[i].Receiver = len(fl.Receivers)
		}
		fl.Receivers = append(fl.Receivers, rx)
	}
	fl.expect(f.Policy, t.DurationS)
	fl.bbox()
	fl.digest = fl.layoutDigest()
	return fl, nil
}

// The fleet's aircraft are open-category A2 aircraft of class C2, as
// the scenario suite's are: the USSP judges an open intent only with a
// subcategory and a class label (Annex IV), and the seed registers the
// class with the aircraft and its binding so the registry agrees.
const (
	LoadSubcategory = "A2"
	LoadClassLabel  = "C2"
)

// AircraftSerial is aircraft i's serial in a fleet of clients operator
// clients: an ANSI/CTA-2063-A serial (manufacturer code LABX, length C,
// then L, the client count, N and the aircraft's number), because the
// systems register and bind only such serials (uspace-core serial).
// The client count is in it because aircraft i flies for client
// i mod clients: a fleet with another client count binds its aircraft
// to other clients, and the USSP refuses a serial bound to another
// client, so the two fleets must not share serials. Distinct for every
// i below a million and clients up to 9999, above maxAircraft and
// maxClients.
func AircraftSerial(i, clients int) string { return fmt.Sprintf("LABXCL%04dN%06d", clients, i) }

// ClientRegistration is operator client c's registration number in a
// fleet of clients clients: the default EU shape uspace-core regnum
// validates (three letters, then 8 to 16 letters and digits), distinct
// per client and per client count (AircraftSerial says why).
func ClientRegistration(c, clients int) string { return fmt.Sprintf("GEOLOAD%04d%04d", clients, c+1) }

// macOfIndex is a locally administered unicast address per aircraft.
func macOfIndex(i int) string {
	return fmt.Sprintf("02:4C:44:%02X:%02X:%02X", (i>>16)&0xff, (i>>8)&0xff, i&0xff)
}

// expect lists the alerts the pairs make due: each crossing whose raise
// window opens inside the run raises proximity on both members.
func (fl *Fleet) expect(pol *scenario.Policy, durationS float64) {
	pr := &fl.paths.Pairs
	half := 2 * pr.AmplitudeM / pr.SpeedMS
	for _, a := range fl.Aircraft {
		if a.Role != RolePair {
			continue
		}
		peer := fl.Aircraft[a.Index^1]
		for c := pr.FirstCrossingS; ; c += half {
			x := Expected{Aircraft: a.Name, Peer: peer.Name, Pair: a.Pair, CrossingS: c,
				RaiseFromS: math.Max(0, c-pol.CPA.TCPAMaxS-pr.RaiseMarginS), RaiseToS: c + pr.RaiseMarginS, ClearToS: c + pr.ClearWithinS}
			if x.RaiseFromS >= durationS {
				break
			}
			x.RaiseDue, x.ClearDue = x.RaiseToS <= durationS, x.ClearToS <= durationS
			fl.Expected = append(fl.Expected, x)
		}
	}
}

func (fl *Fleet) bbox() {
	b := fl.paths.Bounds
	sw := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: b.SouthM, EastM: b.WestM})
	ne := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: b.NorthM, EastM: b.EastM})
	fl.BBox = [4]float64{sw.LonDeg, sw.LatDeg, ne.LonDeg, ne.LatDeg}
}

// pairPos is a pair member's east offset from its line centre and its
// east velocity at t seconds after the start: a triangle wave x between
// -A and +A, the members mirrored, at the centre together at
// FirstCrossingS + k * 2A/v. Without an inner gap the members cross
// there. With one (InnerGapM, g) each member stays on its own side:
// its offset is -(g/2 + (A - g/2) |x| / A) for the member of side +1, so
// it turns back at g/2 from the centre when x is 0 and is at -A when x
// is; the members are then g apart at each crossing time.
func (fl *Fleet) pairPos(a *Aircraft, t float64) (eastM, veMS float64) {
	pr := &fl.paths.Pairs
	amp, v := pr.AmplitudeM, pr.SpeedMS
	u := math.Mod(amp-v*pr.FirstCrossingS+v*t, 4*amp)
	if u < 0 {
		u += 4 * amp
	}
	x, dir := -amp+u, 1.0
	if u > 2*amp {
		x, dir = 3*amp-u, -1
	}
	if g := pr.InnerGapM; g > 0 {
		half := g / 2
		k := (amp - half) / amp
		sign := 1.0
		if x < 0 {
			sign = -1
		}
		return -a.Side * (half + k*math.Abs(x)), -a.Side * k * sign * dir * v
	}
	return a.Side * x, a.Side * dir * v
}

// pairBox is the east extent of a pair member's intent about its line
// centre: with an inner gap, from the far end of its side (100 m past
// the amplitude) to a quarter of the gap short of the centre, so the
// two members' boxes are half the gap apart; without one, the whole
// line.
type pairBox struct{ WestM, EastM float64 }

func (fl *Fleet) pairBox(a *Aircraft) pairBox {
	pr := &fl.paths.Pairs
	far := pr.AmplitudeM + pairIntentMarginM
	if pr.InnerGapM <= 0 {
		return pairBox{WestM: -far, EastM: far}
	}
	if -a.Side < 0 {
		return pairBox{WestM: -far, EastM: -pr.InnerGapM / 4}
	}
	return pairBox{WestM: pr.InnerGapM / 4, EastM: far}
}

// pairIntentMarginM is how far a pair member's intent reaches past its
// path, along and across the line.
const pairIntentMarginM = 100

// step advances a walker one period: a bounded random turn, and a turn
// back towards the centre when the step would leave its disc.
func (fl *Fleet) step(a *Aircraft) {
	w := &fl.paths.Walkers
	dt := fl.period.Seconds()
	a.headingDeg += (a.rng.Float64()*2 - 1) * w.TurnDegPerS * dt
	d := w.SpeedMS * dt
	n, e := a.posN+d*math.Cos(a.headingDeg*math.Pi/180), a.posE+d*math.Sin(a.headingDeg*math.Pi/180)
	if math.Hypot(n-a.centre.NorthM, e-a.centre.EastM) > w.RadiusM {
		a.headingDeg = math.Atan2(a.centre.EastM-a.posE, a.centre.NorthM-a.posN)*180/math.Pi + (a.rng.Float64()*2-1)*30
		n, e = a.posN+d*math.Cos(a.headingDeg*math.Pi/180), a.posE+d*math.Sin(a.headingDeg*math.Pi/180)
	}
	a.posN, a.posE = n, e
	a.headingDeg = math.Mod(a.headingDeg+360, 360)
}

// Sample is the aircraft's sample number k (k periods after the start),
// stamped with the time it is handed out.
func (fl *Fleet) Sample(a *Aircraft, k int64, now, start time.Time) vehicle.Sample {
	var off scenario.Offset
	var vn, ve float64
	switch a.Role {
	case RolePair:
		east, vE := fl.pairPos(a, float64(k)*fl.period.Seconds())
		off, ve = scenario.Offset{NorthM: a.centre.NorthM, EastM: a.centre.EastM + east}, vE
	default:
		if k > 0 {
			fl.step(a)
		}
		off = scenario.Offset{NorthM: a.posN, EastM: a.posE}
		h := a.headingDeg * math.Pi / 180
		v := fl.paths.Walkers.SpeedMS
		vn, ve = v*math.Cos(h), v*math.Sin(h)
	}
	p := scenario.Move(fl.originLatLon(), off)
	speed := math.Hypot(vn, ve)
	track := math.Mod(math.Atan2(ve, vn)*180/math.Pi+360, 360)
	if track >= 360 {
		track = 0
	}
	amsl := fl.origin.AltAMSLM + a.AltRelM
	ts := vehicle.FormatTime(now)
	s := vehicle.Sample{
		Schema: vehicle.Schema, Sysid: a.Sysid, TS: &ts, TimeBootMS: uint32(now.Sub(start) / time.Millisecond),
		LatDeg: p.LatDeg, LonDeg: p.LonDeg, AltAMSLM: amsl, HeightTakeoffM: a.AltRelM,
		SpeedMS: speed, TrackDeg: &track, VNMS: vn, VEMS: ve, Armed: true, Status: vehicle.StatusAirborne, FixOK: true,
	}
	if fl.geo != nil {
		if n, err := fl.geo.UndulationM(p); err == nil {
			s.AltHAEM = vehicle.F(geoid.HAEFromAMSL(amsl, n))
		}
	}
	return s
}

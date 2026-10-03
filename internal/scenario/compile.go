package scenario

import (
	"fmt"
	"sort"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/ed269"
	"github.com/rootxkit/uspace-core/zones"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// Compiled is a scenario turned into what the runner executes: one plan
// per aircraft (what sim/fly.py or the synthetic vehicle flies), the
// knobs and requests in time order, and the zones as uspace-core sees
// them.
type Compiled struct {
	Scenario *Scenario
	Lab      *Lab
	T0       time.Time
	Plans    map[string]*vehicle.Plan // by aircraft name
	// StepOf maps an aircraft's plan step index to the scenario step it
	// came from, for the marks.
	StepOf   map[string][]int
	Timeline []Timed
	Zones    []*zones.Zone
	ED269    []ed269.GeoZone
}

// Timed is a knob or a request at T0 + AtS.
type Timed struct {
	AtS  float64
	Step int
}

// DefaultToleranceM is the arrival tolerance of a goto that names none.
const DefaultToleranceM = 3.0

// Compile builds the plans for a run starting at t0.
func Compile(s *Scenario, lab *Lab, t0 time.Time) (*Compiled, error) {
	c := &Compiled{Scenario: s, Lab: lab, T0: t0, Plans: map[string]*vehicle.Plan{}, StepOf: map[string][]int{}}
	alt := map[string]float64{}
	for i := range s.Aircraft {
		a := &s.Aircraft[i]
		c.Plans[a.Name] = &vehicle.Plan{Sysid: a.Sysid, T0UnixS: vehicle.UnixS(t0)}
	}
	add := func(name string, st vehicle.PlanStep, scenarioStep int) {
		p := c.Plans[name]
		p.Steps = append(p.Steps, st)
		c.StepOf[name] = append(c.StepOf[name], scenarioStep)
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		switch st.Do {
		case DoKnob, DoRequest:
			c.Timeline = append(c.Timeline, Timed{AtS: *st.AtS, Step: i})
			continue
		}
		for _, name := range st.Aircraft {
			atS := st.AtS
			switch st.Do {
			case DoTakeoff:
				add(name, vehicle.PlanStep{AtS: atS, Action: vehicle.ActionArm, ID: st.ID}, i)
				add(name, vehicle.PlanStep{Action: vehicle.ActionTakeoff, AltRelM: st.AltRelM, ID: st.ID}, i)
				alt[name] = st.AltRelM
			case DoGoto:
				to := lab.At(*st.To)
				a := alt[name]
				if st.AltRelM > 0 {
					a = st.AltRelM
				}
				tol := st.ToleranceM
				if tol <= 0 {
					tol = DefaultToleranceM
				}
				add(name, vehicle.PlanStep{AtS: atS, Action: vehicle.ActionGoto, LatDeg: to.LatDeg, LonDeg: to.LonDeg,
					AltRelM: a, SpeedMS: st.SpeedMS, ToleranceM: tol, ID: st.ID}, i)
				alt[name] = a
			case DoHold:
				add(name, vehicle.PlanStep{AtS: atS, Action: vehicle.ActionHold, ForS: st.ForS, ID: st.ID}, i)
			case DoLand:
				add(name, vehicle.PlanStep{AtS: atS, Action: vehicle.ActionLand, ID: st.ID}, i)
			}
		}
	}
	for name, p := range c.Plans {
		if len(p.Steps) == 0 {
			continue
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("aircraft %s: %w", name, err)
		}
	}
	sort.SliceStable(c.Timeline, func(i, j int) bool { return c.Timeline[i].AtS < c.Timeline[j].AtS })
	for _, z := range s.Zones {
		gz, err := ED269Zone(s, lab, z)
		if err != nil {
			return nil, fmt.Errorf("zone %s: %w", z.ID, err)
		}
		jz, err := zones.FromED269(&gz)
		if err != nil {
			return nil, fmt.Errorf("zone %s: %w", z.ID, err)
		}
		c.ED269 = append(c.ED269, gz)
		c.Zones = append(c.Zones, jz)
	}
	return c, nil
}

// ED269Zone is a scenario zone as an ED-269 UASZoneVersion (uspace-core's
// model), placed by the lab's origin.
func ED269Zone(s *Scenario, lab *Lab, z Zone) (ed269.GeoZone, error) {
	if s.Country == "" {
		return ed269.GeoZone{}, core.Fieldf("country", "a scenario with zones names the ED-269 country (ISO 3166-1 alpha-3)")
	}
	var restriction ed269.Restriction
	switch core.ZoneType(z.Type) {
	case core.ZoneProhibited:
		restriction = ed269.RestrictionProhibited
	case core.ZoneReqAuthorization:
		restriction = ed269.RestrictionReqAuthorisation
	case core.ZoneConditional:
		restriction = ed269.RestrictionConditional
	case core.ZoneNoRestriction, core.ZoneUSpace:
		return ed269.GeoZone{}, core.Fieldf("type", "%q is not a zone a scenario raises on", z.Type)
	default:
		return ed269.GeoZone{}, core.Fieldf("type", "%q", z.Type)
	}
	lower, upper := z.Lower.M, z.Upper.M
	vol := ed269.Volume{
		Uom:        ed269.UomMetres,
		LowerLimit: &lower,
		UpperLimit: &upper,
		LowerRef:   core.VerticalRef(z.Lower.Ref),
		UpperRef:   core.VerticalRef(z.Upper.Ref),
	}
	switch {
	case z.Square != nil:
		h := z.Square.SideM / 2
		c := z.Square.Center
		corner := func(n, e float64) core.LatLon {
			return lab.At(Offset{NorthM: c.NorthM + n, EastM: c.EastM + e})
		}
		first := corner(h, -h)
		ring := []core.LatLon{first, corner(h, h), corner(-h, h), corner(-h, -h), first}
		vol.Projection = ed269.HorizontalProjection{Type: ed269.ShapePolygon, Rings: [][]core.LatLon{ring}}
	case z.Circle != nil:
		center := lab.At(z.Circle.Center)
		r := z.Circle.RadiusM
		vol.Projection = ed269.HorizontalProjection{Type: ed269.ShapeCircle, Center: &center, Radius: &r}
	}
	period := ed269.Period{Permanent: true}
	if len(z.Window) == 2 {
		start, err1 := time.Parse(time.RFC3339, z.Window[0])
		end, err2 := time.Parse(time.RFC3339, z.Window[1])
		if err1 != nil || err2 != nil || !end.After(start) {
			return ed269.GeoZone{}, core.Fieldf("window", "two RFC 3339 times, end after start")
		}
		period = ed269.Period{Start: &start, End: &end}
	}
	name := z.ID
	return ed269.GeoZone{
		Identifier:    z.ID,
		Country:       s.Country,
		Name:          &name,
		Type:          "COMMON",
		Restriction:   restriction,
		Reason:        []ed269.Reason{ed269.ReasonOther},
		Applicability: []ed269.Period{period},
		Geometry:      []ed269.Volume{vol},
	}, nil
}

// Mark names a moment of the run: t0, a step id (confirmed by every
// vehicle it names, E-08) or <id>.start.
type Mark = string

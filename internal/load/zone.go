package load

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/seed"
)

// 05 §7 "zone alert within one tick of entry": a tier's zone (systems
// mode) is a geographical zone the seed publishes at the authority, and
// a few aircraft of the fleet fly lines through it, in and out, as the
// pairs fly theirs. Each entry is due one zone_incursion raise on the
// aircraft's USSP traffic stream, timed from the first sample inside;
// each exit its clear. A raise later than one monitor tick fails the
// row, one missing is a missed alert, one outside every entry a false
// alarm.

// RoleZone is an aircraft flying through the tier's zone.
const RoleZone = "zone"

// LoadZoneID is the zone a load seed publishes.
const LoadZoneID = "LOADZ1"

// ZoneCfg is a tier's zone and the aircraft that fly through it.
type ZoneCfg struct {
	// Aircraft fly east-west lines through the zone, SpacingM apart
	// north-south (the zone must be wide enough for every line).
	Aircraft   int             `yaml:"aircraft" json:"aircraft"`
	Centre     scenario.Offset `yaml:"centre" json:"centre"`
	SideM      float64         `yaml:"side_m" json:"side_m"`
	SpacingM   float64         `yaml:"spacing_m" json:"spacing_m"`
	AmplitudeM float64         `yaml:"amplitude_m" json:"amplitude_m"`
	SpeedMS    float64         `yaml:"speed_ms" json:"speed_ms"`
	AltRelM    float64         `yaml:"alt_rel_m" json:"alt_rel_m"`
	// Type is the zone's ED-269 type: CONDITIONAL authorises the
	// aircraft's intent with the zone's condition and is still an
	// incursion (scripts/chaos/background.yaml says why not
	// REQ_AUTHORIZATION).
	Type string `yaml:"type" json:"type"`
	// RaiseMarginS widens the raise window past the entry (transport);
	// ClearWithinS is how long after the exit the clear is due.
	RaiseMarginS float64 `yaml:"raise_margin_s" json:"raise_margin_s"`
	ClearWithinS float64 `yaml:"clear_within_s" json:"clear_within_s"`
}

func (z *ZoneCfg) validate(t *Tier) error {
	switch {
	case z.Aircraft < 1 || z.Aircraft > 10:
		return fmt.Errorf("zone.aircraft %d is not in [1, 10]", z.Aircraft)
	case !(z.SideM >= 50 && z.SideM <= 5000):
		return fmt.Errorf("zone.side_m %v is not in [50, 5000]", z.SideM)
	case float64(z.Aircraft-1)*z.SpacingM >= z.SideM || (z.Aircraft > 1 && !(z.SpacingM > 0)):
		return fmt.Errorf("zone: %d lines %v m apart do not fit in a zone of %v m", z.Aircraft, z.SpacingM, z.SideM)
	case !(z.AmplitudeM > z.SideM/2+2*z.SpeedMS && z.AmplitudeM <= 5000):
		return fmt.Errorf("zone.amplitude_m %v must take the aircraft out of the zone (half side %v m) and be at most 5000", z.AmplitudeM, z.SideM/2)
	case !(z.SpeedMS > 0 && z.SpeedMS <= 30):
		return fmt.Errorf("zone.speed_ms %v is not in (0, 30]", z.SpeedMS)
	case !(z.AltRelM > 0 && z.AltRelM <= 500):
		return fmt.Errorf("zone.alt_rel_m %v is not in (0, 500]", z.AltRelM)
	case z.Type != "CONDITIONAL" && z.Type != "REQ_AUTHORIZATION" && z.Type != "PROHIBITED":
		return fmt.Errorf("zone.type %q is CONDITIONAL, REQ_AUTHORIZATION or PROHIBITED", z.Type)
	case z.RaiseMarginS < 0 || z.RaiseMarginS > 10 || !(z.ClearWithinS > 0 && z.ClearWithinS <= 120):
		return fmt.Errorf("zone: raise_margin_s in [0, 10] and clear_within_s in (0, 120] are required")
	case z.Aircraft > t.Operators.Aircraft-2*t.Watch.Pairs-t.Watch.Walkers:
		return fmt.Errorf("zone.aircraft %d: the tier has no walkers left to fly them", z.Aircraft)
	}
	return nil
}

// ZoneEntry is one entry of a zone aircraft into the zone: the sample
// that is first inside (k, seconds after the generator's start) and the
// first sample outside again.
type ZoneEntry struct {
	Aircraft string  `json:"aircraft"`
	K        int64   `json:"k"`
	EntryS   float64 `json:"entry_s"`
	ExitS    float64 `json:"exit_s"`
	// RaiseDue and ClearDue say whether the run lasts long enough to
	// count the raise or the clear as missed.
	RaiseDue bool `json:"raise_due"`
	ClearDue bool `json:"clear_due"`
}

// zoneEast is a zone aircraft's east offset from the zone's centre and
// its east velocity t seconds after the start: a triangle wave between
// -A and +A starting at -A, so its first entry is a crossing of the
// zone's west edge.
func (z *ZoneCfg) zoneEast(t float64) (eastM, veMS float64) {
	a, v := z.AmplitudeM, z.SpeedMS
	u := math.Mod(v*t, 4*a)
	if u < 2*a {
		return -a + u, v
	}
	return 3*a - u, -v
}

// inside is whether an east offset from the zone's centre is inside it.
func (z *ZoneCfg) inside(eastM float64) bool { return math.Abs(eastM) <= z.SideM/2 }

// zoneCentre is zone aircraft i's line centre: the zone's centre, the
// lines spread north-south about it.
func (z *ZoneCfg) lineCentre(i int) scenario.Offset {
	return scenario.Offset{NorthM: z.Centre.NorthM + (float64(i)-float64(z.Aircraft-1)/2)*z.SpacingM, EastM: z.Centre.EastM}
}

// zoneEntries lists every entry inside the run, from the samples the
// generator makes (one a period): the first inside after one outside.
func (fl *Fleet) zoneEntries(durationS float64) {
	z := fl.zone
	if z == nil {
		return
	}
	p := fl.period.Seconds()
	n := int64(durationS / p)
	for _, a := range fl.Aircraft {
		if a.Role != RoleZone {
			continue
		}
		var cur *ZoneEntry
		prev := false
		for k := int64(0); k <= n+int64(z.ClearWithinS/p)+1; k++ {
			e, _ := z.zoneEast(float64(k) * p)
			in := z.inside(e)
			switch {
			case in && !prev && k > 0 && float64(k)*p < durationS:
				fl.ZoneExpected = append(fl.ZoneExpected, ZoneEntry{Aircraft: a.Name, K: k, EntryS: float64(k) * p, ExitS: math.Inf(1)})
				cur = &fl.ZoneExpected[len(fl.ZoneExpected)-1]
			case !in && prev && cur != nil:
				cur.ExitS = float64(k) * p
				cur = nil
			}
			prev = in
		}
	}
	for i := range fl.ZoneExpected {
		x := &fl.ZoneExpected[i]
		x.RaiseDue = x.EntryS+z.RaiseMarginS+1 <= durationS
		x.ClearDue = !math.IsInf(x.ExitS, 1) && x.ExitS+z.ClearWithinS <= durationS
		if math.IsInf(x.ExitS, 1) {
			x.ExitS = durationS
		}
	}
}

// SeedZone is the zone as the seed publishes it.
func (fl *Fleet) seedZone() *seed.LoadZone {
	z := fl.zone
	if z == nil {
		return nil
	}
	return &seed.LoadZone{Zone: scenario.Zone{ID: LoadZoneID, Type: z.Type,
		Square: &scenario.Square{Center: z.Centre, SideM: z.SideM},
		Lower:  scenario.Limit{M: 0, Ref: "AMSL"}, Upper: scenario.Limit{M: fl.origin.AltAMSLM + 1500, Ref: "AMSL"}}}
}

// ZoneJudgement is the zone's entries against the observed alerts.
type ZoneJudgement struct {
	Due, Raised, Cleared, Missed, MissedClears, Unexpected int
	// TickS is the monitor tick the raises are judged in; Ticks the
	// slowest raise in ticks.
	TickS   float64      `json:"tick_s"`
	Ticks   float64      `json:"ticks"`
	Latency *Histogram   `json:"-"`
	Lines   []string     `json:"lines,omitempty"`
	Raises  []ZoneRaised `json:"raises,omitempty"`
}

// ZoneRaised is one entry's raise: when, and from the first sample
// inside.
type ZoneRaised struct {
	Aircraft string  `json:"aircraft"`
	EntryS   float64 `json:"entry_s"`
	RaiseS   float64 `json:"raise_s"`
	LatencyS float64 `json:"latency_s"`
}

// judgeZone matches each entry to the first unused zone_incursion raise
// of its aircraft (the USSP's, subject the zone) between the entry's
// sample and the raise margin past the next sample, times it from that
// sample's hand-out (handed), and looks for its clear; any other raise
// of the zone on a zone aircraft is unexpected.
func judgeZone(entries []ZoneEntry, events []observe.Event, t0 time.Time, tickS float64, margin, clearWithin float64,
	handed func(aircraft string, k int64) (time.Time, bool)) ZoneJudgement {
	j := ZoneJudgement{TickS: tickS, Latency: NewHistogram()}
	rel := func(t time.Time) float64 { return t.Sub(t0).Seconds() }
	by := map[string][]observe.Event{}
	for i := range events {
		e := &events[i]
		if e.System == scenario.SystemUSSP && e.Kind == "zone_incursion" && (e.Subject == "" || e.Subject == LoadZoneID) {
			by[e.Aircraft] = append(by[e.Aircraft], *e)
		}
	}
	used := map[string]bool{}
	for _, x := range entries {
		if x.RaiseDue {
			j.Due++
		}
		var raise *observe.Event
		for i := range by[x.Aircraft] {
			e := &by[x.Aircraft][i]
			if e.Phase == observe.PhaseRaised && !used[e.AlertID] && rel(e.ObservedAt) >= x.EntryS-1 && rel(e.ObservedAt) <= x.EntryS+tickS+margin {
				raise = e
				break
			}
		}
		if raise == nil {
			if x.RaiseDue {
				j.Missed++
				if len(j.Lines) < maxListed {
					j.Lines = append(j.Lines, fmt.Sprintf("%s: no zone raise for the entry at %.0f s", x.Aircraft, x.EntryS))
				}
			}
			continue
		}
		used[raise.AlertID] = true
		j.Raised++
		if h, ok := handed(x.Aircraft, x.K); ok {
			lat := raise.ObservedAt.Sub(h)
			j.Latency.Add(lat)
			j.Ticks = math.Max(j.Ticks, lat.Seconds()/tickS)
			if len(j.Raises) < maxListed {
				j.Raises = append(j.Raises, ZoneRaised{Aircraft: x.Aircraft, EntryS: round3(x.EntryS), RaiseS: round3(rel(raise.ObservedAt)), LatencyS: round3(lat.Seconds())})
			}
		}
		cleared := false
		for i := range by[x.Aircraft] {
			e := &by[x.Aircraft][i]
			if e.Phase == observe.PhaseCleared && e.AlertID == raise.AlertID && rel(e.ObservedAt) <= x.ExitS+clearWithin {
				cleared = true
				break
			}
		}
		switch {
		case cleared:
			j.Cleared++
		case x.ClearDue:
			j.MissedClears++
			if len(j.Lines) < maxListed {
				j.Lines = append(j.Lines, fmt.Sprintf("%s: the raise for the entry at %.0f s was not cleared within %.0f s of the exit at %.0f s", x.Aircraft, x.EntryS, clearWithin, x.ExitS))
			}
		}
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for i := range by[n] {
			if e := &by[n][i]; e.Phase == observe.PhaseRaised && !used[e.AlertID] {
				j.Unexpected++
				if len(j.Lines) < maxListed {
					j.Lines = append(j.Lines, fmt.Sprintf("%s: a zone raise at %.1f s outside every entry", n, rel(e.ObservedAt)))
				}
			}
		}
	}
	return j
}

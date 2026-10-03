package vehicle

import (
	"fmt"
	"math"
	"time"

	"github.com/rootxkit/uspace-core/core"
)

// Plan actions: what sim/fly.py flies and what the synthetic vehicle
// models. The names and members are fly.py's (its docstring).
const (
	ActionArm     = "arm"
	ActionTakeoff = "takeoff"
	ActionGoto    = "goto"
	ActionHold    = "hold"
	ActionLand    = "land"
)

// Plan is one vehicle's flight, in the shape sim/fly.py reads.
type Plan struct {
	Sysid   int        `json:"sysid"`
	T0UnixS float64    `json:"t0_unix_s"`
	Steps   []PlanStep `json:"steps"`
}

// PlanStep is one step. AtS delays it to T0 + AtS; zero members of other
// actions are left out of the JSON.
type PlanStep struct {
	AtS        *float64 `json:"at_s,omitempty"`
	Action     string   `json:"action"`
	LatDeg     float64  `json:"lat_deg,omitempty"`
	LonDeg     float64  `json:"lon_deg,omitempty"`
	AltRelM    float64  `json:"alt_rel_m,omitempty"`
	SpeedMS    float64  `json:"speed_ms,omitempty"`
	ToleranceM float64  `json:"tolerance_m,omitempty"`
	ForS       float64  `json:"for_s,omitempty"`
	// ID is the scenario step this plan step comes from; not sent to
	// fly.py (it ignores unknown members, but the runner keeps its own
	// index instead).
	ID string `json:"-"`
}

// T0 is the plan's start.
func (p *Plan) T0() time.Time {
	sec, frac := math.Modf(p.T0UnixS)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// Validate refuses what sim/fly.py's validate_plan refuses.
func (p *Plan) Validate() error {
	if p.Sysid < 1 || p.Sysid > 254 {
		return core.Fieldf("sysid", "%d is not 1..254", p.Sysid)
	}
	if len(p.Steps) == 0 || len(p.Steps) > 1000 {
		return core.Fieldf("steps", "%d steps, want 1..1000", len(p.Steps))
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		f := fmt.Sprintf("steps[%d]", i)
		if s.AtS != nil && (!core.IsFinite(*s.AtS) || *s.AtS < 0) {
			return core.Fieldf(f+".at_s", "must be >= 0")
		}
		switch s.Action {
		case ActionArm, ActionLand:
		case ActionTakeoff:
			if !(s.AltRelM > 0) || !core.IsFinite(s.AltRelM) {
				return core.Fieldf(f+".alt_rel_m", "takeoff needs alt_rel_m > 0")
			}
		case ActionGoto:
			if !core.IsFinite(s.LatDeg) || !core.IsFinite(s.LonDeg) || s.LatDeg < -90 || s.LatDeg > 90 || s.LonDeg < -180 || s.LonDeg > 180 {
				return core.Fieldf(f, "goto position out of range")
			}
			if !core.IsFinite(s.AltRelM) || s.SpeedMS < 0 || !core.IsFinite(s.SpeedMS) {
				return core.Fieldf(f, "goto needs a finite alt_rel_m and speed_ms >= 0")
			}
		case ActionHold:
			if !(s.ForS > 0) || !core.IsFinite(s.ForS) {
				return core.Fieldf(f+".for_s", "hold needs for_s > 0")
			}
		default:
			return core.Fieldf(f+".action", "%q is not arm, takeoff, goto, hold or land", short(s.Action))
		}
	}
	return nil
}

// StepEvent is a step's progress as the vehicle confirmed it: one line of
// sim/fly.py's stdout, or the synthetic vehicle's equivalent. Observed is
// what the vehicle reported, never what was commanded (E-04, E-08).
type StepEvent struct {
	Sysid    int            `json:"sysid"`
	Step     int            `json:"step"`
	Action   string         `json:"action"`
	State    string         `json:"state"` // started, confirmed, failed
	AtUnixS  float64        `json:"at_unix_s"`
	Observed map[string]any `json:"observed,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	// MaxDriftM is set on a confirmed hold.
	MaxDriftM *float64 `json:"max_drift_m,omitempty"`
}

// Step event states.
const (
	StepStarted   = "started"
	StepConfirmed = "confirmed"
	StepFailed    = "failed"
)

// At is the event's time.
func (e *StepEvent) At() time.Time {
	sec, frac := math.Modf(e.AtUnixS)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// UnixS is t as fractional Unix seconds.
func UnixS(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

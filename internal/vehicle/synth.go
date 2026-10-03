package vehicle

import (
	"math"
	"math/rand/v2"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"
)

// Home is where a vehicle starts: its position and ground AMSL.
type Home struct {
	LatDeg, LonDeg, AltAMSLM float64
}

// SynthOptions are the synthetic vehicle's dynamics. The defaults are
// ArduCopter's (WPNAV_SPEED 10 m/s, WPNAV_ACCEL 2.5 m/s^2, WPNAV_SPEED_UP
// 2.5 m/s, LAND_SPEED about 1.5 m/s above the final stage): close enough
// that a scenario flown synthetically and in SITL raise the same alerts,
// which the SITL runs then confirm.
type SynthOptions struct {
	SpeedMS       float64 // default horizontal speed when a goto names none
	AccelMS2      float64
	ClimbMS       float64
	DescentMS     float64
	VelNoiseMS    float64 // standard deviation of the reported velocity (C-03: SITL hover noise)
	UTCUnknownFor time.Duration
	Seed          uint64
	// MarkAltInvalid is S-36, as sim/mav_reader.py --mark-alt-invalid.
	MarkAltInvalid bool
}

// DefaultSynthOptions are the ArduCopter defaults with 3 cm/s velocity
// noise.
func DefaultSynthOptions() SynthOptions {
	return SynthOptions{SpeedMS: 10, AccelMS2: 2.5, ClimbMS: 2.5, DescentMS: 1.5, VelNoiseMS: 0.03}
}

// Synth is a kinematic multicopter that flies a Plan and reports what a
// SITL vehicle read by sim/mav_reader.py would: the CI-friendly stand-in
// for SITL plus sim/fly.py. It is not a flight model; it exists so the
// runner, the simulators and the assertions run end to end without
// ArduPilot, and every scenario is also run against SITL before its
// result counts as SITL evidence (the result file names the mode).
//
// Like SITL, it reports alt_hae_m equal to alt_amsl_m (no geoid) and a
// pressure altitude equal to the AMSL altitude (a standard day).
type Synth struct {
	sysid int
	home  Home
	plan  Plan
	opt   SynthOptions
	rng   *rand.Rand

	started  bool
	bootAt   time.Time
	last     time.Time
	pos      core.LatLon
	heightM  float64
	vn, ve   float64
	vz       float64 // up
	armed    bool
	step     int
	stepFrom time.Time
	stepOn   bool
	done     bool
	failed   bool
	events   []StepEvent
}

// NewSynth makes a vehicle on the ground at home, disarmed.
func NewSynth(home Home, plan Plan, opt SynthOptions) *Synth {
	d := DefaultSynthOptions()
	if opt.SpeedMS <= 0 {
		opt.SpeedMS = d.SpeedMS
	}
	if opt.AccelMS2 <= 0 {
		opt.AccelMS2 = d.AccelMS2
	}
	if opt.ClimbMS <= 0 {
		opt.ClimbMS = d.ClimbMS
	}
	if opt.DescentMS <= 0 {
		opt.DescentMS = d.DescentMS
	}
	seed := opt.Seed
	if seed == 0 {
		seed = uint64(plan.Sysid)
	}
	return &Synth{
		sysid: plan.Sysid,
		home:  home,
		plan:  plan,
		opt:   opt,
		rng:   rand.New(rand.NewPCG(seed, 0x5eed)), //nolint:gosec // simulation noise, not security
		pos:   core.LatLon{LatDeg: home.LatDeg, LonDeg: home.LonDeg},
	}
}

// Done reports whether every step is confirmed (or one failed).
func (s *Synth) Done() bool { return s.done || s.failed }

// Failed reports whether a step could not be flown.
func (s *Synth) Failed() bool { return s.failed }

// Step advances the vehicle to now and returns its sample at now and the
// step events that happened since the last call.
func (s *Synth) Step(now time.Time) (Sample, []StepEvent) {
	if !s.started {
		s.started = true
		s.bootAt = now
		s.last = now
	}
	dt := now.Sub(s.last).Seconds()
	if dt < 0 {
		dt = 0
	}
	// Integrate in slices of at most 100 ms so a late tick does not
	// overshoot a target.
	for dt > 0 {
		h := math.Min(dt, 0.1)
		s.last = s.last.Add(time.Duration(h * float64(time.Second)))
		s.advance(h)
		dt -= h
	}
	s.last = now
	ev := s.events
	s.events = nil
	return s.sample(now), ev
}

func (s *Synth) emit(state, reason string, extra *float64) {
	e := StepEvent{Sysid: s.sysid, Step: s.step, State: state, AtUnixS: UnixS(s.last), Reason: reason, MaxDriftM: extra}
	if s.step < len(s.plan.Steps) {
		e.Action = s.plan.Steps[s.step].Action
	}
	if state != StepStarted {
		e.Observed = map[string]any{
			"armed": s.armed, "lat_deg": s.pos.LatDeg, "lon_deg": s.pos.LonDeg, "alt_rel_m": s.heightM,
		}
	}
	s.events = append(s.events, e)
}

func (s *Synth) advance(h float64) {
	tvn, tve, tvz := s.control()
	// First-order approach to the target velocity within the accel limit.
	s.vn = approach(s.vn, tvn, s.opt.AccelMS2*h)
	s.ve = approach(s.ve, tve, s.opt.AccelMS2*h)
	s.vz = approach(s.vz, tvz, 2*s.opt.AccelMS2*h)
	if !s.armed {
		s.vn, s.ve, s.vz = 0, 0, 0
	}
	if d := math.Hypot(s.vn, s.ve) * h; d > 0 {
		bearing := math.Mod(math.Atan2(s.ve, s.vn)*180/math.Pi+360, 360)
		s.pos = geodesy.Destination(s.pos, bearing, d)
	}
	s.heightM += s.vz * h
	if s.heightM < 0 {
		s.heightM = 0
		if s.vz < 0 {
			s.vz = 0
		}
	}
}

// control runs the current step and returns the target velocity (north,
// east, up).
func (s *Synth) control() (tvn, tve, tvz float64) {
	if s.done || s.failed || s.step >= len(s.plan.Steps) {
		return 0, 0, 0
	}
	st := &s.plan.Steps[s.step]
	if !s.stepOn {
		if st.AtS != nil && s.last.Before(s.plan.T0().Add(time.Duration(*st.AtS*float64(time.Second)))) {
			return 0, 0, 0
		}
		s.stepOn = true
		s.stepFrom = s.last
		s.emit(StepStarted, "", nil)
	}
	{
		switch st.Action {
		case ActionArm:
			s.armed = true
			s.confirm(nil)
		case ActionTakeoff:
			if !s.armed {
				s.fail("takeoff: the vehicle reports it is not armed")
				break
			}
			tvz = s.opt.ClimbMS
			if s.heightM >= st.AltRelM*0.95 {
				s.confirm(nil)
			}
		case ActionGoto:
			target := core.LatLon{LatDeg: st.LatDeg, LonDeg: st.LonDeg}
			north, east := geodesy.LocalOffsetM(s.pos, target)
			dist := math.Hypot(north, east)
			speed := st.SpeedMS
			if speed <= 0 {
				speed = s.opt.SpeedMS
			}
			// Brake to arrive: v <= sqrt(2 a d).
			v := math.Min(speed, math.Sqrt(2*s.opt.AccelMS2*dist))
			if dist > 1e-6 {
				tvn, tve = v*north/dist, v*east/dist
			}
			dh := st.AltRelM - s.heightM
			tvz = clamp(dh, -s.opt.DescentMS, s.opt.ClimbMS)
			tol := st.ToleranceM
			if tol <= 0 {
				tol = 3
			}
			if dist <= tol && math.Abs(dh) <= math.Max(1, tol) {
				s.confirm(nil)
			}
		case ActionHold:
			if s.last.Sub(s.stepFrom).Seconds() >= st.ForS {
				zero := 0.0
				s.confirm(&zero)
			}
		case ActionLand:
			tvz = -s.opt.DescentMS
			if s.heightM <= 0 {
				s.armed = false
				s.confirm(nil)
			}
		}
	}
	return tvn, tve, tvz
}

func (s *Synth) confirm(drift *float64) {
	s.emit(StepConfirmed, "", drift)
	s.step++
	s.stepOn = false
	if s.step >= len(s.plan.Steps) {
		s.done = true
	}
}

func (s *Synth) fail(reason string) {
	s.emit(StepFailed, reason, nil)
	s.failed = true
}

func (s *Synth) sample(now time.Time) Sample {
	n := func() float64 {
		if s.opt.VelNoiseMS <= 0 || !s.armed {
			return 0
		}
		return s.rng.NormFloat64() * s.opt.VelNoiseMS
	}
	vn, ve := s.vn+n(), s.ve+n()
	amsl := s.home.AltAMSLM + s.heightM
	out := Sample{
		Schema:         Schema,
		Sysid:          s.sysid,
		TimeBootMS:     uint32(now.Sub(s.bootAt).Milliseconds() + 10_000),
		LatDeg:         s.pos.LatDeg,
		LonDeg:         s.pos.LonDeg,
		AltAMSLM:       amsl,
		AltHAEM:        F(amsl),
		AltPressureM:   F(amsl),
		HeightTakeoffM: s.heightM,
		SpeedMS:        math.Hypot(vn, ve),
		TrackDeg:       TrackFrom(vn, ve, 0.5),
		VSpeedMS:       s.vz,
		VNMS:           vn,
		VEMS:           ve,
		Armed:          s.armed,
		Status:         StatusGround,
		FixOK:          true,
		AltInvalid:     s.opt.MarkAltInvalid,
	}
	if s.armed {
		out.Status = StatusAirborne
	}
	if now.Sub(s.bootAt) >= s.opt.UTCUnknownFor {
		ts := FormatTime(now)
		out.TS = &ts
	}
	return out
}

func approach(v, target, maxStep float64) float64 {
	switch {
	case target > v+maxStep:
		return v + maxStep
	case target < v-maxStep:
		return v - maxStep
	default:
		return target
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

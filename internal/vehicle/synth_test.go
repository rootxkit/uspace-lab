package vehicle

import (
	"math"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"
)

var home = Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}

func at(s float64) *float64 { return &s }

func fly(t *testing.T, v *Synth, from time.Time, d time.Duration) ([]Sample, []StepEvent) {
	t.Helper()
	var ss []Sample
	var ev []StepEvent
	for tick := time.Duration(0); tick <= d; tick += 250 * time.Millisecond {
		s, e := v.Step(from.Add(tick))
		ss = append(ss, s)
		ev = append(ev, e...)
		if v.Done() {
			break
		}
	}
	return ss, ev
}

func TestSynthFliesAPlanAndConfirmsEachStep(t *testing.T) {
	target := geodesy.Destination(core.LatLon{LatDeg: home.LatDeg, LonDeg: home.LonDeg}, 0, 100)
	p := Plan{Sysid: 1, T0UnixS: 1_790_000_000, Steps: []PlanStep{
		{Action: ActionArm},
		{Action: ActionTakeoff, AltRelM: 30},
		{Action: ActionGoto, LatDeg: target.LatDeg, LonDeg: target.LonDeg, AltRelM: 30, SpeedMS: 10, ToleranceM: 2},
		{Action: ActionHold, ForS: 5},
		{Action: ActionLand},
	}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	v := NewSynth(home, p, SynthOptions{})
	ss, ev := fly(t, v, p.T0(), 5*time.Minute)
	if !v.Done() || v.Failed() {
		t.Fatalf("not done: %+v", ev)
	}
	var confirmed []string
	for _, e := range ev {
		if e.State == StepConfirmed {
			confirmed = append(confirmed, e.Action)
		}
	}
	if len(confirmed) != 5 {
		t.Fatalf("confirmed %v", confirmed)
	}
	last := ss[len(ss)-1]
	d, _ := geodesy.DistanceM(last.Pos(), target)
	if d > 3 || last.Armed || last.HeightTakeoffM != 0 {
		t.Fatalf("landed %0.1f m from the target, armed %v, height %v", d, last.Armed, last.HeightTakeoffM)
	}
	// It flew at the commanded speed, not faster.
	maxSpeed := 0.0
	for _, s := range ss {
		maxSpeed = math.Max(maxSpeed, math.Hypot(s.VNMS, s.VEMS))
		if s.AltAMSLM != home.AltAMSLM+s.HeightTakeoffM || *s.AltHAEM != s.AltAMSLM {
			t.Fatal("AMSL is not home + height, or HAE is not AMSL (SITL's behaviour)")
		}
	}
	if maxSpeed > 10.2 || maxSpeed < 9 {
		t.Fatalf("max speed %.2f m/s, want about 10", maxSpeed)
	}
}

func TestSynthWaitsForAtS(t *testing.T) {
	p := Plan{Sysid: 2, T0UnixS: 1_790_000_000, Steps: []PlanStep{{Action: ActionArm, AtS: at(10)}}}
	v := NewSynth(home, p, SynthOptions{})
	_, ev := fly(t, v, p.T0(), 9*time.Second)
	if len(ev) != 0 {
		t.Fatalf("a step started before its at_s: %+v", ev)
	}
	_, ev = fly(t, v, p.T0().Add(9*time.Second), 2*time.Second)
	if len(ev) != 2 || ev[1].State != StepConfirmed || ev[1].At().Sub(p.T0()) < 10*time.Second {
		t.Fatalf("events %+v", ev)
	}
}

func TestSynthRefusesTakeoffWhenDisarmed(t *testing.T) {
	p := Plan{Sysid: 3, Steps: []PlanStep{{Action: ActionTakeoff, AltRelM: 10}}}
	v := NewSynth(home, p, SynthOptions{})
	_, ev := fly(t, v, time.Unix(1_790_000_000, 0), time.Second)
	if !v.Failed() || ev[len(ev)-1].State != StepFailed {
		t.Fatalf("events %+v", ev)
	}
}

func TestSynthHoverHasVelocityNoiseButStays(t *testing.T) {
	p := Plan{Sysid: 5, Steps: []PlanStep{{Action: ActionArm}, {Action: ActionTakeoff, AltRelM: 30}, {Action: ActionHold, ForS: 30}}}
	v := NewSynth(home, p, DefaultSynthOptions())
	ss, _ := fly(t, v, time.Unix(1_790_000_000, 0), time.Minute)
	noisy := false
	for _, s := range ss[len(ss)-20:] {
		if s.VNMS != 0 || s.VEMS != 0 {
			noisy = true
		}
		if math.Hypot(s.VNMS, s.VEMS) > 0.2 {
			t.Fatalf("hover noise %.3f m/s", math.Hypot(s.VNMS, s.VEMS))
		}
	}
	if !noisy {
		t.Fatal("no velocity noise in the hover (C-03 needs it)")
	}
}

func TestSynthUnknownUTC(t *testing.T) {
	p := Plan{Sysid: 6, Steps: []PlanStep{{Action: ActionArm}}}
	v := NewSynth(home, p, SynthOptions{UTCUnknownFor: 2 * time.Second})
	t0 := time.Unix(1_790_000_000, 0)
	s, _ := v.Step(t0)
	if s.TS != nil {
		t.Fatal("ts before UTC is known")
	}
	s, _ = v.Step(t0.Add(3 * time.Second))
	if s.TS == nil {
		t.Fatal("no ts after UTC is known")
	}
}

func TestPlanValidate(t *testing.T) {
	bad := []Plan{
		{Sysid: 0, Steps: []PlanStep{{Action: ActionArm}}},
		{Sysid: 1},
		{Sysid: 1, Steps: []PlanStep{{Action: "loop"}}},
		{Sysid: 1, Steps: []PlanStep{{Action: ActionTakeoff}}},
		{Sysid: 1, Steps: []PlanStep{{Action: ActionGoto, LatDeg: 91}}},
		{Sysid: 1, Steps: []PlanStep{{Action: ActionHold}}},
		{Sysid: 1, Steps: []PlanStep{{Action: ActionArm, AtS: at(-1)}}},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("plan %d accepted", i)
		}
	}
}

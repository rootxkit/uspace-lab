package main

import (
	"strings"
	"testing"
	"time"
)

func held(v bool) *bool { return &v }

func at(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

// row of samples: during from 1 s to 5 s, after from 7 s on.
func samplesWith(during func(i int) (bool, map[string]Readiness), after func(i int) (bool, map[string]Readiness)) []Sample {
	var out []Sample
	for i := 1; i <= 5; i++ {
		h, r := during(i)
		out = append(out, Sample{At: at(float64(i)), Phase: phaseDuring, FaultHeld: held(h), Ready: r, FaultNote: map[bool]string{true: "", false: "ussp-monitor is running"}[h]})
	}
	for i := 7; i <= 9; i++ {
		ok, r := after(i)
		out = append(out, Sample{At: at(float64(i)), Phase: phaseAfter, Restored: held(ok), Ready: r})
	}
	return out
}

func ready(code int, checks map[string]string) Readiness {
	return Readiness{At: t0, Code: code, Checks: checks}
}

func TestAFaultThatNeverHappenedFailsItsRow(t *testing.T) {
	// The script said it killed the monitor; docker kept saying it ran.
	s := samplesWith(func(int) (bool, map[string]Readiness) { return false, nil }, func(int) (bool, map[string]Readiness) { return true, nil })
	fr := judgeFault(FaultSpec{Kind: kindDown}, t0, at(6), s)
	fails := faultFailures(fr)
	if fr.Observed || len(fails) == 0 || !strings.Contains(fails[0], "never observed") {
		t.Fatalf("%+v %v", fr, fails)
	}
	if !strings.Contains(fails[0], "ussp-monitor is running") {
		t.Fatalf("the failure must say what was seen instead: %v", fails)
	}
}

func TestAFaultThatHappenedPasses(t *testing.T) {
	s := samplesWith(func(int) (bool, map[string]Readiness) { return true, nil }, func(i int) (bool, map[string]Readiness) { return i >= 8, nil })
	fr := judgeFault(FaultSpec{Kind: kindDown}, t0, at(6), s)
	if fails := faultFailures(fr); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
	if !fr.Observed || *fr.FirstHeldAfterS != 1 || fr.HeldSamples != 5 || !fr.HeldAtEnd || *fr.RestoredAfterS != 2 {
		t.Fatalf("%+v", fr)
	}
}

func TestAFaultThatUndidItselfOrWasNeverRestoredFails(t *testing.T) {
	// Held at first, gone before the restore (something restarted it).
	s := samplesWith(func(i int) (bool, map[string]Readiness) { return i < 3, nil }, func(int) (bool, map[string]Readiness) { return true, nil })
	if fails := faultFailures(judgeFault(FaultSpec{Kind: kindDown}, t0, at(6), s)); len(fails) != 1 || !strings.Contains(fails[0], "not in place at the end") {
		t.Fatalf("%v", fails)
	}
	// Held, never restored.
	s = samplesWith(func(int) (bool, map[string]Readiness) { return true, nil }, func(int) (bool, map[string]Readiness) { return false, nil })
	if fails := faultFailures(judgeFault(FaultSpec{Kind: kindDown}, t0, at(6), s)); len(fails) != 1 || !strings.Contains(fails[0], "never observed restored") {
		t.Fatalf("%v", fails)
	}
	// No sample at all during the hold.
	if fails := faultFailures(judgeFault(FaultSpec{Kind: kindDown}, t0, at(6), nil)); len(fails) == 0 {
		t.Fatal("no samples passed")
	}
}

func TestReadyAlwaysAndLostBothWays(t *testing.T) {
	up := func(int) (bool, map[string]Readiness) {
		return true, map[string]Readiness{"ussp": ready(200, nil), "cisp": ready(200, nil)}
	}
	flaps := func(i int) (bool, map[string]Readiness) {
		c := 200
		if i == 3 {
			c = 503
		}
		return true, map[string]Readiness{"ussp": ready(c, nil), "cisp": ready(200, nil)}
	}
	always := Expect{System: StringList{"ussp", "cisp"}, Ready: readyAlways}
	if r := judgeExpect(always, phaseDuring, []string{"cisp", "ussp"}, t0, samplesWith(up, up), nil, nil); !r.Pass {
		t.Fatal(r.Observed)
	}
	if r := judgeExpect(always, phaseDuring, []string{"cisp", "ussp"}, t0, samplesWith(flaps, up), nil, nil); r.Pass || !strings.Contains(r.Observed, "ussp not ready at +3.0s") {
		t.Fatalf("one bad sample must fail always: %s", r.Observed)
	}
	lost := Expect{System: StringList{"ussp"}, Ready: readyLost, WithinS: 4}
	if r := judgeExpect(lost, phaseDuring, []string{"ussp"}, t0, samplesWith(flaps, up), nil, nil); !r.Pass || r.AfterS["ussp"] != 3 {
		t.Fatalf("%+v", r)
	}
	if r := judgeExpect(lost, phaseDuring, []string{"ussp"}, t0, samplesWith(up, up), nil, nil); r.Pass {
		t.Fatal("never lost passed")
	}
	// Lost, but only after the bound.
	late := Expect{System: StringList{"ussp"}, Ready: readyLost, WithinS: 2}
	if r := judgeExpect(late, phaseDuring, []string{"ussp"}, t0, samplesWith(flaps, up), nil, nil); r.Pass {
		t.Fatal("a loss after within_s passed")
	}
}

func TestCheckAndRecoveredBothWays(t *testing.T) {
	natsDown := func(i int) (bool, map[string]Readiness) {
		st := "ok"
		if i >= 2 {
			st = "down"
		}
		return true, map[string]Readiness{"ussp": ready(200, map[string]string{"nats": st})}
	}
	natsOK := func(int) (bool, map[string]Readiness) {
		return true, map[string]Readiness{"ussp": ready(200, map[string]string{"nats": "ok"})}
	}
	e := Expect{System: StringList{"ussp"}, Check: "nats", In: []string{"down", "degraded"}, WithinS: 10}
	if r := judgeExpect(e, phaseDuring, []string{"ussp"}, t0, samplesWith(natsDown, natsOK), nil, nil); !r.Pass || r.AfterS["ussp"] != 2 {
		t.Fatalf("%+v", r)
	}
	if r := judgeExpect(e, phaseDuring, []string{"ussp"}, t0, samplesWith(natsOK, natsOK), nil, nil); r.Pass {
		t.Fatal("a check that never left ok passed")
	}
	base := &Sample{Ready: map[string]Readiness{"ussp": ready(200, map[string]string{"nats": "ok"})}}
	rec := Expect{System: StringList{"ussp"}, Recovered: true, WithinS: 5}
	if r := judgeExpect(rec, phaseAfter, []string{"ussp"}, at(6), samplesWith(natsDown, natsOK), base, nil); !r.Pass || r.AfterS["ussp"] != 1 {
		t.Fatalf("%+v", r)
	}
	if r := judgeExpect(rec, phaseAfter, []string{"ussp"}, at(6), samplesWith(natsDown, natsDown), base, nil); r.Pass {
		t.Fatal("never recovered passed")
	}
}

func TestStatusExpectationsBothWays(t *testing.T) {
	frames := map[string][]StatusFrame{"authority": {
		{At: at(1), Degraded: []string{}, NATS: "connected", Sources: map[string]string{"ansp_feed/ansp": "live"}},
		{At: at(3), Degraded: []string{"nats_down"}, NATS: "disconnected", Sources: map[string]string{"ansp_feed/ansp": "stale"}},
	}}
	cases := []struct {
		e    Expect
		pass bool
	}{
		{Expect{System: StringList{"authority"}, Status: "nats_down", WithinS: 5}, true},
		{Expect{System: StringList{"authority"}, Status: "nats_down", WithinS: 2}, false},
		{Expect{System: StringList{"authority"}, Status: "cis_stale", WithinS: 5}, false},
		{Expect{System: StringList{"authority"}, StatusNot: "cis_stale"}, true},
		{Expect{System: StringList{"authority"}, StatusNot: "nats_down"}, false},
		{Expect{System: StringList{"authority"}, Source: "ansp_feed/ansp", In: []string{"stale"}, WithinS: 5}, true},
		{Expect{System: StringList{"authority"}, Source: "ansp_feed/ansp", In: []string{"down"}, WithinS: 5}, false},
		{Expect{System: StringList{"authority"}, Field: "nats", In: []string{"disconnected"}, WithinS: 5}, true},
		{Expect{System: StringList{"authority"}, Field: "nats", In: []string{"closed"}, WithinS: 5}, false},
		// A system without a console stream cannot pass a status claim.
		{Expect{System: StringList{"cisp"}, Field: "nats", In: []string{"connected"}, WithinS: 5}, false},
	}
	for i, c := range cases {
		sys := c.e.System
		r := judgeExpect(c.e, phaseDuring, sys, t0, nil, nil, frames)
		if r.Pass != c.pass {
			t.Errorf("case %d (%s): pass %v, want %v: %s", i, c.e.label(), r.Pass, c.pass, r.Observed)
		}
	}
}

func TestNotInBothWays(t *testing.T) {
	frames := map[string][]StatusFrame{"authority": {{At: at(1), NATS: "connected"}, {At: at(3), NATS: "unavailable"}}}
	e := Expect{System: StringList{"authority"}, Field: "nats", NotIn: []string{"connected"}, WithinS: 5}
	if r := judgeExpect(e, phaseDuring, e.System, t0, nil, nil, frames); !r.Pass || r.AfterS["authority"] != 3 {
		t.Fatalf("%+v", r)
	}
	still := map[string][]StatusFrame{"authority": {{At: at(1), NATS: "connected"}, {At: at(3), NATS: "connected"}}}
	if r := judgeExpect(e, phaseDuring, e.System, t0, nil, nil, still); r.Pass {
		t.Fatal("a bus that stayed connected passed")
	}
	// An absent check is not "anything but ok".
	c := Expect{System: StringList{"cisp"}, Check: "nats", NotIn: []string{"ok"}, WithinS: 10}
	none := func(int) (bool, map[string]Readiness) {
		return true, map[string]Readiness{"cisp": ready(200, map[string]string{})}
	}
	if r := judgeExpect(c, phaseDuring, c.System, t0, samplesWith(none, none), nil, nil); r.Pass {
		t.Fatal("an absent check passed not_in")
	}
}

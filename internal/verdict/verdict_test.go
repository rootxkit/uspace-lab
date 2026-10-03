package verdict

import (
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func at(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

func ev(id, phase string, s float64, reason string) observe.Event {
	return observe.Event{System: "ussp", Kind: "proximity", AlertID: id, Phase: phase, Aircraft: "a", Peer: "b", ObservedAt: at(s), Reason: reason}
}

func sc(expect ...scenario.Expect) *scenario.Scenario {
	return &scenario.Scenario{Expect: expect, JudgedKinds: []string{"ussp:proximity"}}
}

func exp(raiseAfter string, rmin, rmax float64, clr *scenario.Window) scenario.Expect {
	return scenario.Expect{Name: "x", Matcher: scenario.Matcher{System: "ussp", Kind: "proximity", Aircraft: "a", Peer: "b"},
		Raise: scenario.Window{After: raiseAfter, MinS: rmin, MaxS: rmax}, Clear: clr}
}

func TestRaiseAndClearInsideTheirWindows(t *testing.T) {
	o := Evaluate(sc(exp("t0", 0, 10, &scenario.Window{After: "t0", MinS: 20, MaxS: 40, Reason: "resolved"})),
		Marks{"t0": t0}, []observe.Event{ev("1", "raised", 5, ""), ev("1", "cleared", 30, "resolved")})
	if !o.Pass || o.Missed != 0 || o.False != 0 {
		t.Fatalf("%+v", o)
	}
}

func TestMissNamesWhatWasSeen(t *testing.T) {
	o := Evaluate(sc(exp("t0", 0, 10, nil)), Marks{"t0": t0}, []observe.Event{ev("1", "raised", 50, "")})
	if o.Pass || o.Missed != 1 || len(o.Expectations[0].Seen) != 1 || !strings.Contains(o.Failures[1], "no ussp proximity raise") {
		t.Fatalf("%+v", o)
	}
	// The unclaimed raise is a false alert too.
	if o.False != 1 {
		t.Fatalf("false %d", o.False)
	}
}

func TestClearOutsideWindowOrWrongReason(t *testing.T) {
	w := &scenario.Window{After: "t0", MinS: 20, MaxS: 40, Reason: "resolved"}
	o := Evaluate(sc(exp("t0", 0, 10, w)), Marks{"t0": t0}, []observe.Event{ev("1", "raised", 5, ""), ev("1", "cleared", 45, "resolved")})
	if o.Pass || !strings.Contains(strings.Join(o.Failures, "|"), "outside") {
		t.Fatalf("%+v", o.Failures)
	}
	o = Evaluate(sc(exp("t0", 0, 10, w)), Marks{"t0": t0}, []observe.Event{ev("1", "raised", 5, ""), ev("1", "cleared", 30, "stale")})
	if o.Pass || !strings.Contains(strings.Join(o.Failures, "|"), "want resolved") {
		t.Fatalf("%+v", o.Failures)
	}
	o = Evaluate(sc(exp("t0", 0, 10, w)), Marks{"t0": t0}, []observe.Event{ev("1", "raised", 5, "")})
	if o.Pass || !strings.Contains(strings.Join(o.Failures, "|"), "never cleared") {
		t.Fatalf("%+v", o.Failures)
	}
}

func TestHoldUntil(t *testing.T) {
	e := exp("t0", 0, 10, nil)
	e.HoldUntil = &scenario.Window{After: "hover", MinS: 0, MaxS: 0}
	marks := Marks{"t0": t0, "hover": at(40)}
	early := Evaluate(sc(e), marks, []observe.Event{ev("1", "raised", 5, ""), ev("1", "cleared", 20, "resolved")})
	if early.Pass || !strings.Contains(strings.Join(early.Failures, "|"), "before the hold ended") {
		t.Fatalf("a clear during the hold passed: %+v", early.Failures)
	}
	held := Evaluate(sc(e), marks, []observe.Event{ev("1", "raised", 5, ""), ev("1", "cleared", 41, "resolved")})
	if !held.Pass {
		t.Fatalf("%+v", held.Failures)
	}
}

func TestAMarkThatNeverHappenedFails(t *testing.T) {
	o := Evaluate(sc(exp("climb", 0, 10, nil)), Marks{"t0": t0}, []observe.Event{ev("1", "raised", 5, "")})
	if o.Pass || !strings.Contains(strings.Join(o.Failures, "|"), "never happened") {
		t.Fatalf("%+v", o.Failures)
	}
}

func TestNeverAndClaims(t *testing.T) {
	s := sc(exp("t0", 0, 10, nil))
	s.Never = []scenario.Matcher{{System: "ussp", Kind: "proximity", Aircraft: "c"}}
	evs := []observe.Event{ev("1", "raised", 5, "")}
	c := observe.Event{System: "ussp", Kind: "proximity", AlertID: "2", Phase: "raised", Aircraft: "c", ObservedAt: at(6)}
	o := Evaluate(s, Marks{"t0": t0}, append(evs, c))
	if o.Pass || len(o.Never[0].Violations) != 1 {
		t.Fatalf("%+v", o)
	}
	// Absence twin: without c's raise the never-list is clean.
	o = Evaluate(s, Marks{"t0": t0}, evs)
	if !o.Pass {
		t.Fatalf("%+v", o.Failures)
	}
}

func TestStatusSlugsAreNotFalseAlertsUnlessJudged(t *testing.T) {
	s := &scenario.Scenario{Expect: []scenario.Expect{{Name: "d", Matcher: scenario.Matcher{System: "authority", Kind: observe.KindDegraded, Subject: "x"},
		Raise: scenario.Window{After: "t0", MinS: -10, MaxS: 10}}}}
	evs := []observe.Event{
		{System: "authority", Kind: observe.KindDegraded, AlertID: "x", Phase: "raised", Subject: "x", ObservedAt: at(0)},
		{System: "authority", Kind: observe.KindDegraded, AlertID: "y", Phase: "raised", Subject: "y", ObservedAt: at(0)},
	}
	if o := Evaluate(s, Marks{"t0": t0}, evs); !o.Pass {
		t.Fatalf("%+v", o.Failures)
	}
	s.JudgedKinds = []string{"authority:degraded"}
	if o := Evaluate(s, Marks{"t0": t0}, evs); o.Pass || o.False != 1 {
		t.Fatalf("an unexpected judged slug passed: %+v", o)
	}
}

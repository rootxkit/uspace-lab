package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

var bgCfg = Background{Systems: []string{"ussp", "authority"}, Kind: "zone_incursion", Subject: "Z", StaleReasons: []string{"stale", "source_disabled"}}

func ev(sys, id, phase, reason string, s float64) alertEvent {
	return alertEvent{System: sys, AlertID: id, Phase: phase, Reason: reason, At: at(s)}
}

// The standing alert raised at 0 s in both systems; one row from 10 s to
// 40 s; the aircraft leaves at 100 s.
var baseEvents = []alertEvent{ev("ussp", "u1", observe.PhaseRaised, "", 0), ev("authority", "a1", observe.PhaseRaised, "", 0)}

func rowWindow(modes map[string]string) []window {
	return []window{{row: "r", from: at(10), to: at(40), restored: at(30), modes: modes}}
}

// findings judges a row, leaving out the exit (TestTheExitIsJudgedOnEveryEvent).
func findings(evs []alertEvent, modes map[string]string) []AlertFinding {
	var out []AlertFinding
	for _, f := range judgeAlerts(append(append([]alertEvent(nil), baseEvents...), evs...), bgCfg, at(100), at(200), rowWindow(modes)) {
		if f.What != "not_cleared_at_exit" && f.What != "not_open_at_exit" {
			out = append(out, f)
		}
	}
	return out
}

// rowOnly leaves out the exit checks.
func rowOnly(fs []AlertFinding) []AlertFinding {
	var out []AlertFinding
	for _, f := range fs {
		if f.What != "not_cleared_at_exit" && f.What != "not_open_at_exit" {
			out = append(out, f)
		}
	}
	return out
}

func failed(fs []AlertFinding) []AlertFinding {
	var out []AlertFinding
	for _, f := range fs {
		if !f.Allowed {
			out = append(out, f)
		}
	}
	return out
}

func TestAKeptAlertThatStaysOpenHasNoFinding(t *testing.T) {
	// Re-delivered frames of the same alert after a reconnect are
	// updates to the recorder, not events: nothing to judge.
	if fs := findings(nil, nil); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestAClearDuringAKeptRowIsTheAlertLost(t *testing.T) {
	fs := failed(findings([]alertEvent{ev("ussp", "u1", observe.PhaseCleared, "stale", 20), ev("ussp", "u2", observe.PhaseRaised, "", 35)}, nil))
	if len(fs) != 2 || fs[0].What != "cleared" || fs[0].Row != "r" || fs[1].What != "re_raised" {
		t.Fatalf("%+v", fs)
	}
}

func TestAStaleClearIsAllowedOnlyInAStaleOKRowAndOnlyAsStale(t *testing.T) {
	stale := []alertEvent{ev("ussp", "u1", observe.PhaseCleared, "stale", 20), ev("ussp", "u2", observe.PhaseRaised, "", 35)}
	if fs := failed(findings(stale, map[string]string{"ussp": alertsStaleOK})); len(fs) != 0 {
		t.Fatalf("an allowed stale clear and its re-raise failed: %+v", fs)
	}
	all := findings(stale, map[string]string{"ussp": alertsStaleOK})
	if len(all) != 2 || all[0].What != "stale_clear" || !all[0].Allowed || all[1].What != "re_raised" || !all[1].Allowed {
		t.Fatalf("the allowed acts must still be recorded: %+v", all)
	}
	// The same row, the alert cleared as resolved: the aircraft is still
	// inside, so that is a false clear whatever the mode.
	resolved := []alertEvent{ev("ussp", "u1", observe.PhaseCleared, "resolved", 20), ev("ussp", "u2", observe.PhaseRaised, "", 35)}
	if fs := failed(findings(resolved, map[string]string{"ussp": alertsStaleOK})); len(fs) != 2 || fs[0].What != "cleared" {
		t.Fatalf("%+v", fs)
	}
	// stale_ok for the USSP does not cover the authority.
	if fs := failed(findings([]alertEvent{ev("authority", "a1", observe.PhaseCleared, "stale", 20), ev("authority", "a2", observe.PhaseRaised, "", 30)},
		map[string]string{"ussp": alertsStaleOK})); len(fs) != 2 {
		t.Fatalf("%+v", fs)
	}
}

func TestASecondRaiseWhileOpenIsADuplicate(t *testing.T) {
	fs := failed(findings([]alertEvent{ev("authority", "a2", observe.PhaseRaised, "", 32)}, map[string]string{"authority": alertsStaleOK}))
	if len(fs) != 1 || fs[0].What != "duplicate_raise" || fs[0].System != "authority" || fs[0].OffsetS != 22 {
		t.Fatalf("%+v", fs)
	}
}

func TestTheAlertMustBeOpenAgainAfterTheRow(t *testing.T) {
	fs := failed(findings([]alertEvent{ev("ussp", "u1", observe.PhaseCleared, "stale", 20)}, map[string]string{"ussp": alertsStaleOK}))
	if len(fs) != 1 || fs[0].What != "not_open_after" {
		t.Fatalf("%+v", fs)
	}
	// A re-raise after the window's end is too late.
	fs = failed(findings([]alertEvent{ev("ussp", "u1", observe.PhaseCleared, "stale", 20), ev("ussp", "u2", observe.PhaseRaised, "", 50)}, map[string]string{"ussp": alertsStaleOK}))
	if len(fs) == 0 || fs[0].What != "not_open_after" {
		t.Fatalf("%+v", fs)
	}
}

func TestTheExitClearIsNotJudged(t *testing.T) {
	if fs := findings([]alertEvent{ev("ussp", "u1", observe.PhaseCleared, "resolved", 101), ev("authority", "a1", observe.PhaseCleared, "resolved", 102)}, nil); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
	// A clear between rows is reported with no row.
	fs := failed(judgeAlerts(append(append([]alertEvent(nil), baseEvents...), ev("ussp", "u1", observe.PhaseCleared, "stale", 60)), bgCfg, at(100), at(200), rowWindow(nil)))
	if len(fs) == 0 || fs[0].Row != "" {
		t.Fatalf("%+v", fs)
	}
}

func TestAlertWatchKeepsEveryRaiseAndClearOfTheStandingAlert(t *testing.T) {
	w := newAlertWatch(bgCfg)
	w.see(observe.Event{System: "ussp", Kind: "zone_incursion", Subject: "Z", AlertID: "u1", Phase: observe.PhaseRaised, ObservedAt: at(1)})
	w.see(observe.Event{System: "ussp", Kind: "proximity", Subject: "Z", AlertID: "p", Phase: observe.PhaseRaised, ObservedAt: at(1)})
	w.see(observe.Event{System: "ussp", Kind: "zone_incursion", Subject: "OTHER", AlertID: "o", Phase: observe.PhaseRaised, ObservedAt: at(1)})
	select {
	case <-w.ready:
		t.Fatal("ready before the authority raised")
	default:
	}
	w.see(observe.Event{System: "authority", Kind: "zone_incursion", Subject: "Z", AlertID: "a1", Phase: observe.PhaseRaised, ObservedAt: at(2)})
	select {
	case <-w.ready:
	case <-time.After(time.Second):
		t.Fatal("not ready after both raised")
	}
	// A second raise must not close the channel twice.
	w.see(observe.Event{System: "authority", Kind: "zone_incursion", Subject: "Z", AlertID: "a2", Phase: observe.PhaseRaised, ObservedAt: at(3)})
	evs, dropped := w.all()
	if len(evs) != 3 || dropped != 0 || evs[2].AlertID != "a2" {
		t.Fatalf("%+v", evs)
	}
}

func TestWriteBackgroundSetsTheHoldAndStillLoads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	p, err := writeBackground("background.yaml", "inside", 1234, dir)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := scenario.Load("background.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scenario.Load(p)
	if err != nil {
		t.Fatalf("the copy does not load: %v", err)
	}
	var hold float64
	for _, st := range sc.Steps {
		if st.ID == "inside" {
			hold = st.ForS
		}
	}
	if hold != 1234 || sc.DurationS != orig.DurationS+1234 || sc.PolicyDoc.PolicyVersion != orig.PolicyDoc.PolicyVersion {
		t.Fatalf("hold %g, duration %g (orig %g)", hold, sc.DurationS, orig.DurationS)
	}
	if _, err := writeBackground("background.yaml", "climb", 10, dir); err == nil {
		t.Fatal("a step that is not a hold was accepted")
	}
	if _, err := writeBackground("background.yaml", "nope", 10, dir); err == nil {
		t.Fatal("a missing step was accepted")
	}
	if _, err := os.Stat("background.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestALostAlertIsReportedByTheRowThatLostItOnly(t *testing.T) {
	windows := []window{
		{row: "r1", from: at(10), to: at(40), restored: at(30), modes: map[string]string{"ussp": alertsStaleOK}},
		{row: "r2", from: at(50), to: at(70), restored: at(60)},
	}
	evs := append(append([]alertEvent(nil), baseEvents...), ev("ussp", "u1", observe.PhaseCleared, "stale", 20))
	fs := failed(rowOnly(judgeAlerts(evs, bgCfg, at(100), at(100), windows)))
	if len(fs) != 1 || fs[0].Row != "r1" || fs[0].What != "not_open_after" {
		t.Fatalf("%+v", fs)
	}
}

func exitEvents(evs ...alertEvent) []alertEvent {
	return append(append([]alertEvent(nil), baseEvents...), evs...)
}

func TestTheExitIsJudgedOnEveryEvent(t *testing.T) {
	ok := exitEvents(ev("ussp", "u1", observe.PhaseCleared, "resolved", 105), ev("authority", "a1", observe.PhaseCleared, "resolved", 106))
	if fs := failed(judgeAlerts(ok, bgCfg, at(100), at(150), nil)); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
	// The authority never clears after the exit.
	fs := failed(judgeAlerts(exitEvents(ev("ussp", "u1", observe.PhaseCleared, "resolved", 105)), bgCfg, at(100), at(150), nil))
	if len(fs) != 1 || fs[0].What != "not_cleared_at_exit" || fs[0].System != "authority" {
		t.Fatalf("%+v", fs)
	}
	// A raise after the exit.
	fs = failed(judgeAlerts(append(ok, ev("ussp", "u9", observe.PhaseRaised, "", 110)), bgCfg, at(100), at(150), nil))
	if len(fs) != 1 || fs[0].What != "raised_after_exit" {
		t.Fatalf("%+v", fs)
	}
	// Nothing open when the aircraft left: the alert was lost before.
	lost := exitEvents(ev("ussp", "u1", observe.PhaseCleared, "stale", 90), ev("authority", "a1", observe.PhaseCleared, "resolved", 106))
	fs = failed(judgeAlerts(lost, bgCfg, at(100), at(150), nil))
	if len(fs) < 2 || fs[1].What != "not_open_at_exit" {
		t.Fatalf("%+v", fs)
	}
}

func TestAnEventIsTheLatestRowsWhenWindowsMeet(t *testing.T) {
	windows := []window{
		{row: "r1", from: at(10), to: at(30), restored: at(20)},
		{row: "r2", from: at(30), to: at(60), restored: at(50), modes: map[string]string{"ussp": alertsStaleOK}},
	}
	evs := exitEvents(ev("ussp", "u1", observe.PhaseCleared, "stale", 30), ev("ussp", "u2", observe.PhaseRaised, "", 40))
	all := rowOnly(judgeAlerts(evs, bgCfg, at(100), at(100), windows))
	if len(failed(all)) != 0 || all[0].Row != "r2" {
		t.Fatalf("%+v", all)
	}
}

func TestARepeatedClearIsNotANewAct(t *testing.T) {
	evs := []alertEvent{ev("authority", "a1", observe.PhaseCleared, "stale", 20), ev("authority", "a1", observe.PhaseCleared, "stale", 20),
		ev("authority", "a1", observe.PhaseCleared, "stale", 21), ev("authority", "a2", observe.PhaseRaised, "", 25)}
	all := findings(evs, map[string]string{"authority": alertsStaleOK})
	if len(all) != 2 || all[0].What != "stale_clear" || all[1].What != "re_raised" || len(failed(all)) != 0 {
		t.Fatalf("%+v", all)
	}
}

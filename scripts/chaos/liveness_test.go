package main

import (
	"strings"
	"testing"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// liveCfg is bgCfg with the matrix's silence bound.
var liveCfg = func() Background { b := bgCfg; b.MaxSilenceS = 6; return b }()

// heard feeds a liveWatch one status frame every 2 s from 0 s to 100 s
// on each background stream, leaving out the seconds silent says, and
// adds the extra frames.
func heard(silent map[string][2]float64, extra ...observe.Frame) map[string]StreamLiveness {
	w := newLiveWatch(liveCfg)
	streams := map[string]string{"ussp-alerts-a": "ussp", "authority-picture": "authority"}
	var fs []observe.Frame
	for s := 0.0; s <= 100; s += 2 {
		for name, sys := range streams {
			if g, ok := silent[sys]; ok && s > g[0] && s < g[1] {
				continue
			}
			fs = append(fs, observe.Frame{Stream: name, System: sys, Schema: wire.SchemaStatus, At: at(s)})
		}
		for _, e := range extra {
			if e.At.Equal(at(s)) {
				fs = append(fs, e)
			}
		}
	}
	for _, f := range fs {
		w.frame(f)
	}
	return w.all()
}

// rowResult judges the alert and the streams of one kept row from 10 s
// to 40 s and places the findings as the run does.
func rowResult(streams map[string]StreamLiveness, down []string) *RunResult {
	wins := []window{{row: "r", from: at(10), to: at(40), restored: at(30), streamsDown: down}}
	fs := judgeAlerts(append([]alertEvent(nil), baseEvents...), liveCfg, at(100), at(200), wins)
	fs = append(fs, judgeLiveness(streams, liveCfg, at(100), at(100), wins)...)
	var rowFs []AlertFinding
	for _, f := range fs {
		if f.Row != "" {
			rowFs = append(rowFs, f)
		}
	}
	res := &RunResult{Rows: []*RowResult{{ID: "r", Verdict: verdictPass}}}
	placeFindings(res, rowFs)
	return res
}

// The absence half: both streams heard every 2 s, the alert never
// cleared: the kept row passes.
func TestAKeptAlertOnAHeardStreamPasses(t *testing.T) {
	res := rowResult(heard(nil), nil)
	if rr := res.Rows[0]; rr.Verdict != verdictPass || len(rr.Failures) != 0 {
		t.Fatalf("a kept alert on live streams failed: %v", rr.Failures)
	}
}

// The presence half, the review's case: the USSP's alert stream drops
// 15 s into the row and says nothing for 20 s, so no clear could have
// been seen; the alert findings alone pass the row (no clear, no second
// raise), and the silence must fail it.
func TestASilentStreamFailsAKeptRow(t *testing.T) {
	streams := heard(map[string][2]float64{"ussp": {14, 36}})
	if fs := rowOnly(judgeAlerts(append([]alertEvent(nil), baseEvents...), liveCfg, at(100), at(200), rowWindow(nil))); len(fs) != 0 {
		t.Fatalf("precondition: the alert findings alone already fail the row: %+v", fs)
	}
	rr := rowResult(streams, nil).Rows[0]
	if rr.Verdict != verdictFail || len(rr.Failures) != 1 || !strings.Contains(rr.Failures[0], "ussp stream silent") ||
		!strings.Contains(rr.Failures[0], "ussp-alerts-a") {
		t.Fatalf("a silent stream did not fail the row: %s %v", rr.Verdict, rr.Failures)
	}
}

// A stream that drops and never comes back is silent to the end.
func TestAStreamThatNeverCameBackFailsTheRow(t *testing.T) {
	rr := rowResult(heard(map[string][2]float64{"authority": {20, 1000}}), nil).Rows[0]
	if rr.Verdict != verdictFail || !strings.Contains(strings.Join(rr.Failures, "; "), "authority stream silent") {
		t.Fatalf("%s %v", rr.Verdict, rr.Failures)
	}
}

// A system with no stream heard at all cannot keep an alert either.
func TestASystemWithNoStreamFailsEveryRow(t *testing.T) {
	streams := heard(nil)
	delete(streams, "authority-picture")
	rr := rowResult(streams, nil).Rows[0]
	if rr.Verdict != verdictFail || !strings.Contains(strings.Join(rr.Failures, "; "), "no frame from any console stream") {
		t.Fatalf("%s %v", rr.Verdict, rr.Failures)
	}
}

// A silence under the bound is a stream between two status frames.
func TestAShortSilenceIsNotAFinding(t *testing.T) {
	rr := rowResult(heard(map[string][2]float64{"ussp": {14, 20}}), nil).Rows[0]
	if rr.Verdict != verdictPass {
		t.Fatalf("a 6 s silence failed the row: %v", rr.Failures)
	}
}

// A row whose fault takes the stream down may have the silence, inside
// its window, when the stream comes back re-sending the open alerts;
// without them, or for a system it does not name, it fails.
func TestAStreamsDownRowNeedsTheAlertsReSent(t *testing.T) {
	gap := map[string][2]float64{"ussp": {14, 30}}
	back := observe.Frame{Stream: "ussp-alerts-a", System: "ussp", Schema: wire.SchemaAlert, At: at(30)}
	if rr := rowResult(heard(gap, back), []string{"ussp"}).Rows[0]; rr.Verdict != verdictPass {
		t.Fatalf("an allowed stream-down failed: %v", rr.Failures)
	}
	if rr := rowResult(heard(gap), []string{"ussp"}).Rows[0]; rr.Verdict != verdictFail || !strings.Contains(rr.Failures[0], "without re-sending") {
		t.Fatalf("a stream back without its alerts passed: %s %v", rr.Verdict, rr.Failures)
	}
	if rr := rowResult(heard(gap, back), []string{"authority"}).Rows[0]; rr.Verdict != verdictFail {
		t.Fatalf("a stream the row does not take down passed: %v", rr.Failures)
	}
	// A snapshot is the authority's re-read.
	snap := observe.Frame{Stream: "authority-picture", System: "authority", Schema: wire.SchemaSnapshot, At: at(30)}
	if rr := rowResult(heard(map[string][2]float64{"authority": {14, 30}}, snap), []string{"authority"}).Rows[0]; rr.Verdict != verdictPass {
		t.Fatalf("a snapshot did not count: %v", rr.Failures)
	}
	// Past the window, the silence is not the fault's.
	if rr := rowResult(heard(map[string][2]float64{"ussp": {14, 60}}, observe.Frame{Stream: "ussp-alerts-a", System: "ussp", Schema: wire.SchemaAlert, At: at(60)}), []string{"ussp"}).Rows[0]; rr.Verdict != verdictFail {
		t.Fatalf("a silence past the window passed: %v", rr.Failures)
	}
}

// A silence between two rows belongs to no row and fails the run.
func TestASilenceBetweenRowsIsAFindingOfNoRow(t *testing.T) {
	wins := []window{{row: "a", from: at(10), to: at(30)}, {row: "b", from: at(60), to: at(90)}}
	fs := judgeLiveness(heard(map[string][2]float64{"ussp": {34, 56}}), liveCfg, at(100), at(100), wins)
	if len(fs) != 1 || fs[0].Row != "" || fs[0].Allowed || fs[0].System != "ussp" {
		t.Fatalf("%+v", fs)
	}
	res := &RunResult{}
	placeFindings(res, fs)
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "between rows") {
		t.Fatalf("%v", res.Failures)
	}
}

// The watch keeps only background systems, and a silence with its ends.
func TestLiveWatchRecordsSilences(t *testing.T) {
	w := newLiveWatch(liveCfg)
	w.frame(observe.Frame{Stream: "ansp-manned", System: "ansp", Schema: wire.SchemaStatus, At: at(0)})
	for _, s := range []float64{0, 2, 4, 20, 22} {
		w.frame(observe.Frame{Stream: "authority-picture", System: "authority", Schema: wire.SchemaStatus, At: at(s)})
	}
	w.frame(observe.Frame{Stream: "authority-picture", System: "authority", Schema: wire.SchemaSnapshot, At: at(23)})
	got := w.all()
	if _, ok := got["ansp-manned"]; ok {
		t.Fatal("a stream of a system outside the background was kept")
	}
	l := got["authority-picture"]
	if l.Frames != 6 || len(l.Silences) != 1 || !l.Silences[0].From.Equal(at(4)) || !l.Silences[0].To.Equal(at(20)) || !l.Silences[0].Reread {
		t.Fatalf("%+v", l)
	}
}

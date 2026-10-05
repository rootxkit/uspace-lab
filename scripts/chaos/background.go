package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/runner"
)

// rowOverheadS is what each row costs beyond its hold and recovery in
// the background's hold (the scripts, the preflight, the samples): the
// background must outlast the rows, and a row that would run past it is
// not started (it would see the aircraft leave the zone).
const rowOverheadS = 30

// backgroundHoldS is the hold the selected rows need.
func backgroundHoldS(bg Background, rows []Row) float64 {
	h := bg.RaisedWithinS
	for i := range rows {
		h += rows[i].HoldS + max(rows[i].RecoverWithinS, bg.RealertWithinS) + rowOverheadS
		h += float64(len(rows[i].Skew)) * 15
	}
	return h
}

// writeBackground writes a copy of the background scenario whose hold
// step lasts holdS and whose duration covers it, with the policy path
// made relative to dir, into dir; the copy is what the run loads (its digest is
// in the result) and the committed file stays as it is.
func writeBackground(src, holdStep string, holdS float64, dir string) (string, error) {
	b, err := os.ReadFile(src) //nolint:gosec // the matrix names the scenario
	if err != nil {
		return "", fmt.Errorf("background: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("background %s: %w", src, err)
	}
	steps, ok := doc["steps"].([]any)
	if !ok {
		return "", fmt.Errorf("background %s: no steps", src)
	}
	found := false
	for _, s := range steps {
		m, ok := s.(map[string]any)
		if ok && m["id"] == holdStep {
			if m["do"] != "hold" {
				return "", fmt.Errorf("background %s: step %s is %v, not a hold", src, holdStep, m["do"])
			}
			m["for_s"] = holdS
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("background %s: no step %s", src, holdStep)
	}
	var dur float64
	switch v := doc["duration_s"].(type) {
	case uint64:
		dur = float64(v)
	case int64:
		dur = float64(v)
	case float64:
		dur = v
	default:
		return "", fmt.Errorf("background %s: duration_s is not a number", src)
	}
	doc["duration_s"] = dur + holdS
	// The scenario loader joins the policy path to the scenario's
	// directory, so the copy names it relative to where it is written.
	if p, ok := doc["policy"].(string); ok && !filepath.IsAbs(p) {
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(src), p))
		if err != nil {
			return "", fmt.Errorf("background: %w", err)
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("background: %w", err)
		}
		rel, err := filepath.Rel(absDir, abs)
		if err != nil {
			return "", fmt.Errorf("background: the policy %s cannot be named from %s: %w", abs, dir, err)
		}
		doc["policy"] = filepath.ToSlash(rel)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("background: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("background: %w", err)
	}
	p := filepath.Join(dir, "background.yaml")
	if err := os.WriteFile(p, out, 0o600); err != nil {
		return "", fmt.Errorf("background: %w", err)
	}
	return p, nil
}

// maxAlertEvents bounds the events an alertWatch keeps.
const maxAlertEvents = 10000

// alertEvent is one raise or clear of the standing alert.
type alertEvent struct {
	System  string    `json:"system"`
	AlertID string    `json:"alert_id"`
	Phase   string    `json:"phase"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

// alertWatch sees the background run's events as they are recorded,
// keeps every raise and clear of the standing alert (the runner's alert
// groups keep only an alert's first raise and first clear, and a raise
// after a clear under the same id is exactly what a row must see), and
// says when the alert is up in every system.
type alertWatch struct {
	mu      sync.Mutex
	kind    string
	subject string
	want    map[string]bool
	raised  map[string]time.Time
	events  []alertEvent
	dropped int
	ready   chan struct{}
	closed  bool
}

func newAlertWatch(bg Background) *alertWatch {
	w := &alertWatch{kind: bg.Kind, subject: bg.Subject, want: map[string]bool{}, raised: map[string]time.Time{}, ready: make(chan struct{})}
	for _, s := range bg.Systems {
		w.want[s] = true
	}
	return w
}

func (w *alertWatch) see(e observe.Event) {
	if e.Kind != w.kind || (w.subject != "" && e.Subject != w.subject) || !w.want[e.System] {
		return
	}
	if e.Phase != observe.PhaseRaised && e.Phase != observe.PhaseCleared {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.events) < maxAlertEvents {
		w.events = append(w.events, alertEvent{System: e.System, AlertID: e.AlertID, Phase: e.Phase, Reason: e.Reason, At: e.ObservedAt})
	} else {
		w.dropped++
	}
	if e.Phase != observe.PhaseRaised {
		return
	}
	if _, ok := w.raised[e.System]; !ok {
		w.raised[e.System] = e.ObservedAt
	}
	if !w.closed && len(w.raised) == len(w.want) {
		w.closed = true
		close(w.ready)
	}
}

func (w *alertWatch) snapshot() map[string]time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range w.raised {
		out[k] = v
	}
	return out
}

func (w *alertWatch) all() ([]alertEvent, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]alertEvent(nil), w.events...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, w.dropped
}

// AlertFinding is one thing the standing alert did that a row's claim
// says it must not, or one thing it did that the claim allows (Allowed).
type AlertFinding struct {
	System  string    `json:"system"`
	What    string    `json:"what"` // duplicate_raise, cleared, not_open_after, stale_clear, re_raised
	AlertID string    `json:"alert_id,omitempty"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason,omitempty"`
	Row     string    `json:"row,omitempty"`
	OffsetS float64   `json:"offset_s"`
	Allowed bool      `json:"allowed"`
}

// window is one row's span for the alert judgement: from its injection
// to its end, or to its restore plus the re-alert bound when that is
// later, with the mode each background system is held to.
type window struct {
	row      string
	from, to time.Time
	restored time.Time
	modes    map[string]string
	// streamsDown are the systems whose console stream the row's fault
	// takes down (Row.StreamsDown).
	streamsDown []string
}

func (w window) mode(sys string) string {
	if m, ok := w.modes[sys]; ok {
		return m
	}
	return alertsKept
}

// judgeAlerts reads the standing alert's raises and clears, before the
// aircraft left the zone (outStart), against the rows' windows:
//   - a raise while another alert of the system is open is a duplicate
//     (the state was lost and rebuilt beside the old one);
//   - a clear is the alert lost, unless the row is stale_ok for that
//     system and the reason is one of bg.StaleReasons;
//   - a raise after a clear is a re-raise, allowed after an allowed
//     clear;
//   - at the end of each row's window the system must have an open alert.
//
// Anything outside every window is reported with no row (between rows).
func judgeAlerts(evs []alertEvent, bg Background, outStart, end time.Time, windows []window) []AlertFinding {
	var out []AlertFinding
	// The latest row whose window holds t: a row's window may reach past
	// the next row's injection only when the caller did not cap it, and
	// what happens after an injection is that row's.
	find := func(t time.Time) (window, bool) {
		var got window
		ok := false
		for _, w := range windows {
			if !t.Before(w.from) && !t.After(w.to) {
				got, ok = w, true
			}
		}
		return got, ok
	}
	for _, sys := range bg.Systems {
		open := map[string]bool{}
		lastClearAllowed := false
		seen := false
		var sysEvents, exitEvents []alertEvent
		for _, e := range evs {
			switch {
			case e.System != sys:
			case e.At.Before(outStart):
				sysEvents = append(sysEvents, e)
			default:
				exitEvents = append(exitEvents, e)
			}
		}
		for _, e := range sysEvents {
			w, inRow := find(e.At)
			f := AlertFinding{System: sys, AlertID: e.AlertID, At: e.At, Reason: e.Reason, Row: w.row}
			if inRow {
				f.OffsetS = round1(e.At.Sub(w.from).Seconds())
			}
			switch e.Phase {
			case observe.PhaseRaised:
				switch {
				case !seen:
					// The standing alert's first raise: the baseline.
				case len(open) > 0:
					f.What = "duplicate_raise"
					out = append(out, f)
				default:
					f.What, f.Allowed = "re_raised", lastClearAllowed
					out = append(out, f)
				}
				seen = true
				open[e.AlertID] = true
			case observe.PhaseCleared:
				if !open[e.AlertID] {
					// A clear of an alert already closed (a system that
					// replays its cleared frames after an outage) is not
					// a new act.
					continue
				}
				delete(open, e.AlertID)
				allowed := inRow && w.mode(sys) == alertsStaleOK && slices.Contains(bg.StaleReasons, e.Reason)
				f.What, f.Allowed = "cleared", allowed
				if allowed {
					f.What = "stale_clear"
				}
				lastClearAllowed = allowed
				out = append(out, f)
			}
		}
		// Open at the end of every window that it was open at the start
		// of (an alert lost before a row is that earlier row's finding,
		// not one per row after it).
		// openAt counts the events strictly before t: at a window's end
		// capped by the next row's injection, an event at that instant
		// is the next row's.
		openAt := func(t time.Time) bool {
			open := map[string]bool{}
			for _, e := range sysEvents {
				if !e.At.Before(t) {
					break
				}
				if e.Phase == observe.PhaseRaised {
					open[e.AlertID] = true
				} else {
					delete(open, e.AlertID)
				}
			}
			return len(open) > 0
		}
		for _, w := range windows {
			if !w.to.Before(outStart) || !openAt(w.from) {
				continue
			}
			if !openAt(w.to) {
				out = append(out, AlertFinding{System: sys, What: "not_open_after", At: w.to, Row: w.row,
					OffsetS: round1(w.to.Sub(w.from).Seconds())})
			}
		}
		// The exit: the aircraft leaves the zone at outStart; the alert
		// open then must clear as resolved before the run ends, and no
		// other raise may follow (the scenario's own expectation, judged
		// on every raise and clear: after an allowed stale clear and
		// re-raise the runner's verdict counts the new id as a false
		// alert, which it is not).
		if !openAt(outStart) {
			out = append(out, AlertFinding{System: sys, What: "not_open_at_exit", At: outStart})
		}
		cleared := false
		for _, e := range exitEvents {
			if e.At.After(end) {
				break
			}
			switch {
			case e.Phase == observe.PhaseCleared && e.Reason == "resolved":
				cleared = true
			case e.Phase == observe.PhaseRaised:
				out = append(out, AlertFinding{System: sys, What: "raised_after_exit", AlertID: e.AlertID, At: e.At})
			}
		}
		if openAt(outStart) && !cleared {
			out = append(out, AlertFinding{System: sys, What: "not_cleared_at_exit", At: end})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// BackgroundResult is what the background run showed.
type BackgroundResult struct {
	Scenario       string               `json:"scenario"`
	ScenarioDigest string               `json:"scenario_digest,omitempty"`
	HoldS          float64              `json:"hold_s"`
	RaisedAt       map[string]time.Time `json:"raised_at,omitempty"`
	Verdict        string               `json:"verdict,omitempty"`
	Missed         int                  `json:"missed_alerts"`
	False          int                  `json:"false_alert_count"`
	Failures       []string             `json:"failures,omitempty"`
	Findings       []AlertFinding       `json:"findings,omitempty"`
	Events         []alertEvent         `json:"events,omitempty"`
	// Streams is each background console stream's frames and silences:
	// what the alert findings' absences rest on.
	Streams       map[string]StreamLiveness `json:"streams,omitempty"`
	EventsDropped int                       `json:"events_dropped,omitempty"`
	ResultFile    string                    `json:"result_file,omitempty"`
	Note          string                    `json:"note,omitempty"`
	Error         string                    `json:"error,omitempty"`
}

func summarise(res *runner.Result, br *BackgroundResult) {
	if res == nil {
		return
	}
	br.ScenarioDigest = res.ScenarioDigest
	br.Verdict = res.Verdict
	br.Missed = res.Outcome.Missed
	br.False = res.Outcome.False
	br.Failures = res.Failures
}

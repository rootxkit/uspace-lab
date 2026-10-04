// Package verdict judges a scenario run: every expected raise and clear
// observed inside its window, nothing on the never-list, the missed-alert
// and false-alert counts, and the ledgers. It reports what was observed,
// never what was expected (LESSONS E-04): a miss names the raises that
// were seen instead.
package verdict

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// Group is one alert: its first raise and its clear.
type Group struct {
	System   string         `json:"system"`
	Kind     string         `json:"kind"`
	AlertID  string         `json:"alert_id"`
	Aircraft string         `json:"aircraft,omitempty"`
	Peer     string         `json:"peer,omitempty"`
	Subject  string         `json:"subject,omitempty"`
	Raise    *observe.Event `json:"raise,omitempty"`
	Clear    *observe.Event `json:"clear,omitempty"`
	claimed  bool
}

// Groups folds the events into alerts in the order they were first seen.
func Groups(events []observe.Event) []*Group {
	byID := map[string]*Group{}
	var out []*Group
	for i := range events {
		e := events[i]
		key := e.System + "|" + e.AlertID
		g := byID[key]
		if g == nil {
			g = &Group{System: e.System, Kind: e.Kind, AlertID: e.AlertID, Aircraft: e.Aircraft, Peer: e.Peer, Subject: e.Subject}
			byID[key] = g
			out = append(out, g)
		}
		if g.Peer == "" {
			g.Peer = e.Peer
		}
		switch e.Phase {
		case observe.PhaseRaised:
			if g.Raise == nil {
				g.Raise = &e
			}
		case observe.PhaseCleared:
			if g.Clear == nil {
				g.Clear = &e
			}
		}
	}
	return out
}

func matches(m scenario.Matcher, g *Group) bool {
	return m.System == g.System && m.Kind == g.Kind &&
		(m.Aircraft == "" || m.Aircraft == g.Aircraft) &&
		(m.Peer == "" || m.Peer == g.Peer) &&
		(m.Subject == "" || m.Subject == g.Subject) &&
		detailMatches(m.Detail, g)
}

// detailMatches reports whether the alert's raise carries every member
// of want with an equal value. YAML reads a number as an int or a float
// and a frame's JSON as a float64, so numbers compare as float64.
func detailMatches(want map[string]any, g *Group) bool {
	if len(want) == 0 {
		return true
	}
	if g.Raise == nil {
		return false
	}
	for k, w := range want {
		got, ok := g.Raise.Detail[k]
		if !ok || !sameValue(w, got) {
			return false
		}
	}
	return true
}

func sameValue(a, b any) bool {
	fa, aNum := number(a)
	fb, bNum := number(b)
	if aNum || bNum {
		return aNum && bNum && fa == fb
	}
	switch av := a.(type) {
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	}
	return false
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// Marks are the moments the windows are measured from.
type Marks map[string]time.Time

// Span is a resolved window.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func (s Span) contains(t time.Time) bool { return !t.Before(s.From) && !t.After(s.To) }

func resolve(w *scenario.Window, marks Marks) (Span, error) {
	m, ok := marks[w.After]
	if !ok {
		return Span{}, fmt.Errorf("mark %q never happened (no vehicle confirmed it, E-08)", w.After)
	}
	return Span{From: m.Add(seconds(w.MinS)), To: m.Add(seconds(w.MaxS))}, nil
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// ExpectResult is one expectation's outcome.
type ExpectResult struct {
	Name        string           `json:"name"`
	Matcher     scenario.Matcher `json:"matcher"`
	RaiseWindow *Span            `json:"raise_window,omitempty"`
	ClearWindow *Span            `json:"clear_window,omitempty"`
	HoldUntil   *time.Time       `json:"hold_until,omitempty"`
	Raise       *observe.Event   `json:"raise,omitempty"`
	Clear       *observe.Event   `json:"clear,omitempty"`
	OK          bool             `json:"ok"`
	Why         []string         `json:"why,omitempty"`
	// Seen lists the matching alerts that were observed but did not fit
	// (outside the window, already claimed), so a miss says what happened.
	Seen []*Group `json:"seen,omitempty"`
}

// NeverResult is one never-matcher and what violated it.
type NeverResult struct {
	Matcher    scenario.Matcher `json:"matcher"`
	Violations []*Group         `json:"violations,omitempty"`
}

// Outcome is the run's verdict on alerts.
type Outcome struct {
	Expectations []ExpectResult `json:"expectations"`
	Never        []NeverResult  `json:"never,omitempty"`
	FalseAlerts  []*Group       `json:"false_alerts,omitempty"`
	Missed       int            `json:"missed_alerts"`
	False        int            `json:"false_alert_count"`
	Groups       []*Group       `json:"alerts"`
	Pass         bool           `json:"pass"`
	Failures     []string       `json:"failures,omitempty"`
}

// Evaluate judges the events against the scenario.
func Evaluate(s *scenario.Scenario, marks Marks, events []observe.Event) Outcome {
	groups := Groups(events)
	out := Outcome{Groups: groups}
	for i := range s.Expect {
		e := &s.Expect[i]
		r := ExpectResult{Name: e.Name, Matcher: e.Matcher}
		rw, err := resolve(&e.Raise, marks)
		if err != nil {
			r.Why = append(r.Why, "raise: "+err.Error())
			out.Missed++
			out.Expectations = append(out.Expectations, r)
			continue
		}
		r.RaiseWindow = &rw
		var pick *Group
		for _, g := range groups {
			if !matches(e.Matcher, g) || g.Raise == nil {
				continue
			}
			if g.claimed || !rw.contains(g.Raise.ObservedAt) {
				r.Seen = append(r.Seen, g)
				continue
			}
			pick = g
			break
		}
		if pick == nil {
			out.Missed++
			r.Why = append(r.Why, fmt.Sprintf("no %s %s raise for %s in [%s, %s]", e.System, e.Kind, who(e.Matcher), clock(rw.From), clock(rw.To)))
			out.Expectations = append(out.Expectations, r)
			continue
		}
		pick.claimed = true
		r.Raise = pick.Raise
		r.Clear = pick.Clear
		r.OK = true
		if e.HoldUntil != nil {
			hw, err := resolve(e.HoldUntil, marks)
			switch {
			case err != nil:
				r.OK = false
				r.Why = append(r.Why, "hold_until: "+err.Error())
			case pick.Clear != nil && pick.Clear.ObservedAt.Before(hw.From):
				r.OK = false
				r.Why = append(r.Why, fmt.Sprintf("cleared %s (%s) at %s, before the hold ended at %s", pick.Clear.Reason, pick.AlertID, clock(pick.Clear.ObservedAt), clock(hw.From)))
			}
			t := hw.From
			r.HoldUntil = &t
		}
		if e.Clear != nil {
			cw, err := resolve(e.Clear, marks)
			switch {
			case err != nil:
				r.OK = false
				r.Why = append(r.Why, "clear: "+err.Error())
			case pick.Clear == nil:
				r.OK = false
				r.Why = append(r.Why, fmt.Sprintf("raised at %s, never cleared (clear window [%s, %s])", clock(pick.Raise.ObservedAt), clock(cw.From), clock(cw.To)))
			case !cw.contains(pick.Clear.ObservedAt):
				r.OK = false
				r.Why = append(r.Why, fmt.Sprintf("cleared at %s, outside [%s, %s]", clock(pick.Clear.ObservedAt), clock(cw.From), clock(cw.To)))
			case e.Clear.Reason != "" && pick.Clear.Reason != e.Clear.Reason:
				r.OK = false
				r.Why = append(r.Why, fmt.Sprintf("cleared %s, want %s", pick.Clear.Reason, e.Clear.Reason))
			}
			if err == nil {
				r.ClearWindow = &cw
			}
		}
		out.Expectations = append(out.Expectations, r)
	}
	for _, m := range s.Never {
		nr := NeverResult{Matcher: m}
		for _, g := range groups {
			if g.Raise != nil && matches(m, g) {
				nr.Violations = append(nr.Violations, g)
			}
		}
		out.Never = append(out.Never, nr)
	}
	kinds := s.Kinds()
	explicit := map[string]bool{}
	for _, jk := range s.JudgedKinds {
		explicit[jk] = true
	}
	for _, g := range groups {
		if g.claimed || g.Raise == nil || !contains(kinds[g.System], g.Kind) {
			continue
		}
		// A status slug or a manned track's presence is not an alert: an
		// unexpected one is a false alert only when judged_kinds names
		// its kind.
		if (g.Kind == observe.KindDegraded || g.Kind == observe.KindMannedTrack) && !explicit[g.System+":"+g.Kind] {
			continue
		}
		out.FalseAlerts = append(out.FalseAlerts, g)
	}
	out.False = len(out.FalseAlerts)
	for i := range out.Expectations {
		r := &out.Expectations[i]
		if !r.OK {
			for _, w := range r.Why {
				out.Failures = append(out.Failures, r.Name+": "+w)
			}
		}
	}
	for _, n := range out.Never {
		for _, g := range n.Violations {
			out.Failures = append(out.Failures, fmt.Sprintf("never %s %s for %s: raised %s at %s", n.Matcher.System, n.Matcher.Kind, who(n.Matcher), g.AlertID, clock(g.Raise.ObservedAt)))
		}
	}
	for _, g := range out.FalseAlerts {
		out.Failures = append(out.Failures, fmt.Sprintf("false alert: %s %s %s (aircraft %q, peer %q, subject %q) raised at %s", g.System, g.Kind, g.AlertID, g.Aircraft, g.Peer, g.Subject, clock(g.Raise.ObservedAt)))
	}
	sort.Strings(out.Failures)
	out.Pass = len(out.Failures) == 0 && out.Missed == 0 && out.False == 0
	return out
}

func who(m scenario.Matcher) string {
	s := m.Aircraft
	if s == "" {
		s = "any aircraft"
	}
	if m.Peer != "" {
		s += " with " + m.Peer
	}
	if m.Subject != "" {
		s += " on " + m.Subject
	}
	if len(m.Detail) > 0 {
		keys := make([]string, 0, len(m.Detail))
		for k := range m.Detail {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, m.Detail[k]))
		}
		s += " with detail " + strings.Join(parts, ",")
	}
	return s
}

func clock(t time.Time) string { return t.UTC().Format("15:04:05.000") }

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

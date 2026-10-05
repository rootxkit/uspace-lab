package load

import (
	"fmt"
	"sort"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// Judgement is the expected set against the observed alerts.
type Judgement struct {
	Summary      AlertSummary
	DueRaises    int
	MissedRaises int
	MissedClears int
	Unexpected   int
	Latency      *Histogram
}

// judge matches every expected raise to the first unused proximity raise
// of its aircraft inside its window, times it from the sample its first
// frame named (handedFor), and looks for its clear; a proximity raise no
// expectation took is unexpected. Times are seconds after t0.
func judge(expected []Expected, events []observe.Event, t0 time.Time, handedFor func(alertID string) (time.Time, bool)) Judgement {
	byAircraft := map[string][]observe.Event{}
	for i := range events {
		e := &events[i]
		if e.System != scenario.SystemUSSP || e.Kind != "proximity" {
			continue
		}
		byAircraft[e.Aircraft] = append(byAircraft[e.Aircraft], *e)
	}
	rel := func(t time.Time) float64 { return t.Sub(t0).Seconds() }
	j := Judgement{Latency: NewHistogram()}
	used := map[string]bool{}
	for _, x := range expected {
		if x.RaiseDue {
			j.DueRaises++
		}
		var raise *observe.Event
		for i := range byAircraft[x.Aircraft] {
			e := &byAircraft[x.Aircraft][i]
			if e.Phase == observe.PhaseRaised && !used[e.AlertID] && rel(e.ObservedAt) >= x.RaiseFromS && rel(e.ObservedAt) <= x.RaiseToS {
				raise = e
				break
			}
		}
		if raise == nil {
			if x.RaiseDue {
				j.MissedRaises++
				if len(j.Summary.Missed) < maxListed {
					j.Summary.Missed = append(j.Summary.Missed, x)
				}
			}
			continue
		}
		used[raise.AlertID] = true
		j.Summary.Raised++
		rec := RaiseRecord{Aircraft: x.Aircraft, AlertID: raise.AlertID, ObservedS: round3(rel(raise.ObservedAt))}
		if raise.CapturedAt == nil {
			j.Summary.Untraceable++
		} else {
			c := round3(rel(*raise.CapturedAt))
			rec.CapturedS = &c
			if handed, ok := handedFor(raise.AlertID); ok && !raise.ObservedAt.Before(handed) {
				lat := raise.ObservedAt.Sub(handed)
				j.Latency.Add(lat)
				l := round3(lat.Seconds())
				rec.LatencyS = &l
			} else {
				// No sample of the pair at that captured_at, or one handed
				// out after the raise arrived: either way not a latency.
				j.Summary.Untraceable++
			}
		}
		if len(j.Summary.Raises) < maxListed {
			j.Summary.Raises = append(j.Summary.Raises, rec)
		}
		cleared := false
		for i := range byAircraft[x.Aircraft] {
			e := &byAircraft[x.Aircraft][i]
			if e.Phase == observe.PhaseCleared && e.AlertID == raise.AlertID && rel(e.ObservedAt) <= x.ClearToS {
				cleared = true
				break
			}
		}
		switch {
		case cleared:
			j.Summary.Cleared++
		case x.ClearDue:
			j.MissedClears++
			if len(j.Summary.MissedClear) < maxListed {
				j.Summary.MissedClear = append(j.Summary.MissedClear, x)
			}
		}
	}
	names := make([]string, 0, len(byAircraft))
	for n := range byAircraft {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for i := range byAircraft[n] {
			e := &byAircraft[n][i]
			if e.Phase == observe.PhaseRaised && !used[e.AlertID] {
				j.Unexpected++
				if len(j.Summary.Unexpected) < maxListed {
					j.Summary.Unexpected = append(j.Summary.Unexpected, fmt.Sprintf("%s raised at %+.1f s (peer %s)", n, rel(e.ObservedAt), e.Peer))
				}
			}
		}
	}
	j.Summary.Expected = j.DueRaises
	return j
}

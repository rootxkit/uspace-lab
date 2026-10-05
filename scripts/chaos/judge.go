package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Phases of a row.
const (
	phaseBefore = "before"
	phaseDuring = "during"
	phaseAfter  = "after"
)

// Sample is one look at the stack: every probed endpoint's readiness and,
// for the row's targets, what docker says, with whether the fault was in
// place (during) or gone (after).
type Sample struct {
	At         time.Time            `json:"at"`
	Phase      string               `json:"phase"`
	Ready      map[string]Readiness `json:"ready"`
	Containers map[string]Container `json:"containers,omitempty"`
	FaultHeld  *bool                `json:"fault_held,omitempty"`
	Restored   *bool                `json:"restored,omitempty"`
	FaultNote  string               `json:"fault_note,omitempty"`
	DockerErr  string               `json:"docker_error,omitempty"`
}

// ExpectResult is one expectation judged.
type ExpectResult struct {
	Phase   string   `json:"phase"`
	Expect  Expect   `json:"expect"`
	Systems []string `json:"systems"`
	Pass    bool     `json:"pass"`
	// AfterS is when, from the phase's start, the expectation was first
	// met (eventually kinds), per system.
	AfterS   map[string]float64 `json:"after_s,omitempty"`
	Observed string             `json:"observed"`
}

// FaultResult is whether the fault happened, held and went away.
type FaultResult struct {
	Spec FaultSpec `json:"spec"`
	// Observed is the fault seen in place at least once during the hold.
	Observed bool `json:"observed"`
	// FirstHeldAfterS is when, from the injection, it was first seen.
	FirstHeldAfterS *float64 `json:"first_held_after_s,omitempty"`
	HeldSamples     int      `json:"held_samples"`
	DuringSamples   int      `json:"during_samples"`
	// HeldAtEnd is the fault still in place in the last sample before
	// the restore (it did not undo itself).
	HeldAtEnd bool `json:"held_at_end"`
	// Restored is the fault seen gone after the restore.
	Restored       bool     `json:"restored"`
	RestoredAfterS *float64 `json:"restored_after_s,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	// Drops are the packets each partitioned container's filter dropped.
	Drops map[string]uint64 `json:"drops,omitempty"`
}

// judgeFault folds the samples into the fault's result. A row passes only
// when its fault was observed, held to the end of the hold and was seen
// restored: a fault that never happened cannot pass a row.
func judgeFault(spec FaultSpec, injected, restored time.Time, samples []Sample) FaultResult {
	fr := FaultResult{Spec: spec}
	var lastDuring *Sample
	notes := map[string]bool{}
	for i := range samples {
		s := &samples[i]
		switch s.Phase {
		case phaseDuring:
			fr.DuringSamples++
			lastDuring = s
			if s.FaultHeld != nil && *s.FaultHeld {
				fr.HeldSamples++
				if !fr.Observed {
					fr.Observed = true
					v := round1(s.At.Sub(injected).Seconds())
					fr.FirstHeldAfterS = &v
				}
			} else if s.FaultNote != "" {
				notes["during: "+s.FaultNote] = true
			}
		case phaseAfter:
			if s.Restored != nil && *s.Restored && !fr.Restored {
				fr.Restored = true
				v := round1(s.At.Sub(restored).Seconds())
				fr.RestoredAfterS = &v
			}
			if s.Restored != nil && !*s.Restored && !fr.Restored && s.FaultNote != "" {
				notes["after: "+s.FaultNote] = true
			}
		}
	}
	fr.HeldAtEnd = lastDuring != nil && lastDuring.FaultHeld != nil && *lastDuring.FaultHeld
	for n := range notes {
		fr.Notes = append(fr.Notes, n)
	}
	sort.Strings(fr.Notes)
	if len(fr.Notes) > 8 {
		fr.Notes = append(fr.Notes[:8], fmt.Sprintf("and %d more", len(fr.Notes)-8))
	}
	return fr
}

// faultFailures are the reasons a fault result fails its row.
func faultFailures(fr FaultResult) []string {
	var out []string
	switch {
	case fr.DuringSamples == 0:
		out = append(out, "fault: no sample was taken during the hold")
	case !fr.Observed:
		out = append(out, fmt.Sprintf("fault: never observed in place in %d sample(s) of the hold (%s): the row proves nothing", fr.DuringSamples, strings.Join(fr.Notes, "; ")))
	case !fr.HeldAtEnd:
		out = append(out, fmt.Sprintf("fault: not in place at the end of the hold (held in %d of %d samples)", fr.HeldSamples, fr.DuringSamples))
	}
	if !fr.Restored {
		out = append(out, "fault: never observed restored after the restore")
	}
	return out
}

// judgeExpect judges one expectation on the samples of its phase.
// baseline is the last sample before the fault; status holds each
// system's console/status/v1 frames in the phase.
func judgeExpect(e Expect, phase string, systems []string, start time.Time, samples []Sample, baseline *Sample, status map[string][]StatusFrame) ExpectResult {
	res := ExpectResult{Phase: phase, Expect: e, Systems: systems, Pass: true}
	var obs []string
	var phaseSamples []Sample
	for _, s := range samples {
		if s.Phase == phase {
			phaseSamples = append(phaseSamples, s)
		}
	}
	if len(phaseSamples) == 0 && e.Status == "" && e.StatusNot == "" && e.Source == "" && e.Field == "" {
		res.Pass = false
		res.Observed = "no sample in the phase"
		return res
	}
	deadline := start.Add(time.Duration(e.WithinS * float64(time.Second)))
	firstMet := func(sys string, ok func(r Readiness) bool) (float64, bool, Readiness) {
		var last Readiness
		for _, s := range phaseSamples {
			if s.At.After(deadline) {
				break
			}
			last = s.Ready[sys]
			if ok(last) {
				return round1(s.At.Sub(start).Seconds()), true, last
			}
		}
		return 0, false, last
	}
	met := func(sys string, after float64) {
		if res.AfterS == nil {
			res.AfterS = map[string]float64{}
		}
		res.AfterS[sys] = after
	}
	for _, sys := range systems {
		switch {
		case e.Ready == readyAlways:
			n := 0
			for _, s := range phaseSamples {
				r := s.Ready[sys]
				if !r.Ready() {
					res.Pass = false
					obs = append(obs, fmt.Sprintf("%s not ready at +%.1fs (%s)", sys, s.At.Sub(start).Seconds(), describe(r)))
					break
				}
				n++
			}
			if n == len(phaseSamples) {
				obs = append(obs, fmt.Sprintf("%s ready in all %d samples", sys, n))
			}
		case e.Ready == readyLost || e.Ready == readyBack:
			want := e.Ready == readyBack
			after, ok, last := firstMet(sys, func(r Readiness) bool { return r.Ready() == want })
			if ok {
				met(sys, after)
				obs = append(obs, fmt.Sprintf("%s %s at +%.1fs", sys, map[bool]string{true: "ready", false: "not ready"}[want], after))
			} else {
				res.Pass = false
				obs = append(obs, fmt.Sprintf("%s still %s at +%gs (%s)", sys, map[bool]string{true: "not ready", false: "ready"}[want], e.WithinS, describe(last)))
			}
		case e.Check != "" && e.Always:
			ok := true
			for _, s := range phaseSamples {
				st := s.Ready[sys].Checks[e.Check]
				if !slices.Contains(e.In, st) {
					ok = false
					obs = append(obs, fmt.Sprintf("%s check %s %q at +%.1fs, want %v", sys, e.Check, st, s.At.Sub(start).Seconds(), e.In))
					break
				}
			}
			if ok {
				obs = append(obs, fmt.Sprintf("%s check %s in %v in all %d samples", sys, e.Check, e.In, len(phaseSamples)))
			} else {
				res.Pass = false
			}
		case e.Check != "":
			after, ok, last := firstMet(sys, func(r Readiness) bool { return slices.Contains(e.In, r.Checks[e.Check]) })
			if ok {
				met(sys, after)
				obs = append(obs, fmt.Sprintf("%s check %s %q at +%.1fs", sys, e.Check, last.Checks[e.Check], after))
			} else {
				res.Pass = false
				st := last.Checks[e.Check]
				if st == "" {
					st = "absent"
				}
				obs = append(obs, fmt.Sprintf("%s check %s %s until +%gs, want %v (%s)", sys, e.Check, st, e.WithinS, e.In, describe(last)))
			}
		case e.Recovered:
			var base Readiness
			if baseline != nil {
				base = baseline.Ready[sys]
			}
			after, ok, last := firstMet(sys, func(r Readiness) bool { return recovered(base, r) })
			if ok {
				met(sys, after)
				obs = append(obs, fmt.Sprintf("%s recovered at +%.1fs", sys, after))
			} else {
				res.Pass = false
				obs = append(obs, fmt.Sprintf("%s not recovered by +%gs (%s)", sys, e.WithinS, describe(last)))
			}
		case e.Status != "":
			found := false
			for _, f := range status[sys] {
				if f.At.After(deadline) {
					break
				}
				if slices.Contains(f.Degraded, e.Status) {
					met(sys, round1(f.At.Sub(start).Seconds()))
					obs = append(obs, fmt.Sprintf("%s console status degraded %q at +%.1fs", sys, e.Status, f.At.Sub(start).Seconds()))
					found = true
					break
				}
			}
			if !found {
				res.Pass = false
				obs = append(obs, fmt.Sprintf("%s console status never listed %q by +%gs in %d frame(s)%s", sys, e.Status, e.WithinS, len(status[sys]), lastDegraded(status[sys])))
			}
		case e.Source != "":
			found := false
			last := "absent"
			for _, f := range status[sys] {
				if f.At.After(deadline) {
					break
				}
				st, ok := f.Sources[e.Source]
				if ok {
					last = st
				}
				if ok && slices.Contains(e.In, st) {
					met(sys, round1(f.At.Sub(start).Seconds()))
					obs = append(obs, fmt.Sprintf("%s console status source %s %q at +%.1fs", sys, e.Source, st, f.At.Sub(start).Seconds()))
					found = true
					break
				}
			}
			if !found {
				res.Pass = false
				obs = append(obs, fmt.Sprintf("%s console status source %s %s until +%gs in %d frame(s), want %v", sys, e.Source, last, e.WithinS, len(status[sys]), e.In))
			}
		case e.Field != "":
			found := false
			last := "absent"
			for _, f := range status[sys] {
				if f.At.After(deadline) {
					break
				}
				v := f.NATS
				if e.Field == "dp_state" {
					v = f.DPState
				}
				if v != "" {
					last = v
				}
				if slices.Contains(e.In, v) {
					met(sys, round1(f.At.Sub(start).Seconds()))
					obs = append(obs, fmt.Sprintf("%s console status %s %q at +%.1fs", sys, e.Field, v, f.At.Sub(start).Seconds()))
					found = true
					break
				}
			}
			if !found {
				res.Pass = false
				obs = append(obs, fmt.Sprintf("%s console status %s %s until +%gs in %d frame(s), want %v", sys, e.Field, last, e.WithinS, len(status[sys]), e.In))
			}
		case e.StatusNot != "":
			bad := false
			for _, f := range status[sys] {
				if slices.Contains(f.Degraded, e.StatusNot) {
					res.Pass = false
					bad = true
					obs = append(obs, fmt.Sprintf("%s console status degraded %q at +%.1fs", sys, e.StatusNot, f.At.Sub(start).Seconds()))
					break
				}
			}
			if !bad {
				obs = append(obs, fmt.Sprintf("%s console status did not list %q in %d frame(s)", sys, e.StatusNot, len(status[sys])))
			}
		}
	}
	res.Observed = strings.Join(obs, "; ")
	return res
}

// recovered is r back to what base was: answering 200, every check that
// was ok in base ok again.
func recovered(base, r Readiness) bool {
	if !r.Ready() {
		return false
	}
	for k, v := range base.Checks {
		if v == stateOK && r.Checks[k] != stateOK {
			return false
		}
	}
	return true
}

func describe(r Readiness) string {
	switch {
	case r.At.IsZero():
		return "not probed"
	case !r.Answered():
		return "no answer: " + r.Err
	}
	s := fmt.Sprintf("HTTP %d", r.Code)
	if r.Status != "" {
		s += " " + r.Status
	}
	if bad := r.NotOK(); len(bad) > 0 {
		s += " [" + strings.Join(bad, ", ") + "]"
	}
	return s
}

func lastDegraded(fs []StatusFrame) string {
	if len(fs) == 0 {
		return ""
	}
	return fmt.Sprintf("; last degraded %v", fs[len(fs)-1].Degraded)
}

func round1(v float64) float64 { return float64(int64(v*10+0.5*sign(v))) / 10 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

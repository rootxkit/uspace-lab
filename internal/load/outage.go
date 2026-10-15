package load

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-lab/internal/readiness"
)

// The cross-system outage row (05 §7, 05 §6, the failure columns of
// 02): one system taken down for 5 min while the load runs, and every
// other system judged on what the spec says it does meanwhile, from its
// readiness and the streams the run reads. Each expectation of an
// outage fault is one claim; outage_unflagged counts the claims not
// observed (and an outage whose fault was not seen in place, which
// measures nothing).

// Readiness systems an expectation may name ("others": every system the
// run reads but the faulted one; "all": every one).
var outageSystems = []string{"authority", "cisp", "ussp", "ansp", "issuer", "dss"}

// Bounds of the outage judgement (E-10).
const (
	outageSampleEvery   = 2 * time.Second
	outageProbeTimeout  = 3 * time.Second
	outageDefaultWithin = 10.0
	outageDefaultRecovS = 120.0
	maxOutageSamples    = 2000
	maxExpectations     = 32
)

// OutageExpect is one claim of an outage fault, one of four kinds:
//
//   - ready: "lost" (the system answers no 200 within within_s and until
//     the restore: the fault itself, seen in place) or "always" (every
//     sample of the hold answered 200);
//   - check with in: the system's named check is in one of the states
//     within within_s of the injection (it says so: flagged);
//   - check with age_grows: the age the check states (its detail's
//     "age N s", "age_s=N") grows by at least half the hold over the hold
//     (flagged with age: the consumer serves a cache and says how old);
//   - stream with flag: the stream (picture, traffic) lists the flag in
//     its degraded set within within_s.
type OutageExpect struct {
	System   string   `yaml:"system,omitempty" json:"system,omitempty"`
	Ready    string   `yaml:"ready,omitempty" json:"ready,omitempty"`
	Check    string   `yaml:"check,omitempty" json:"check,omitempty"`
	In       []string `yaml:"in,omitempty" json:"in,omitempty"`
	AgeGrows bool     `yaml:"age_grows,omitempty" json:"age_grows,omitempty"`
	Stream   string   `yaml:"stream,omitempty" json:"stream,omitempty"`
	Flag     string   `yaml:"flag,omitempty" json:"flag,omitempty"`
	WithinS  float64  `yaml:"within_s,omitempty" json:"within_s,omitempty"`
	Why      string   `yaml:"why" json:"why"`
}

// validate refuses an expectation that is not exactly one kind, names
// an unknown system or stream, or gives a bound outside the hold.
func (e *OutageExpect) validate(holdS float64) error {
	kinds := 0
	if e.Ready != "" {
		kinds++
		if e.Ready != "lost" && e.Ready != "always" {
			return fmt.Errorf("ready %q is lost or always", e.Ready)
		}
		if e.Check != "" || e.Stream != "" {
			return fmt.Errorf("ready names no check or stream")
		}
	}
	if e.Check != "" {
		kinds++
		if (len(e.In) > 0) == e.AgeGrows {
			return fmt.Errorf("check %s needs either in or age_grows", e.Check)
		}
	}
	if e.Stream != "" {
		kinds++
		if e.Stream != streamPicture && e.Stream != streamTraffic || e.Flag == "" || e.System != "" {
			return fmt.Errorf("stream %q is picture or traffic, with a flag and no system", e.Stream)
		}
	}
	if kinds != 1 {
		return fmt.Errorf("an expectation is one of ready, check or stream")
	}
	if e.Stream == "" && e.System != "others" && e.System != "all" && !slices.Contains(outageSystems, e.System) {
		return fmt.Errorf("system %q is one of %v, others or all", e.System, outageSystems)
	}
	if e.Check != "" && (e.System == "others" || e.System == "all") {
		return fmt.Errorf("a check names one system")
	}
	if e.WithinS < 0 || e.WithinS > holdS {
		return fmt.Errorf("within_s %v is not in [0, hold_s %v]", e.WithinS, holdS)
	}
	if e.Why == "" {
		return fmt.Errorf("why is required (the spec's words)")
	}
	return nil
}

func (e *OutageExpect) within() time.Duration {
	if e.WithinS > 0 {
		return seconds(e.WithinS)
	}
	return seconds(outageDefaultWithin)
}

// String names the expectation for the report.
func (e *OutageExpect) String() string {
	switch {
	case e.Ready != "":
		return e.System + " ready " + e.Ready
	case e.AgeGrows:
		return e.System + " " + e.Check + " age grows"
	case e.Check != "":
		return e.System + " " + e.Check + " in " + strings.Join(e.In, "|")
	default:
		return e.Stream + " shows " + e.Flag
	}
}

// OutageResult is one expectation as observed.
type OutageResult struct {
	Expect OutageExpect `json:"expect"`
	Met    bool         `json:"met"`
	// Observed says what was seen, met or not.
	Observed string `json:"observed"`
}

// outageSample is one sample of everything an outage is judged on.
type outageSample struct {
	at    time.Time
	ready map[string]readiness.Readiness
	flags map[string][]string // stream kind -> the degraded flags it shows now
}

// outageProbe samples the systems' readiness and the streams' current
// degraded flags.
type outageProbe struct {
	urls  map[string]string
	hc    *http.Client
	flags func() map[string][]string
}

func (p *outageProbe) sample(ctx context.Context) outageSample {
	s := outageSample{at: time.Now(), ready: map[string]readiness.Readiness{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, u := range p.urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := readiness.Probe(ctx, p.hc, u, outageProbeTimeout)
			mu.Lock()
			s.ready[name] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	if p.flags != nil {
		s.flags = p.flags()
	}
	return s
}

// ageOf is the age a check's detail states, in seconds.
var ageOf = regexp.MustCompile(`age(?:_s)?[= ](\d+(?:\.\d+)?)`)

func detailAge(d string) (float64, bool) {
	m := ageOf.FindStringSubmatch(d)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil
}

// judgeOutage judges one outage fault's expectations on the samples of
// its hold: from injected (the inject script's start) to restoring (the
// restore script's).
func judgeOutage(f *Fault, samples []outageSample, injected, restoring time.Time) []OutageResult {
	hold := make([]outageSample, 0, len(samples))
	for _, s := range samples {
		if !s.at.Before(injected) && s.at.Before(restoring) {
			hold = append(hold, s)
		}
	}
	out := make([]OutageResult, 0, len(f.Expect)+1)
	for i := range f.Expect {
		e := &f.Expect[i]
		res := OutageResult{Expect: *e}
		systems := []string{e.System}
		switch e.System {
		case "others":
			systems = slices.DeleteFunc(slices.Clone(outageSystems), func(s string) bool { return s == f.System })
		case "all":
			systems = outageSystems
		}
		switch {
		case len(hold) == 0:
			res.Observed = "no sample inside the hold"
		case e.Ready == "lost":
			res.Met, res.Observed = judgeLost(e.System, hold, injected.Add(e.within()))
		case e.Ready == "always":
			res.Met, res.Observed = judgeAlways(systems, hold)
		case e.AgeGrows:
			res.Met, res.Observed = judgeAge(e, hold, f.HoldS)
		case e.Check != "":
			res.Met, res.Observed = judgeCheck(e, hold, injected.Add(e.within()))
		default:
			res.Met, res.Observed = judgeFlag(e, hold, injected.Add(e.within()))
		}
		out = append(out, res)
	}
	return out
}

func judgeLost(system string, hold []outageSample, by time.Time) (bool, string) {
	first := time.Time{}
	readyAfter := 0
	for _, s := range hold {
		r, ok := s.ready[system]
		if !ok {
			return false, system + " was not probed"
		}
		if !r.Ready() && first.IsZero() {
			first = s.at
		}
		if !first.IsZero() && r.Ready() {
			readyAfter++
		}
	}
	switch {
	case first.IsZero():
		return false, fmt.Sprintf("%s answered 200 in all %d samples of the hold: the fault was not seen in place", system, len(hold))
	case first.After(by):
		return false, fmt.Sprintf("%s lost readiness %s after the injection, later than its bound", system, first.Sub(hold[0].at).Round(time.Second))
	case readyAfter > 0:
		return false, fmt.Sprintf("%s answered 200 again in %d samples before the restore", system, readyAfter)
	}
	return true, fmt.Sprintf("%s not ready from %s after the injection to the restore (%d samples)", system, first.Sub(hold[0].at).Round(time.Second), len(hold))
}

func judgeAlways(systems []string, hold []outageSample) (bool, string) {
	var bad []string
	for _, sys := range systems {
		n := 0
		last := ""
		for _, s := range hold {
			r, ok := s.ready[sys]
			if !ok {
				return false, sys + " was not probed"
			}
			if !r.Ready() {
				n++
				last = fmt.Sprintf("%d %s %s", r.Code, r.Err, strings.Join(r.NotOK(), ","))
			}
		}
		if n > 0 {
			bad = append(bad, fmt.Sprintf("%s not ready in %d of %d samples (last: %s)", sys, n, len(hold), strings.TrimSpace(last)))
		}
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, fmt.Sprintf("%s ready in all %d samples", strings.Join(systems, ", "), len(hold))
}

func judgeCheck(e *OutageExpect, hold []outageSample, by time.Time) (bool, string) {
	seen := map[string]bool{}
	for _, s := range hold {
		st, ok := s.ready[e.System].Checks[e.Check]
		if !ok {
			continue
		}
		seen[st] = true
		if slices.Contains(e.In, st) && !s.at.After(by) {
			return true, fmt.Sprintf("%s %s %s %s after the injection (%s)", e.System, e.Check, st, s.at.Sub(hold[0].at).Round(time.Second), s.ready[e.System].Details[e.Check])
		}
	}
	states := make([]string, 0, len(seen))
	for st := range seen {
		states = append(states, st)
	}
	sort.Strings(states)
	if len(states) == 0 {
		return false, fmt.Sprintf("%s showed no check %s during the hold", e.System, e.Check)
	}
	return false, fmt.Sprintf("%s %s was %v during the hold, never %v within %.0f s", e.System, e.Check, states, e.In, e.within().Seconds())
}

func judgeAge(e *OutageExpect, hold []outageSample, holdS float64) (bool, string) {
	first, last := -1.0, -1.0
	n := 0
	for _, s := range hold {
		d, ok := s.ready[e.System].Details[e.Check]
		if !ok {
			continue
		}
		age, ok := detailAge(d)
		if !ok {
			continue
		}
		if first < 0 {
			first = age
		}
		last = age
		n++
	}
	if n == 0 {
		return false, fmt.Sprintf("%s %s stated no age during the hold", e.System, e.Check)
	}
	if last-first < holdS/2 {
		return false, fmt.Sprintf("%s %s age went from %.0f s to %.0f s over the %.0f s hold (%d samples): it did not age with the outage", e.System, e.Check, first, last, holdS, n)
	}
	return true, fmt.Sprintf("%s %s age from %.0f s to %.0f s over the hold (%d samples)", e.System, e.Check, first, last, n)
}

func judgeFlag(e *OutageExpect, hold []outageSample, by time.Time) (bool, string) {
	for _, s := range hold {
		if slices.Contains(s.flags[e.Stream], e.Flag) && !s.at.After(by) {
			return true, fmt.Sprintf("the %s stream showed %s %s after the injection", e.Stream, e.Flag, s.at.Sub(hold[0].at).Round(time.Second))
		}
	}
	return false, fmt.Sprintf("the %s stream did not show %s within %.0f s of the injection", e.Stream, e.Flag, e.within().Seconds())
}

// judgeRecovered says whether every system probed answered 200 again
// within the fault's recovery bound after its restore.
func judgeRecovered(f *Fault, samples []outageSample, restored time.Time) OutageResult {
	bound := f.recoverWithin()
	e := OutageExpect{System: "all", Ready: "always", Why: "the stack recovers after the restore"}
	res := OutageResult{Expect: e}
	var after []outageSample
	for _, s := range samples {
		if s.at.After(restored) {
			after = append(after, s)
		}
	}
	for _, s := range after {
		all := true
		for _, sys := range outageSystems {
			if r, ok := s.ready[sys]; ok && !r.Ready() {
				all = false
			}
		}
		if all && !s.at.After(restored.Add(bound)) {
			res.Met, res.Observed = true, fmt.Sprintf("every system ready %s after the restore", s.at.Sub(restored).Round(time.Second))
			return res
		}
	}
	res.Observed = fmt.Sprintf("not every system was ready within %s of the restore (%d samples after it)", bound, len(after))
	return res
}

func (f *Fault) recoverWithin() time.Duration {
	if f.RecoverWithinS > 0 {
		return seconds(f.RecoverWithinS)
	}
	return seconds(outageDefaultRecovS)
}

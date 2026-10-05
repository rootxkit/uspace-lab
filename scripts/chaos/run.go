package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// harness runs the rows.
type harness struct {
	m       *Matrix
	lab     *Lab
	docker  Docker
	scripts string // directory of the domain scripts
	bash    string
	out     io.Writer
	status  map[string]*StatusLog
	skewRig *skewRig
	// shell runs a domain script; tests replace it.
	shell func(ctx context.Context, domain, action string, args []string) ScriptRun
}

// ScriptRun is one domain script run: what it printed and how it exited.
type ScriptRun struct {
	Command  string    `json:"command"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	ExitCode int       `json:"exit_code"`
	Output   []string  `json:"output"`
}

// maxScriptLines bounds what is kept of a script's output.
const maxScriptLines = 200

// scriptTimeout bounds one script run: the slowest act is stopping the
// whole stack, 10 s of grace per container in one docker stop.
const scriptTimeout = 3 * time.Minute

func (h *harness) runScript(ctx context.Context, domain, action string, args []string) ScriptRun {
	path := filepath.Join(h.scripts, domain+".sh")
	sr := ScriptRun{Command: strings.TrimSpace(fmt.Sprintf("scripts/chaos/%s.sh %s %s", domain, action, strings.Join(args, " "))), Start: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bash, append([]string{path, action}, args...)...) //nolint:gosec // G204: a script of this directory with validated names
	cmd.Env = append(os.Environ(), "CHAOS_PROJECT="+h.lab.Project, "CHAOS_NETWORK="+h.lab.Network)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{b: &buf}, &limited{b: &buf}
	err := cmd.Run()
	sr.End = time.Now().UTC()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		sr.ExitCode = ee.ExitCode()
	case err != nil:
		sr.ExitCode = -1
		sr.Output = append(sr.Output, "could not run: "+err.Error())
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		if len(sr.Output) >= maxScriptLines {
			sr.Output = append(sr.Output, "(output truncated)")
			break
		}
		sr.Output = append(sr.Output, sc.Text())
	}
	return sr
}

// systemNames are the probed endpoints' names in matrix order.
func (h *harness) systemNames() []string {
	out := make([]string, 0, len(h.m.Systems))
	for _, s := range h.m.Systems {
		out = append(out, s.Name)
	}
	return out
}

// probeAll probes every endpoint at once.
func (h *harness) probeAll(ctx context.Context) map[string]Readiness {
	out := make(map[string]Readiness, len(h.m.Systems))
	var mu sync.Mutex
	var wg sync.WaitGroup
	timeout := time.Duration(h.m.Probe.TimeoutS * float64(time.Second))
	for _, s := range h.m.Systems {
		u, err := h.lab.URL(s.Host, s.Path)
		if err != nil {
			out[s.Name] = Readiness{At: time.Now().UTC(), Err: err.Error()}
			continue
		}
		wg.Go(func() {
			r := probeReadiness(ctx, h.lab.HTTP, u, timeout)
			mu.Lock()
			out[s.Name] = r
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

// sample takes one look. spec and before are nil outside a row.
func (h *harness) sample(ctx context.Context, phase string, spec *FaultSpec, before map[string]Container) (Sample, map[string]Container) {
	s := Sample{Phase: phase}
	var cs map[string]Container
	var derr error
	var wg sync.WaitGroup
	wg.Go(func() {
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cs, derr = h.docker.Containers(dctx, h.lab.Project)
	})
	s.Ready = h.probeAll(ctx)
	wg.Wait()
	s.At = time.Now().UTC()
	if derr != nil {
		s.DockerErr = derr.Error()
		return s, nil
	}
	if spec != nil && spec.Kind != kindSkew {
		s.Containers = map[string]Container{}
		for _, svc := range spec.Services {
			s.Containers[svc] = cs[svc]
		}
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		switch phase {
		case phaseDuring:
			held, note := spec.heldNow(dctx, h.docker, cs)
			s.FaultHeld, s.FaultNote = &held, note
		case phaseAfter:
			ok, note := spec.restoredNow(dctx, h.docker, cs, before)
			s.Restored, s.FaultNote = &ok, note
		}
	}
	return s, cs
}

// tick waits for the next sample time or ctx.
func (h *harness) tick(ctx context.Context, t *time.Ticker) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// preflight waits until every endpoint answers 200, bounded, and returns
// that sample as the baseline with docker's view of the project.
func (h *harness) preflight(ctx context.Context) (Sample, map[string]Container, error) {
	deadline := time.Now().Add(time.Duration(h.m.Probe.PreflightS * float64(time.Second)))
	t := time.NewTicker(time.Duration(h.m.Probe.EveryS * float64(time.Second)))
	defer t.Stop()
	for {
		s, cs := h.sample(ctx, phaseBefore, nil, nil)
		var notReady []string
		for _, name := range h.systemNames() {
			if r := s.Ready[name]; !r.Ready() {
				notReady = append(notReady, name+" ("+describe(r)+")")
			}
		}
		if len(notReady) == 0 && cs != nil {
			return s, cs, nil
		}
		if time.Now().After(deadline) {
			why := strings.Join(notReady, ", ")
			if cs == nil {
				why = strings.TrimPrefix(why+", docker: "+s.DockerErr, ", ")
			}
			return s, cs, fmt.Errorf("not ready within %gs: %s", h.m.Probe.PreflightS, why)
		}
		if !h.tick(ctx, t) {
			return s, cs, ctx.Err()
		}
	}
}

// RowResult is one row as run.
type RowResult struct {
	ID       string    `json:"id"`
	Domain   string    `json:"domain"`
	Args     []string  `json:"args,omitempty"`
	System   string    `json:"system,omitempty"`
	Cite     []string  `json:"cite"`
	Claim    string    `json:"claim"`
	Pending  string    `json:"pending,omitempty"`
	HoldS    float64   `json:"hold_s"`
	Baseline *Sample   `json:"baseline,omitempty"`
	Injected time.Time `json:"injected_at"`
	// FaultAt is when the fault was first seen in place; the during
	// bounds count from it.
	FaultAt  time.Time         `json:"fault_at"`
	Restored time.Time         `json:"restored_at"`
	Ended    time.Time         `json:"ended_at"`
	Inject   *ScriptRun        `json:"inject,omitempty"`
	Restore  *ScriptRun        `json:"restore,omitempty"`
	Fault    *FaultResult      `json:"fault,omitempty"`
	Skew     []SkewResult      `json:"skew,omitempty"`
	Expect   []ExpectResult    `json:"expectations"`
	Alerts   map[string]string `json:"alerts,omitempty"`
	// AlertFindings are the background alert's duplicate raises and
	// clears inside this row's window (filled after the background ends).
	AlertFindings []AlertFinding           `json:"alert_findings,omitempty"`
	StatusFrames  map[string]int           `json:"status_frames,omitempty"`
	StreamEvents  map[string][]StreamEvent `json:"stream_events,omitempty"`
	Samples       int                      `json:"samples"`
	SamplesFile   string                   `json:"samples_file,omitempty"`
	Verdict       string                   `json:"verdict"` // pass, fail, not_run
	Failures      []string                 `json:"failures,omitempty"`

	samples []Sample
}

// Row verdicts.
const (
	verdictPass   = "pass"
	verdictFail   = "fail"
	verdictNotRun = "not_run"
)

func (h *harness) say(f string, a ...any) {
	_, _ = fmt.Fprintf(h.out, "chaos: %s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), fmt.Sprintf(f, a...))
}

// runRow runs one row: preflight, inject, hold, restore, recover.
//
//nolint:gocyclo // one ordered procedure, each step checked
func (h *harness) runRow(ctx context.Context, r Row) *RowResult {
	rr := &RowResult{ID: r.ID, Domain: r.Domain, Args: r.Args, System: r.System, Cite: r.Cite, Claim: r.Claim,
		Pending: r.Pending, HoldS: r.HoldS, Alerts: r.Alerts}
	fail := func(f string, a ...any) { rr.Failures = append(rr.Failures, fmt.Sprintf(f, a...)) }
	h.say("row %s: %s %s (%s)", r.ID, r.Domain, strings.Join(r.Args, " "), strings.Join(r.Cite, "; "))
	base, cs, err := h.preflight(ctx)
	rr.Baseline = &base
	if err != nil {
		rr.Verdict = verdictNotRun
		fail("preflight: %v (nothing was injected)", err)
		rr.Ended = time.Now().UTC()
		return rr
	}
	spec, err := faultSpec(&r, h.lab, cs)
	if err != nil {
		rr.Verdict = verdictNotRun
		fail("%v (nothing was injected)", err)
		rr.Ended = time.Now().UTC()
		return rr
	}
	every := time.Duration(h.m.Probe.EveryS * float64(time.Second))
	samples := []Sample{base}

	if r.Domain == "clock" {
		rr.Injected = time.Now().UTC()
		if h.skewRig == nil {
			rr.Verdict = verdictNotRun
			fail("the clock row needs the targets file's receiver (--targets)")
			return rr
		}
		for _, sk := range r.Skew {
			res := runSkew(ctx, h.skewRig, h.lab.Transport, sk, skewedClock)
			h.say("row %s: receiver clock %+gs: batch left %+.1fs off, authority answered %d (%s), want %s",
				r.ID, sk.SkewS, res.Measured, res.Code, res.Got, sk.Want)
			rr.Skew = append(rr.Skew, res)
			s, _ := h.sample(ctx, phaseDuring, nil, nil)
			samples = append(samples, s)
			if !res.Injected {
				fail("clock %+gs: the fault did not happen (%s)", sk.SkewS, res.Note)
			} else if !res.Pass {
				fail("clock %+gs: authority %s (%d %s), want %s", sk.SkewS, res.Got, res.Code, res.Answer, sk.Want)
			}
		}
		rr.Restored = time.Now().UTC()
		for i := range r.During {
			e := &r.During[i]
			rr.Expect = append(rr.Expect, judgeExpect(*e, phaseDuring, e.systems(h.systemNames(), &r), rr.Injected, samples, &base, nil))
		}
		rr.Ended = time.Now().UTC()
		h.finishRow(rr, samples)
		return rr
	}

	inj := h.shell(ctx, r.Domain, "inject", r.Args)
	rr.Inject = &inj
	rr.Injected = inj.Start
	for _, l := range inj.Output {
		_, _ = fmt.Fprintln(h.out, "  "+l)
	}
	if inj.ExitCode != 0 {
		fail("inject: %s exited %d", inj.Command, inj.ExitCode)
	}
	// The hold is counted from the end of the injection: a partition of
	// a whole system takes its script tens of seconds to put in place.
	t := time.NewTicker(every)
	holdEnd := inj.End.Add(time.Duration(r.HoldS * float64(time.Second)))
	for time.Now().Before(holdEnd) {
		s, _ := h.sample(ctx, phaseDuring, &spec, nil)
		samples = append(samples, s)
		if !h.tick(ctx, t) {
			break
		}
	}
	t.Stop()
	// The restore runs even when the injection failed: a half-applied
	// fault must not be left in the stack.
	rst := h.shell(context.WithoutCancel(ctx), r.Domain, "restore", r.Args)
	rr.Restore = &rst
	rr.Restored = rst.End
	for _, l := range rst.Output {
		_, _ = fmt.Fprintln(h.out, "  "+l)
	}
	if rst.ExitCode != 0 {
		fail("restore: %s exited %d", rst.Command, rst.ExitCode)
	}
	afterMax := r.RecoverWithinS
	for i := range r.After {
		afterMax = max(afterMax, r.After[i].WithinS)
	}
	afterEnd := rr.Restored.Add(time.Duration(afterMax * float64(time.Second)))
	t = time.NewTicker(every)
	for time.Now().Before(afterEnd) {
		// cs is docker's view before the fault: a restarted container
		// is one whose StartedAt moved on docker's clock.
		s, _ := h.sample(ctx, phaseAfter, &spec, cs)
		samples = append(samples, s)
		if h.afterMet(r, rr.Restored, samples, &base) {
			break
		}
		if !h.tick(ctx, t) {
			break
		}
	}
	t.Stop()
	rr.Ended = time.Now().UTC()

	fr := judgeFault(spec, rr.Injected, rr.Restored, samples)
	fr.Drops = spec.PartitionDrops()
	rr.Fault = &fr
	for _, f := range append(faultFailures(fr), spec.partitionFailures()...) {
		fail("%s", f)
	}
	// A during-bound counts from when the fault was first seen in place
	// (the systems cannot react to a fault before it exists), else from
	// the injection.
	faultAt := rr.Injected
	if fr.FirstHeldAfterS != nil {
		faultAt = rr.Injected.Add(time.Duration(*fr.FirstHeldAfterS * float64(time.Second)))
	}
	rr.FaultAt = faultAt
	during, after := h.statusIn(rr.Injected, rr.Restored), h.statusIn(rr.Restored, rr.Ended.Add(time.Second))
	for i := range r.During {
		e := &r.During[i]
		rr.Expect = append(rr.Expect, judgeExpect(*e, phaseDuring, e.systems(h.systemNames(), &r), faultAt, samples, &base, during))
	}
	for i := range r.After {
		e := &r.After[i]
		rr.Expect = append(rr.Expect, judgeExpect(*e, phaseAfter, e.systems(h.systemNames(), &r), rr.Restored, samples, &base, after))
	}
	h.finishRow(rr, samples)
	return rr
}

// afterMet is every after-expectation already met (the row can stop
// sampling early); the fault must also be seen restored.
func (h *harness) afterMet(r Row, restored time.Time, samples []Sample, base *Sample) bool {
	last := samples[len(samples)-1]
	if last.Restored == nil || !*last.Restored {
		return false
	}
	for i := range r.After {
		e := &r.After[i]
		if e.Ready == readyAlways || e.StatusNot != "" || (e.Check != "" && e.Always) {
			return false // an "always" needs the whole window
		}
		if e.Status != "" || e.Source != "" || e.Field != "" {
			if !judgeExpect(*e, phaseAfter, e.systems(h.systemNames(), &r), restored, samples, base, h.statusIn(restored, time.Now().Add(time.Second))).Pass {
				return false
			}
			continue
		}
		if !judgeExpect(*e, phaseAfter, e.systems(h.systemNames(), &r), restored, samples, base, nil).Pass {
			return false
		}
	}
	return true
}

func (h *harness) statusIn(from, to time.Time) map[string][]StatusFrame {
	out := map[string][]StatusFrame{}
	for sys, l := range h.status {
		fs, _ := l.Between(from, to)
		out[sys] = fs
	}
	return out
}

func (h *harness) finishRow(rr *RowResult, samples []Sample) {
	rr.samples = samples
	rr.Samples = len(samples)
	rr.StatusFrames = map[string]int{}
	rr.StreamEvents = map[string][]StreamEvent{}
	for sys, l := range h.status {
		fs, es := l.Between(rr.Injected, rr.Ended.Add(time.Second))
		rr.StatusFrames[sys] = len(fs)
		if len(es) > 0 {
			if len(es) > 20 {
				es = es[:20]
			}
			rr.StreamEvents[sys] = es
		}
	}
	for i := range rr.Expect {
		if e := &rr.Expect[i]; !e.Pass {
			rr.Failures = append(rr.Failures, fmt.Sprintf("%s %s: %s (%s)", e.Phase, e.Expect.label(), e.Observed, e.Expect.Why))
		}
	}
	if rr.Verdict == "" {
		rr.Verdict = verdictPass
		if len(rr.Failures) > 0 {
			rr.Verdict = verdictFail
		}
	}
}

// label names an expectation in a failure line.
func (e Expect) label() string {
	sys := strings.Join(e.System, ",")
	switch {
	case e.Ready != "":
		return sys + " ready " + e.Ready
	case e.Check != "":
		return fmt.Sprintf("%s check %s in %v", sys, e.Check, e.In)
	case e.Recovered:
		return sys + " recovered"
	case e.Status != "":
		return sys + " status degraded " + e.Status
	case e.StatusNot != "":
		return sys + " status not degraded " + e.StatusNot
	case e.Source != "":
		return fmt.Sprintf("%s source %s in %v", sys, e.Source, e.In)
	case e.Field != "":
		return fmt.Sprintf("%s status %s in %v", sys, e.Field, e.In)
	}
	return sys
}

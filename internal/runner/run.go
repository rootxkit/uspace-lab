// Package runner is the scenario runner (cmd/scenario, docs/PLAN.md D6):
// it loads a scenario and a targets file, starts the vehicles (SITL
// through the harness, or the synthetic stand-in), the simulators with
// the scenario's knobs and the collectors of each system's console
// stream, executes the timeline, and judges what was observed against
// the scenario's expectations, writing a result file.
package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/ed269"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/reftarget"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simop"
	"github.com/rootxkit/uspace-lab/internal/simrx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/verdict"
)

// Vehicle modes.
const (
	VehiclesSynthetic = "synthetic"
	VehiclesSITL      = "sitl"
)

// ErrNotRunnable is returned when the scenario cannot be judged by the
// targets given (for example a USSP-only kind against the reference
// target); the run is not started.
var ErrNotRunnable = errors.New("not runnable against these targets")

// Options configure one run.
type Options struct {
	Scenario string
	Targets  string
	Vehicles string
	// Lab overrides the targets file's sitl.env.
	Lab string
	// OutDir receives <scenario>.json; empty writes nothing.
	OutDir string
	Run    string
	// Prepare is the time between start and t0 (tokens, intents, streams
	// connecting); default 5 s synthetic, 15 s SITL.
	Prepare time.Duration
	Repo    string
	Log     *slog.Logger
}

// referenceKinds are what the reference target judges, per system.
var referenceKinds = map[string]map[string]bool{
	scenario.SystemUSSP:      {"proximity": true, "zone_incursion": true, "lost_link": true, observe.KindDegraded: true},
	scenario.SystemAuthority: {"zone_incursion": true, "height_120m": true, "unregistered": true, observe.KindDegraded: true},
}

// CheckRunnable refuses, before anything starts, a scenario the targets
// cannot judge.
func CheckRunnable(s *scenario.Scenario, t *Targets) error {
	if t.Mode == ModeReference {
		if !s.Reference {
			return fmt.Errorf("%w: scenario %s is for the systems (reference: false): %s", ErrNotRunnable, s.ID, s.Note)
		}
		for sys, kinds := range s.Kinds() {
			for _, k := range kinds {
				if !referenceKinds[sys][k] {
					return fmt.Errorf("%w: the reference target does not judge %s %s", ErrNotRunnable, sys, k)
				}
			}
		}
		return nil
	}
	for _, sys := range s.Systems {
		switch sys {
		case scenario.SystemUSSP:
			if t.USSP == nil {
				return fmt.Errorf("%w: the targets file has no ussp", ErrNotRunnable)
			}
		case scenario.SystemAuthority:
			if t.Authority == nil {
				return fmt.Errorf("%w: the targets file has no authority", ErrNotRunnable)
			}
		case scenario.SystemANSP:
			if t.ANSP == nil {
				return fmt.Errorf("%w: the targets file has no ansp", ErrNotRunnable)
			}
		}
	}
	return nil
}

type run struct {
	opt      Options
	sc       *scenario.Scenario
	tg       *Targets
	lab      *scenario.Lab
	comp     *scenario.Compiled
	geo      geoid.Undulator
	geoDesc  string
	log      *slog.Logger
	bus      *vehicle.Bus
	rec      *observe.Recorder
	ref      *reftarget.Target
	refURL   string
	refCreds refCredentials
	t0       time.Time

	mu        sync.Mutex
	steps     []vehicle.StepEvent
	stepFails []string
	samples   map[string]uint64
	intents   []IntentRecord
	timeline  []TimelineRecord
	operators map[string]*simop.Client
	receivers map[string]*simrx.Receiver
	feeds     map[string]feedHandle
	captures  map[string]string
	done      map[string]bool
}

type refCredentials struct {
	clientSecrets map[string]string
	rxBearer      map[string]string
	rxHMAC        map[string][]byte
	console       string
}

// Run executes one scenario and returns its result.
func Run(ctx context.Context, opt Options) (*Result, error) {
	if opt.Log == nil {
		opt.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	sc, err := scenario.Load(opt.Scenario)
	if err != nil {
		return nil, err
	}
	tg, err := LoadTargets(opt.Targets)
	if err != nil {
		return nil, err
	}
	if err := CheckRunnable(sc, tg); err != nil {
		return nil, err
	}
	labPath := opt.Lab
	if labPath == "" {
		labPath = tg.path(tg.Lab)
	}
	lab, err := scenario.LoadLab(labPath)
	if err != nil {
		return nil, err
	}
	g, desc, err := geoidx.Load(tg.Geoid)
	if err != nil {
		return nil, err
	}
	if opt.Vehicles == "" {
		opt.Vehicles = VehiclesSynthetic
	}
	if opt.Vehicles != VehiclesSynthetic && opt.Vehicles != VehiclesSITL {
		return nil, fmt.Errorf("vehicles is synthetic or sitl")
	}
	if opt.Vehicles == VehiclesSITL && (tg.SITL == nil || len(tg.SITL.ReaderCmd) == 0 || len(tg.SITL.FlyCmd) == 0) {
		return nil, fmt.Errorf("%w: SITL vehicles need sitl.reader_cmd and sitl.fly_cmd in the targets file", ErrNotRunnable)
	}
	if opt.Prepare <= 0 {
		opt.Prepare = 5 * time.Second
		if opt.Vehicles == VehiclesSITL {
			opt.Prepare = 15 * time.Second
		}
	}
	if opt.Run == "" {
		opt.Run = time.Now().UTC().Format("20060102T150405Z")
	}
	serials := map[string]string{}
	for _, a := range sc.Aircraft {
		serials[a.Serial] = a.Name
	}
	r := &run{
		opt: opt, sc: sc, tg: tg, lab: lab, geo: g, geoDesc: desc, log: opt.Log, bus: vehicle.NewBus(),
		rec: observe.NewRecorder(serials), samples: map[string]uint64{}, operators: map[string]*simop.Client{},
		receivers: map[string]*simrx.Receiver{}, feeds: map[string]feedHandle{}, captures: map[string]string{}, done: map[string]bool{},
	}
	return r.execute(ctx)
}

func (r *run) execute(ctx context.Context) (*Result, error) {
	started := time.Now().UTC()
	r.t0 = started.Add(r.opt.Prepare).Truncate(time.Second).Add(time.Second)
	comp, err := scenario.Compile(r.sc, r.lab, r.t0)
	if err != nil {
		return nil, err
	}
	r.comp = comp
	for _, p := range comp.Plans {
		if len(p.Steps) > 0 && p.Steps[0].AtS == nil {
			zero := 0.0
			p.Steps[0].AtS = &zero
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	if r.tg.Mode == ModeReference {
		if err := r.startReference(runCtx, &wg); err != nil {
			return nil, err
		}
	}
	// The runner's own record of the vehicle stream.
	all := r.bus.Subscribe("record", 4096)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := range all.C {
			name := r.nameOf(s.Sysid)
			r.mu.Lock()
			r.samples[name]++
			r.mu.Unlock()
		}
	}()
	simCtx, cancelSims := context.WithCancel(context.Background())
	defer cancelSims()
	var simWG sync.WaitGroup
	if err := r.startFeeds(runCtx, &wg); err != nil {
		return nil, err
	}
	if err := r.startOperators(runCtx, simCtx, &simWG); err != nil {
		return nil, err
	}
	if err := r.startReceivers(simCtx, &simWG); err != nil {
		return nil, err
	}
	r.startCollectors(runCtx)
	vehCtx, cancelVehicles := context.WithCancel(runCtx)
	defer cancelVehicles()
	var vehWG sync.WaitGroup
	if err := r.startVehicles(vehCtx, &vehWG); err != nil {
		return nil, err
	}
	if err := r.writeZones(); err != nil {
		return nil, err
	}
	r.log.Info("scenario started", "scenario", r.sc.ID, "t0", r.t0, "vehicles", r.opt.Vehicles, "targets", r.tg.Mode)
	r.runTimeline(runCtx)
	r.waitForEnd(runCtx)
	cancelVehicles()
	vehWG.Wait()
	// Drain the simulators: the ledger is read when everything sent is
	// acknowledged (or after a bound), never after a sleep.
	dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
	res := r.result(dctx, started)
	dcancel()
	cancelSims()
	simWG.Wait()
	ectx, ecancel := context.WithTimeout(context.Background(), 30*time.Second)
	r.endIntents(ectx, res)
	ecancel()
	cancel()
	r.bus.Close()
	wg.Wait()
	return res, nil
}

// writeZones writes the scenario's zones as an ED-269 document beside the
// result, placed by this run's origin, for a deployment to import into
// the authority before a systems run (the reference target loads them
// itself).
func (r *run) writeZones() error {
	if r.opt.OutDir == "" || len(r.comp.ED269) == 0 {
		return nil
	}
	title := "uspace-lab " + r.sc.ID
	doc := &ed269.Document{Title: &title, Zones: r.comp.ED269, Wrapper: ed269.WrapperFeatures}
	b, err := ed269.Export(doc)
	if err != nil {
		return fmt.Errorf("zones: %w", err)
	}
	if err := os.MkdirAll(r.opt.OutDir, 0o750); err != nil {
		return fmt.Errorf("zones: %w", err)
	}
	return os.WriteFile(filepath.Join(r.opt.OutDir, r.sc.ID+".zones.ed269.json"), b, 0o600)
}

func (r *run) nameOf(sysid int) string {
	for _, a := range r.sc.Aircraft {
		if a.Sysid == sysid {
			return a.Name
		}
	}
	return fmt.Sprintf("sysid-%d", sysid)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *run) clientName(a *scenario.Aircraft) string {
	if a.Operator.Client != "" {
		return a.Operator.Client
	}
	return "default"
}

// startReference starts the reference target with credentials made for
// this run (never written anywhere).
func (r *run) startReference(ctx context.Context, wg *sync.WaitGroup) error {
	r.refCreds = refCredentials{clientSecrets: map[string]string{}, rxBearer: map[string]string{}, rxHMAC: map[string][]byte{}, console: randomHex(16)}
	serialsByClient := map[string][]string{}
	for i := range r.sc.Aircraft {
		a := &r.sc.Aircraft[i]
		if a.Operator != nil {
			serialsByClient[r.clientName(a)] = append(serialsByClient[r.clientName(a)], a.Serial)
		}
	}
	clients := make([]reftarget.Client, 0, len(serialsByClient))
	for name, serials := range serialsByClient {
		secret := randomHex(16)
		r.refCreds.clientSecrets[name] = secret
		clients = append(clients, reftarget.Client{ID: "lab-" + name, Secret: secret, Serials: serials,
			Scopes: []string{reftarget.ScopeTelemetry, reftarget.ScopeIntents, reftarget.ScopeTraffic}})
	}
	receivers := make([]reftarget.Receiver, 0, len(r.sc.Receivers))
	for _, rx := range r.sc.Receivers {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		bearer := randomHex(16)
		r.refCreds.rxBearer[rx.ID], r.refCreds.rxHMAC[rx.ID] = bearer, key
		receivers = append(receivers, reftarget.Receiver{ID: rx.ID, BearerKey: bearer, HMACKey: key})
	}
	tg, err := reftarget.New(ctx, reftarget.Config{Policy: r.sc.PolicyDoc, Zones: r.comp.Zones, Geoid: r.geo,
		Audience: "reference.lab.invalid", Clients: clients, Receivers: receivers, ConsoleToken: r.refCreds.console})
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("reference target: %w", err)
	}
	srv := &http.Server{Handler: tg.Handler(), ReadHeaderTimeout: 5 * time.Second}
	r.ref, r.refURL = tg, "http://"+ln.Addr().String()
	wg.Add(2)
	go func() { defer wg.Done(); tg.Run(ctx) }()
	go func() {
		defer wg.Done()
		go func() {
			<-ctx.Done()
			_ = srv.Close()
		}()
		_ = srv.Serve(ln)
	}()
	return nil
}

// macOf is a deterministic locally administered unicast address per
// scenario, aircraft and receiver: the same transmitter every run.
func macOf(scenarioID, aircraft string) string {
	h := sha256.Sum256([]byte(scenarioID + "/" + aircraft))
	h[0] = 0x02
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", h[0], h[1], h[2], h[3], h[4], h[5])
}

// waitForEnd returns when every vehicle has flown its plan, the timeline
// is past and tail_s has run, or at t0 + duration_s.
func (r *run) waitForEnd(ctx context.Context) {
	hard := time.NewTimer(time.Until(r.t0.Add(seconds(r.sc.DurationS))))
	defer hard.Stop()
	lastTimed := r.t0
	for _, t := range r.comp.Timeline {
		if at := r.t0.Add(seconds(t.AtS)); at.After(lastTimed) {
			lastTimed = at
		}
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hard.C:
			r.log.Warn("duration_s reached before every vehicle finished")
			return
		case <-tick.C:
		}
		if time.Now().Before(lastTimed) || !r.allDone() {
			continue
		}
		select {
		case <-ctx.Done():
		case <-hard.C:
		case <-time.After(seconds(r.sc.TailS)):
		}
		return
	}
}

func (r *run) allDone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, p := range r.comp.Plans {
		if len(p.Steps) > 0 && !r.done[name] {
			return false
		}
	}
	return true
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// marks resolves t0, each step id (confirmed by every vehicle it names)
// and <id>.start.
func (r *run) marks() verdict.Marks {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := verdict.Marks{"t0": r.t0}
	confirmed := map[int]map[string]time.Time{}
	started := map[int]time.Time{}
	for _, ev := range r.steps {
		name := r.nameOf(ev.Sysid)
		idx := r.comp.StepOf[name]
		if ev.Step < 0 || ev.Step >= len(idx) {
			continue
		}
		si := idx[ev.Step]
		switch ev.State {
		case vehicle.StepConfirmed:
			if confirmed[si] == nil {
				confirmed[si] = map[string]time.Time{}
			}
			// The last plan step of the scenario step wins.
			if t := ev.At(); t.After(confirmed[si][name]) {
				confirmed[si][name] = t
			}
		case vehicle.StepStarted:
			if t, ok := started[si]; !ok || ev.At().Before(t) {
				started[si] = ev.At()
			}
		}
	}
	for i := range r.sc.Steps {
		st := &r.sc.Steps[i]
		if st.ID == "" {
			continue
		}
		if t, ok := started[i]; ok {
			m[st.ID+".start"] = t
		}
		if st.Do == scenario.DoKnob || st.Do == scenario.DoRequest {
			for _, tl := range r.timeline {
				if tl.Step == i && tl.Error == "" {
					m[st.ID], m[st.ID+".start"] = tl.At, tl.At
				}
			}
			continue
		}
		// Confirmed only when every vehicle the step names confirmed its
		// last plan step for it.
		lastIdx := map[string]int{}
		for name, idx := range r.comp.StepOf {
			for pi, si := range idx {
				if si == i {
					lastIdx[name] = pi
				}
			}
		}
		var latest time.Time
		ok := len(lastIdx) > 0
		for name, pi := range lastIdx {
			found := false
			for _, ev := range r.steps {
				if r.nameOf(ev.Sysid) == name && ev.Step == pi && ev.State == vehicle.StepConfirmed {
					found = true
					if ev.At().After(latest) {
						latest = ev.At()
					}
				}
			}
			ok = ok && found
		}
		if ok {
			m[st.ID] = latest
		}
	}
	return m
}

func (r *run) result(drain context.Context, started time.Time) *Result {
	res := &Result{Format: ResultFormat, Run: r.opt.Run, Scenario: r.sc.ID, Title: r.sc.Title, Source: r.sc.Source,
		Owners: r.sc.Owners, StartedAt: started, T0: r.t0, Images: r.tg.Images, PolicyVersion: r.sc.PolicyDoc.PolicyVersion,
		Policy: r.sc.PolicyDoc, Geoid: r.geoDesc}
	res.Mode.Vehicles, res.Mode.Targets, res.Mode.Name = r.opt.Vehicles, r.tg.Mode, r.tg.Name
	switch {
	case r.tg.Mode == ModeReference:
		res.Evidence = "runner self-check: judged by the lab's reference target (uspace-core alerting under the scenario's policy); never evidence for a system"
	case r.opt.Vehicles == VehiclesSynthetic:
		res.Evidence = "systems under test with synthetic vehicles: evidence of the systems' behaviour, not a SITL run"
	default:
		res.Evidence = "systems under test with SITL vehicles flown by sim/fly.py: INV-02 evidence when the images are pinned by digest"
	}
	repo := r.opt.Repo
	if repo == "" {
		repo = "."
	}
	res.Commits = commits(repo)
	res.Lab = LabInfo{File: r.lab.Path, OriginLat: r.lab.Origin.LatDeg, OriginLon: r.lab.Origin.LonDeg, OriginAMSL: r.lab.Origin.AltAMSLM, SpacingM: r.lab.SpacingM}
	names := make([]string, 0, len(r.operators))
	for n := range r.operators {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		res.Operators = append(res.Operators, r.operators[n].Drain(drain))
	}
	rxs := make([]string, 0, len(r.receivers))
	for n := range r.receivers {
		rxs = append(rxs, n)
	}
	sort.Strings(rxs)
	for _, n := range rxs {
		res.Receivers = append(res.Receivers, r.receivers[n].Drain(drain))
	}
	res.EndedAt = time.Now().UTC()
	marks := r.marks()
	res.Marks = marks
	events := r.rec.Events()
	res.Outcome = verdict.Evaluate(r.sc, marks, events)
	res.Frames, res.StreamErrors = r.rec.Frames(), r.rec.Errors()
	res.Latency = latency(res.Outcome.Groups)
	if r.ref != nil {
		res.TargetCounters = r.ref.Counters()
	}
	r.mu.Lock()
	res.Steps = append([]vehicle.StepEvent(nil), r.steps...)
	res.StepFailures = append([]string(nil), r.stepFails...)
	res.Intents = append([]IntentRecord(nil), r.intents...)
	res.Timeline = append([]TimelineRecord(nil), r.timeline...)
	res.VehicleSamples = map[string]uint64{}
	for k, v := range r.samples {
		res.VehicleSamples[k] = v
	}
	r.mu.Unlock()
	res.Failures = append(res.Failures, res.Outcome.Failures...)
	// Every step a vehicle was asked to fly must have been confirmed by
	// the vehicle (E-08): a run that ended with a vehicle short of its
	// plan did not fly the scenario, whatever the alerts say (found in
	// SITL: two vehicles held against rising terrain at full lean, their
	// goto never confirmed, while the alerts looked fine).
	for _, a := range r.sc.Aircraft {
		plan := r.comp.Plans[a.Name]
		confirmed := map[int]bool{}
		for _, ev := range res.Steps {
			if ev.Sysid == a.Sysid && ev.State == vehicle.StepConfirmed {
				confirmed[ev.Step] = true
			}
		}
		for i := range plan.Steps {
			if !confirmed[i] {
				res.Failures = append(res.Failures, fmt.Sprintf("vehicle %s: plan step %d (%s) was not confirmed by the vehicle", a.Name, i, plan.Steps[i].Action))
				break
			}
		}
	}
	for _, f := range res.StepFailures {
		res.Failures = append(res.Failures, "vehicle: "+f)
	}
	for i := range res.Operators {
		l := &res.Operators[i]
		if !l.Balanced {
			res.Failures = append(res.Failures, fmt.Sprintf("operator %s ledger does not balance: sent %d, accepted %d + refused %d + dropped %d + duplicate %d", l.Serial, l.Sent, l.Accepted, l.Refused, l.Dropped, l.Duplicate))
		}
	}
	for _, l := range res.Receivers {
		if !l.Balanced {
			res.Failures = append(res.Failures, fmt.Sprintf("receiver %s ledger does not balance: observed %d, sent %d, accepted %d + duplicates %d + refused %d, pending %d", l.ReceiverID, l.Observed, l.Sent, l.Accepted, l.Duplicates, l.Refused, l.Pending))
		}
	}
	for _, in := range res.Intents {
		if in.Error != "" {
			res.Failures = append(res.Failures, fmt.Sprintf("intent of %s: %s", in.Aircraft, in.Error))
		}
	}
	for _, ie := range r.sc.ExpectIntents {
		found := false
		for _, in := range res.Intents {
			if in.Aircraft != ie.Aircraft {
				continue
			}
			found = true
			if in.Decision != ie.Decision || (ie.State != "" && in.State != ie.State) {
				res.Failures = append(res.Failures, fmt.Sprintf("intent of %s: decision %q state %q, want %q %q", ie.Aircraft, in.Decision, in.State, ie.Decision, ie.State))
			}
		}
		if !found {
			res.Failures = append(res.Failures, "intent of "+ie.Aircraft+": not filed")
		}
	}
	for _, tl := range res.Timeline {
		if tl.Error != "" {
			res.Failures = append(res.Failures, fmt.Sprintf("step %d (%s): %s", tl.Step, tl.Do, tl.Error))
		}
	}
	for _, a := range r.sc.Aircraft {
		if res.VehicleSamples[a.Name] == 0 {
			res.Failures = append(res.Failures, "no vehicle samples from "+a.Name)
		}
	}
	sort.Strings(res.Failures)
	res.Failures = dedupe(res.Failures)
	res.Verdict = "pass"
	if len(res.Failures) > 0 || !res.Outcome.Pass {
		res.Verdict = "fail"
	}
	return res
}

func dedupe(xs []string) []string {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// RepoRoot finds the repository root from a path inside it.
func RepoRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

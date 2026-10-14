package load

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// FaultsFormat names a fault plan (load/faults/*.yaml).
const FaultsFormat = "lab-load-faults/v1"

// Rows a fault serves (05 §7).
const (
	FaultRestart   = "restart"
	FaultPartition = "partition"
	FaultOutage    = "outage"
)

// Bounds of a fault plan (E-10).
const (
	maxFaults       = 50
	maxFaultHoldS   = 1800
	faultGapS       = 30 // after one fault's restore, before the next fault
	faultScriptTime = 3 * time.Minute
	faultOutputMax  = 64 << 10
)

// FaultPlan is a fault plan: faults injected mid-run by the WP-L9
// scripts (scripts/chaos/<domain>.sh inject|restore ARGS), one at a time.
type FaultPlan struct {
	Format string  `yaml:"format" json:"format"`
	Faults []Fault `yaml:"faults" json:"faults"`
}

// Fault is one fault: which 05 §7 row it serves, when (seconds after
// the generator's start) and for how long, and the script that does it.
type Fault struct {
	Row    string   `yaml:"row" json:"row"`
	AtS    float64  `yaml:"at_s" json:"at_s"`
	HoldS  float64  `yaml:"hold_s" json:"hold_s"`
	Domain string   `yaml:"domain" json:"domain"`
	Args   []string `yaml:"args" json:"args"`
	// System is the system the fault is in (partition: the others must
	// show nothing).
	System string `yaml:"system" json:"system"`
}

var faultName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// LoadFaults reads and checks a fault plan against the run's duration
// and the scripts there are: rows known, names plain, every fault inside
// the run with its restore, one at a time with faultGapS between, at most
// maxFaults. Nothing is run here.
func LoadFaults(path, scripts string, durationS float64) (*FaultPlan, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the plan
	if err != nil {
		return nil, fmt.Errorf("load faults: %w", err)
	}
	var p FaultPlan
	if err := yaml.UnmarshalWithOptions(b, &p, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("load faults %s: %w", path, err)
	}
	if p.Format != FaultsFormat {
		return nil, fmt.Errorf("load faults %s: format %q, want %s", path, p.Format, FaultsFormat)
	}
	if len(p.Faults) == 0 || len(p.Faults) > maxFaults {
		return nil, fmt.Errorf("load faults %s: 1 to %d faults", path, maxFaults)
	}
	sort.SliceStable(p.Faults, func(i, j int) bool { return p.Faults[i].AtS < p.Faults[j].AtS })
	free := 0.0
	for i, f := range p.Faults {
		switch f.Row {
		case FaultRestart, FaultPartition, FaultOutage:
		default:
			return nil, fmt.Errorf("load faults: fault %d: row %q is restart, partition or outage", i, f.Row)
		}
		if !faultName.MatchString(f.Domain) || !faultName.MatchString(f.System) || len(f.Args) == 0 {
			return nil, fmt.Errorf("load faults: fault %d: a domain, a system and arguments are required, plain names", i)
		}
		for _, a := range f.Args {
			if !faultName.MatchString(a) {
				return nil, fmt.Errorf("load faults: fault %d: argument %q is not a plain name", i, a)
			}
		}
		if _, err := os.Stat(filepath.Join(scripts, f.Domain+".sh")); err != nil {
			return nil, fmt.Errorf("load faults: fault %d: no script %s.sh in %s", i, f.Domain, scripts)
		}
		if !(f.HoldS > 0 && f.HoldS <= maxFaultHoldS) {
			return nil, fmt.Errorf("load faults: fault %d: hold_s %v is not in (0, %d]", i, f.HoldS, maxFaultHoldS)
		}
		if f.AtS < free {
			return nil, fmt.Errorf("load faults: fault %d at %v s starts before the one before it is restored and %d s have passed (%v s)", i, f.AtS, faultGapS, free)
		}
		if f.AtS+f.HoldS+faultGapS > durationS {
			return nil, fmt.Errorf("load faults: fault %d (%v s + %v s) does not end %d s before the run (%v s)", i, f.AtS, f.HoldS, faultGapS, durationS)
		}
		free = f.AtS + f.HoldS + faultGapS
	}
	return &p, nil
}

// FaultRecord is what a fault did, on the generator's clock.
type FaultRecord struct {
	Fault
	// StartedAt is when the inject script started (the fault's window
	// opens), InjectedAt when it returned.
	StartedAt  *time.Time `json:"started_at,omitempty"`
	InjectedAt *time.Time `json:"injected_at,omitempty"`
	RestoredAt *time.Time `json:"restored_at,omitempty"`
	InjectOut  string     `json:"inject_output,omitempty"`
	RestoreOut string     `json:"restore_output,omitempty"`
	Error      string     `json:"error,omitempty"`
	// Recovered is the first sample newly shown after the restore, on
	// the picture and on any traffic stream (absent: never).
	PictureBackS *float64 `json:"picture_back_s,omitempty"`
	TrafficBackS *float64 `json:"traffic_back_s,omitempty"`
}

// faultRunner runs the plan's faults from the generator's start and
// learns, for each, when the first sample after its restore was shown.
type faultRunner struct {
	plan    *FaultPlan
	scripts string
	run     func(ctx context.Context, script string, args ...string) (string, error)

	mu      sync.Mutex
	records []FaultRecord
}

func newFaultRunner(plan *FaultPlan, scripts string) *faultRunner {
	fr := &faultRunner{plan: plan, scripts: scripts, records: make([]FaultRecord, len(plan.Faults))}
	for i, f := range plan.Faults {
		fr.records[i].Fault = f
	}
	fr.run = fr.bash
	return fr
}

// bash runs scripts/chaos/<script> with args, bounded, and returns its
// output (it prints what it did and when).
func (fr *faultRunner) bash(ctx context.Context, script string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, faultScriptTime)
	defer cancel()
	argv := append([]string{"bash", filepath.ToSlash(filepath.Join(fr.scripts, script))}, args...)
	b, err := command(ctx, argv)
	out := string(b)
	if len(out) > faultOutputMax {
		out = out[:faultOutputMax]
	}
	return strings.TrimSpace(out), err
}

// execute injects and restores every fault at its time; a script that
// fails is a run error (its restore is still run).
func (fr *faultRunner) execute(ctx context.Context, t0 time.Time, fail func(string, ...any)) {
	for i, f := range fr.plan.Faults {
		at := time.NewTimer(time.Until(t0.Add(seconds(f.AtS))))
		select {
		case <-ctx.Done():
			at.Stop()
			return
		case <-at.C:
		}
		// The fault's window opens as its script starts: the script acts
		// first and checks after, and what fails meanwhile is the fault's.
		started := time.Now()
		fr.mu.Lock()
		fr.records[i].StartedAt = &started
		fr.mu.Unlock()
		out, err := fr.run(ctx, f.Domain+".sh", append([]string{"inject"}, f.Args...)...)
		now := time.Now()
		fr.mu.Lock()
		fr.records[i].InjectedAt, fr.records[i].InjectOut = &now, out
		if err != nil {
			fr.records[i].Error = "inject: " + err.Error()
		}
		fr.mu.Unlock()
		if err != nil {
			fail("fault %d (%s %v): inject failed: %v", i, f.Domain, f.Args, err)
		}
		hold := time.NewTimer(seconds(f.HoldS))
		select {
		case <-ctx.Done():
		case <-hold.C:
		}
		hold.Stop()
		// The restore runs whatever happened, past a cancelled run too.
		out, err = fr.run(context.WithoutCancel(ctx), f.Domain+".sh", append([]string{"restore"}, f.Args...)...)
		now = time.Now()
		fr.mu.Lock()
		fr.records[i].RestoredAt, fr.records[i].RestoreOut = &now, out
		if err != nil {
			fr.records[i].Error += " restore: " + err.Error()
		}
		fr.mu.Unlock()
		if err != nil {
			fail("fault %d (%s %v): restore failed: %v", i, f.Domain, f.Args, err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// shown is told every sample newly shown (kind picture or traffic): the
// first after each restore is that fault's recovery.
func (fr *faultRunner) shown(kind string, at time.Time) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for i := range fr.records {
		r := &fr.records[i]
		if r.RestoredAt == nil || at.Before(*r.RestoredAt) {
			continue
		}
		d := round3(at.Sub(*r.RestoredAt).Seconds())
		switch {
		case kind == streamPicture && r.PictureBackS == nil:
			r.PictureBackS = &d
		case kind == streamTraffic && r.TrafficBackS == nil:
			r.TrafficBackS = &d
		}
	}
}

// observe turns the faults into the restart and partition rows. end is
// when the run stopped generating (a fault never recovered counts as
// down until then); events are the alerts seen; lost is the backlog the
// clients lost (shed or still pending after the drain); silent is the
// picture's silent loss.
func (fr *faultRunner) observe(obs map[string]Observation, end time.Time, events []observe.Event, lost, silent uint64, flags []flagSeen) []FaultRecord {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	worst, restarts, partitions := 0.0, 0, 0
	cross := 0
	for i := range fr.records {
		r := &fr.records[i]
		switch r.Row {
		case FaultRestart:
			restarts++
			if r.RestoredAt == nil {
				worst = max(worst, maxFaultHoldS)
				continue
			}
			down := round3(end.Sub(*r.RestoredAt).Seconds())
			back := func(p *float64) float64 {
				if p == nil {
					return down
				}
				return *p
			}
			worst = max(worst, back(r.PictureBackS), back(r.TrafficBackS))
		case FaultPartition:
			partitions++
			if r.StartedAt == nil || r.RestoredAt == nil {
				continue
			}
			// Another system's stream that came to say something is
			// degraded during the partition: an effect where 05 §6 says
			// there is none.
			for _, f := range flags {
				if f.system != r.System && !f.at.Before(*r.StartedAt) && !f.at.After(r.RestoredAt.Add(faultGapS*time.Second)) {
					cross++
				}
			}
		}
	}
	none := func(row string) Observation {
		return notMeasured(fmt.Sprintf("no %s fault in the run (loadgen run --faults load/faults/<plan>.yaml)", row))
	}
	if restarts > 0 {
		obs["restart_recovery_s"] = valueObs(worst)
		obs["restart_duplicate_alerts"] = valueObs(float64(duplicateRaises(events)))
		obs["restart_lost_backlog"] = valueObs(float64(lost))
	} else {
		obs["restart_recovery_s"], obs["restart_duplicate_alerts"], obs["restart_lost_backlog"] = none(FaultRestart), none(FaultRestart), none(FaultRestart)
	}
	if partitions > 0 {
		obs["partition_unreplayed"] = valueObs(float64(silent))
		obs["partition_cross_effects"] = valueObs(float64(cross))
	} else {
		obs["partition_unreplayed"], obs["partition_cross_effects"] = none(FaultPartition), none(FaultPartition)
	}
	return append([]FaultRecord(nil), fr.records...)
}

// duplicateRaises counts the alerts raised for an aircraft while another
// of the same kind was open for it: a restart that loses an alert's
// identity raises it again under a new id (the chaos findings F6, F8).
func duplicateRaises(events []observe.Event) int {
	evs := append([]observe.Event(nil), events...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].ObservedAt.Before(evs[j].ObservedAt) })
	open := map[string]map[string]bool{}
	n := 0
	for i := range evs {
		e := &evs[i]
		if e.System != scenario.SystemUSSP || e.Aircraft == "" {
			continue
		}
		k := e.Aircraft + "/" + e.Kind
		if open[k] == nil {
			open[k] = map[string]bool{}
		}
		switch e.Phase {
		case observe.PhaseRaised:
			if !open[k][e.AlertID] && len(open[k]) > 0 {
				n++
			}
			open[k][e.AlertID] = true
		case observe.PhaseCleared:
			delete(open[k], e.AlertID)
		}
	}
	return n
}

// during reports whether t falls inside a fault: from the start of its
// inject script to
// faultGapS after its restore (or the run's end when it was never
// restored).
func (fr *faultRunner) during(t time.Time) bool {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for i := range fr.records {
		r := &fr.records[i]
		if r.StartedAt == nil || t.Before(*r.StartedAt) {
			continue
		}
		if r.RestoredAt == nil || !t.After(r.RestoredAt.Add(faultGapS*time.Second)) {
			return true
		}
	}
	return false
}

// flagSeen is a degraded flag a stream showed for the first time.
type flagSeen struct {
	system, flag string
	at           time.Time
}

// seconds is s seconds as a duration.
func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

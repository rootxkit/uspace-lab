package load

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

const goodPlan = `format: lab-load-faults/v1
faults:
  - {row: restart, at_s: 100, hold_s: 5, domain: hotpath, args: [ussp-monitor], system: ussp}
  - {row: partition, at_s: 200, hold_s: 60, domain: nats, args: [authority], system: authority}
`

func writePlan(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A fault plan is checked whole before the run: format, rows, plain
// names, a script for the domain, every fault restored 30 s before the
// next and before the run's end. The committed plans load.
func TestFaultPlansFailClosed(t *testing.T) {
	scripts := repo("scripts/chaos")
	if _, err := LoadFaults(writePlan(t, goodPlan), scripts, 3600); err != nil {
		t.Fatalf("the good plan: %v", err)
	}
	for _, p := range []struct {
		file string
		dur  float64
	}{{"load/faults/ci.yaml", 120}, {"load/faults/60min.yaml", 3600}} {
		if _, err := LoadFaults(repo(p.file), scripts, p.dur); err != nil {
			t.Errorf("%s: %v", p.file, err)
		}
	}
	for name, c := range map[string]struct {
		text string
		dur  float64
		want string
	}{
		"format":     {strings.Replace(goodPlan, "v1", "v0", 1), 3600, "format"},
		"row":        {strings.Replace(goodPlan, "row: restart", "row: reboot", 1), 3600, "row"},
		"no script":  {strings.Replace(goodPlan, "domain: hotpath", "domain: nosuch", 1), 3600, "no script"},
		"argument":   {strings.Replace(goodPlan, "[ussp-monitor]", "[\"ussp-monitor; reboot\"]", 1), 3600, "plain name"},
		"overlap":    {strings.Replace(goodPlan, "at_s: 200", "at_s: 120", 1), 3600, "before the one before"},
		"beyond end": {goodPlan, 250, "does not end"},
		"hold":       {strings.Replace(goodPlan, "hold_s: 5", "hold_s: 0", 1), 3600, "hold_s"},
	} {
		if _, err := LoadFaults(writePlan(t, c.text), scripts, c.dur); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	f := loadSmall(t)
	if _, err := Run(context.Background(), Options{Files: f, TargetsPath: repo("targets/reference.yaml"), Run: "x", RepoRoot: repo("."), FaultsPath: writePlan(t, goodPlan)}); err == nil || !strings.Contains(err.Error(), "systems target") {
		t.Fatalf("faults against the reference target: %v", err)
	}
}

// The faults run at their times, each restore after its hold, a failed
// injection is a run error and its restore still runs; the first
// sample shown after a restore is its recovery, and a fault never
// recovered counts as down until the generator stopped.
func TestFaultsRunAndRecover(t *testing.T) {
	plan := &FaultPlan{Format: FaultsFormat, Faults: []Fault{
		{Row: FaultRestart, AtS: 0.01, HoldS: 0.01, Domain: "hotpath", Args: []string{"ussp-monitor"}, System: "ussp"},
		{Row: FaultRestart, AtS: 0.05, HoldS: 0.01, Domain: "hotpath", Args: []string{"authority-detect"}, System: "authority"},
	}}
	fr := newFaultRunner(plan, "scripts")
	var mu sync.Mutex
	var calls []string
	fr.run = func(_ context.Context, script string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, script+" "+strings.Join(args, " "))
		if args[0] == "inject" && args[1] == "authority-detect" {
			return "", errors.New("exit status 1")
		}
		return "chaos: done", nil
	}
	var fails []string
	t0 := time.Now()
	fr.execute(context.Background(), t0, func(f string, _ ...any) { fails = append(fails, f) })
	if len(calls) != 4 || calls[1] != "hotpath.sh restore ussp-monitor" || calls[3] != "hotpath.sh restore authority-detect" {
		t.Fatalf("calls %v", calls)
	}
	if len(fails) != 1 || !strings.Contains(fails[0], "inject failed") {
		t.Fatalf("fails %v", fails)
	}
	r0 := *fr.records[0].RestoredAt
	// The second fault, as if restored 10 s after the first: nothing is
	// shown after it, so it is never back.
	r1 := r0.Add(10 * time.Second)
	fr.records[1].RestoredAt = &r1
	fr.shown(streamPicture, r0.Add(-time.Second)) // before the restore: not a recovery
	fr.shown(streamPicture, r0.Add(2*time.Second))
	fr.shown(streamTraffic, r0.Add(3*time.Second))
	end := r0.Add(20 * time.Second)
	obs := map[string]Observation{}
	recs := fr.observe(obs, end, nil, 0, 0, nil)
	if recs[0].PictureBackS == nil || *recs[0].PictureBackS != 2 || *recs[0].TrafficBackS != 3 {
		t.Fatalf("recovery %+v", recs[0])
	}
	// Never back: down from its restore until the end, 10 s.
	if v := *obs["restart_recovery_s"].Value; v != 10 {
		t.Fatalf("restart_recovery_s %v", v)
	}
	if obs["partition_unreplayed"].Measured {
		t.Fatal("a run without a partition measured the partition row")
	}
}

// Duplicate raises: a second alert for an aircraft while another of the
// same kind is open is one; a raise after the clear is not; and other
// systems' alerts do not count.
func TestDuplicateRaises(t *testing.T) {
	t0 := time.Now()
	ev := func(s float64, id, phase, aircraft, system string) observe.Event {
		return observe.Event{System: system, Kind: "proximity", AlertID: id, Phase: phase, Aircraft: aircraft, ObservedAt: t0.Add(seconds(s))}
	}
	u, a := scenario.SystemUSSP, scenario.SystemAuthority
	events := []observe.Event{
		ev(1, "a1", observe.PhaseRaised, "ac-0", u),
		ev(2, "a2", observe.PhaseRaised, "ac-0", u), // duplicate: a1 open
		ev(3, "a1", observe.PhaseCleared, "ac-0", u),
		ev(4, "a2", observe.PhaseCleared, "ac-0", u),
		ev(5, "a3", observe.PhaseRaised, "ac-0", u), // nothing open
		ev(1, "v1", observe.PhaseRaised, "ac-1", a),
		ev(2, "v2", observe.PhaseRaised, "ac-1", a), // another system
	}
	if n := duplicateRaises(events); n != 1 {
		t.Fatalf("%d duplicates, want 1", n)
	}
}

// A partition's cross effects are the degraded states another system's
// stream came to show from the injection to 30 s after the restore; the
// partitioned system's own are expected.
func TestPartitionCrossEffects(t *testing.T) {
	inj := time.Now()
	res := inj.Add(60 * time.Second)
	fr := &faultRunner{records: []FaultRecord{{Fault: Fault{Row: FaultPartition, System: "authority"}, InjectedAt: &inj, RestoredAt: &res}}}
	flags := []flagSeen{
		{system: "authority", flag: "nats", at: inj.Add(time.Second)},     // its own
		{system: "ussp", flag: "registry", at: inj.Add(10 * time.Second)}, // an effect
		{system: "ussp", flag: "policy", at: inj.Add(-10 * time.Second)},  // before
		{system: "ussp", flag: "manned", at: res.Add(40 * time.Second)},   // after the window
	}
	obs := map[string]Observation{}
	fr.observe(obs, res.Add(time.Minute), nil, 0, 7, flags)
	if *obs["partition_cross_effects"].Value != 1 || *obs["partition_unreplayed"].Value != 7 {
		t.Fatalf("cross %v unreplayed %v", *obs["partition_cross_effects"].Value, *obs["partition_unreplayed"].Value)
	}
	if obs["restart_recovery_s"].Measured {
		t.Fatal("a run without a restart measured the restart row")
	}
}

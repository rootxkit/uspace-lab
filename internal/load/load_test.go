package load

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

func repo(p string) string { return filepath.Join(wiretest.Root(), filepath.FromSlash(p)) }

// smallTier is a run small and short enough for go test: one pair
// crossing at 6 s, four walkers, three heard aircraft, one console.
const smallTier = `
tier: test
title: test
column: 100
duration_s: 16
drain_s: 10
operators: {aircraft: 6, clients: 2, telemetry_hz: 1, telemetry_hz_status: pending GCAA, poll_ms: 20, intents: all}
receivers: {heard_fraction: 0.5, aircraft_per_receiver: 2, transports: [pack, single], batch_ms: 50}
consoles: 1
watch: {pairs: 1, walkers: 1}
min_samples: 5
memory_sample_s: 1
required: [operator_rate, rid_rate, operator_to_console_p99, receiver_to_picture_p99, proximity_raise_p99, expected_set,
  missed_raises, missed_clears, no_false_alarm, alerts_traceable, operator_identity, receiver_identity, picture_identity,
  picture_traceable, evaluation_period]
`

func smallPaths(policy string) string {
	return `
policy: ` + policy + `
seed: 7
bounds_m: {south_m: -6000, north_m: 20000, west_m: -2000, east_m: 20000}
walkers: {origin: {north_m: 2000, east_m: 2000}, spacing_m: 700, radius_m: 100, speed_ms: 8, turn_deg_per_s: 30,
  layers_rel_m: [15, 40, 65, 90], layer_tile: 2, margin_m: 5}
pairs: {origin: {north_m: -3000, east_m: 1500}, spacing_m: 6000, alt_rel_m: 115, speed_ms: 20, amplitude_m: 1300,
  first_crossing_s: 6, raise_margin_s: 2, clear_within_s: 9}
`
}

func writeFiles(t *testing.T, tier, paths string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tp, pp := filepath.Join(dir, "tier.yaml"), filepath.Join(dir, "paths.yaml")
	if err := os.WriteFile(tp, []byte(tier), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pp, []byte(paths), 0o600); err != nil {
		t.Fatal(err)
	}
	return tp, pp
}

func loadSmall(t *testing.T) *Files {
	t.Helper()
	tp, pp := writeFiles(t, smallTier, smallPaths(filepath.ToSlash(repo("scenarios/policy/demo.yaml"))))
	f, err := LoadFiles(tp, pp, repo("load/criteria.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The committed tiers, paths and criteria load; a file with an unknown
// key, a check on an unknown metric, a required check the criteria lack
// and a layout whose walkers could conflict are each refused.
func TestFilesFailClosed(t *testing.T) {
	for _, tier := range []string{"ci", "100", "1000", "1000-soak", "5000"} {
		if _, err := LoadFiles(repo("load/tiers/"+tier+".yaml"), repo("load/paths.yaml"), repo("load/criteria.yaml")); err != nil {
			t.Errorf("tier %s: %v", tier, err)
		}
	}
	policy := filepath.ToSlash(repo("scenarios/policy/demo.yaml"))
	cases := map[string]struct{ tier, paths, want string }{
		"unknown key":            {smallTier + "\nsurprise: 1\n", smallPaths(policy), "surprise"},
		"unknown required":       {strings.Replace(smallTier, "required: [", "required: [no_such_check, ", 1), smallPaths(policy), "no_such_check"},
		"walkers conflict":       {smallTier, strings.Replace(smallPaths(policy), "spacing_m: 700", "spacing_m: 500", 1), "horizontal minimum"},
		"layers too close":       {smallTier, strings.Replace(smallPaths(policy), "[15, 40, 65, 90]", "[15, 30, 65, 90]", 1), "vertical minimum"},
		"one alert per crossing": {smallTier, strings.Replace(smallPaths(policy), "amplitude_m: 1300", "amplitude_m: 700", 1), "between a clear and the next raise"},
		"too fast for a period":  {strings.Replace(smallTier, "telemetry_hz: 1,", "telemetry_hz: 5,", 1), smallPaths(policy), "telemetry_hz"},
	}
	for name, c := range cases {
		tp, pp := writeFiles(t, c.tier, c.paths)
		_, err := LoadFiles(tp, pp, repo("load/criteria.yaml"))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
	bad := filepath.Join(t.TempDir(), "criteria.yaml")
	_ = os.WriteFile(bad, []byte("source: x\nrows:\n  - {id: r, property: p, criterion: c, checks: [{id: k, metric: invented, stat: value, op: \"==\", value: 0, status: lab, basis: b}]}\n"), 0o600)
	tp, pp := writeFiles(t, smallTier, smallPaths(policy))
	if _, err := LoadFiles(tp, pp, bad); err == nil || !strings.Contains(err.Error(), "unknown metric") {
		t.Errorf("a check on an invented metric: %v", err)
	}
}

func reportWith(required []string, obs map[string]Observation) *Report {
	return &Report{Tier: &Tier{Required: required}, Metrics: obs}
}

var oneCheck = &Criteria{Source: "t", Rows: []Row{{ID: "r", Property: "p", Criterion: "c", Checks: []Check{
	{ID: "lat", Metric: "operator_to_ussp_console_s", Stat: StatP99, Op: "<", Value: 1, Status: StatusSpec, Basis: "b"},
	{ID: "loss", Metric: "picture_silent_loss", Stat: StatValue, Op: "==", Value: 0, Status: StatusSpec, Basis: "b"},
}}}}

func latencyObs(p99 float64) Observation {
	return Observation{Measured: true, N: 100, P50: &p99, P95: &p99, P99: &p99, Max: &p99}
}

// The verdict both ways: measured and within the criteria passes; a run
// that measured nothing fails; a required check not measured fails; a
// measured check outside its criterion fails.
func TestEvaluateFailsClosed(t *testing.T) {
	pass := reportWith([]string{"lat", "loss"}, map[string]Observation{"operator_to_ussp_console_s": latencyObs(0.2), "picture_silent_loss": valueObs(0)})
	pass.Evaluate(oneCheck, 60)
	if pass.Verdict != VerdictPass || !pass.Complete || pass.Measured != 2 {
		t.Fatalf("a measured, passing run: %s %v", pass.Verdict, pass.Failures)
	}
	nothing := reportWith(nil, map[string]Observation{})
	nothing.Evaluate(oneCheck, 60)
	if nothing.Verdict != VerdictFail || !strings.Contains(strings.Join(nothing.Failures, ";"), "measured nothing") {
		t.Fatalf("a run that measured nothing: %s %v", nothing.Verdict, nothing.Failures)
	}
	missing := reportWith([]string{"lat"}, map[string]Observation{"picture_silent_loss": valueObs(0)})
	missing.Evaluate(oneCheck, 60)
	if missing.Verdict != VerdictFail || missing.Rows[0].Result != "partial" {
		t.Fatalf("a required check not measured: %s %s", missing.Verdict, missing.Rows[0].Result)
	}
	slow := reportWith(nil, map[string]Observation{"operator_to_ussp_console_s": latencyObs(1.5), "picture_silent_loss": valueObs(0)})
	slow.Evaluate(oneCheck, 60)
	if slow.Verdict != VerdictFail || slow.Rows[0].Result != ResultFail {
		t.Fatalf("p99 1.5 s against < 1 s: %s", slow.Verdict)
	}
	short := &Criteria{Source: "t", Rows: []Row{{ID: "m", Property: "p", Criterion: "c", Checks: []Check{
		{ID: "mem", Metric: "memory_monotonic", Stat: StatValue, Op: "==", Value: 0, Status: StatusSpec, Basis: "b", MinDurationS: 600}}}}}
	tooShort := reportWith([]string{"mem"}, map[string]Observation{"memory_monotonic": valueObs(0)})
	tooShort.Evaluate(short, 120)
	if tooShort.Verdict != VerdictFail || tooShort.Rows[0].Checks[0].Result != ResultNotMeasured {
		t.Fatalf("a soak check on a two-minute run: %s %s", tooShort.Verdict, tooShort.Rows[0].Checks[0].Result)
	}
}

func TestHistogram(t *testing.T) {
	h := NewHistogram()
	if o := h.Observe(1); o.Measured {
		t.Fatal("an empty histogram measured")
	}
	for i := 1; i <= 100; i++ {
		h.Add(time.Duration(i) * time.Millisecond)
	}
	h.Add(-time.Millisecond)
	h.Add(90 * time.Second) // beyond the buckets
	if h.N() != 101 || h.Negative() != 1 {
		t.Fatalf("n %d negative %d", h.N(), h.Negative())
	}
	if q := h.Quantile(0.5); q != 0.052 { // the 51st of 101 is 51 ms, in the [51, 52) ms bucket
		t.Fatalf("p50 %v", q)
	}
	if q := h.Quantile(1); q != 90 {
		t.Fatalf("p100 %v", q)
	}
	if o := h.Observe(1000); o.Measured {
		t.Fatal("101 samples measured against a minimum of 1000")
	}
}

// A frame shows the sample nearest its captured_at once; a repeat and a
// frame that names no sample are counted apart; a sample never shown is
// unshown when it leaves the ring or at the close.
func TestLedger(t *testing.T) {
	l := NewLedger()
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for k := range ringSize + 2 {
		l.Hand("a", t0.Add(time.Duration(k)*time.Second))
	}
	if c := l.Counts(); c.Unshown != 2 {
		t.Fatalf("two samples left the ring unshown, counted %d", c.Unshown)
	}
	last := t0.Add(time.Duration(ringSize+1) * time.Second)
	if lat, ok := l.Show("a", last.Add(80*time.Millisecond), last.Add(300*time.Millisecond)); !ok || lat != 300*time.Millisecond {
		t.Fatalf("latency %v %v", lat, ok)
	}
	if _, ok := l.Show("a", last, last.Add(time.Second)); ok {
		t.Fatal("a repeat was timed")
	}
	if _, ok := l.Show("a", last.Add(10*time.Second), last.Add(11*time.Second)); ok {
		t.Fatal("a frame 10 s from every sample was timed")
	}
	if _, ok := l.Show("nobody", last, last); ok {
		t.Fatal("an unknown aircraft was timed")
	}
	c := l.Close()
	if c.Shown != 1 || c.Repeats != 1 || c.Untraceable != 2 || c.Unshown != 2+ringSize-1 || c.Handed != ringSize+2 {
		t.Fatalf("%+v", c)
	}
}

func TestMemoryVerdict(t *testing.T) {
	if _, _, ok := memoryVerdict([]uint64{1, 2, 3}); ok {
		t.Fatal("judged three samples")
	}
	growing := []uint64{100, 110, 120, 130, 140, 150, 160, 170, 180}
	if ratio, mono, ok := memoryVerdict(growing); !ok || !mono || ratio < 1.4 {
		t.Fatalf("growth not seen: %v %v %v", ratio, mono, ok)
	}
	flat := []uint64{100, 140, 90, 120, 100, 130, 95, 125, 105}
	if _, mono, ok := memoryVerdict(flat); !ok || mono {
		t.Fatalf("noise read as growth: %v %v", mono, ok)
	}
}

// The pair members cross at the expected times, turn at the amplitude,
// and the walkers keep inside their discs; the expected set has one
// raise per member per crossing inside the run.
func TestFleetPaths(t *testing.T) {
	f := loadSmall(t)
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	a, b := fl.Aircraft[0], fl.Aircraft[1]
	pr := f.Paths.Pairs
	for _, c := range []float64{pr.FirstCrossingS, pr.FirstCrossingS + 2*pr.AmplitudeM/pr.SpeedMS} {
		ea, va := fl.pairPos(a, c)
		eb, vb := fl.pairPos(b, c)
		if math.Abs(ea) > 1e-6 || math.Abs(eb) > 1e-6 || va != -vb {
			t.Fatalf("at the crossing %v s: %v %v (%v %v)", c, ea, eb, va, vb)
		}
	}
	if e, _ := fl.pairPos(a, pr.FirstCrossingS+pr.AmplitudeM/pr.SpeedMS); math.Abs(math.Abs(e)-pr.AmplitudeM) > 1e-6 {
		t.Fatalf("not at the amplitude at the turn: %v", e)
	}
	if len(fl.Expected) != 2 || fl.Expected[0].Peer != b.Name {
		t.Fatalf("expected %+v", fl.Expected)
	}
	start := time.Now()
	w := fl.Aircraft[2]
	for k := range int64(600) {
		s := fl.Sample(w, k, start.Add(time.Duration(k)*time.Second), start)
		if d := math.Hypot(w.posN-w.centre.NorthM, w.posE-w.centre.EastM); d > f.Paths.Walkers.RadiusM+1e-6 {
			t.Fatalf("walker left its disc by %v m at %d", d, k)
		}
		if s.AltHAEM == nil || s.TS == nil || !s.Armed {
			t.Fatalf("sample %+v", s)
		}
	}
	heard := 0
	for _, a := range fl.Aircraft {
		if a.Receiver >= 0 {
			heard++
		}
	}
	if heard != 3 || len(fl.Receivers) != 2 {
		t.Fatalf("heard %d in %d receivers", heard, len(fl.Receivers))
	}
}

// End to end against the reference target: the tier's operators,
// receivers, a console and the traffic of the watched aircraft; every
// required check is measured and passes, both pair members' alerts are
// raised, timed and cleared.
func TestRunAgainstTheReference(t *testing.T) {
	if testing.Short() {
		t.Skip("a 16 s load run")
	}
	f := loadSmall(t)
	rep, err := Run(context.Background(), Options{Files: f, TargetsPath: repo("targets/reference.yaml"), Run: "test", RepoRoot: wiretest.Root()})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictPass {
		t.Fatalf("verdict %s: %v\nalerts %+v\nloss %+v", rep.Verdict, rep.Failures, rep.Alerts, rep.Loss)
	}
	if rep.Alerts.Expected != 2 || rep.Alerts.Raised != 2 || rep.Alerts.Cleared != 2 {
		t.Fatalf("alerts %+v", rep.Alerts)
	}
	for _, m := range []string{"operator_to_ussp_console_s", "receiver_to_authority_picture_s", "alert_raise_s"} {
		if o := rep.Metrics[m]; !o.Measured || o.P99 == nil {
			t.Fatalf("%s not measured: %+v", m, o)
		}
	}
	if rep.Complete {
		t.Fatal("a reference run claims every 05 §7 row")
	}
	dir := filepath.Join(t.TempDir(), "run")
	if err := rep.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := rep.Write(dir); err == nil {
		t.Fatal("a second write over the record was allowed")
	}
	back, err := ReadReport(filepath.Join(dir, "report.json"))
	if err != nil || back.Verdict != VerdictPass {
		t.Fatalf("read back: %v", err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "report.md"))
	if !strings.Contains(string(md), "not measured") || !strings.Contains(string(md), "| **Ingest-to-picture latency** (pass)") {
		t.Fatalf("markdown:\n%s", md)
	}
}

// A run whose context ends before anything is measured fails: it says
// it measured nothing, never pass.
func TestRunThatMeasuresNothingFails(t *testing.T) {
	f := loadSmall(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := Run(ctx, Options{Files: f, TargetsPath: repo("targets/reference.yaml"), Run: "cancelled", RepoRoot: wiretest.Root()})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != VerdictFail || rep.Measured != 0 || !strings.Contains(strings.Join(rep.Failures, ";"), "measured nothing") {
		t.Fatalf("verdict %s, measured %d: %v", rep.Verdict, rep.Measured, rep.Failures)
	}
}

// Only the reference target is driven: a systems targets file is
// refused before anything starts.
func TestSystemsModeRefused(t *testing.T) {
	f := loadSmall(t)
	_, err := Run(context.Background(), Options{Files: f, TargetsPath: repo("targets/systems.example.yaml"), Run: "x"})
	if err == nil || !strings.Contains(err.Error(), "only the reference target") {
		t.Fatalf("%v", err)
	}
}

package load

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// TestMain lets the test binary stand in for a probe command
// (LOAD_PROBE_HELPER set): "fetch <url>" prints that process's /metrics
// for the scrape it is (a counter file under LOAD_PROBE_DIR), "sql
// <system> <statement>" prints a size or a lag, "fail" exits 3.
func TestMain(m *testing.M) {
	if os.Getenv("LOAD_PROBE_HELPER") == "1" {
		os.Exit(probeHelper(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func probeHelper(args []string) int {
	dir := os.Getenv("LOAD_PROBE_DIR")
	if len(args) == 0 || args[0] == "fail" {
		fmt.Fprintln(os.Stderr, "probe helper: refused")
		return 3
	}
	switch args[0] {
	case "stats":
		// docker stats: two of the project's containers (one unreadable)
		// and another project's.
		fmt.Print("lab-x-ussp-nats-1\t12.5%\nother-nats-1\t50%\nlab-x-dss-1\tbad\n")
		return 0
	case "fetch":
		url := args[len(args)-1]
		f := filepath.Join(dir, strings.NewReplacer(":", "_", "/", "_").Replace(url))
		n := 0
		if b, err := os.ReadFile(f); err == nil {
			n, _ = strconv.Atoi(string(b))
		}
		_ = os.WriteFile(f, []byte(strconv.Itoa(n+1)), 0o600)
		// The monitor's memory rises in thirds, the writer's stays flat;
		// the monitor checks 60 000 pairs between the first and the
		// last scrape; the writer's queue is 3 s old once; a gap counter
		// moves by 5.
		switch {
		case strings.Contains(url, "monitor"):
			fmt.Printf("# HELP x\nprocess_resident_memory_bytes %d\nussp_traffic_pair_checks{component=\"cpa\"} %d\nprocess_cpu_seconds_total %d\n", 100_000_000+n*20_000_000, n*60_000, n*2)
		case strings.Contains(url, "writer"):
			q := 0
			if n == 2 {
				q = 3
			}
			fmt.Printf("process_resident_memory_bytes 50000000\nussp_tsdb_writer_queue_age_seconds{stream=\"TRK\"} %d\nuspace_authority_gaps{component=\"tsdb_writer\"} %d\nussp_published_trk %d\nnot a series line\n", q, 5*n, 7*n)
		}
		return 0
	case "sql":
		system, stmt := args[1], args[len(args)-1]
		if strings.Contains(stmt, "compression") {
			fmt.Println("0")
			return 0
		}
		// The display provider's cache: how long it has cached, and what
		// is older than 24 h, as the test sets them.
		if strings.Contains(stmt, "network_rid") {
			fmt.Println(os.Getenv("LOAD_PROBE_DP_SINCE"))
			return 0
		}
		if strings.Contains(stmt, "ussp_flights") {
			fmt.Println(os.Getenv("LOAD_PROBE_DP_OVER"))
			return 0
		}
		f := filepath.Join(dir, "size-"+system)
		n := 0
		if b, err := os.ReadFile(f); err == nil {
			n, _ = strconv.Atoi(string(b))
		}
		_ = os.WriteFile(f, []byte(strconv.Itoa(n+1)), 0o600)
		// 600 000 bytes grow over the run.
		fmt.Println(10_000_000 + n*600_000)
		return 0
	}
	return 2
}

func helperProbes(t *testing.T) *runner.LoadProbes {
	t.Helper()
	t.Setenv("LOAD_PROBE_HELPER", "1")
	t.Setenv("LOAD_PROBE_DIR", t.TempDir())
	self := os.Args[0]
	return &runner.LoadProbes{
		Fetch:     []string{self, "fetch"},
		Metrics:   map[string]string{"ussp-monitor": "http://ussp-monitor:8083/metrics", "ussp-tsdb-writer": "http://ussp-tsdb-writer:8086/metrics"},
		Databases: []runner.LoadDatabase{{System: "ussp", Query: []string{self, "sql", "ussp"}}},
	}
}

// The probes read the systems as a run does (sizes, a scrape per
// period, a last scrape and the sizes after) and turn it into the
// rows: the monitor's rising memory is monotonic, the writer queue's
// largest age, the pair checks per second over the budget, the disk
// growth over the samples stored, the compression lag, and the gap
// counter that moved, listed. Each figure is the helper's, worked out
// by hand.
func TestProbesReadTheSystems(t *testing.T) {
	p := newProbes(helperProbes(t))
	ctx := context.Background()
	p.sizes(ctx, false)
	for range 9 {
		p.scrape(ctx)
	}
	p.sizes(ctx, true)
	obs := map[string]Observation{}
	sys := p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1000, 0, nil)
	if len(sys.Errors) != 0 {
		t.Fatalf("errors %v", sys.Errors)
	}
	val := func(m string) float64 {
		t.Helper()
		o := obs[m]
		if !o.Measured || o.Value == nil {
			t.Fatalf("%s not measured: %s", m, o.Reason)
		}
		return *o.Value
	}
	if val("memory_monotonic") != 1 || val("memory_growth_ratio") < 1.5 {
		t.Fatalf("memory %v %v", val("memory_monotonic"), val("memory_growth_ratio"))
	}
	if val("writer_queue_max_s") != 3 {
		t.Fatalf("writer queue %v", val("writer_queue_max_s"))
	}
	if r := sys.PairChecksPerS["ussp-monitor"]; r <= 0 || val("cpa_pair_checks_per_worker") != round3(r/50000) {
		t.Fatalf("pair checks %v/s, ratio %v", r, val("cpa_pair_checks_per_worker"))
	}
	if sys.CPUCores["ussp-monitor"] <= 0 || sys.CPUCores["ussp-tsdb-writer"] != 0 {
		t.Fatalf("CPU %v: the monitor's rises, the writer exports none", sys.CPUCores)
	}
	if val("disk_growth_vs_model") != 1 || val("compression_lag_chunks") != 0 {
		t.Fatalf("storage %v lag %v (%+v)", val("disk_growth_vs_model"), val("compression_lag_chunks"), sys.Storage)
	}
	if len(sys.Counters) != 1 || sys.Counters[0].Series != `uspace_authority_gaps{component="tsdb_writer"}` || sys.Counters[0].Delta != 40 {
		t.Fatalf("counters %+v", sys.Counters)
	}
	md := (&Report{Tier: &Tier{Tier: "t"}, Policy: &scenario.Policy{}, Systems: sys}).Markdown()
	for _, want := range []string{"## The systems (read by the probes)", "| ussp-monitor |", "uspace_authority_gaps", "| ussp | 1000 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report does not show %q", want)
		}
	}
}

// A probe that fails is said, and what it would have measured is not
// measured, never zero; probes that cannot read the systems are
// refused before a run starts.
func TestProbesFailClosed(t *testing.T) {
	cfg := helperProbes(t)
	cfg.Fetch = []string{os.Args[0], "fail"}
	cfg.Databases[0].Query = []string{os.Args[0], "fail"}
	p := newProbes(cfg)
	ctx := context.Background()
	p.sizes(ctx, false)
	p.scrape(ctx)
	p.scrape(ctx)
	p.sizes(ctx, true)
	obs := map[string]Observation{}
	sys := p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1000, 0, nil)
	if sys.ErrorsN == 0 || !strings.Contains(strings.Join(sys.Errors, ";"), "refused") {
		t.Fatalf("errors %v", sys.Errors)
	}
	for _, m := range []string{"memory_growth_ratio", "writer_queue_max_s", "cpa_pair_checks_per_worker", "disk_growth_vs_model", "compression_lag_chunks"} {
		if obs[m].Measured {
			t.Errorf("%s measured from a failed probe", m)
		}
	}
	for name, bad := range map[string]*runner.LoadProbes{
		"no fetch":    {Metrics: map[string]string{"a": "http://a/metrics"}},
		"no metrics":  {Fetch: []string{"x"}},
		"not http":    {Fetch: []string{"x"}, Metrics: map[string]string{"a": "file:///etc/passwd"}},
		"no query":    {Fetch: []string{"x"}, Metrics: map[string]string{"a": "http://a"}, Databases: []runner.LoadDatabase{{System: "u"}}},
		"named twice": {Fetch: []string{"x"}, Metrics: map[string]string{"a": "http://a"}, Databases: []runner.LoadDatabase{{System: "u", Query: []string{"q"}}, {System: "u", Query: []string{"q"}}}},
	} {
		if err := checkProbes(bad); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	if err := checkProbes(helperProbes(t)); err != nil {
		t.Fatalf("good probes refused: %v", err)
	}
}

// A systems run without probes does not judge the memory row on the
// harness's own heap (that is not the systems' memory) nor any probed
// row: each says the targets file names no probes.
func TestASystemsRunWithoutProbesSaysSo(t *testing.T) {
	f := loadSmall(t)
	for _, systems := range []bool{true, false} {
		r := &run{tier: f.Tier, files: f, fleet: &Fleet{}, picture: NewLedger(), traffic: NewReceiptLedger(), readers: &readers{},
			rec: observe.NewRecorder(nil), memory: make([]uint64, 12)}
		for i := range r.memory {
			r.memory[i] = 1000
		}
		if systems {
			r.systems = &systemsTarget{}
		}
		rep := &Report{}
		r.collect(rep, time.Now(), 1)
		for _, m := range []string{"memory_growth_ratio", "memory_monotonic", "writer_queue_max_s", "disk_growth_vs_model"} {
			o := rep.Metrics[m]
			switch {
			case systems && (o.Measured || !strings.Contains(o.Reason, "no probes")):
				t.Errorf("systems %s: %+v", m, o)
			case !systems && strings.HasPrefix(m, "memory") && !o.Measured:
				t.Errorf("reference %s: the harness's heap was not judged: %+v", m, o)
			}
		}
	}
}

// A probe that fails inside a fault the run injected (the killed
// process has no /metrics) is counted apart, not a run error; outside
// every fault it is one.
func TestProbeFailuresDuringAFaultAreExcused(t *testing.T) {
	cfg := helperProbes(t)
	cfg.Fetch = []string{os.Args[0], "fail"}
	p := newProbes(cfg)
	p.scrape(context.Background())
	inj, res := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	fr := &faultRunner{records: []FaultRecord{{StartedAt: &inj, InjectedAt: &inj, RestoredAt: &res}}}
	sys := p.observe(map[string]Observation{}, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1, 0, fr.during)
	if sys.ErrorsN != 0 || sys.ErrorsDuringFaults != 2 {
		t.Fatalf("inside the fault: %d errors, %d during faults", sys.ErrorsN, sys.ErrorsDuringFaults)
	}
	sys = p.observe(map[string]Observation{}, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1, 0, nil)
	if sys.ErrorsN != 2 {
		t.Fatalf("without faults: %d errors", sys.ErrorsN)
	}
}

// A scrape the run cancels (its probes stop when the generator ends)
// is not a probe failure: the recorded 1000 runs listed every process
// as failed, "context canceled", at that moment. The same fetch failing
// on a live context is one per process.
func TestACancelledScrapeIsNoFailure(t *testing.T) {
	cfg := helperProbes(t)
	cfg.Fetch = []string{os.Args[0], "fail"}
	p := newProbes(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.scrape(ctx)
	if len(p.errs) != 0 {
		t.Fatalf("a cancelled scrape failed: %+v", p.errs)
	}
	p.scrape(context.Background())
	if len(p.errs) != 2 {
		t.Fatalf("a failing scrape: %+v", p.errs)
	}
}

// A process restarted during the run starts its counters at zero again:
// the increase over the run is the sum of the increases between scrapes,
// a fall being a restart (the counter's new value is all new). Taken as
// last - first, the 100-tier run's restarted monitor reported -33 pair
// checks a second, and the pair-check row passed on it.
func TestCountersSurviveARestart(t *testing.T) {
	p := newProbes(&runner.LoadProbes{Metrics: map[string]string{"m": "http://m"}})
	t0 := time.Now()
	for i, v := range []float64{1000, 4000, 7000, 500, 3500} { // restart after 7000
		p.record("m", map[string]float64{`ussp_traffic_pair_checks{component="cpa"}`: v, `x_dropped_total`: v / 100}, t0.Add(time.Duration(i)*10*time.Second))
	}
	obs := map[string]Observation{}
	sys := p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 0, 0, nil)
	// 3000 + 3000 + 500 + 3000 = 9500 over 40 s.
	if r := sys.PairChecksPerS["m"]; r != round3(9500.0/40) {
		t.Fatalf("pair checks %v a second, want %v", r, round3(9500.0/40))
	}
	if len(sys.Counters) != 1 || sys.Counters[0].Delta != 95 {
		t.Fatalf("counters %+v", sys.Counters)
	}
}

// The display provider's 24 h bound is judged only on a cache older than
// the bound and the hour its disposal is checked in: a younger one cannot
// hold anything older than 24 h, so its 0 is not measured. On an old
// enough cache an entry past 24 h is counted (and fails the check), none
// passes; a run whose probes name no cache does not measure the row.
func TestDPCacheIsJudgedOnlyWhenOldEnough(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, since, over string
		measured          bool
		value             float64
	}{
		{"young", "3600", "0", false, 0},
		{"old, one entry past 24 h", "95000", "1", true, 1},
		{"old, none past", "95000", "0", true, 0},
	} {
		cfg := helperProbes(t)
		cfg.Databases[0].DPCache = true
		t.Setenv("LOAD_PROBE_DP_SINCE", c.since)
		t.Setenv("LOAD_PROBE_DP_OVER", c.over)
		p := newProbes(cfg)
		p.sizes(ctx, false)
		p.sizes(ctx, true)
		obs := map[string]Observation{}
		sys := p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1000, 0, nil)
		o := obs["f3411_dp_cache_over_24h"]
		if o.Measured != c.measured || (c.measured && *o.Value != c.value) || sys.DPCache == nil {
			t.Errorf("%s: %+v (cache %+v)", c.name, o, sys.DPCache)
		}
		if !c.measured && !strings.Contains(o.Reason, "has cached for 1.0 h") {
			t.Errorf("%s: reason %q", c.name, o.Reason)
		}
	}
	p := newProbes(helperProbes(t))
	p.sizes(ctx, false)
	p.sizes(ctx, true)
	obs := map[string]Observation{}
	p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1000, 0, nil)
	if obs["f3411_dp_cache_over_24h"].Measured || !strings.Contains(obs["f3411_dp_cache_over_24h"].Reason, "dp_cache") {
		t.Errorf("no cache named: %+v", obs["f3411_dp_cache_over_24h"])
	}
}

// docker stats is read for the project's containers only, an
// unreadable line skipped, each container's mean and largest kept.
func TestContainerCPU(t *testing.T) {
	cfg := helperProbes(t)
	cfg.Stats, cfg.StatsPrefix = []string{os.Args[0], "stats"}, "lab-x-"
	p := newProbes(cfg)
	p.stats(context.Background())
	p.stats(context.Background())
	obs := map[string]Observation{}
	sys := p.observe(obs, Models{TrackBytes: 600, CPAPairsPerCorePerS: 50000}, 1000, 0, nil)
	if len(sys.ContainerCPU) != 1 || sys.ContainerCPU[0].Container != "lab-x-ussp-nats-1" || sys.ContainerCPU[0].Samples != 2 ||
		sys.ContainerCPU[0].MeanPct != 12.5 || sys.ContainerCPU[0].MaxPct != 12.5 {
		t.Fatalf("container CPU %+v", sys.ContainerCPU)
	}
}

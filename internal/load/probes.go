package load

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

// Bounds of the probes (E-10): one command's time and output, the
// samples kept per process, the counters a report lists.
const (
	probeTimeout       = 20 * time.Second
	probeMaxOutput     = 4 << 20
	probeMaxSamples    = 100_000
	probeMaxProcesses  = 64
	maxSystemCounters  = 200
	probeErrorsKept    = 20
	metricRSS          = "process_resident_memory_bytes"
	metricCPU          = "process_cpu_seconds_total"
	metricWriterQueueS = "_tsdb_writer_queue_age_seconds"
	metricPairChecks   = "_pair_checks"
)

// lossCounter names the systems' counters of what they did not keep or
// show (05 §7 "every dropped_*, gap, degraded counter accounted for in
// the report"): the report lists each one that moved during the run.
var lossCounter = regexp.MustCompile(`(^|_)(dropped|gaps?|degraded|lost|refused|rate_limited|shed|overflow)(_|$)`)

// command runs argv with extra appended, bounded in time and output; an
// error carries the command's own words (its stderr, bounded).
func command(ctx context.Context, argv []string, extra ...string) ([]byte, error) {
	return commandWithin(ctx, probeTimeout, argv, extra...)
}

// commandWithin is command bounded by timeout instead of a probe's.
func commandWithin(ctx context.Context, timeout time.Duration, argv []string, extra ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("load: probe: no command")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(append([]string{}, argv[1:]...), extra...)
	cmd := exec.CommandContext(ctx, argv[0], args...) //nolint:gosec // the operator's targets file names the probe command
	var out, errb limitedBuffer
	out.max, errb.max = probeMaxOutput, 4096
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("load: probe %s: %w: %s", argv[0], err, strings.TrimSpace(errb.String()))
	}
	if out.over {
		return nil, fmt.Errorf("load: probe %s: more than %d bytes of output", argv[0], probeMaxOutput)
	}
	return out.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// parseProm reads a Prometheus text exposition into series name{labels}
// -> value, skipping comments; a line that is not "series value" is
// skipped, never guessed at.
func parseProm(b []byte) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i <= 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(line[:i])] = v
	}
	return out
}

// seriesName is the metric name of a series (before its labels).
func seriesName(s string) string {
	if i := strings.IndexByte(s, '{'); i >= 0 {
		return s[:i]
	}
	return s
}

// probes reads the systems while a run is under way: each process's
// /metrics every period (its resident memory, the writer queue age, the
// pair checks, the loss counters) and each telemetry database's size
// before and after the run.
type probes struct {
	cfg *runner.LoadProbes

	mu        sync.Mutex
	rss       map[string][]uint64
	queueMax  float64
	queueSeen bool
	// prev is each process's series at its last scrape, rise the sum of
	// their increases between scrapes (a fall is a restart: the new
	// value is all new), firstAt and lastAt the scrapes' times.
	prev    map[string]map[string]float64
	rise    map[string]map[string]float64
	firstAt map[string]time.Time
	lastAt  map[string]time.Time
	errs    []probeError
	sizes0  map[string]float64
	sizes1  map[string]float64
	lag     map[string]float64
	// dp is the display provider's cache as read after the run (nil:
	// not read).
	dp *DPCache
	// cpuPct are each container's CPU percentages as docker stats gave
	// them (bounded).
	cpuPct map[string][]float64
}

// DPCache is the display provider's cache as read after a run: how long
// the display provider has been caching (its first network track) and
// how many cached entries are older than F3411's 24 h.
type DPCache struct {
	System string  `json:"system"`
	SinceS float64 `json:"caching_for_s"`
	Over   float64 `json:"entries_over_24h"`
}

// Queries of the display provider's cache (uspace-authority's
// ussp_flights and tracks hypertables at the image under test).
const (
	sqlDPOver  = "SELECT count(*) FROM ussp_flights WHERE rx_ts < now() - interval '24 hours'"
	sqlDPSince = "SELECT coalesce(extract(epoch FROM now() - min(rx_ts)), 0) FROM tracks WHERE source = 'network_rid'"
	// dpCacheJudgedAfterS is how long the display provider must have
	// cached before the 24 h bound can be judged: a day, and the hour
	// the authority checks its disposal in.
	dpCacheJudgedAfterS = 25 * 3600
)

func newProbes(cfg *runner.LoadProbes) *probes {
	return &probes{cfg: cfg, rss: map[string][]uint64{}, prev: map[string]map[string]float64{}, rise: map[string]map[string]float64{},
		firstAt: map[string]time.Time{}, lastAt: map[string]time.Time{}, sizes0: map[string]float64{}, sizes1: map[string]float64{}, lag: map[string]float64{}}
}

// probeError is a probe that failed, and when.
type probeError struct {
	at  time.Time
	msg string
}

// maxProbeErrors bounds the failures kept; past it every failure is
// one the run cannot excuse.
const maxProbeErrors = 10_000

func (p *probes) fail(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs = append(p.errs, probeError{at: time.Now(), msg: fmt.Sprintf(format, a...)})
	if len(p.errs) > maxProbeErrors {
		p.errs = p.errs[:maxProbeErrors]
		p.errs = append(p.errs, probeError{msg: "more probe failures than are kept"})
	}
}

// processes is the metrics endpoints in a stable order.
func (p *probes) processes() []string {
	out := make([]string, 0, len(p.cfg.Metrics))
	for k := range p.cfg.Metrics {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scrape reads every endpoint once.
func (p *probes) scrape(ctx context.Context) {
	for _, proc := range p.processes() {
		b, err := command(ctx, p.cfg.Fetch, p.cfg.Metrics[proc])
		if err != nil {
			if ctx.Err() != nil {
				// The run stopped the probes mid-scrape (the generator
				// ended): that says nothing of the process.
				return
			}
			p.fail("metrics of %s: %v", proc, err)
			continue
		}
		p.record(proc, parseProm(b), time.Now())
	}
}

// record takes one scrape of proc at now.
func (p *probes) record(proc string, m map[string]float64, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.firstAt[proc]; !ok {
		p.firstAt[proc] = now
		p.rise[proc] = map[string]float64{}
	} else {
		for s, v := range m {
			last, seen := p.prev[proc][s]
			switch {
			case !seen:
			case v >= last:
				p.rise[proc][s] += v - last
			default:
				// The process restarted and counts from zero again.
				p.rise[proc][s] += v
			}
		}
	}
	p.prev[proc], p.lastAt[proc] = m, now
	if v, ok := m[metricRSS]; ok && v >= 0 && len(p.rss[proc]) < probeMaxSamples {
		p.rss[proc] = append(p.rss[proc], uint64(v))
	}
	for s, v := range m {
		if strings.HasSuffix(seriesName(s), metricWriterQueueS) && !math.IsNaN(v) {
			p.queueSeen = true
			p.queueMax = math.Max(p.queueMax, v)
		}
	}
}

// rises is a copy of every process's series rises so far.
func (p *probes) rises() map[string]map[string]float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]map[string]float64, len(p.rise))
	for proc, m := range p.rise {
		c := make(map[string]float64, len(m))
		for s, v := range m {
			c[s] = v
		}
		out[proc] = c
	}
	return out
}

// run scrapes every period until ctx ends.
func (p *probes) run(ctx context.Context, period time.Duration) {
	var wg sync.WaitGroup
	defer wg.Wait()
	if len(p.cfg.Stats) > 0 {
		// docker stats takes seconds: on its own ticker, never holding
		// the scrapes back.
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := time.NewTicker(period)
			defer tk.Stop()
			for {
				p.stats(ctx)
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
				}
			}
		}()
	}
	p.scrape(ctx)
	tk := time.NewTicker(period)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			p.scrape(ctx)
		}
	}
}

// stats reads every container's CPU once.
func (p *probes) stats(ctx context.Context) {
	b, err := command(ctx, p.cfg.Stats)
	if err != nil {
		if ctx.Err() == nil {
			p.fail("container CPU: %v", err)
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cpuPct == nil {
		p.cpuPct = map[string][]float64{}
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, pct, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !strings.HasPrefix(name, p.cfg.StatsPrefix) {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(pct), "%"), 64)
		if err != nil || len(p.cpuPct[name]) >= probeMaxSamples {
			continue
		}
		p.cpuPct[name] = append(p.cpuPct[name], v)
	}
}

const (
	sqlSize = "SELECT pg_database_size(current_database())"
	// sqlLag counts the chunks of every hypertable with a compression
	// policy that are past its compress_after and still uncompressed.
	sqlLag = "SELECT count(*) FROM timescaledb_information.chunks c JOIN timescaledb_information.jobs j " +
		"ON j.hypertable_schema = c.hypertable_schema AND j.hypertable_name = c.hypertable_name AND j.proc_name = 'policy_compression' " +
		"WHERE NOT c.is_compressed AND c.range_end < now() - (j.config->>'compress_after')::interval"
)

func (p *probes) query(ctx context.Context, db runner.LoadDatabase, sql string) (float64, error) {
	b, err := command(ctx, db.Query, sql)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, fmt.Errorf("load: %s: %q is not a number", db.System, strings.TrimSpace(string(b)))
	}
	return v, nil
}

// sizes reads every database's size into into (sizes0 before, sizes1
// after); after also counts the compression lag.
func (p *probes) sizes(ctx context.Context, after bool) {
	for _, db := range p.cfg.Databases {
		v, err := p.query(ctx, db, sqlSize)
		if err != nil {
			p.fail("size of %s: %v", db.System, err)
			continue
		}
		p.mu.Lock()
		if after {
			p.sizes1[db.System] = v
		} else {
			p.sizes0[db.System] = v
		}
		p.mu.Unlock()
		if !after {
			continue
		}
		lag, err := p.query(ctx, db, sqlLag)
		if err != nil {
			p.fail("compression lag of %s: %v", db.System, err)
			continue
		}
		p.mu.Lock()
		p.lag[db.System] = lag
		p.mu.Unlock()
		if db.DPCache {
			p.dpCache(ctx, db)
		}
	}
}

// dpCache reads the display provider's cache of db.
func (p *probes) dpCache(ctx context.Context, db runner.LoadDatabase) {
	since, err := p.query(ctx, db, sqlDPSince)
	if err != nil {
		p.fail("display provider cache age in %s: %v", db.System, err)
		return
	}
	over, err := p.query(ctx, db, sqlDPOver)
	if err != nil {
		p.fail("display provider cache in %s: %v", db.System, err)
		return
	}
	p.mu.Lock()
	p.dp = &DPCache{System: db.System, SinceS: math.Round(since), Over: over}
	p.mu.Unlock()
}

// ContainerCPU is one container's CPU over the run, percent of a core.
type ContainerCPU struct {
	Container string  `json:"container"`
	Samples   int     `json:"samples"`
	MeanPct   float64 `json:"mean_pct"`
	MaxPct    float64 `json:"max_pct"`
}

// SystemCounter is one loss counter of a system that moved in the run.
type SystemCounter struct {
	Process string  `json:"process"`
	Series  string  `json:"series"`
	Delta   float64 `json:"delta"`
}

// SystemMemory is a process's resident memory over the run.
type SystemMemory struct {
	Process string  `json:"process"`
	Samples int     `json:"samples"`
	FirstB  uint64  `json:"first_bytes"`
	LastB   uint64  `json:"last_bytes"`
	MaxB    uint64  `json:"max_bytes"`
	Growth  float64 `json:"growth_ratio,omitempty"`
	Rising  bool    `json:"monotonic"`
}

// SystemStorage is a database's growth over the run.
type SystemStorage struct {
	System  string  `json:"system"`
	BeforeB float64 `json:"before_bytes"`
	AfterB  float64 `json:"after_bytes"`
	// Samples is the fleet's telemetry samples over the run, the model's
	// basis (05 §1: raw telemetry stored per system, the whole fleet).
	Samples   uint64  `json:"fleet_samples"`
	ModelB    float64 `json:"model_bytes"`
	Ratio     float64 `json:"growth_vs_model,omitempty"`
	LagChunks float64 `json:"compression_lag_chunks"`
}

// Systems is what the probes read of the systems (systems mode).
type Systems struct {
	Memory   []SystemMemory  `json:"memory"`
	Storage  []SystemStorage `json:"storage"`
	Counters []SystemCounter `json:"loss_counters"`
	// CountersMore is how many moved counters were not listed (bound).
	CountersMore   int                `json:"loss_counters_not_listed,omitempty"`
	PairChecksPerS map[string]float64 `json:"pair_checks_per_s,omitempty"`
	// CPUCores is each process's CPU over the run per second of it (1 is
	// one core busy throughout): where a tier's load lands (05 §3, the
	// 5000 attempt's "what broke first").
	CPUCores map[string]float64 `json:"cpu_cores,omitempty"`
	// ContainerCPU is every container's CPU as docker stats sampled it,
	// mean and largest, in percent of one core: the brokers and the
	// databases too.
	ContainerCPU []ContainerCPU `json:"container_cpu,omitempty"`
	Errors       []string       `json:"errors,omitempty"`
	ErrorsN      int            `json:"errors_count,omitempty"`
	// ErrorsDuringFaults are the probes that failed inside a fault the
	// run injected.
	ErrorsDuringFaults int `json:"errors_during_faults,omitempty"`
	// DPCache is the display provider's cache after the run.
	DPCache *DPCache `json:"dp_cache,omitempty"`
}

// observe turns what the probes read into observations: memory, the
// writer queue, the pair checks, the storage, and the report's Systems.
// fleetSamples is the telemetry samples the fleet made in the run (05
// §1 stores the whole fleet's raw telemetry in every system with a
// telemetry hypertable, about track_bytes each); warmup is how many
// memory samples to leave out.
//
// excused says whether a failure at t falls inside a fault the run
// injected (a killed process does not answer its /metrics); such a
// failure is counted, not an error of the run (nil: none is).
func (p *probes) observe(obs map[string]Observation, models Models, fleetSamples uint64, warmup int, excused func(time.Time) bool) *Systems {
	p.mu.Lock()
	defer p.mu.Unlock()
	sys := &Systems{PairChecksPerS: map[string]float64{}, CPUCores: map[string]float64{}}
	for _, e := range p.errs {
		if excused != nil && !e.at.IsZero() && excused(e.at) {
			sys.ErrorsDuringFaults++
			continue
		}
		sys.ErrorsN++
		if len(sys.Errors) < probeErrorsKept {
			sys.Errors = append(sys.Errors, e.at.UTC().Format(time.RFC3339)+" "+e.msg)
		}
	}
	// Memory: per process, judged as the harness's heap is.
	worstG, anyRising, judged := 0.0, false, 0
	for _, proc := range sortedKeysOf(p.rss) {
		s := p.rss[proc]
		m := SystemMemory{Process: proc, Samples: len(s)}
		if len(s) > 0 {
			m.FirstB, m.LastB = s[0], s[len(s)-1]
			for _, v := range s {
				m.MaxB = max(m.MaxB, v)
			}
		}
		if g, rising, ok := memoryVerdict(s[min(warmup, len(s)):]); ok {
			m.Growth, m.Rising = round3(g), rising
			worstG = math.Max(worstG, g)
			anyRising = anyRising || rising
			judged++
		}
		sys.Memory = append(sys.Memory, m)
	}
	if judged > 0 {
		obs["memory_growth_ratio"] = valueObs(round3(worstG))
		v := 0.0
		if anyRising {
			v = 1
		}
		obs["memory_monotonic"] = valueObs(v)
	} else {
		obs["memory_growth_ratio"] = notMeasured(fmt.Sprintf("no system process had 6 resident-memory samples after the warm-up (%d processes read)", len(p.rss)))
		obs["memory_monotonic"] = obs["memory_growth_ratio"]
	}
	// The writer queue.
	if p.queueSeen {
		obs["writer_queue_max_s"] = valueObs(p.queueMax)
	} else {
		obs["writer_queue_max_s"] = notMeasured("no process exposed a *" + metricWriterQueueS + " series")
	}
	// Pair checks per second, per process (a CPA worker is a process).
	busiest, seen := 0.0, false
	for _, proc := range sortedKeysOf(p.prev) {
		dt := p.lastAt[proc].Sub(p.firstAt[proc]).Seconds()
		if dt <= 0 {
			continue
		}
		if c, ok := p.rise[proc][metricCPU]; ok {
			sys.CPUCores[proc] = round3(c / dt)
		}
		var d float64
		has := false
		for s := range p.prev[proc] {
			if strings.HasSuffix(seriesName(s), metricPairChecks) {
				d += p.rise[proc][s]
				has = true
			}
		}
		if has {
			rate := d / dt
			sys.PairChecksPerS[proc] = round3(rate)
			busiest = math.Max(busiest, rate)
			seen = true
		}
	}
	if seen && models.CPAPairsPerCorePerS > 0 {
		obs["cpa_pair_checks_per_worker"] = valueObs(round3(busiest / models.CPAPairsPerCorePerS))
	} else {
		obs["cpa_pair_checks_per_worker"] = notMeasured("no process exposed a *" + metricPairChecks + " counter read twice")
	}
	// Loss counters that moved.
	for _, proc := range sortedKeysOf(p.rise) {
		r := p.rise[proc]
		for _, s := range sortedKeysOf(r) {
			if !lossCounter.MatchString(seriesName(s)) {
				continue
			}
			if d := r[s]; d > 0 {
				if len(sys.Counters) < maxSystemCounters {
					sys.Counters = append(sys.Counters, SystemCounter{Process: proc, Series: s, Delta: d})
				} else {
					sys.CountersMore++
				}
			}
		}
	}
	// Storage.
	var grew, model float64
	var lag float64
	measured, lagMeasured := 0, 0
	for _, db := range p.cfg.Databases {
		st := SystemStorage{System: db.System, BeforeB: p.sizes0[db.System], AfterB: p.sizes1[db.System], Samples: fleetSamples,
			ModelB: float64(fleetSamples) * models.TrackBytes, LagChunks: p.lag[db.System]}
		_, ok0 := p.sizes0[db.System]
		_, ok1 := p.sizes1[db.System]
		if ok0 && ok1 && st.ModelB > 0 {
			st.Ratio = round3((st.AfterB - st.BeforeB) / st.ModelB)
			grew += st.AfterB - st.BeforeB
			model += st.ModelB
			measured++
		}
		if _, ok := p.lag[db.System]; ok {
			lag += st.LagChunks
			lagMeasured++
		}
		sys.Storage = append(sys.Storage, st)
	}
	if measured > 0 && measured == len(p.cfg.Databases) {
		obs["disk_growth_vs_model"] = valueObs(round3(grew / model))
	} else {
		obs["disk_growth_vs_model"] = notMeasured(fmt.Sprintf("%d of %d databases read before and after a run that made samples", measured, len(p.cfg.Databases)))
	}
	if lagMeasured > 0 && lagMeasured == len(p.cfg.Databases) {
		obs["compression_lag_chunks"] = valueObs(lag)
	} else {
		obs["compression_lag_chunks"] = notMeasured(fmt.Sprintf("the compression lag of %d of %d databases was read", lagMeasured, len(p.cfg.Databases)))
	}
	for _, name := range sortedKeysOf(p.cpuPct) {
		s := p.cpuPct[name]
		c := ContainerCPU{Container: name, Samples: len(s)}
		for _, v := range s {
			c.MeanPct += v / float64(len(s))
			c.MaxPct = math.Max(c.MaxPct, v)
		}
		c.MeanPct = round3(c.MeanPct)
		sys.ContainerCPU = append(sys.ContainerCPU, c)
	}
	// The display provider's cache: judged only once it has cached for
	// longer than the bound (and the hour disposal is checked in); a
	// younger cache cannot hold anything older than 24 h, so a 0 there
	// would prove nothing.
	sys.DPCache = p.dp
	named := false
	for _, db := range p.cfg.Databases {
		named = named || db.DPCache
	}
	switch {
	case !named:
		obs["f3411_dp_cache_over_24h"] = notMeasured("no probed database holds the display provider's cache (deploy/systems/probes.yaml dp_cache)")
	case p.dp == nil:
		obs["f3411_dp_cache_over_24h"] = notMeasured("the display provider's cache could not be read (the probes' errors say why)")
	case p.dp.SinceS < dpCacheJudgedAfterS:
		obs["f3411_dp_cache_over_24h"] = notMeasured(fmt.Sprintf("the authority's display provider has cached for %.1f h (its first network track); the 24 h bound is judged once it has cached for %d h (a day, and the hour disposal is checked in)",
			p.dp.SinceS/3600, dpCacheJudgedAfterS/3600))
	default:
		obs["f3411_dp_cache_over_24h"] = valueObs(p.dp.Over)
	}
	return sys
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

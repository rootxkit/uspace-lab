package load

import (
	"context"
	"fmt"
	"sort"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

// LossCounters reads every process's /metrics through the probes'
// fetch command once and keeps its loss counters (lossCounter: what a
// system did not keep or show), by process; a process that does not
// answer is named in the errors and left out. The chaos matrix reads
// them before and after each row (scripts/chaos), as a load run does
// over the run.
func LossCounters(ctx context.Context, p *runner.LoadProbes) (map[string]map[string]float64, []string) {
	out := map[string]map[string]float64{}
	var errs []string
	if p == nil {
		return out, []string{"no probes"}
	}
	procs := make([]string, 0, len(p.Metrics))
	for proc := range p.Metrics {
		procs = append(procs, proc)
	}
	sort.Strings(procs)
	for _, proc := range procs {
		b, err := command(ctx, p.Fetch, p.Metrics[proc])
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", proc, err))
			continue
		}
		m := map[string]float64{}
		for s, v := range parseProm(b) {
			if lossCounter.MatchString(seriesName(s)) {
				m[s] = v
			}
		}
		out[proc] = m
	}
	return out, errs
}

// CounterRises are the loss counters that rose from before to after, by
// process and series, sorted: a counter that fell restarted with its
// process, so all of its value after is new. A process read only once
// rises by nothing.
func CounterRises(before, after map[string]map[string]float64) []SystemCounter {
	var out []SystemCounter
	for _, proc := range sortedKeysOf(after) {
		b, ok := before[proc]
		if !ok {
			continue
		}
		for _, s := range sortedKeysOf(after[proc]) {
			v := after[proc][s]
			d := v - b[s]
			if v < b[s] {
				d = v
			}
			if d > 0 {
				out = append(out, SystemCounter{Process: proc, Series: s, Delta: d})
			}
		}
	}
	return out
}

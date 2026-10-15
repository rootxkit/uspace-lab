package load

import (
	"context"
	"testing"
)

// The loss counters are read per process, only the loss counters, and
// a process that does not answer is named; between two reads a rise is
// the difference, a fall a restart (all of the value after is new), no
// rise nothing, and a process read once rises by nothing.
func TestLossCountersAndTheirRises(t *testing.T) {
	cfg := helperProbes(t)
	before, errs := LossCounters(context.Background(), cfg)
	if len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	w := before["ussp-tsdb-writer"]
	if _, ok := w[`uspace_authority_gaps{component="tsdb_writer"}`]; !ok || len(w) != 1 {
		t.Fatalf("writer counters %v, want the gap counter only", w)
	}
	after, _ := LossCounters(context.Background(), cfg)
	rises := CounterRises(before, after)
	if len(rises) != 1 || rises[0].Process != "ussp-tsdb-writer" || rises[0].Delta != 5 {
		t.Fatalf("rises %+v, want the writer's gaps by 5", rises)
	}
	restarted := map[string]map[string]float64{"p": {"x_dropped_total": 3}}
	if r := CounterRises(map[string]map[string]float64{"p": {"x_dropped_total": 40}}, restarted); len(r) != 1 || r[0].Delta != 3 {
		t.Fatalf("a restarted counter: %+v", r)
	}
	if r := CounterRises(map[string]map[string]float64{}, restarted); len(r) != 0 {
		t.Fatalf("a process read once: %+v", r)
	}
	if r := CounterRises(restarted, restarted); len(r) != 0 {
		t.Fatalf("no rise: %+v", r)
	}
	if _, errs := LossCounters(context.Background(), nil); len(errs) != 1 {
		t.Fatal("no probes said nothing")
	}
	fail := helperProbes(t)
	fail.Fetch = []string{fail.Fetch[0], "fail"}
	if out, errs := LossCounters(context.Background(), fail); len(errs) != len(fail.Metrics) || len(out) != 0 {
		t.Fatalf("a fetch that fails: %v %v", out, errs)
	}
}

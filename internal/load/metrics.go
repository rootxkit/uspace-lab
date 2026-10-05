package load

import (
	"math"
	"sort"
	"time"
)

// Metric kinds.
const (
	KindLatency = "latency" // seconds, read as a percentile
	KindValue   = "value"   // one number
)

// MetricDef is a metric the harness knows. NotImplemented says why the
// harness cannot measure it at all yet; such a metric is always "not
// measured" with that reason, so the gap is visible in every report.
type MetricDef struct {
	Kind           string `json:"kind"`
	Unit           string `json:"unit"`
	Doc            string `json:"doc"`
	NotImplemented string `json:"not_implemented,omitempty"`
}

const (
	niF3411   = "F3411 timings need the authority's DP poller and a peer SP against the DSS; the load harness drives neither yet (WP-L8 left: systems mode)"
	niF3548   = "F3548 timings need two USSPs against the lab DSS; the load harness drives neither yet (WP-L8 left: systems mode)"
	niMetrics = "read from a system's /metrics by name; the reference target has no /metrics and systems mode is not built yet"
	niChaos   = "the kill / partition / outage scripts are WP-L9's (scripts/chaos/), not built yet"
	niStorage = "the reference target has no database; storage is measured against the systems' images"
	niZone    = "the load paths script no zone entry yet; zone alerts are proved by the scenario suite (sc-03)"
	niArt13   = "Art. 13(2) notices go to peers and the ANSP, neither of which the load harness drives yet"
	niVectors = "the vectors run in uspace-core's CI and per image in the conformance suite, not in a load run"
)

// Metrics is every metric a criteria check may name.
var Metrics = map[string]MetricDef{
	"operator_to_ussp_console_s": {Kind: KindLatency, Unit: "s",
		Doc: "a sample handed to its operator client -> the traffic/product/v1 on that flight's USSP traffic stream that first shows it (time_of_report matched)"},
	"receiver_to_authority_picture_s": {Kind: KindLatency, Unit: "s",
		Doc: "a sample handed to the receiver that hears it -> the track/telemetry/v1 on the timed authority console that first shows it (captured_at matched)"},
	"alert_raise_s": {Kind: KindLatency, Unit: "s",
		Doc: "the sample the alert names (its captured_at matched) handed to the operator client -> the alert/v1 raise on the flight's traffic stream"},
	"operator_rate_ratio":         {Kind: KindValue, Doc: "operator telemetry sent per second / the tier's aircraft x telemetry_hz"},
	"rid_rate_ratio":              {Kind: KindValue, Doc: "Remote ID locations handed to receivers per second / heard aircraft x telemetry_hz"},
	"expected_raises":             {Kind: KindValue, Doc: "alerts the scripted conflicts make due inside the run (the missed-alert count's set)"},
	"missed_raises":               {Kind: KindValue, Doc: "expected raises not observed in their window"},
	"missed_clears":               {Kind: KindValue, Doc: "expected clears not observed in their window"},
	"unexpected_alerts":           {Kind: KindValue, Doc: "proximity raises outside every expected window, on any watched aircraft (separated walkers included)"},
	"alert_untraceable":           {Kind: KindValue, Doc: "raises whose captured_at names no sample the generator sent"},
	"operator_ledgers_unbalanced": {Kind: KindValue, Doc: "operator clients whose sent != accepted + refused + dropped + duplicate after the drain, or with frames pending"},
	"receiver_ledgers_unbalanced": {Kind: KindValue, Doc: "receivers whose sent != accepted + duplicates + refused, or observed != dropped + sent + shed + pending, after the drain"},
	"picture_silent_loss":         {Kind: KindValue, Doc: "heard samples never shown on the timed console beyond every counted loss (refused observations, dropped_frames, generator and receiver sheds)"},
	"picture_untraceable_frames":  {Kind: KindValue, Doc: "track frames on the timed console that match no sample the generator sent"},
	"cpa_evaluation_period_max_s": {Kind: KindValue, Unit: "s", Doc: "the largest evaluation_period_s any proximity alert carried"},
	"memory_growth_ratio":         {Kind: KindValue, Doc: "median heap of the last third of the run / of the first third (after the first sample)"},
	"memory_monotonic":            {Kind: KindValue, Doc: "1 when the heap medians of the three thirds rise strictly and the last is over 10 % above the first (growth beyond GC noise), else 0"},
	"f3411_sp_flights_s":          {Kind: KindLatency, Unit: "s", Doc: "SP GET /uss/flights", NotImplemented: niF3411},
	"f3411_dp_display_s":          {Kind: KindLatency, Unit: "s", Doc: "authority DP display after the SP response", NotImplemented: niF3411},
	"f3411_details_s":             {Kind: KindLatency, Unit: "s", Doc: "flight details", NotImplemented: niF3411},
	"f3411_dp_cache_over_24h":     {Kind: KindValue, Doc: "DP cache entries older than 24 h", NotImplemented: niF3411},
	"f3548_intent_change_s":       {Kind: KindLatency, Unit: "s", Doc: "peer notification of an intent change", NotImplemented: niF3548},
	"f3548_conflict_notify_s":     {Kind: KindLatency, Unit: "s", Doc: "conflicting-intent notification to the other USS", NotImplemented: niF3548},
	"f3548_details_s":             {Kind: KindLatency, Unit: "s", Doc: "details request answered", NotImplemented: niF3548},
	"f3548_constraint_notify_s":   {Kind: KindLatency, Unit: "s", Doc: "constraint notification", NotImplemented: niF3548},
	"zone_alert_ticks":            {Kind: KindValue, Doc: "monitor ticks from zone entry to the zone alert", NotImplemented: niZone},
	"art13_notice_s":              {Kind: KindLatency, Unit: "s", Doc: "Art. 13(2) notice to peers and the ANSP, acknowledged", NotImplemented: niArt13},
	"cpa_pair_checks_per_worker":  {Kind: KindValue, Doc: "pair checks per CPA worker per second / its budget", NotImplemented: niMetrics},
	"writer_queue_max_s":          {Kind: KindValue, Unit: "s", Doc: "TimescaleDB writer queue depth", NotImplemented: niStorage},
	"compression_lag_chunks":      {Kind: KindValue, Doc: "uncompressed chunks older than the policy", NotImplemented: niStorage},
	"disk_growth_vs_model":        {Kind: KindValue, Doc: "disk growth / the 05 §4 model", NotImplemented: niStorage},
	"restart_recovery_s":          {Kind: KindValue, Unit: "s", Doc: "picture back after a component is killed", NotImplemented: niChaos},
	"restart_duplicate_alerts":    {Kind: KindValue, Doc: "alerts raised twice across a restart", NotImplemented: niChaos},
	"restart_lost_backlog":        {Kind: KindValue, Doc: "backlog samples lost across a restart", NotImplemented: niChaos},
	"partition_unreplayed":        {Kind: KindValue, Doc: "samples not replayed after NATS of one system is killed for 60 s", NotImplemented: niChaos},
	"partition_cross_effects":     {Kind: KindValue, Doc: "effects on the other systems during that partition", NotImplemented: niChaos},
	"outage_unflagged":            {Kind: KindValue, Doc: "degraded behaviours not flagged with age during a 5 min outage", NotImplemented: niChaos},
	"vectors_failed":              {Kind: KindValue, Doc: "knowledge/vectors cases failing in an image under test", NotImplemented: niVectors},
}

// Observation is what a run measured for one metric.
type Observation struct {
	Measured bool   `json:"measured"`
	Reason   string `json:"reason,omitempty"`
	N        uint64 `json:"n,omitempty"`
	// Latency figures (seconds): each percentile is the upper edge of
	// its 1 ms bucket, so it never reads better than the sample.
	P50  *float64 `json:"p50,omitempty"`
	P95  *float64 `json:"p95,omitempty"`
	P99  *float64 `json:"p99,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Mean *float64 `json:"mean,omitempty"`
	// Value for a value metric.
	Value *float64 `json:"value,omitempty"`
}

func notMeasured(reason string) Observation { return Observation{Reason: reason} }

func valueObs(v float64) Observation { return Observation{Measured: true, N: 1, Value: &v} }

// Histogram holds latencies in 1 ms buckets up to histMaxMS, and counts
// what lies beyond (bounded memory at any run length, E-10).
type Histogram struct {
	buckets  []uint32
	over     uint64
	n        uint64
	sumS     float64
	maxS     float64
	negative uint64
}

const histMaxMS = 60_000

// NewHistogram makes an empty histogram.
func NewHistogram() *Histogram { return &Histogram{buckets: make([]uint32, histMaxMS)} }

// Add records one latency. A negative one (the frame arrived before the
// sample was sent) cannot be a latency: it is counted apart.
func (h *Histogram) Add(d time.Duration) {
	if d < 0 {
		h.negative++
		return
	}
	s := d.Seconds()
	h.n++
	h.sumS += s
	h.maxS = math.Max(h.maxS, s)
	ms := int(d / time.Millisecond)
	if ms >= histMaxMS {
		h.over++
		return
	}
	h.buckets[ms]++
}

// N is the number of latencies recorded.
func (h *Histogram) N() uint64 { return h.n }

// Negative is the number of refused negative latencies.
func (h *Histogram) Negative() uint64 { return h.negative }

// Quantile is the upper edge of the bucket holding the q-th latency
// (nearest rank), never above the maximum seen; beyond the buckets, the
// maximum.
func (h *Histogram) Quantile(q float64) float64 {
	if h.n == 0 {
		return math.NaN()
	}
	rank := uint64(math.Ceil(q * float64(h.n)))
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i, c := range h.buckets {
		cum += uint64(c)
		if cum >= rank {
			return math.Min(float64(i+1)/1000, h.maxS)
		}
	}
	return h.maxS
}

// Observe turns the histogram into an observation; below minN samples
// it is not measured.
func (h *Histogram) Observe(minN int) Observation {
	if h.n == 0 {
		return notMeasured("no sample was observed")
	}
	if h.n < uint64(minN) {
		return Observation{Reason: "only " + itoa(int(h.n)) + " samples, the check needs " + itoa(minN), N: h.n}
	}
	p := func(q float64) *float64 { v := round3(h.Quantile(q)); return &v }
	mx, mean := round3(h.maxS), round3(h.sumS/float64(h.n))
	return Observation{Measured: true, N: h.n, P50: p(0.50), P95: p(0.95), P99: p(0.99), Max: &mx, Mean: &mean}
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// memoryVerdict judges heap samples: the ratio of the last third's median
// to the first third's, and whether the three medians rise strictly by
// more than GC noise (10 % from first to last).
func memoryVerdict(samples []uint64) (ratio float64, monotonic bool, ok bool) {
	if len(samples) < 6 {
		return 0, false, false
	}
	third := len(samples) / 3
	med := func(xs []uint64) float64 {
		c := append([]uint64(nil), xs...)
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
		return float64(c[len(c)/2])
	}
	a, b, c := med(samples[:third]), med(samples[third:2*third]), med(samples[2*third:])
	if a <= 0 {
		return 0, false, false
	}
	return c / a, a < b && b < c && c > 1.1*a, true
}

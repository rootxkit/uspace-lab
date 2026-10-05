package load

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simrx"
)

// Status of a figure in the load files: where it comes from. GCAA has
// not answered its policy questions (docs/PLAN.md §7.1), so a figure the
// spec set as a national default is "pending GCAA".
const (
	StatusPendingGCAA = "pending GCAA"
	StatusStandard    = "standard" // ASTM F3411 / F3548
	StatusSpec        = "spec"     // a design target of spec 05
	StatusLab         = "lab"      // the harness's own self-check
)

// Bounds on what a tier may ask for (E-10): far above 05 §1's 5000
// drones, far below what would make a typo a denial of service.
const (
	maxAircraft    = 20000
	maxClients     = 1000
	maxConsoles    = 200
	maxDurationS   = 6 * 3600
	maxTelemetryHz = 2 // the USSP's per-flight live bound (05 §5)
)

// Tier is load/tiers/<name>.yaml: the volumes of one 05 §1 column, the
// duration, and what this tier scales down and why.
type Tier struct {
	Tier  string `yaml:"tier" json:"tier"`
	Title string `yaml:"title" json:"title"`
	// Column is the 05 §1 column the volumes come from (100, 1000, 5000).
	Column    int     `yaml:"column" json:"column"`
	DurationS float64 `yaml:"duration_s" json:"duration_s"`
	// DrainS bounds the wait after the generator stops for every ledger
	// to balance and every frame to arrive.
	DrainS    float64 `yaml:"drain_s" json:"drain_s"`
	Operators struct {
		Aircraft int `yaml:"aircraft" json:"aircraft"`
		Clients  int `yaml:"clients" json:"clients"`
		// TelemetryHz is 05 §1's 1 Hz (the authority's Art. 8(3)
		// determination, pending GCAA).
		TelemetryHz     float64 `yaml:"telemetry_hz" json:"telemetry_hz"`
		TelemetryStatus string  `yaml:"telemetry_hz_status" json:"telemetry_hz_status"`
		// PollMS is how often each operator client looks for a new
		// sample: the bound on the client's own share of the latency.
		PollMS int `yaml:"poll_ms" json:"poll_ms"`
		// RampPerS is how many operator clients connect per second at
		// the start (a fleet dialling at once is a connection storm,
		// not the steady load the tier measures).
		RampPerS float64 `yaml:"ramp_per_s" json:"ramp_per_s"`
		// Intents: "all" files one intent per aircraft at the start,
		// "watched" only for the aircraft whose traffic is read.
		Intents string `yaml:"intents" json:"intents"`
	} `yaml:"operators" json:"operators"`
	Receivers struct {
		// HeardFraction is 05 §1's ~40 % of airborne UAS heard by a
		// receiver.
		HeardFraction       float64  `yaml:"heard_fraction" json:"heard_fraction"`
		AircraftPerReceiver int      `yaml:"aircraft_per_receiver" json:"aircraft_per_receiver"`
		Transports          []string `yaml:"transports" json:"transports"`
		// BatchMS is the receivers' batch period: the bound on their
		// share of the latency (05 §5: at most 1 s per batch).
		BatchMS int `yaml:"batch_ms" json:"batch_ms"`
	} `yaml:"receivers" json:"receivers"`
	// Consoles read the authority picture with the whole area
	// subscribed; the first one is timed.
	Consoles int `yaml:"consoles" json:"consoles"`
	Watch    struct {
		// Pairs is the number of scripted conflict pairs (every one is
		// watched: its expected alerts are the missed-alert count's set).
		Pairs int `yaml:"pairs" json:"pairs"`
		// Walkers is how many separated aircraft have their traffic read
		// too: the absence half (no alert where none is due).
		Walkers int `yaml:"walkers" json:"walkers"`
	} `yaml:"watch" json:"watch"`
	// MinSamples is the default number of samples a latency check needs
	// before its percentile counts as measured.
	MinSamples int `yaml:"min_samples" json:"min_samples"`
	// MemorySampleS is the period of the memory samples (the live heap).
	MemorySampleS float64 `yaml:"memory_sample_s" json:"memory_sample_s"`
	// MemoryWarmupS is how much of the start the memory row leaves out:
	// the time the target's windows take to fill.
	MemoryWarmupS float64 `yaml:"memory_warmup_s" json:"memory_warmup_s"`
	// Required are the check ids this tier must measure and pass.
	Required []string `yaml:"required" json:"required"`
	// Scale says what this tier changes from 05 §1 / 05 §7, with the
	// factor and why (a scaled run says so in its report).
	Scale []ScaleFactor `yaml:"scale" json:"scale"`
	// NotGenerated names what 05 §7 asks the load to include that this
	// harness does not generate, with the reason (shown in the report).
	NotGenerated []NotGenerated `yaml:"not_generated" json:"not_generated"`
}

// ScaleFactor is one scaled quantity.
type ScaleFactor struct {
	Quantity string  `yaml:"quantity" json:"quantity"`
	Spec     string  `yaml:"spec" json:"spec"`
	Here     string  `yaml:"here" json:"here"`
	Factor   float64 `yaml:"factor" json:"factor"`
	Why      string  `yaml:"why" json:"why"`
}

// NotGenerated is a load source 05 §7 names that the run does not drive.
type NotGenerated struct {
	Source string `yaml:"source" json:"source"`
	Why    string `yaml:"why" json:"why"`
}

// Paths is load/paths.yaml: where the synthetic aircraft fly, as offsets
// from the lab origin (sim/sitl.env; INV-03: no coordinate here).
type Paths struct {
	// Policy is the policy file the separation guarantees are checked
	// against and the reference target judges with.
	Policy string `yaml:"policy" json:"policy"`
	Seed   uint64 `yaml:"seed" json:"seed"`
	// Bounds is the box every path stays inside: the demo area.
	Bounds struct {
		SouthM float64 `yaml:"south_m" json:"south_m"`
		NorthM float64 `yaml:"north_m" json:"north_m"`
		WestM  float64 `yaml:"west_m" json:"west_m"`
		EastM  float64 `yaml:"east_m" json:"east_m"`
	} `yaml:"bounds_m" json:"bounds_m"`
	Walkers WalkerPaths `yaml:"walkers" json:"walkers"`
	Pairs   PairPaths   `yaml:"pairs" json:"pairs"`
}

// WalkerPaths are the random walks: one walker per grid cell, inside a
// disc of RadiusM around the cell centre, at one of the altitude layers
// in a LayerTile x LayerTile pattern.
type WalkerPaths struct {
	Origin      scenario.Offset `yaml:"origin" json:"origin"`
	SpacingM    float64         `yaml:"spacing_m" json:"spacing_m"`
	RadiusM     float64         `yaml:"radius_m" json:"radius_m"`
	SpeedMS     float64         `yaml:"speed_ms" json:"speed_ms"`
	TurnDegPerS float64         `yaml:"turn_deg_per_s" json:"turn_deg_per_s"`
	// LayersRelM are heights above the origin's AMSL altitude.
	LayersRelM []float64 `yaml:"layers_rel_m" json:"layers_rel_m"`
	LayerTile  int       `yaml:"layer_tile" json:"layer_tile"`
	// MarginM is added to every policy minimum the layout must keep.
	MarginM float64 `yaml:"margin_m" json:"margin_m"`
}

// PairPaths are the scripted conflicts: two aircraft on one east-west
// line, mirrored, flying a triangle wave of AmplitudeM so they cross head
// on at the line's centre every 2 * AmplitudeM / SpeedMS seconds, the
// first time at FirstCrossingS.
type PairPaths struct {
	Origin         scenario.Offset `yaml:"origin" json:"origin"`
	SpacingM       float64         `yaml:"spacing_m" json:"spacing_m"`
	AltRelM        float64         `yaml:"alt_rel_m" json:"alt_rel_m"`
	SpeedMS        float64         `yaml:"speed_ms" json:"speed_ms"`
	AmplitudeM     float64         `yaml:"amplitude_m" json:"amplitude_m"`
	FirstCrossingS float64         `yaml:"first_crossing_s" json:"first_crossing_s"`
	// RaiseMarginS widens the raise window around crossing - t_cpa_max_s.
	RaiseMarginS float64 `yaml:"raise_margin_s" json:"raise_margin_s"`
	// ClearWithinS is how long after a crossing its clear is due.
	ClearWithinS float64 `yaml:"clear_within_s" json:"clear_within_s"`
}

// Criteria is load/criteria.yaml: 05 §7's table as checks.
type Criteria struct {
	Source string `yaml:"source" json:"source"`
	Rows   []Row  `yaml:"rows" json:"rows"`
}

// Row is one 05 §7 property.
type Row struct {
	ID        string  `yaml:"id" json:"id"`
	Property  string  `yaml:"property" json:"property"`
	Criterion string  `yaml:"criterion" json:"criterion"`
	Checks    []Check `yaml:"checks" json:"checks"`
}

// Check is one figure of a row.
type Check struct {
	ID     string  `yaml:"id" json:"id"`
	Metric string  `yaml:"metric" json:"metric"`
	Stat   string  `yaml:"stat" json:"stat"`
	Op     string  `yaml:"op" json:"op"`
	Value  float64 `yaml:"value" json:"value"`
	Unit   string  `yaml:"unit" json:"unit"`
	Status string  `yaml:"status" json:"status"`
	Basis  string  `yaml:"basis" json:"basis"`
	// MinSamples overrides the tier's for this check; MinDurationS is
	// the run length below which the check is not measured (memory
	// growth means nothing over two minutes).
	MinSamples   int     `yaml:"min_samples" json:"min_samples,omitempty"`
	MinDurationS float64 `yaml:"min_duration_s" json:"min_duration_s,omitempty"`
}

// Stats a check can read.
const (
	StatP50   = "p50"
	StatP95   = "p95"
	StatP99   = "p99"
	StatMax   = "max"
	StatValue = "value"
)

var (
	ops        = []string{"<", "<=", "==", ">="}
	statuses   = []string{StatusPendingGCAA, StatusStandard, StatusSpec, StatusLab}
	transports = []string{simrx.TransportPack, simrx.TransportSingle}
)

// Files is what a run loaded, with each file's digest (E-05).
type Files struct {
	Tier     *Tier     `json:"-"`
	Paths    *Paths    `json:"-"`
	Criteria *Criteria `json:"-"`
	Policy   *scenario.Policy
	Digests  map[string]string `json:"digests"`
	// PolicyPath is the policy file as resolved.
	PolicyPath string `json:"policy_path"`
}

func readStrict(path string, v any) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the load files
	if err != nil {
		return "", err
	}
	if err := yaml.UnmarshalWithOptions(b, v, yaml.DisallowUnknownField()); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// LoadFiles reads and checks the tier, the paths, the criteria and the
// policy the paths name. Everything is validated before anything runs
// (nothing is started, nothing is written, for a file that fails).
func LoadFiles(tierPath, pathsPath, criteriaPath string) (*Files, error) {
	f := &Files{Tier: &Tier{}, Paths: &Paths{}, Criteria: &Criteria{}, Digests: map[string]string{}}
	for _, x := range []struct {
		name, path string
		v          any
	}{{"tier", tierPath, f.Tier}, {"paths", pathsPath, f.Paths}, {"criteria", criteriaPath, f.Criteria}} {
		d, err := readStrict(x.path, x.v)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", x.name, err)
		}
		f.Digests[x.name] = d
	}
	if f.Paths.Policy == "" {
		return nil, fmt.Errorf("load paths: policy is required")
	}
	f.PolicyPath = f.Paths.Policy
	if !filepath.IsAbs(f.PolicyPath) {
		f.PolicyPath = filepath.Join(filepath.Dir(pathsPath), f.PolicyPath)
	}
	pol, err := scenario.LoadPolicy(f.PolicyPath)
	if err != nil {
		return nil, fmt.Errorf("load paths: %w", err)
	}
	f.Policy = pol
	b, err := os.ReadFile(f.PolicyPath)
	if err != nil {
		return nil, fmt.Errorf("load paths: %w", err)
	}
	sum := sha256.Sum256(b)
	f.Digests["policy"] = "sha256:" + hex.EncodeToString(sum[:])
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}

// Validate checks every file and the separation the paths promise under
// the policy: a walker can never come within the CPA minima of another,
// so the only alerts due are the pairs' (the expected set is exact).
func (f *Files) Validate() error {
	if err := f.Tier.validate(); err != nil {
		return fmt.Errorf("load tier: %w", err)
	}
	if err := f.Criteria.validate(); err != nil {
		return fmt.Errorf("load criteria: %w", err)
	}
	known := map[string]bool{}
	for _, r := range f.Criteria.Rows {
		for i := range r.Checks {
			known[r.Checks[i].ID] = true
		}
	}
	for _, id := range f.Tier.Required {
		if !known[id] {
			return fmt.Errorf("load tier: required check %q is not in the criteria", id)
		}
	}
	if err := f.Paths.validate(f.Policy, f.Tier); err != nil {
		return fmt.Errorf("load paths: %w", err)
	}
	return nil
}

func finitePos(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fmt.Errorf("%s must be a positive number, got %v", name, v)
	}
	return nil
}

func (t *Tier) validate() error {
	switch {
	case t.Tier == "":
		return fmt.Errorf("tier is required")
	case t.DurationS <= 0 || t.DurationS > maxDurationS:
		return fmt.Errorf("duration_s %v is not in (0, %d]", t.DurationS, maxDurationS)
	case t.DrainS <= 0 || t.DrainS > 600:
		return fmt.Errorf("drain_s %v is not in (0, 600]", t.DrainS)
	case t.Operators.Aircraft < 1 || t.Operators.Aircraft > maxAircraft:
		return fmt.Errorf("operators.aircraft %d is not in [1, %d]", t.Operators.Aircraft, maxAircraft)
	case t.Operators.Clients < 1 || t.Operators.Clients > maxClients || t.Operators.Clients > t.Operators.Aircraft:
		return fmt.Errorf("operators.clients %d is not in [1, min(%d, aircraft)]", t.Operators.Clients, maxClients)
	case t.Operators.TelemetryHz <= 0 || t.Operators.TelemetryHz > maxTelemetryHz:
		return fmt.Errorf("operators.telemetry_hz %v is not in (0, %d]", t.Operators.TelemetryHz, maxTelemetryHz)
	case !slices.Contains(statuses, t.Operators.TelemetryStatus):
		return fmt.Errorf("operators.telemetry_hz_status %q is not one of %v", t.Operators.TelemetryStatus, statuses)
	case t.Operators.PollMS < 1 || t.Operators.PollMS > 1000:
		return fmt.Errorf("operators.poll_ms %d is not in [1, 1000]", t.Operators.PollMS)
	case t.Operators.RampPerS < 10 || t.Operators.RampPerS > 10000:
		return fmt.Errorf("operators.ramp_per_s %v is not in [10, 10000]", t.Operators.RampPerS)
	case t.Operators.Intents != "all" && t.Operators.Intents != "watched":
		return fmt.Errorf("operators.intents is all or watched")
	case t.Receivers.HeardFraction < 0 || t.Receivers.HeardFraction > 1:
		return fmt.Errorf("receivers.heard_fraction %v is not in [0, 1]", t.Receivers.HeardFraction)
	case t.Receivers.HeardFraction > 0 && (t.Receivers.AircraftPerReceiver < 1 || t.Receivers.AircraftPerReceiver > simrx.MaxObservations/2):
		// Location and Basic ID of each in one batch: at most 64.
		return fmt.Errorf("receivers.aircraft_per_receiver %d is not in [1, %d]", t.Receivers.AircraftPerReceiver, simrx.MaxObservations/2)
	case t.Receivers.HeardFraction > 0 && len(t.Receivers.Transports) == 0:
		return fmt.Errorf("receivers.transports is required")
	case t.Receivers.HeardFraction > 0 && (t.Receivers.BatchMS < 10 || t.Receivers.BatchMS > 1000):
		return fmt.Errorf("receivers.batch_ms %d is not in [10, 1000]", t.Receivers.BatchMS)
	case t.Consoles < 0 || t.Consoles > maxConsoles:
		return fmt.Errorf("consoles %d is not in [0, %d]", t.Consoles, maxConsoles)
	case t.Watch.Pairs < 0 || 2*t.Watch.Pairs > t.Operators.Aircraft:
		return fmt.Errorf("watch.pairs %d needs %d aircraft", t.Watch.Pairs, 2*t.Watch.Pairs)
	case t.Watch.Walkers < 0 || t.Watch.Walkers > t.Operators.Aircraft-2*t.Watch.Pairs:
		return fmt.Errorf("watch.walkers %d is more than the walkers there are", t.Watch.Walkers)
	case t.MinSamples < 1:
		return fmt.Errorf("min_samples must be at least 1")
	case t.MemorySampleS <= 0 || t.MemorySampleS > 600:
		return fmt.Errorf("memory_sample_s %v is not in (0, 600]", t.MemorySampleS)
	case t.MemoryWarmupS < 0 || t.MemoryWarmupS >= t.DurationS:
		return fmt.Errorf("memory_warmup_s %v is not in [0, duration_s)", t.MemoryWarmupS)
	}
	for _, tr := range t.Receivers.Transports {
		if !slices.Contains(transports, tr) {
			return fmt.Errorf("receivers.transports: %q is not one of %v", tr, transports)
		}
	}
	for i, s := range t.Scale {
		if s.Quantity == "" || s.Why == "" || math.IsNaN(s.Factor) || s.Factor < 0 {
			return fmt.Errorf("scale[%d]: quantity, a factor (0 for none) and why are required", i)
		}
	}
	for i, n := range t.NotGenerated {
		if n.Source == "" || n.Why == "" {
			return fmt.Errorf("not_generated[%d]: source and why are required", i)
		}
	}
	return nil
}

func (c *Criteria) validate() error {
	if c.Source == "" || len(c.Rows) == 0 {
		return fmt.Errorf("source and rows are required")
	}
	ids := map[string]bool{}
	for _, r := range c.Rows {
		if r.ID == "" || r.Property == "" || r.Criterion == "" || len(r.Checks) == 0 {
			return fmt.Errorf("row %q: id, property, criterion and checks are required", r.ID)
		}
		if ids["row:"+r.ID] {
			return fmt.Errorf("row %q twice", r.ID)
		}
		ids["row:"+r.ID] = true
		for i := range r.Checks {
			k := &r.Checks[i]
			def, ok := Metrics[k.Metric]
			switch {
			case k.ID == "" || ids[k.ID]:
				return fmt.Errorf("row %s: check id %q is empty or repeated", r.ID, k.ID)
			case !ok:
				// Fail closed: a check on a metric the harness does not
				// know would be "not measured" forever and look planned.
				return fmt.Errorf("check %s: unknown metric %q", k.ID, k.Metric)
			case def.Kind == KindLatency && k.Stat != StatP50 && k.Stat != StatP95 && k.Stat != StatP99 && k.Stat != StatMax:
				return fmt.Errorf("check %s: a latency is read as p50, p95, p99 or max, not %q", k.ID, k.Stat)
			case def.Kind == KindValue && k.Stat != StatValue:
				return fmt.Errorf("check %s: a value is read as value, not %q", k.ID, k.Stat)
			case !slices.Contains(ops, k.Op):
				return fmt.Errorf("check %s: op %q is not one of %v", k.ID, k.Op, ops)
			case math.IsNaN(k.Value) || math.IsInf(k.Value, 0):
				return fmt.Errorf("check %s: value is not a number", k.ID)
			case !slices.Contains(statuses, k.Status):
				return fmt.Errorf("check %s: status %q is not one of %v", k.ID, k.Status, statuses)
			case k.Basis == "":
				return fmt.Errorf("check %s: basis is required (where the figure comes from)", k.ID)
			case k.MinSamples < 0 || k.MinDurationS < 0:
				return fmt.Errorf("check %s: min_samples and min_duration_s are not negative", k.ID)
			}
			ids[k.ID] = true
		}
	}
	return nil
}

func (p *Paths) validate(pol *scenario.Policy, t *Tier) error {
	w, pr := &p.Walkers, &p.Pairs
	for name, v := range map[string]float64{
		"walkers.spacing_m": w.SpacingM, "walkers.radius_m": w.RadiusM, "walkers.speed_ms": w.SpeedMS,
		"walkers.turn_deg_per_s": w.TurnDegPerS, "walkers.margin_m": w.MarginM,
		"pairs.spacing_m": pr.SpacingM, "pairs.speed_ms": pr.SpeedMS, "pairs.amplitude_m": pr.AmplitudeM,
		"pairs.raise_margin_s": pr.RaiseMarginS, "pairs.clear_within_s": pr.ClearWithinS,
	} {
		if err := finitePos(name, v); err != nil {
			return err
		}
	}
	if p.Bounds.SouthM >= p.Bounds.NorthM || p.Bounds.WestM >= p.Bounds.EastM {
		return fmt.Errorf("bounds_m is empty")
	}
	if w.LayerTile < 1 || len(w.LayersRelM) != w.LayerTile*w.LayerTile {
		return fmt.Errorf("walkers: layer_tile %d needs %d layers, got %d", w.LayerTile, w.LayerTile*w.LayerTile, len(w.LayersRelM))
	}
	tCPA, dH, dV := pol.CPA.TCPAMaxS, pol.CPA.DHorizontalMinM, pol.CPA.DVerticalMinM
	// Two walkers on one layer are at least layer_tile cells apart; in
	// t_cpa_max_s each moves at most speed * t, so the predicted closest
	// approach is at least that distance minus both discs and both
	// moves. It must stay above the horizontal minimum.
	sameLayer := float64(w.LayerTile)*w.SpacingM - 2*w.RadiusM - 2*w.SpeedMS*tCPA
	if sameLayer < dH+w.MarginM {
		return fmt.Errorf("walkers: two walkers on one layer can come within %.0f m in t_cpa_max_s (%.0f s), the policy's horizontal minimum is %.0f m (+%.0f m margin): widen spacing_m or layer_tile",
			sameLayer, tCPA, dH, w.MarginM)
	}
	if w.RadiusM*2 >= w.SpacingM {
		return fmt.Errorf("walkers: radius_m %v overlaps the next cell (spacing_m %v)", w.RadiusM, w.SpacingM)
	}
	layers := append(slices.Clone(w.LayersRelM), pr.AltRelM)
	for i := range layers {
		for j := i + 1; j < len(layers); j++ {
			if math.Abs(layers[i]-layers[j]) < dV+w.MarginM {
				return fmt.Errorf("walkers/pairs: altitudes %v and %v m are closer than the vertical minimum %.0f m (+%.0f m margin)", layers[i], layers[j], dV, w.MarginM)
			}
		}
	}
	// Pairs: the next pair is spacing_m along the row; neither member
	// leaves its line by more than the amplitude.
	if pairGap := pr.SpacingM - 2*pr.AmplitudeM - 2*pr.SpeedMS*tCPA; t.Watch.Pairs > 1 && pairGap < dH+w.MarginM {
		return fmt.Errorf("pairs: two pairs can come within %.0f m: widen pairs.spacing_m", pairGap)
	}
	if pr.FirstCrossingS < 0 || pr.FirstCrossingS*pr.SpeedMS > pr.AmplitudeM {
		return fmt.Errorf("pairs: first_crossing_s %v puts the members beyond the amplitude (at most %.0f s)", pr.FirstCrossingS, pr.AmplitudeM/pr.SpeedMS)
	}
	// One alert per crossing: the next raise comes after this clear.
	if gap := 2*pr.AmplitudeM/pr.SpeedMS - tCPA - pr.RaiseMarginS; gap <= pr.ClearWithinS {
		return fmt.Errorf("pairs: crossings %.0f s apart leave %.0f s between a clear and the next raise, under clear_within_s %v: raise amplitude_m",
			2*pr.AmplitudeM/pr.SpeedMS, gap, pr.ClearWithinS)
	}
	if pr.ClearWithinS <= pol.Monitor.ClearAfterS {
		return fmt.Errorf("pairs: clear_within_s %v is not above the policy's clear_after_s %v", pr.ClearWithinS, pol.Monitor.ClearAfterS)
	}
	return p.checkBounds(t)
}

// checkBounds checks that every path stays inside bounds_m.
func (p *Paths) checkBounds(t *Tier) error {
	walkers := t.Operators.Aircraft - 2*t.Watch.Pairs
	cols, rows := gridOf(walkers)
	w := &p.Walkers
	in := func(n, e float64) bool {
		return n >= p.Bounds.SouthM && n <= p.Bounds.NorthM && e >= p.Bounds.WestM && e <= p.Bounds.EastM
	}
	if walkers > 0 {
		n0, e0 := w.Origin.NorthM-w.RadiusM, w.Origin.EastM-w.RadiusM
		n1 := w.Origin.NorthM + float64(rows-1)*w.SpacingM + w.RadiusM
		e1 := w.Origin.EastM + float64(cols-1)*w.SpacingM + w.RadiusM
		if !in(n0, e0) || !in(n1, e1) {
			return fmt.Errorf("walkers: the %d x %d grid leaves bounds_m", rows, cols)
		}
	}
	if t.Watch.Pairs > 0 {
		pr := &p.Pairs
		e1 := pr.Origin.EastM + float64(t.Watch.Pairs-1)*pr.SpacingM + pr.AmplitudeM
		if !in(pr.Origin.NorthM, pr.Origin.EastM-pr.AmplitudeM) || !in(pr.Origin.NorthM, e1) {
			return fmt.Errorf("pairs: the row of %d pairs leaves bounds_m", t.Watch.Pairs)
		}
	}
	return nil
}

// gridOf is the near-square grid n walkers fill.
func gridOf(n int) (cols, rows int) {
	if n <= 0 {
		return 0, 0
	}
	cols = int(math.Ceil(math.Sqrt(float64(n))))
	rows = (n + cols - 1) / cols
	return cols, rows
}

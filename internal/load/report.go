package load

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/conformance/report"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// ReportFormat names the report's shape.
const ReportFormat = "load-report/v1"

// Verdicts and check results.
const (
	VerdictPass = "pass"
	VerdictFail = "fail"

	ResultPass        = "pass"
	ResultFail        = "fail"
	ResultNotMeasured = "not measured"
)

// Report is results/load/<run>/report.json (docs/PLAN.md D8, E-05): the
// tier and what it scaled, the code and the host, every 05 §7 row with
// what was observed, the loss accounting, and the verdict.
type Report struct {
	Format    string    `json:"format"`
	Run       string    `json:"run"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// GeneratorS is how long the generator ran (the rates' denominator).
	GeneratorS float64                `json:"generator_s"`
	Tier       *Tier                  `json:"tier"`
	Scaled     bool                   `json:"scaled"`
	Target     TargetInfo             `json:"target"`
	Host       Host                   `json:"host"`
	Commits    report.Commits         `json:"commits"`
	Files      map[string]string      `json:"files"`
	Policy     *scenario.Policy       `json:"policy"`
	Lab        LabInfo                `json:"lab"`
	Volumes    Volumes                `json:"volumes"`
	Rows       []RowResult            `json:"rows"`
	Metrics    map[string]Observation `json:"metrics"`
	Loss       Loss                   `json:"loss"`
	Alerts     AlertSummary           `json:"alerts"`
	Streams    []StreamStats          `json:"streams"`
	// Memory is the live heap every memory_sample_s, generator and
	// reference target together (one process).
	Memory     []uint64 `json:"memory_live_heap_bytes,omitempty"`
	Errors     []string `json:"errors,omitempty"`
	Measured   int      `json:"checks_measured"`
	Unmeasured int      `json:"checks_not_measured"`
	// Complete is true only when every check of every row was measured.
	Complete bool     `json:"complete"`
	Verdict  string   `json:"verdict"`
	Failures []string `json:"failures,omitempty"`
}

// TargetInfo says what was under load.
type TargetInfo struct {
	Mode     string            `json:"mode"`
	Name     string            `json:"name"`
	Evidence string            `json:"evidence"`
	Images   map[string]string `json:"images,omitempty"`
}

// Host is the machine the run used (L-Q1: numbers are comparable only
// on a named host size).
type Host struct {
	Name          string  `json:"name"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	CPUs          int     `json:"cpus"`
	MemTotalBytes *uint64 `json:"mem_total_bytes"`
	Go            string  `json:"go"`
	Note          string  `json:"note"`
}

// LabInfo is the origin the paths were laid from.
type LabInfo struct {
	File       string  `json:"file"`
	OriginLat  float64 `json:"origin_lat_deg"`
	OriginLon  float64 `json:"origin_lon_deg"`
	OriginAMSL float64 `json:"origin_alt_amsl_m"`
	Geoid      string  `json:"geoid"`
}

// Volumes are what the run offered, against what 05 §1 asks at the tier.
type Volumes struct {
	Aircraft           int     `json:"aircraft"`
	Clients            int     `json:"operator_clients"`
	Heard              int     `json:"heard_aircraft"`
	Receivers          int     `json:"receivers"`
	Consoles           int     `json:"consoles"`
	Watched            int     `json:"watched_aircraft"`
	IntentsFiled       int     `json:"intents_filed"`
	IntentErrors       int     `json:"intent_errors"`
	OperatorMsgsPerS   float64 `json:"operator_msgs_per_s"`
	OperatorTargetPerS float64 `json:"operator_target_per_s"`
	RIDLocationsPerS   float64 `json:"rid_locations_per_s"`
	RIDTargetPerS      float64 `json:"rid_target_per_s"`
	ConsoleFramesPerS  float64 `json:"console_frames_per_s"`
}

// Loss is the accounting behind "no silent loss" (05 §7, 05 §5): every
// counter that says something was not delivered, and the identities.
type Loss struct {
	Operator  OperatorLoss `json:"operator"`
	Receivers ReceiverLoss `json:"receivers"`
	// GeneratorShed are samples the generator's own bounded queues could
	// not hand to a client (counted, so accounted).
	GeneratorShedOperator uint64                       `json:"generator_shed_operator"`
	GeneratorShedRID      uint64                       `json:"generator_shed_rid"`
	Picture               LedgerCounts                 `json:"picture"`
	Traffic               LedgerCounts                 `json:"traffic"`
	PictureDroppedFrames  uint64                       `json:"picture_dropped_frames"`
	PictureAccounted      uint64                       `json:"picture_accounted"`
	PictureSilent         uint64                       `json:"picture_silent"`
	TargetCounters        map[string]map[string]uint64 `json:"target_counters,omitempty"`
}

// OperatorLoss sums the operator clients' ledgers (simop).
type OperatorLoss struct {
	Sent       uint64            `json:"sent"`
	Accepted   uint64            `json:"accepted"`
	Refused    uint64            `json:"refused"`
	Dropped    uint64            `json:"dropped"`
	Duplicate  uint64            `json:"duplicate"`
	QueueShed  uint64            `json:"queue_shed"`
	Pending    uint64            `json:"pending"`
	Unbalanced uint64            `json:"unbalanced"`
	Outcomes   map[string]uint64 `json:"outcomes"`
	// Unbalance names the first unbalanced clients (bounded).
	Unbalance []string `json:"unbalance,omitempty"`
}

// ReceiverLoss sums the receivers' ledgers (simrx).
type ReceiverLoss struct {
	Observed      uint64   `json:"observed"`
	Sent          uint64   `json:"sent"`
	Accepted      uint64   `json:"accepted"`
	Duplicates    uint64   `json:"duplicates"`
	Refused       uint64   `json:"refused"`
	BacklogShed   uint64   `json:"backlog_shed"`
	DroppedByKnob uint64   `json:"dropped_by_knob"`
	Pending       uint64   `json:"pending"`
	Unbalanced    uint64   `json:"unbalanced"`
	Unbalance     []string `json:"unbalance,omitempty"`
}

// AlertSummary is the expected set against what was observed.
type AlertSummary struct {
	Expected    int        `json:"expected"`
	Raised      int        `json:"raised"`
	Cleared     int        `json:"cleared"`
	Missed      []Expected `json:"missed,omitempty"`
	MissedClear []Expected `json:"missed_clear,omitempty"`
	Unexpected  []string   `json:"unexpected,omitempty"`
	Untraceable int        `json:"untraceable"`
	// Raises are the matched raises with their timing (bounded).
	Raises []RaiseRecord `json:"raises,omitempty"`
}

// RaiseRecord is one matched raise: when it arrived and the sample it
// names, seconds after the generator's start.
type RaiseRecord struct {
	Aircraft  string   `json:"aircraft"`
	AlertID   string   `json:"alert_id"`
	ObservedS float64  `json:"observed_s"`
	CapturedS *float64 `json:"captured_s"`
	LatencyS  *float64 `json:"latency_s"`
}

// RowResult is one 05 §7 row with its checks.
type RowResult struct {
	ID        string        `json:"id"`
	Property  string        `json:"property"`
	Criterion string        `json:"criterion"`
	Result    string        `json:"result"`
	Checks    []CheckResult `json:"checks"`
}

// CheckResult is one check: the figure, what was observed, the result.
type CheckResult struct {
	Check
	Required bool        `json:"required"`
	Observed Observation `json:"observed"`
	Result   string      `json:"result"`
	Reason   string      `json:"reason,omitempty"`
}

// maxListed bounds the lists a report carries (E-10); the counts stay
// exact.
const maxListed = 50

// Evaluate fills the rows from the observations and sets the verdict.
// It fails closed: a required check that was not measured fails the
// run, and a run that measured no check at all fails whatever else it
// says.
func (r *Report) Evaluate(c *Criteria, durationS float64) {
	required := map[string]bool{}
	for _, id := range r.Tier.Required {
		required[id] = true
	}
	r.Rows, r.Measured, r.Unmeasured, r.Failures = nil, 0, 0, nil
	for _, row := range c.Rows {
		rr := RowResult{ID: row.ID, Property: row.Property, Criterion: row.Criterion}
		nPass, nFail, nNot := 0, 0, 0
		for i := range row.Checks {
			k := &row.Checks[i]
			cr := CheckResult{Check: *k, Required: required[k.ID]}
			obs, ok := r.Metrics[k.Metric]
			if !ok {
				obs = notMeasured("the run produced no observation of " + k.Metric)
			}
			if obs.Measured && k.MinDurationS > 0 && durationS < k.MinDurationS {
				obs = Observation{Reason: fmt.Sprintf("the run lasted %.0f s, the check needs %.0f s", durationS, k.MinDurationS), N: obs.N}
			}
			if obs.Measured && k.MinSamples > 0 && obs.N < uint64(k.MinSamples) {
				obs = Observation{Reason: fmt.Sprintf("only %d samples, the check needs %d", obs.N, k.MinSamples), N: obs.N}
			}
			cr.Observed = obs
			switch {
			case !obs.Measured:
				cr.Result, cr.Reason = ResultNotMeasured, obs.Reason
				nNot++
				r.Unmeasured++
				if cr.Required {
					r.Failures = append(r.Failures, fmt.Sprintf("%s: required and not measured: %s", k.ID, obs.Reason))
				}
			default:
				r.Measured++
				v, okv := statOf(obs, k.Stat)
				switch {
				case !okv:
					cr.Result, cr.Reason = ResultFail, "the observation has no "+k.Stat
				case compare(v, k.Op, k.Value):
					cr.Result = ResultPass
				default:
					cr.Result = ResultFail
					cr.Reason = fmt.Sprintf("%s %s is %s, the criterion is %s %s", k.Metric, k.Stat, fmtNum(v), k.Op, fmtNum(k.Value))
				}
				if cr.Result == ResultPass {
					nPass++
				} else {
					nFail++
					r.Failures = append(r.Failures, k.ID+": "+cr.Reason)
				}
			}
			rr.Checks = append(rr.Checks, cr)
		}
		switch {
		case nFail > 0:
			rr.Result = ResultFail
		case nNot == 0:
			rr.Result = ResultPass
		case nPass == 0:
			rr.Result = ResultNotMeasured
		default:
			rr.Result = "partial"
		}
		r.Rows = append(r.Rows, rr)
	}
	r.Complete = r.Unmeasured == 0
	if r.Measured == 0 {
		r.Failures = append(r.Failures, "the run measured nothing: no check had an observation")
	}
	for _, e := range r.Errors {
		r.Failures = append(r.Failures, "run error: "+e)
	}
	r.Verdict = VerdictPass
	if len(r.Failures) > 0 {
		r.Verdict = VerdictFail
	}
}

func statOf(o Observation, stat string) (float64, bool) {
	var p *float64
	switch stat {
	case StatP50:
		p = o.P50
	case StatP95:
		p = o.P95
	case StatP99:
		p = o.P99
	case StatMax:
		p = o.Max
	case StatValue:
		p = o.Value
	}
	if p == nil || math.IsNaN(*p) {
		return 0, false
	}
	return *p, true
}

func compare(v float64, op string, want float64) bool {
	switch op {
	case "<":
		return v < want
	case "<=":
		return v <= want
	case "==":
		return v == want
	case ">=":
		return v >= want
	}
	return false // an unknown op never passes (validated at load)
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func itoa(i int) string { return strconv.Itoa(i) }

// Write writes report.json and report.md into dir, which must not exist
// yet: a run never overwrites an earlier run's record. Each file is
// written to a temporary name and renamed, so a reader never sees half
// a report.
func (r *Report) Write(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("load: %s exists; a run never overwrites a record", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, "report.json"), append(b, '\n')); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "report.md"), []byte(r.Markdown()))
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	return nil
}

// ReadReport reads a report.json.
func ReadReport(path string) (*Report, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the report
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Format != ReportFormat {
		return nil, fmt.Errorf("%s: format %q, want %s", path, r.Format, ReportFormat)
	}
	return &r, nil
}

// Markdown renders the report: the verdict, what was scaled, and the
// 05 §7 table with the observed figures. "not measured" is written out
// with its reason, never left blank.
func (r *Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# Load run %s: tier %s\n\n", r.Run, r.Tier.Tier)
	w("**Verdict: %s.** %d checks measured, %d not measured", strings.ToUpper(r.Verdict), r.Measured, r.Unmeasured)
	if r.Complete {
		w(" (every 05 §7 row observed).\n\n")
	} else {
		w(" (05 §7 is not fully observed by this run).\n\n")
	}
	w("- Target: %s (%s). %s\n", r.Target.Name, r.Target.Mode, r.Target.Evidence)
	for _, k := range sortedKeys(r.Target.Images) {
		w("- Image %s: `%s`\n", k, r.Target.Images[k])
	}
	mem := "unknown"
	if r.Host.MemTotalBytes != nil {
		mem = fmt.Sprintf("%.1f GiB", float64(*r.Host.MemTotalBytes)/(1<<30))
	}
	w("- Host: %s: %s/%s, %d CPUs, memory %s, %s. %s\n", r.Host.Name, r.Host.OS, r.Host.Arch, r.Host.CPUs, mem, r.Host.Go, r.Host.Note)
	dirty := ""
	if r.Commits.LabDirty {
		dirty = " (dirty tree)"
	}
	w("- Commits: uspace-lab `%s`%s, uspace-core `%s`.\n", r.Commits.Lab, dirty, r.Commits.Core)
	w("- Policy: version %d (`%s`); figures marked pending GCAA are the spec's defaults until GCAA answers.\n", r.Policy.PolicyVersion, r.Files["policy"])
	w("- Ran %s to %s; the generator ran %.0f s of the tier's %.0f s.\n\n", r.StartedAt.Format(time.RFC3339), r.EndedAt.Format(time.RFC3339), r.GeneratorS, r.Tier.DurationS)
	if r.Scaled || len(r.Tier.Scale) > 0 {
		w("## Scaled from 05 §1 / 05 §7\n\n| Quantity | Spec | This run | Factor | Why |\n|---|---|---|---|---|\n")
		for _, s := range r.Tier.Scale {
			w("| %s | %s | %s | %s | %s |\n", s.Quantity, s.Spec, s.Here, fmtNum(s.Factor), s.Why)
		}
		w("\n")
	}
	if len(r.Tier.NotGenerated) > 0 {
		w("## Not generated\n\n")
		for _, n := range r.Tier.NotGenerated {
			w("- %s: %s\n", n.Source, n.Why)
		}
		w("\n")
	}
	v := r.Volumes
	w("## Offered load\n\n| Quantity | Observed | Tier target |\n|---|---|---|\n")
	w("| Operator telemetry (msg/s) | %.1f | %.1f |\n", v.OperatorMsgsPerS, v.OperatorTargetPerS)
	w("| Remote ID locations (msg/s) | %.1f | %.1f |\n", v.RIDLocationsPerS, v.RIDTargetPerS)
	w("| Console frames in (frames/s, all consoles) | %.1f | |\n", v.ConsoleFramesPerS)
	w("| Aircraft / operator clients / heard / receivers / consoles / watched | %d / %d / %d / %d / %d / %d | |\n", v.Aircraft, v.Clients, v.Heard, v.Receivers, v.Consoles, v.Watched)
	w("| Intents filed (errors) | %d (%d) | |\n\n", v.IntentsFiled, v.IntentErrors)
	w("## 05 §7\n\n| Property | Check | Criterion | Observed | n | Result | Figure from |\n|---|---|---|---|---|---|---|\n")
	for _, row := range r.Rows {
		for i := range row.Checks {
			c := &row.Checks[i]
			prop := ""
			if i == 0 {
				prop = fmt.Sprintf("**%s** (%s)", row.Property, row.Result)
			}
			req := ""
			if c.Required {
				req = " (required)"
			}
			res := c.Result
			if c.Reason != "" {
				res += ": " + c.Reason
			}
			n := ""
			if c.Observed.N > 0 && Metrics[c.Metric].Kind == KindLatency { // a count is one value, not n samples
				n = strconv.FormatUint(c.Observed.N, 10)
			}
			w("| %s | `%s`%s | %s %s %s%s | %s | %s | %s | %s |\n", prop, c.ID, req, c.Stat, c.Op, fmtNum(c.Value), unit(c.Unit), observed(c), n, res, c.Status)
		}
	}
	w("\n## Loss accounting\n\n")
	l := r.Loss
	w("- Operator telemetry: sent %d = accepted %d + refused %d + dropped %d + duplicate %d; %d pending; %d clients unbalanced.\n",
		l.Operator.Sent, l.Operator.Accepted, l.Operator.Refused, l.Operator.Dropped, l.Operator.Duplicate, l.Operator.Pending, l.Operator.Unbalanced)
	w("- Receivers: observed %d, sent %d = accepted %d + duplicates %d + refused %d; shed %d; pending %d; %d receivers unbalanced.\n",
		l.Receivers.Observed, l.Receivers.Sent, l.Receivers.Accepted, l.Receivers.Duplicates, l.Receivers.Refused, l.Receivers.BacklogShed, l.Receivers.Pending, l.Receivers.Unbalanced)
	w("- Picture (timed console): handed %d, shown %d, unshown %d, accounted %d (refused, dropped_frames %d, sheds), silent %d; untraceable frames %d.\n",
		l.Picture.Handed, l.Picture.Shown, l.Picture.Unshown, l.PictureAccounted, l.PictureDroppedFrames, l.PictureSilent, l.Picture.Untraceable)
	w("- Traffic streams (watched flights): handed %d, shown %d, unshown %d, untraceable %d.\n", l.Traffic.Handed, l.Traffic.Shown, l.Traffic.Unshown, l.Traffic.Untraceable)
	w("- Generator sheds: operator %d, Remote ID %d.\n\n", l.GeneratorShedOperator, l.GeneratorShedRID)
	a := r.Alerts
	w("## Alerts\n\nExpected %d, raised %d, cleared %d, missed %d, clears missed %d, unexpected %d, untraceable %d.\n\n",
		a.Expected, a.Raised, a.Cleared, len(a.Missed), len(a.MissedClear), len(a.Unexpected), a.Untraceable)
	if len(r.Failures) > 0 {
		w("## Failures\n\n")
		for _, f := range r.Failures {
			w("- %s\n", f)
		}
		w("\n")
	}
	return b.String()
}

func unit(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}

func observed(c *CheckResult) string {
	o := c.Observed
	if !o.Measured {
		return "not measured"
	}
	if o.Value != nil {
		return fmtNum(*o.Value)
	}
	f := func(p *float64) string {
		if p == nil {
			return "-"
		}
		return fmtNum(*p)
	}
	return fmt.Sprintf("p50 %s / p95 %s / p99 %s / max %s", f(o.P50), f(o.P95), f(o.P99), f(o.Max))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

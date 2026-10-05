package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// MatrixFormat is the matrix format this harness reads.
const MatrixFormat = "chaos-matrix/v1"

// Bounds a matrix is held to (bounded everything): no row can hold a
// fault, or wait for a recovery, longer than this.
const (
	maxHoldS    = 1800
	maxWithinS  = 1800
	maxRows     = 64
	maxExpects  = 32
	maxSkewS    = 3600
	minEveryS   = 0.2
	maxTimeoutS = 30
)

// Matrix is scripts/chaos/matrix.yaml: the systems probed, the
// background traffic and the rows (one per failure domain of 05 §6 and
// the scripts that inject it). Every figure is here, none in the code.
type Matrix struct {
	Format     string      `yaml:"format"`
	Source     string      `yaml:"source"`
	Pending    []string    `yaml:"pending"`
	Probe      ProbeCfg    `yaml:"probe"`
	Systems    []SystemCfg `yaml:"systems"`
	Background Background  `yaml:"background"`
	Rows       []Row       `yaml:"rows"`
}

// ProbeCfg is how often and how long the harness looks.
type ProbeCfg struct {
	EveryS     float64 `yaml:"every_s"`
	TimeoutS   float64 `yaml:"timeout_s"`
	PreflightS float64 `yaml:"preflight_s"`
}

// SystemCfg is one probed endpoint: a system's readiness, or the lab
// issuer's and the DSS's health, on the host demo.env names.
type SystemCfg struct {
	Name string `yaml:"name"`
	Host string `yaml:"host"` // demo.env key, e.g. USSP_HOST
	Path string `yaml:"path"`
}

// Background is the traffic that runs under every row.
type Background struct {
	Scenario string `yaml:"scenario"`
	// HoldStep is the scenario step the faults run during; its for_s and
	// the scenario's duration_s are set to cover the selected rows.
	HoldStep string `yaml:"hold_step"`
	// RaisedWithinS bounds the wait for the standing alert, in every
	// system named in Systems, before the first row.
	RaisedWithinS float64 `yaml:"raised_within_s"`
	// RealertWithinS bounds how long after a row's restore the standing
	// alert must be open again in every system.
	RealertWithinS float64 `yaml:"realert_within_s"`
	// StaleReasons are the clear reasons a stale_ok row allows.
	StaleReasons []string `yaml:"stale_reasons"`
	Systems      []string `yaml:"systems"`
	Kind         string   `yaml:"kind"`
	Subject      string   `yaml:"subject"`
	// MaxSilenceS is the longest a background system's console stream
	// may go without a frame (console/status/v1 comes every 2 s, spec
	// 04 §1) before what it did not say about the alert stops counting:
	// a silence longer than this inside a row fails the row unless the
	// row names the system in streams_down. Below realert_within_s.
	MaxSilenceS float64 `yaml:"max_silence_s"`
}

// Row is one failure domain run.
type Row struct {
	ID     string   `yaml:"id"`
	Domain string   `yaml:"domain"`
	Args   []string `yaml:"args"`
	// System is the system the fault is in ("" for a lab component or
	// the whole stack); "others" in an expectation means every probed
	// system but this one.
	System string   `yaml:"system"`
	Cite   []string `yaml:"cite"`
	Claim  string   `yaml:"claim"`
	// Pending names a figure of this row that is the spec's default and
	// waits for GCAA (L-Q4).
	Pending        string  `yaml:"pending"`
	HoldS          float64 `yaml:"hold_s"`
	RecoverWithinS float64 `yaml:"recover_within_s"`
	// Alerts is what the row promises the background alert, per system
	// of the background (a system not named is "kept"):
	//   kept      it stays open under its id: no clear, no second raise
	//             (the system's state survives the fault);
	//   stale_ok  the fault stops the aircraft's data reaching the
	//             system, so the alert may clear as stale (or
	//             source_disabled), never otherwise, and must be open
	//             again within background.realert_within_s of the
	//             restore; never two open at once.
	// In every mode the alert must be open again after the row.
	Alerts       map[string]string `yaml:"alerts"`
	AlertsReason string            `yaml:"alerts_reason"`
	// StreamsDown are the background systems whose console stream the
	// fault itself takes down (the process serving it, or the system,
	// stops or is cut off): their silence is allowed inside the row's
	// window when the stream comes back re-sending the open alerts (a
	// console/snapshot/v1, or the active alert frames a stream sends on
	// connect), so the alert after it is re-read. Any other silence longer
	// than background.max_silence_s fails the row.
	StreamsDown       []string `yaml:"streams_down"`
	StreamsDownReason string   `yaml:"streams_down_reason"`
	During            []Expect `yaml:"during"`
	After             []Expect `yaml:"after"`
	Skew              []Skew   `yaml:"skew"`
}

// Expect is one claim judged on the samples of a phase.
//
//	ready: always       the endpoint answers 200 in every sample
//	ready: lost         it stops answering 200 within within_s
//	ready: back         it answers 200 again within within_s
//	(not_in in place of in: any state but those, present and named)
//	check + in          the named readiness check is in one of the
//	                    states within within_s (always: in every sample)
//	recovered: true     ready, and every check that was ok before the
//	                    fault ok again, within within_s
//	status              the system's console/status/v1 lists the
//	                    degraded slug within within_s ("status_absent":
//	                    it does not list it in any frame, and there is at
//	                    least one frame: no frame proves nothing)
//	source + in         the system's console/status/v1 shows the source
//	                    ("source/instance", "*" for the type) in one of
//	                    the states within within_s
//	field + in          the system's console/status/v1 member (nats,
//	                    dp_state) is in one of the states within within_s
type Expect struct {
	System    StringList `yaml:"system"`
	Ready     string     `yaml:"ready"`
	Check     string     `yaml:"check"`
	In        []string   `yaml:"in"`
	NotIn     []string   `yaml:"not_in"`
	Always    bool       `yaml:"always"`
	Recovered bool       `yaml:"recovered"`
	Status    string     `yaml:"status"`
	StatusNot string     `yaml:"status_absent"`
	Source    string     `yaml:"source"`
	Field     string     `yaml:"field"`
	WithinS   float64    `yaml:"within_s"`
	Why       string     `yaml:"why"`
}

// Skew is one case of the clock row: a receiver whose clock is SkewS
// seconds off sends one signed batch, and the authority must accept or
// refuse it (06 T2: a body more than 30 s from the ingest clock is
// refused).
type Skew struct {
	SkewS float64 `yaml:"skew_s"`
	Want  string  `yaml:"want"` // accepted | refused
	// Problem is the problem type a refusal must carry to count
	// (required for want: refused): any other 4xx is another refusal.
	Problem string `yaml:"problem"`
}

// StringList is a YAML string or list of strings.
type StringList []string

// UnmarshalYAML accepts "a" or [a, b].
func (s *StringList) UnmarshalYAML(b []byte) error {
	var one string
	if err := yaml.Unmarshal(b, &one); err == nil {
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := yaml.Unmarshal(b, &many); err != nil {
		return errors.New("a string or a list of strings")
	}
	*s = many
	return nil
}

// Expectation kinds.
const (
	readyAlways = "always"
	readyLost   = "lost"
	readyBack   = "back"

	alertsKept    = "kept"
	alertsStaleOK = "stale_ok"

	skewAccepted = "accepted"
	skewRefused  = "refused"

	sysAll    = "all"
	sysOthers = "others"
)

// domainArgs is the number of arguments each domain script takes
// (scripts/chaos/<domain>.sh), and the domain "clock" that the harness
// runs itself.
var domainArgs = map[string]int{
	"adapter": 1, "api": 1, "hotpath": 1, "stall": 1, "source": 1, "nats": 1,
	"timescale": 2, "postgres": 2, "dbhost": 1, "system": 1, "partition": 1,
	"dss": 1, "ansp-feed": 1, "issuer": 1, "stack": 0, "clock": 0,
}

// The same names lib.sh accepts.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// LoadMatrix reads and validates the matrix; nothing runs on a matrix
// that does not validate.
func LoadMatrix(path string) (*Matrix, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the matrix
	if err != nil {
		return nil, fmt.Errorf("matrix: %w", err)
	}
	var m Matrix
	if err := yaml.UnmarshalWithOptions(b, &m, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("matrix %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("matrix %s: %w", path, err)
	}
	return &m, nil
}

// Validate checks every row before anything is injected.
//
//nolint:gocyclo // one list of independent checks, each with its own message
func (m *Matrix) Validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if m.Format != MatrixFormat {
		bad("format is %q, want %q", m.Format, MatrixFormat)
	}
	if m.Probe.EveryS < minEveryS || m.Probe.EveryS > 10 {
		bad("probe.every_s %g: between %g and 10", m.Probe.EveryS, minEveryS)
	}
	if m.Probe.TimeoutS <= 0 || m.Probe.TimeoutS > maxTimeoutS {
		bad("probe.timeout_s %g: above 0, at most %d", m.Probe.TimeoutS, maxTimeoutS)
	}
	if m.Probe.PreflightS <= 0 || m.Probe.PreflightS > maxWithinS {
		bad("probe.preflight_s %g: above 0, at most %d", m.Probe.PreflightS, maxWithinS)
	}
	systems := map[string]bool{}
	for i, s := range m.Systems {
		if !nameRe.MatchString(s.Name) || systems[s.Name] || s.Name == sysAll || s.Name == sysOthers {
			bad("systems[%d].name %q: a unique name", i, s.Name)
		}
		systems[s.Name] = true
		if !slices.Contains(hostKeys, s.Host) {
			bad("systems[%d].host %q: one of %s", i, s.Host, strings.Join(hostKeys, ", "))
		}
		if !strings.HasPrefix(s.Path, "/") {
			bad("systems[%d].path %q: an absolute path", i, s.Path)
		}
	}
	if len(m.Systems) == 0 {
		bad("systems: at least one")
	}
	bg := m.Background
	if bg.Scenario != "" {
		if bg.HoldStep == "" || bg.Kind == "" || len(bg.Systems) == 0 {
			bad("background: hold_step, kind and systems are required with a scenario")
		}
		if bg.RaisedWithinS <= 0 || bg.RaisedWithinS > maxWithinS {
			bad("background.raised_within_s %g: above 0, at most %d", bg.RaisedWithinS, maxWithinS)
		}
		if bg.RealertWithinS <= 0 || bg.RealertWithinS > maxWithinS {
			bad("background.realert_within_s %g: above 0, at most %d", bg.RealertWithinS, maxWithinS)
		}
		if len(bg.StaleReasons) == 0 {
			bad("background.stale_reasons: at least one")
		}
		if bg.MaxSilenceS <= 0 || bg.MaxSilenceS >= bg.RealertWithinS {
			bad("background.max_silence_s %g: above 0, below realert_within_s (a row's window must outlast it)", bg.MaxSilenceS)
		}
	}
	if len(m.Rows) == 0 || len(m.Rows) > maxRows {
		bad("rows: 1 to %d", maxRows)
	}
	ids := map[string]bool{}
	for i := range m.Rows {
		r := &m.Rows[i]
		at := fmt.Sprintf("rows[%d] %s", i, r.ID)
		if !idRe.MatchString(r.ID) || ids[r.ID] {
			bad("%s: id must be unique, lower-case letters, digits and dashes", at)
		}
		ids[r.ID] = true
		n, ok := domainArgs[r.Domain]
		if !ok {
			bad("%s: unknown domain %q", at, r.Domain)
		} else if len(r.Args) != n {
			bad("%s: domain %s takes %d argument(s), got %d", at, r.Domain, n, len(r.Args))
		}
		for _, a := range r.Args {
			if !nameRe.MatchString(a) {
				bad("%s: argument %q is not a valid name", at, a)
			}
		}
		if r.System != "" && !systems[r.System] {
			bad("%s: system %q is not probed", at, r.System)
		}
		if len(r.Cite) == 0 || strings.TrimSpace(r.Claim) == "" {
			bad("%s: cite and claim are required (a row asserts the spec's words)", at)
		}
		if r.HoldS <= 0 || r.HoldS > maxHoldS {
			bad("%s: hold_s %g: above 0, at most %d", at, r.HoldS, maxHoldS)
		}
		if r.RecoverWithinS <= 0 || r.RecoverWithinS > maxWithinS {
			bad("%s: recover_within_s %g: above 0, at most %d", at, r.RecoverWithinS, maxWithinS)
		}
		for sys, mode := range r.Alerts {
			if !slices.Contains(m.Background.Systems, sys) {
				bad("%s: alerts names %q, not a system of the background", at, sys)
			}
			switch mode {
			case alertsKept:
			case alertsStaleOK:
				if strings.TrimSpace(r.AlertsReason) == "" {
					bad("%s: alerts stale_ok needs alerts_reason (why the data stops)", at)
				}
			default:
				bad("%s: alerts %s %q: kept or stale_ok", at, sys, mode)
			}
		}
		for _, sys := range r.StreamsDown {
			if !slices.Contains(m.Background.Systems, sys) {
				bad("%s: streams_down names %q, not a system of the background", at, sys)
			}
		}
		if len(r.StreamsDown) > 0 && strings.TrimSpace(r.StreamsDownReason) == "" {
			bad("%s: streams_down needs streams_down_reason (what in the fault takes the stream down)", at)
		}
		if r.Domain == "clock" {
			if len(r.Skew) < 2 {
				bad("%s: the clock row needs an accepted and a refused case (presence and absence)", at)
			}
			want := map[string]bool{}
			for j, s := range r.Skew {
				if s.SkewS == 0 || s.SkewS < -maxSkewS || s.SkewS > maxSkewS {
					bad("%s: skew[%d].skew_s %g: non-zero, within ±%d", at, j, s.SkewS, maxSkewS)
				}
				if s.Want != skewAccepted && s.Want != skewRefused {
					bad("%s: skew[%d].want %q: accepted or refused", at, j, s.Want)
				}
				if s.Want == skewRefused && !strings.HasPrefix(s.Problem, "https://") {
					bad("%s: skew[%d]: a refused case needs problem, the type of the refusal it is about", at, j)
				}
				want[s.Want] = true
			}
			if !want[skewAccepted] || !want[skewRefused] {
				bad("%s: the clock row needs both an accepted and a refused case", at)
			}
		} else if len(r.Skew) > 0 {
			bad("%s: skew is for the clock domain only", at)
		}
		if len(r.During)+len(r.After) > maxExpects {
			bad("%s: at most %d expectations", at, maxExpects)
		}
		if r.Domain != "clock" && len(r.After) == 0 {
			bad("%s: after needs at least one expectation (a fault that is restored must be seen to recover)", at)
		}
		for j := range r.During {
			for _, msg := range r.During[j].check(systems, r, true) {
				bad("%s: during[%d]: %s", at, j, msg)
			}
		}
		for j := range r.After {
			for _, msg := range r.After[j].check(systems, r, false) {
				bad("%s: after[%d]: %s", at, j, msg)
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (e *Expect) check(systems map[string]bool, r *Row, during bool) []string {
	var out []string
	if len(e.System) == 0 {
		out = append(out, "system is required")
	}
	for _, s := range e.System {
		switch {
		case s == sysAll:
		case s == sysOthers:
			if r.System == "" {
				out = append(out, `"others" needs the row's system`)
			}
		case !systems[s]:
			out = append(out, fmt.Sprintf("system %q is not probed", s))
		}
	}
	kinds := 0
	if e.Ready != "" {
		kinds++
		switch e.Ready {
		case readyAlways:
		case readyLost:
			if !during {
				out = append(out, "ready: lost belongs to during")
			}
		case readyBack:
			if during {
				out = append(out, "ready: back belongs to after")
			}
		default:
			out = append(out, fmt.Sprintf("ready %q: always, lost or back", e.Ready))
		}
	}
	if e.Check != "" {
		kinds++
		if (len(e.In) == 0) == (len(e.NotIn) == 0) {
			out = append(out, "check needs one of in or not_in")
		}
	}
	if e.Recovered {
		kinds++
		if during {
			out = append(out, "recovered belongs to after")
		}
	}
	if e.Status != "" {
		kinds++
	}
	if e.Source != "" {
		kinds++
		if (len(e.In) == 0) == (len(e.NotIn) == 0) {
			out = append(out, "source needs one of in or not_in")
		}
	}
	if e.Field != "" {
		kinds++
		if e.Field != "nats" && e.Field != "dp_state" {
			out = append(out, fmt.Sprintf("field %q: nats or dp_state", e.Field))
		}
		if (len(e.In) == 0) == (len(e.NotIn) == 0) {
			out = append(out, "field needs one of in or not_in")
		}
	}
	if e.StatusNot != "" {
		kinds++
	}
	if kinds != 1 {
		out = append(out, "exactly one of ready, check, recovered, status, status_absent, source, field")
	}
	eventually := e.Ready == readyLost || e.Ready == readyBack || e.Recovered || e.Status != "" || e.Source != "" || e.Field != "" || (e.Check != "" && !e.Always)
	if eventually && (e.WithinS <= 0 || e.WithinS > maxWithinS) {
		out = append(out, fmt.Sprintf("within_s %g: above 0, at most %d", e.WithinS, maxWithinS))
	}
	if strings.TrimSpace(e.Why) == "" {
		out = append(out, "why is required (the spec's words this checks, cited)")
	}
	return out
}

// Select returns the rows named by ids (comma-separated), in matrix
// order; empty selects every row.
func (m *Matrix) Select(ids string) ([]Row, error) {
	if strings.TrimSpace(ids) == "" {
		return m.Rows, nil
	}
	want := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		want[strings.TrimSpace(id)] = true
	}
	var out []Row
	for i := range m.Rows {
		if id := m.Rows[i].ID; want[id] {
			out = append(out, m.Rows[i])
			delete(want, id)
		}
	}
	if len(want) > 0 {
		var missing []string
		for id := range want {
			missing = append(missing, id)
		}
		slices.Sort(missing)
		return nil, fmt.Errorf("matrix: no row %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Systems expands an expectation's systems.
func (e *Expect) systems(all []string, row *Row) []string {
	var out []string
	for _, s := range e.System {
		switch s {
		case sysAll:
			out = append(out, all...)
		case sysOthers:
			for _, x := range all {
				if x != row.System {
					out = append(out, x)
				}
			}
		default:
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// matches is a state the expectation accepts: one of In, or, with NotIn,
// any state but those (an absent check or member never matches).
func (e *Expect) matches(st string) bool {
	if st == "" {
		return false
	}
	if len(e.NotIn) > 0 {
		return !slices.Contains(e.NotIn, st)
	}
	return slices.Contains(e.In, st)
}

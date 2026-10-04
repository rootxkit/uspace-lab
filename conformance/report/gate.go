package report

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

// BaselineFormat names conformance/baseline/<target>.json.
const BaselineFormat = "conformance-baseline/v1"

// Baseline is the status each requirement had in the run a person
// reviewed: what CI compares every later run of the same target with.
type Baseline struct {
	Format string `json:"format"`
	Target string `json:"target"`
	// FromRun names the reviewed run and its digest.
	FromRun      string                   `json:"from_run"`
	FromDigest   string                   `json:"from_digest"`
	Requirements map[string]BaselineEntry `json:"requirements"`
}

// BaselineEntry is one requirement's reviewed status. A known failure
// says where it is tracked.
type BaselineEntry struct {
	Status result.Status `json:"status"`
	Note   string        `json:"note,omitempty"`
	// Failing lists, for a known failure, each failed check as
	// "<check> <subject>": a failure outside the list is a regression
	// even though the requirement was already failing.
	Failing []string `json:"failing,omitempty"`
}

// failing is the sorted "<check> <subject>" of a requirement's failed
// outcomes.
func failing(r *Report, id string) []string {
	var out []string
	for _, o := range r.Outcomes {
		if o.Requirement == id && o.Status == result.Fail {
			out = append(out, o.Check+" "+o.Subject)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// ReadBaseline reads and checks a baseline file.
func ReadBaseline(path string) (*Baseline, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own baseline
	if err != nil {
		return nil, err
	}
	var bl Baseline
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bl); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := bl.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &bl, nil
}

// validate is the one rule set a baseline obeys, whether NewBaseline
// makes it or ReadBaseline reads it: the format, a target, a valid
// status per requirement, and a note on every known failure (gate or
// informative: an accepted failure says where it is tracked).
func (bl *Baseline) validate() error {
	if bl.Format != BaselineFormat || bl.Target == "" {
		return fmt.Errorf("format %q and a target are required", bl.Format)
	}
	var missing []string
	for id, e := range bl.Requirements {
		if !e.Status.Valid() {
			return fmt.Errorf("%s: status %q", id, e.Status)
		}
		if e.Status == result.Fail && e.Note == "" {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("known failures without a note saying where each is tracked: %s", strings.Join(missing, ", "))
	}
	return nil
}

// NewBaseline is the baseline a reviewed report sets. Known failures
// get the note given per id (a failure without one is refused: it must
// be tracked before it is accepted), and a note for a requirement the
// report does not have is refused as a typo. What it returns,
// ReadBaseline reads.
func NewBaseline(r *Report, digest string, notes map[string]string) (*Baseline, error) {
	bl := &Baseline{Format: BaselineFormat, Target: r.Target.Name, FromRun: r.Run, FromDigest: digest, Requirements: map[string]BaselineEntry{}}
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		e := BaselineEntry{Status: rr.Status, Note: notes[rr.ID]}
		if rr.Status == result.Fail {
			e.Failing = failing(r, rr.ID)
		}
		bl.Requirements[rr.ID] = e
	}
	var unknown []string
	for id := range notes {
		if _, ok := bl.Requirements[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("notes for requirements the report does not have: %s", strings.Join(unknown, ", "))
	}
	if err := bl.validate(); err != nil {
		return nil, err
	}
	return bl, nil
}

// Finding is one difference between a run and its baseline.
type Finding struct {
	Requirement string        `json:"requirement"`
	Was         result.Status `json:"was"`
	Now         result.Status `json:"now"`
	Regression  bool          `json:"regression"`
	Why         string        `json:"why"`
}

// Gate compares a report with the baseline of its target. A gate
// requirement regresses when it fails and the baseline does not record
// that failure, or when the baseline has it passing and it does not pass
// now (a pass that became not applicable is a check that stopped
// running). Informative requirements never regress. An improvement is
// reported so the baseline can be updated by a person.
func Gate(r *Report, bl *Baseline) ([]Finding, error) {
	if bl.Target != r.Target.Name {
		return nil, fmt.Errorf("the baseline is for %s, the report for %s", bl.Target, r.Target.Name)
	}
	var out []Finding
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		e, known := bl.Requirements[rr.ID]
		was := e.Status
		if !known {
			was = ""
		}
		f := Finding{Requirement: rr.ID, Was: was, Now: rr.Status}
		gate := rr.Role == RoleGate
		var fresh []string
		if rr.Status == result.Fail && was == result.Fail {
			for _, x := range failing(r, rr.ID) {
				if !slices.Contains(e.Failing, x) {
					fresh = append(fresh, x)
				}
			}
		}
		switch {
		case rr.Status == result.Fail && was != result.Fail:
			f.Regression, f.Why = gate, "a failure the baseline does not record"
		case len(fresh) > 0:
			f.Regression, f.Why = gate, "a known failure with new failing checks: "+strings.Join(firstN(fresh, 5), "; ")
		case was == result.Pass && rr.Status != result.Pass:
			f.Regression, f.Why = gate, "passed in the baseline, "+string(rr.Status)+" now"
		case was == result.Fail && rr.Status == result.Pass:
			f.Why = "a known failure now passes: update the baseline"
		case was != rr.Status:
			f.Why = "changed without regressing"
		default:
			continue
		}
		if !gate && f.Regression {
			f.Regression = false
		}
		if !gate {
			f.Why += " (informative)"
		}
		out = append(out, f)
	}
	for id := range bl.Requirements {
		if _, ok := r.Status(id); !ok {
			out = append(out, Finding{Requirement: id, Was: bl.Requirements[id].Status, Why: "in the baseline, not in the report", Regression: bl.Requirements[id].Status == result.Pass})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requirement < out[j].Requirement })
	return out, nil
}

// Regressed reports whether any finding is a regression.
func Regressed(fs []Finding) bool {
	for _, f := range fs {
		if f.Regression {
			return true
		}
	}
	return false
}

// Unreviewed lists the gate requirements of r that were not applicable
// and that no reviewed baseline accepts as such: a run with any of them
// is incomplete, not a pass. bl may be nil (no baseline: every not
// applicable gate requirement is unreviewed). A run in which no gate
// requirement applied at all checked nothing, so every one of them is
// listed whatever the baseline says.
func Unreviewed(r *Report, bl *Baseline) []string {
	applied := false
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		if rr.Role == RoleGate && rr.Status != result.NotApplicable {
			applied = true
		}
	}
	var out []string
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		if rr.Role != RoleGate || rr.Status != result.NotApplicable {
			continue
		}
		if applied && bl != nil {
			if e, ok := bl.Requirements[rr.ID]; ok && e.Status == result.NotApplicable {
				continue
			}
		}
		out = append(out, rr.ID)
	}
	sort.Strings(out)
	return out
}

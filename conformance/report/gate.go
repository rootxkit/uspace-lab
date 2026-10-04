package report

import (
	"encoding/json"
	"fmt"
	"os"
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
	if bl.Format != BaselineFormat || bl.Target == "" {
		return nil, fmt.Errorf("%s: format %q and a target are required", path, bl.Format)
	}
	for id, e := range bl.Requirements {
		if !e.Status.Valid() {
			return nil, fmt.Errorf("%s: %s: status %q", path, id, e.Status)
		}
		if e.Status == result.Fail && e.Note == "" {
			return nil, fmt.Errorf("%s: %s: a known failure needs a note saying where it is tracked", path, id)
		}
	}
	return &bl, nil
}

// NewBaseline is the baseline a reviewed report sets. Known failures
// get the note given per id (a failure without one is refused: it must
// be tracked before it is accepted).
func NewBaseline(r *Report, digest string, notes map[string]string) (*Baseline, error) {
	bl := &Baseline{Format: BaselineFormat, Target: r.Target.Name, FromRun: r.Run, FromDigest: digest, Requirements: map[string]BaselineEntry{}}
	var missing []string
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		e := BaselineEntry{Status: rr.Status, Note: notes[rr.ID]}
		if rr.Status == result.Fail && e.Note == "" && rr.Role == RoleGate {
			missing = append(missing, rr.ID)
		}
		bl.Requirements[rr.ID] = e
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("failures without a note (where each is tracked): %s", strings.Join(missing, ", "))
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
		switch {
		case rr.Status == result.Fail && was != result.Fail:
			f.Regression, f.Why = gate, "a failure the baseline does not record"
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

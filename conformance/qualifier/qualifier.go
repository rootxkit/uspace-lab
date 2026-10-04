// Package qualifier reads a uss_qualifier test run report (report.json,
// the TestRunReport of monitoring/uss_qualifier/reports/report.py at the
// commit conformance/uss_qualifier/SOURCE pins) and turns the checks it
// attributes to the target's participant into outcomes: one per InterUSS
// requirement id, pass when at least one check of it passed and none
// failed, fail when one failed. The suite reports what uss_qualifier
// checks and nothing more (spec 09 §3).
package qualifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/rootxkit/uspace-lab/conformance/report"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

// MaxReportBytes bounds a report read (a full F3548 run is tens of MB).
const MaxReportBytes = 512 << 20

// Run is what the import found in one report.
type Run struct {
	CodebaseVersion string
	CommitHash      string
	Digest          string
	Outcomes        []result.Outcome
}

type check struct {
	Name         string   `json:"name"`
	Summary      string   `json:"summary"`
	Severity     string   `json:"severity"`
	Requirements []string `json:"requirements"`
	Participants []string `json:"participants"`
}

// Import reads path and returns the outcomes of participant's checks
// for catalogue requirement req (F3411-SP, F3548-SCD, ...).
func Import(path, req, participant string) (*Run, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxReportBytes {
		return nil, fmt.Errorf("%s is above %d bytes", path, MaxReportBytes)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the qualifier's own output
	if err != nil {
		return nil, err
	}
	var top struct {
		CodebaseVersion string          `json:"codebase_version"`
		CommitHash      string          `json:"commit_hash"`
		Report          json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%s: not a TestRunReport: %w", path, err)
	}
	if top.CommitHash == "" || len(top.Report) == 0 {
		return nil, errors.New(path + ": not a TestRunReport (no commit_hash or report)")
	}
	var tree any
	if err := json.Unmarshal(top.Report, &tree); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	type tally struct {
		passed []string
		failed []string
	}
	per := map[string]*tally{}
	var execErrors []string
	walk(tree, func(m map[string]any) {
		for _, kind := range []string{"passed_checks", "failed_checks"} {
			l, _ := m[kind].([]any)
			for _, item := range l {
				raw, _ := json.Marshal(item)
				var c check
				if json.Unmarshal(raw, &c) != nil || !slices.Contains(c.Participants, participant) {
					continue
				}
				for _, rid := range c.Requirements {
					t := per[rid]
					if t == nil {
						t = &tally{}
						per[rid] = t
					}
					if kind == "passed_checks" {
						t.passed = append(t.passed, c.Name)
					} else {
						t.failed = append(t.failed, fmt.Sprintf("%s (%s): %s", c.Name, c.Severity, c.Summary))
					}
				}
			}
		}
		if e, ok := m["execution_error"].(map[string]any); ok {
			name, _ := m["name"].(string)
			msg, _ := e["message"].(string)
			execErrors = append(execErrors, fmt.Sprintf("%s: %s", name, msg))
		}
	})
	run := &Run{CodebaseVersion: top.CodebaseVersion, CommitHash: top.CommitHash, Digest: report.Digest(b)}
	ids := make([]string, 0, len(per))
	for id := range per {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := per[id]
		if len(t.failed) > 0 {
			run.Outcomes = append(run.Outcomes, result.Failed(req, "uss_qualifier", id, 0,
				fmt.Sprintf("%d failed check(s)", len(t.failed)), strings.Join(first(t.failed, 5), "; ")))
			continue
		}
		run.Outcomes = append(run.Outcomes, result.Passed(req, "uss_qualifier", id, 0, fmt.Sprintf("%d passed check(s)", len(t.passed))))
	}
	for _, e := range execErrors {
		run.Outcomes = append(run.Outcomes, result.Failed(req, "uss_qualifier", "execution", 0, "uss_qualifier did not complete a scenario", e))
	}
	if len(run.Outcomes) == 0 {
		run.Outcomes = append(run.Outcomes, result.Skipped(req, "uss_qualifier", participant, "uss_qualifier attributed no check to participant "+participant))
	}
	return run, nil
}

func walk(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		fn(t)
		for _, x := range t {
			walk(x, fn)
		}
	case []any:
		for _, x := range t {
			walk(x, fn)
		}
	}
}

func first(l []string, n int) []string {
	if len(l) <= n {
		return l
	}
	return append(slices.Clone(l[:n]), fmt.Sprintf("and %d more", len(l)-n))
}

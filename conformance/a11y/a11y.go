// Package a11y imports the axe run of the conformance suite
// (conformance/axe, Playwright with @axe-core/playwright): one outcome
// per public page the suite loaded, for A11Y-PUBLIC, which is
// informative (docs/PLAN.md §7.1 L-Q10, pending GCAA).
package a11y

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

// Format names the results file conformance/axe writes.
const Format = "conformance-axe/v1"

// Requirement is the catalogue id the axe run decides.
const Requirement = "A11Y-PUBLIC"

// MaxResultsBytes bounds the results file.
const MaxResultsBytes = 8 << 20

// Results is the file conformance/axe writes.
type Results struct {
	Format     string   `json:"format"`
	AxeVersion string   `json:"axe_version"`
	Standard   string   `json:"standard"`
	Tags       []string `json:"tags"`
	Pages      []Page   `json:"pages"`
}

// Page is one page as axe saw it.
type Page struct {
	URL        string      `json:"url"`
	Status     int         `json:"status"`
	Error      string      `json:"error,omitempty"`
	Passes     int         `json:"passes"`
	Violations []Violation `json:"violations"`
}

// Violation is one axe rule that failed on the page.
type Violation struct {
	ID     string `json:"id"`
	Impact string `json:"impact"`
	Nodes  int    `json:"nodes"`
}

// Import reads the results and returns one outcome per page: pass with
// no violation, fail with the rules that failed or when the page did not
// load (nothing was checked, so it cannot pass).
func Import(path string) (*Results, []result.Outcome, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > MaxResultsBytes {
		return nil, nil, fmt.Errorf("%s is above %d bytes", path, MaxResultsBytes)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the axe run's own output
	if err != nil {
		return nil, nil, err
	}
	var r Results
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Format != Format {
		return nil, nil, fmt.Errorf("%s: format %q, want %s", path, r.Format, Format)
	}
	var out []result.Outcome
	for _, p := range r.Pages {
		switch {
		case p.Error != "":
			out = append(out, result.Failed(Requirement, "axe", p.URL, p.Status, "the page did not load: nothing was checked", p.Error))
		case p.Status < 200 || p.Status > 299:
			out = append(out, result.Failed(Requirement, "axe", p.URL, p.Status, fmt.Sprintf("the page answered %d: nothing was checked", p.Status), ""))
		case len(p.Violations) > 0:
			var ids []string
			for _, v := range p.Violations {
				ids = append(ids, fmt.Sprintf("%s (%s, %d nodes)", v.ID, v.Impact, v.Nodes))
			}
			sort.Strings(ids)
			out = append(out, result.Failed(Requirement, "axe", p.URL, p.Status,
				fmt.Sprintf("%d axe rule(s) failed at %s", len(p.Violations), r.Standard), strings.Join(ids, "; ")))
		default:
			out = append(out, result.Passed(Requirement, "axe", p.URL, p.Status, fmt.Sprintf("axe %s: %d rules passed, none failed (%s)", r.AxeVersion, p.Passes, r.Standard)))
		}
	}
	if len(out) == 0 {
		out = append(out, result.Skipped(Requirement, "axe", "pages", "the axe run loaded no page"))
	}
	return &r, out, nil
}

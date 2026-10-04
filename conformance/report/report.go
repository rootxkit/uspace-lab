// Package report is the conformance suite's combined report
// (docs/WORKPACKAGES/WP-L7.md "Report and onboarding"): one JSON per run
// that folds every check's outcome into one status per requirement of
// conformance/requirements.yaml, with the commits of this repository and
// of uspace-core, the pinned InterUSS commit, the images under test and
// the policy the run applied (E-05). It is hashed and signed with the lab
// issuer's key (L-Q12 default, "proposed"), and compared with a
// committed baseline so that CI fails on a regression.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/conformance/policy"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

// Format names the report shape.
const Format = "conformance-report/v1"

// CatalogueFormat names conformance/requirements.yaml.
const CatalogueFormat = "conformance-requirements/v1"

// Requirement is one catalogue entry.
type Requirement struct {
	ID        string   `yaml:"id" json:"id"`
	Title     string   `yaml:"title" json:"title"`
	Source    string   `yaml:"source" json:"source"`
	Systems   []string `yaml:"systems" json:"systems"`
	DecidedBy []string `yaml:"decided_by" json:"decided_by"`
}

// Catalogue is conformance/requirements.yaml.
type Catalogue struct {
	Format       string        `yaml:"format"`
	Requirements []Requirement `yaml:"requirements"`
}

// LoadCatalogue reads and checks the catalogue.
func LoadCatalogue(path string) (*Catalogue, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own configuration
	if err != nil {
		return nil, fmt.Errorf("requirements: %w", err)
	}
	var c Catalogue
	if err := yaml.UnmarshalWithOptions(b, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("requirements %s: %w", path, err)
	}
	if c.Format != CatalogueFormat {
		return nil, fmt.Errorf("requirements %s: format %q, want %s", path, c.Format, CatalogueFormat)
	}
	seen := map[string]bool{}
	for _, r := range c.Requirements {
		if r.ID == "" || r.Title == "" || r.Source == "" || len(r.Systems) == 0 || len(r.DecidedBy) == 0 {
			return nil, fmt.Errorf("requirements %s: %q needs id, title, source, systems and decided_by", path, r.ID)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("requirements %s: %s twice", path, r.ID)
		}
		seen[r.ID] = true
	}
	return &c, nil
}

// Get returns the requirement with id.
func (c *Catalogue) Get(id string) (Requirement, bool) {
	for _, r := range c.Requirements {
		if r.ID == id {
			return r, true
		}
	}
	return Requirement{}, false
}

// Roles of a requirement in a report.
const (
	RoleGate        = "gate"
	RoleInformative = "informative"
)

// Verdicts of a run.
const (
	// VerdictPass: every gate requirement that applies passed and at
	// least one gate requirement applied.
	VerdictPass = "pass"
	// VerdictFail: a gate requirement failed.
	VerdictFail = "fail"
	// VerdictIncomplete: nothing failed, but no gate requirement applied
	// or some were not applicable: not a pass.
	VerdictIncomplete = "incomplete"
)

// RequirementResult is one requirement's status in a run.
type RequirementResult struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Source   string        `json:"source"`
	Role     string        `json:"role"`
	Status   result.Status `json:"status"`
	Reasons  []string      `json:"reasons,omitempty"`
	Outcomes struct {
		Pass          int `json:"pass"`
		Fail          int `json:"fail"`
		NotApplicable int `json:"not_applicable"`
	} `json:"outcomes"`
}

// Target is what was tested.
type Target struct {
	System  string `json:"system"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	BaseURL string `json:"base_url"`
}

// Commits are the code that ran the suite (E-05).
type Commits struct {
	Lab      string `json:"lab"`
	LabDirty bool   `json:"lab_dirty"`
	Core     string `json:"core"`
	Go       string `json:"go"`
}

// Contract is the OpenAPI file the national tests read.
type Contract struct {
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// Qualifier is one uss_qualifier configuration as it ran (or why not).
type Qualifier struct {
	Config       string `json:"config"`
	Requirement  string `json:"requirement"`
	Ran          bool   `json:"ran"`
	Reason       string `json:"reason,omitempty"`
	ReportSHA256 string `json:"report_sha256,omitempty"`
	CommitHash   string `json:"commit_hash,omitempty"`
}

// InterUSS is the pinned monitoring commit and image (uss_qualifier/SOURCE).
type InterUSS struct {
	Repo        string `json:"repo"`
	Tag         string `json:"tag"`
	Commit      string `json:"commit"`
	Image       string `json:"image"`
	ImageDigest string `json:"image_digest"`
}

// Summary counts the requirements by status.
type Summary struct {
	Pass          int    `json:"pass"`
	Fail          int    `json:"fail"`
	NotApplicable int    `json:"not_applicable"`
	Verdict       string `json:"verdict"`
}

// Report is report/<run>/report.json.
type Report struct {
	Format       string              `json:"format"`
	Run          string              `json:"run"`
	Wording      string              `json:"wording"`
	Target       Target              `json:"target"`
	StartedAt    time.Time           `json:"started_at"`
	EndedAt      time.Time           `json:"ended_at"`
	Commits      Commits             `json:"commits"`
	Images       map[string]string   `json:"images,omitempty"`
	InterUSS     InterUSS            `json:"interuss"`
	Contract     *Contract           `json:"contract,omitempty"`
	Policy       *policy.Policy      `json:"policy"`
	Summary      Summary             `json:"summary"`
	Requirements []RequirementResult `json:"requirements"`
	Qualifier    []Qualifier         `json:"qualifier,omitempty"`
	Outcomes     []result.Outcome    `json:"outcomes"`
}

// Wording is the status of the report format itself (L-Q12).
const Wording = "Proposed format (docs/PLAN.md §7.1 L-Q12, pending GCAA): this report states what the suite observed; it certifies nothing."

// Build folds outcomes into a report for one target system. Every
// catalogue requirement for the system gets a status; an outcome for a
// requirement the catalogue does not have, or an invalid outcome, is an
// error (the suite never reports what it cannot name).
func Build(cat *Catalogue, pol *policy.Policy, t Target, outcomes []result.Outcome) (*Report, error) {
	by := map[string][]result.Outcome{}
	for _, o := range outcomes {
		if err := o.Validate(); err != nil {
			return nil, err
		}
		if _, ok := cat.Get(o.Requirement); !ok {
			return nil, fmt.Errorf("outcome for %s, which is not in the catalogue", o.Requirement)
		}
		by[o.Requirement] = append(by[o.Requirement], o)
	}
	r := &Report{Format: Format, Wording: Wording, Target: t, Policy: pol, Outcomes: outcomes}
	gateApplied, gateMissing := 0, 0
	for _, req := range cat.Requirements {
		if !slices.Contains(req.Systems, t.System) {
			if len(by[req.ID]) > 0 {
				return nil, fmt.Errorf("outcomes for %s, which the catalogue does not apply to %s", req.ID, t.System)
			}
			continue
		}
		rr := RequirementResult{ID: req.ID, Title: req.Title, Source: req.Source, Role: RoleGate}
		if pol.IsInformative(req.ID) {
			rr.Role = RoleInformative
		}
		os := by[req.ID]
		rr.Status, rr.Reasons = result.Fold(os)
		if len(os) == 0 {
			rr.Reasons = []string{"no check of this run applied (decided by " + strings.Join(req.DecidedBy, ", ") + ")"}
		}
		for _, o := range os {
			switch o.Status {
			case result.Pass:
				rr.Outcomes.Pass++
			case result.Fail:
				rr.Outcomes.Fail++
			case result.NotApplicable:
				rr.Outcomes.NotApplicable++
			}
		}
		switch rr.Status {
		case result.Pass:
			r.Summary.Pass++
		case result.Fail:
			r.Summary.Fail++
		case result.NotApplicable:
			r.Summary.NotApplicable++
		}
		if rr.Role == RoleGate {
			if rr.Status == result.NotApplicable {
				gateMissing++
			} else {
				gateApplied++
			}
		}
		r.Requirements = append(r.Requirements, rr)
	}
	r.Summary.Verdict = VerdictPass
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		if rr.Role == RoleGate && rr.Status == result.Fail {
			r.Summary.Verdict = VerdictFail
		}
	}
	if r.Summary.Verdict == VerdictPass && (gateApplied == 0 || gateMissing > 0) {
		r.Summary.Verdict = VerdictIncomplete
	}
	return r, nil
}

// Status returns a requirement's result.
func (r *Report) Status(id string) (RequirementResult, bool) {
	for i := range r.Requirements {
		rr := &r.Requirements[i]
		if rr.ID == id {
			return *rr, true
		}
	}
	return RequirementResult{}, false
}

// Marshal is the report's bytes as written and signed.
func (r *Report) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Read reads a report file.
func Read(path string) (*Report, []byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the report
	if err != nil {
		return nil, nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Format != Format {
		return nil, nil, fmt.Errorf("%s: format %q, want %s", path, r.Format, Format)
	}
	return &r, b, nil
}

// LabCommits are this repository's commit (git, else the binary's VCS
// stamp) and uspace-core's version from the build.
func LabCommits(repo string) Commits {
	c := Commits{Lab: "unknown", Core: "unknown"}
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output(); err == nil { //nolint:gosec // fixed git arguments
		c.Lab = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Output(); err == nil { //nolint:gosec // fixed git arguments
		c.LabDirty = len(strings.TrimSpace(string(out))) > 0
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		c.Go = bi.GoVersion
		for _, d := range bi.Deps {
			if d.Path == "github.com/rootxkit/uspace-core" {
				c.Core = d.Version
			}
		}
		if c.Lab == "unknown" {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					c.Lab = s.Value
				case "vcs.modified":
					c.LabDirty = s.Value == "true"
				}
			}
		}
	}
	return c
}

// ReadInterUSS reads conformance/uss_qualifier/SOURCE (key = value
// lines, # comments).
func ReadInterUSS(path string) (InterUSS, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own pin
	if err != nil {
		return InterUSS{}, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	in := InterUSS{Repo: kv["repo"], Tag: kv["tag"], Commit: kv["commit"], Image: kv["image"], ImageDigest: kv["image_digest"]}
	if len(in.Commit) != 40 || in.Image == "" || !strings.HasPrefix(in.ImageDigest, "sha256:") {
		return InterUSS{}, fmt.Errorf("%s: commit (40 hex), image and image_digest (sha256:) are required", path)
	}
	return in, nil
}

// RunID names a run: UTC time and the target name.
func RunID(now time.Time, name string) string {
	return now.UTC().Format("20060102T150405Z") + "-" + name
}

// Write writes report.json under dir/<run>/ and returns its path and
// bytes. The run id must be one plain path element: a run that would
// write outside dir is refused.
func (r *Report) Write(dir string) (string, []byte, error) {
	if r.Run == "" || r.Run != filepath.Base(r.Run) || strings.ContainsAny(r.Run, `/\:`) || strings.Contains(r.Run, "..") {
		return "", nil, fmt.Errorf("run id %q is not a plain directory name", r.Run)
	}
	out := filepath.Join(dir, r.Run)
	if err := os.MkdirAll(out, 0o750); err != nil {
		return "", nil, err
	}
	b, err := r.Marshal()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(out, "report.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", nil, err
	}
	return path, b, nil
}

// Text is the report as lines for a terminal and a CI summary.
func (r *Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "conformance %s: %s (%s) at %s: %s, %d pass, %d fail, %d not applicable\n",
		r.Run, r.Target.Name, r.Target.System, r.Target.BaseURL, r.Summary.Verdict, r.Summary.Pass, r.Summary.Fail, r.Summary.NotApplicable)
	rs := slices.Clone(r.Requirements)
	sort.SliceStable(rs, func(i, j int) bool { return rank(rs[i].Status) < rank(rs[j].Status) })
	for i := range rs {
		rr := &rs[i]
		role := ""
		if rr.Role == RoleInformative {
			role = " (informative)"
		}
		fmt.Fprintf(&b, "  %-14s %-20s%s %d/%d/%d", rr.Status, rr.ID, role, rr.Outcomes.Pass, rr.Outcomes.Fail, rr.Outcomes.NotApplicable)
		if len(rr.Reasons) > 0 && rr.Status != result.Pass {
			fmt.Fprintf(&b, "  %s", strings.Join(firstN(rr.Reasons, 3), "; "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func rank(s result.Status) int {
	switch s {
	case result.Fail:
		return 0
	case result.NotApplicable:
		return 1
	case result.Pass:
		return 2
	}
	return 3
}

func firstN(l []string, n int) []string {
	if len(l) <= n {
		return l
	}
	return append(slices.Clone(l[:n]), fmt.Sprintf("and %d more", len(l)-n))
}

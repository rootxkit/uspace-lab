package resultsite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed results render: every run gets a page, the index lists
// the runs and the trend, a failing and a passing verdict both show, and
// a JSON file that is not result/v1 is listed, not dropped.
func TestCommittedResultsRender(t *testing.T) {
	dir := t.TempDir()
	if err := copyDir("../../results/20261003-synthetic-reference", filepath.Join(dir, "20261003-synthetic-reference")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.json"), []byte(`{"format":"something/v9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var run *Run
	for _, r := range s.Runs {
		if r.ID == "20261003-synthetic-reference" {
			run = r
		}
	}
	if run == nil || run.Pass == 0 || run.Fail == 0 {
		t.Fatalf("runs %+v", s.Runs)
	}
	if len(s.Unreadable) != 1 || s.Unreadable[0] != "stray.json" {
		t.Fatalf("unreadable %v", s.Unreadable)
	}
	ps, err := s.Pages()
	if err != nil {
		t.Fatal(err)
	}
	idx := string(ps["index.html"])
	for _, want := range []string{"20261003-synthetic-reference", "sc-01-hover-inside-minima", "selftest-wrong-expectation", `class="fail"`, `class="pass"`, "stray.json"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	page := string(ps["runs/20261003-synthetic-reference.html"])
	for _, want := range []string{"selftest-wrong-expectation", "Failures", "Operator ledgers", "never evidence"} {
		if !strings.Contains(page, want) {
			t.Errorf("run page lacks %q", want)
		}
	}
	if err := s.Write(filepath.Join(dir, "site")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "runs", "20261003-synthetic-reference.html")); err != nil {
		t.Fatal(err)
	}
}

func TestPageNameIsAFileName(t *testing.T) {
	if got := PageName("run/../x y"); got != "run_.._x_y.html" {
		t.Fatal(got)
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	es, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

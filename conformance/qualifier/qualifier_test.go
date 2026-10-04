package qualifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

// runReport builds a TestRunReport with the members report.py names: a
// suite whose scenario has one case with one step holding the checks.
func runReport(t *testing.T, passed, failed []map[string]any, execErr bool) string {
	t.Helper()
	scenario := map[string]any{
		"name": "DisplayProviderBehavior", "scenario_type": "scenarios.astm.netrid.v22a.DisplayProviderBehavior",
		"cases": []any{map[string]any{"name": "case", "steps": []any{map[string]any{
			"name": "step", "passed_checks": passed, "failed_checks": failed,
		}}}},
	}
	if execErr {
		scenario["execution_error"] = map[string]any{"type": "RuntimeError", "message": "resource not created"}
	}
	doc := map[string]any{
		"codebase_version": "interuss/monitoring/v0.36.0",
		"commit_hash":      "0fabe238ef177f66266eba6048545ae1e6bbb4a0",
		"report": map[string]any{"test_suite": map[string]any{
			"name": "ASTM F3411-22a", "actions": []any{map[string]any{"test_scenario": scenario}},
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func chk(name, req string, parts ...string) map[string]any {
	return map[string]any{"name": name, "requirements": []string{req}, "participants": parts, "summary": "s", "severity": "High"}
}

func statusOf(out []result.Outcome, subject string) result.Status {
	for _, o := range out {
		if o.Subject == subject {
			return o.Status
		}
	}
	return ""
}

func TestImportBothWays(t *testing.T) {
	p := runReport(t,
		[]map[string]any{chk("ok", "astm.f3411.v22a.NET0610", "authority-01"), chk("ok2", "astm.f3411.v22a.NET0620", "authority-01"), chk("other", "astm.f3411.v22a.NET0999", "uss2")},
		[]map[string]any{chk("bad", "astm.f3411.v22a.NET0620", "authority-01")}, false)
	run, err := Import(p, "F3411-DP", "authority-01")
	if err != nil {
		t.Fatal(err)
	}
	if run.CommitHash != "0fabe238ef177f66266eba6048545ae1e6bbb4a0" {
		t.Errorf("commit %q", run.CommitHash)
	}
	if st := statusOf(run.Outcomes, "astm.f3411.v22a.NET0610"); st != result.Pass {
		t.Errorf("NET0610 %s, want pass", st)
	}
	if st := statusOf(run.Outcomes, "astm.f3411.v22a.NET0620"); st != result.Fail {
		t.Errorf("NET0620 %s, want fail (one failed check beside a passed one)", st)
	}
	if st := statusOf(run.Outcomes, "astm.f3411.v22a.NET0999"); st != "" {
		t.Errorf("another participant's check was attributed: %s", st)
	}
}

func TestImportNothingAttributed(t *testing.T) {
	p := runReport(t, []map[string]any{chk("ok", "astm.f3411.v22a.NET0610", "uss2")}, nil, false)
	run, err := Import(p, "F3411-DP", "authority-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Outcomes) != 1 || run.Outcomes[0].Status != result.NotApplicable {
		t.Errorf("outcomes %+v, want one not_applicable", run.Outcomes)
	}
}

func TestImportExecutionErrorFails(t *testing.T) {
	p := runReport(t, []map[string]any{chk("ok", "astm.f3411.v22a.NET0610", "authority-01")}, nil, true)
	run, err := Import(p, "F3411-DP", "authority-01")
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(run.Outcomes, "execution") != result.Fail {
		t.Errorf("an execution error did not fail: %+v", run.Outcomes)
	}
}

func TestImportRefusesOtherFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(p, []byte(`{"format":"conformance-report/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(p, "F3411-DP", "authority-01"); err == nil {
		t.Error("a file that is not a TestRunReport was imported")
	}
}

// qualifierVars are values for every placeholder the configurations use.
var qualifierVars = map[string]string{
	"QUALIFIER_DSS_HOST": "dss", "QUALIFIER_DSS_URL": "http://dss:8082", "QUALIFIER_PARTICIPANT": "authority-01",
	"QUALIFIER_MOCK_RIDSP_URL": "http://mock-ridsp", "QUALIFIER_MOCK_RIDDP_URL": "http://mock-riddp", "QUALIFIER_MOCK_SCD_URL": "http://mock-scd",
	"QUALIFIER_TARGET_URL_REGEX": "http://authority.*", "QUALIFIER_MOCK_URL_REGEX": "http://mock-.*",
	"QUALIFIER_TARGET_INTERFACE_URL": "http://authority:8080/v1/dp/observations",
}

func TestCoverageAndRender(t *testing.T) {
	src := filepath.Join("..", "uss_qualifier")
	cov, err := LoadCoverage(filepath.Join(src, "coverage.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range cov.IDs() {
		cv := cov.Requirements[id]
		if cv.None != "" {
			continue
		}
		out := t.TempDir()
		p, err := Render(src, out, cv.Config, qualifierVars)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, f := range []string{p, filepath.Join(out, "library", "environment.yaml")} {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if placeholder.Match(b) {
				t.Errorf("%s: %s keeps a placeholder", id, f)
			}
		}
		// The presence half: an unset variable is an error, not an
		// empty value.
		partial := map[string]string{}
		for k, v := range qualifierVars {
			if k != "QUALIFIER_PARTICIPANT" {
				partial[k] = v
			}
		}
		if _, err := Render(src, t.TempDir(), cv.Config, partial); err == nil {
			t.Errorf("%s: rendered without QUALIFIER_PARTICIPANT", id)
		}
	}
}

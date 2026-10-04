package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/conformance/report"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

const labRoot = "../.."

func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(args, &out, &errb)
	return code, out.String() + errb.String()
}

func onlyReport(t *testing.T, dir string) string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*", "report.json"))
	if err != nil || len(m) != 1 {
		t.Fatalf("want one report under %s, found %v (%v)", dir, m, err)
	}
	return m[0]
}

func status(t *testing.T, path, id string) report.RequirementResult {
	t.Helper()
	r, _, err := report.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	rr, ok := r.Status(id)
	if !ok {
		t.Fatalf("%s: no %s", path, id)
	}
	return rr
}

// qualifierReport is a uss_qualifier TestRunReport at the pinned commit
// with one check for participant.
func qualifierReport(t *testing.T, participant string, failed bool) string {
	t.Helper()
	in, err := report.ReadInterUSS(filepath.Join(labRoot, "conformance", "uss_qualifier", "SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	chk := map[string]any{"name": "Service provider answers", "requirements": []string{"astm.f3411.v22a.NET0710"},
		"participants": []string{participant}, "summary": "no answer", "severity": "High"}
	step := map[string]any{"name": "step", "passed_checks": []any{}, "failed_checks": []any{}}
	if failed {
		step["failed_checks"] = []any{chk}
	} else {
		step["passed_checks"] = []any{chk}
	}
	doc := map[string]any{"codebase_version": "0.36.0", "commit_hash": in.Commit,
		"report": map[string]any{"test_scenario": map[string]any{"name": "NominalBehavior", "cases": []any{map[string]any{"steps": []any{step}}}}}}
	b, _ := json.Marshal(doc)
	p := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCandidateRunAndGate: the onboarding candidate (no national
// contract) runs end to end: what uss_qualifier cannot decide says why,
// an incomplete run is not a pass unless accepted explicitly or by a
// reviewed baseline, a failing qualifier report fails the run, and the
// baseline gate turns that into a regression while the reviewed run
// itself passes the gate.
func TestCandidateRunAndGate(t *testing.T) {
	t.Setenv("SIM_USSP_CONFORMANCE_BASE_URL", "http://127.0.0.1:1")
	target := filepath.Join(labRoot, "conformance", "targets", "sim-ussp.yaml")

	// Nothing a gate depends on was checked: exit 3, naming the way out.
	code, log := runCmd(t, "run", "--lab-root", labRoot, "--target", target, "--out", t.TempDir())
	if code != 3 || !strings.Contains(log, "incomplete") || !strings.Contains(log, "--allow-incomplete") {
		t.Fatalf("exit %d, want 3 (incomplete) naming --allow-incomplete:\n%s", code, log)
	}
	out := t.TempDir()
	code, log = runCmd(t, "run", "--lab-root", labRoot, "--target", target, "--out", out, "--allow-incomplete")
	if code != 0 {
		t.Fatalf("exit %d with --allow-incomplete, want 0:\n%s", code, log)
	}
	rep := onlyReport(t, out)
	if rr := status(t, rep, "F3411-SP"); rr.Status != result.NotApplicable || !strings.Contains(strings.Join(rr.Reasons, " "), "rid_injection") {
		t.Errorf("F3411-SP %+v: want not applicable for the missing interface", rr)
	}
	if rr := status(t, rep, "F3548-CP"); rr.Status != result.NotApplicable || !strings.Contains(strings.Join(rr.Reasons, " "), "constraint processing") {
		t.Errorf("F3548-CP %+v: want not applicable, no requirement set at the pinned commit", rr)
	}
	r, _, _ := report.Read(rep)
	if r.Summary.Verdict != report.VerdictIncomplete {
		t.Errorf("verdict %s, want incomplete", r.Summary.Verdict)
	}
	if !strings.Contains(log, "signed: no") {
		t.Errorf("the run does not say the report is unsigned:\n%s", log)
	}

	// The reviewed run with a passing qualifier report is the baseline.
	// It is still incomplete (no national contract), so it is accepted
	// only explicitly.
	out2 := t.TempDir()
	passArgs := []string{"run", "--lab-root", labRoot, "--target", target, "--qualifier-report", "F3411-SP=" + qualifierReport(t, "sim-ussp-01", false)}
	if code, log := runCmd(t, append(passArgs, "--out", t.TempDir())...); code != 3 {
		t.Fatalf("exit %d with a passing qualifier report and the rest not applicable, want 3:\n%s", code, log)
	}
	code, log = runCmd(t, append(passArgs, "--out", out2, "--allow-incomplete")...)
	if code != 0 {
		t.Fatalf("exit %d with a passing qualifier report:\n%s", code, log)
	}
	passing := onlyReport(t, out2)
	if rr := status(t, passing, "F3411-SP"); rr.Status != result.Pass {
		t.Fatalf("F3411-SP %+v, want pass", rr)
	}
	bl := filepath.Join(t.TempDir(), "sim-ussp.json")
	if code, log := runCmd(t, "baseline", "--report", passing, "--out", bl); code != 0 {
		t.Fatalf("baseline: %d\n%s", code, log)
	}
	if code, log := runCmd(t, "gate", "--report", passing, "--baseline", bl); code != 0 {
		t.Fatalf("the baseline's own run does not pass the gate: %d\n%s", code, log)
	}
	// The baseline reviewed what was not applicable: the same run against
	// it passes without --allow-incomplete ...
	if code, log := runCmd(t, append(passArgs, "--out", t.TempDir(), "--baseline", bl)...); code != 0 {
		t.Fatalf("against its reviewed baseline: exit %d, want 0:\n%s", code, log)
	}
	// ... and a baseline that does not record a not applicable gate
	// requirement did not review it.
	rb, err := report.ReadBaseline(bl)
	if err != nil {
		t.Fatal(err)
	}
	delete(rb.Requirements, "F3548-SCD")
	partialBL := filepath.Join(t.TempDir(), "sim-ussp.json")
	if err := writeJSON(partialBL, rb); err != nil {
		t.Fatal(err)
	}
	if code, log := runCmd(t, append(passArgs, "--out", t.TempDir(), "--baseline", partialBL)...); code != 3 || !strings.Contains(log, "F3548-SCD") {
		t.Fatalf("against a baseline without F3548-SCD: exit %d, want 3 naming it:\n%s", code, log)
	}

	// A failing qualifier report: the verdict fails the run, and against
	// the baseline it is a regression.
	failing := qualifierReport(t, "sim-ussp-01", true)
	out3 := t.TempDir()
	if code, log := runCmd(t, "run", "--lab-root", labRoot, "--target", target, "--out", out3, "--qualifier-report", "F3411-SP="+failing); code != 1 {
		t.Fatalf("exit %d with a failing qualifier report, want 1:\n%s", code, log)
	}
	code, log = runCmd(t, "run", "--lab-root", labRoot, "--target", target, "--out", t.TempDir(), "--qualifier-report", "F3411-SP="+failing, "--baseline", bl)
	if code != 1 || !strings.Contains(log, "REGRESSION") {
		t.Fatalf("exit %d against the baseline, want 1 with a regression:\n%s", code, log)
	}

	// A report of another InterUSS commit is refused, not imported.
	other := qualifierReport(t, "sim-ussp-01", false)
	b, _ := os.ReadFile(other)
	_ = os.WriteFile(other, bytes.Replace(b, []byte(`"commit_hash":"0fabe238`), []byte(`"commit_hash":"1fabe238`), 1), 0o600)
	if code, _ := runCmd(t, "run", "--lab-root", labRoot, "--target", target, "--out", t.TempDir(), "--qualifier-report", "F3411-SP="+other); code != 2 {
		t.Errorf("a report of another commit: exit %d, want 2", code)
	}
}

func TestSignVerifyCommands(t *testing.T) {
	t.Setenv("SIM_USSP_CONFORMANCE_BASE_URL", "http://127.0.0.1:1")
	dir := t.TempDir()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	keyFile := filepath.Join(dir, "signing-key.pem")
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Kid": "lab-k1"}, Bytes: der}), 0o600)
	iss, err := auth.NewIssuer("http://lab-issuer", k, "lab-k1")
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := json.Marshal(iss.JWKS())
	jwks := filepath.Join(dir, "jwks.json")
	_ = os.WriteFile(jwks, jb, 0o600)

	out := t.TempDir()
	code, log := runCmd(t, "run", "--lab-root", labRoot, "--target", filepath.Join(labRoot, "conformance", "targets", "sim-ussp.yaml"), "--out", out, "--sign-key", keyFile, "--allow-incomplete")
	if code != 0 || !strings.Contains(log, "signed: sha256:") {
		t.Fatalf("exit %d:\n%s", code, log)
	}
	rep := onlyReport(t, out)
	if code, log := runCmd(t, "verify", "--report", rep, "--jwks", jwks); code != 0 {
		t.Fatalf("verify: %d\n%s", code, log)
	}
	b, _ := os.ReadFile(rep)
	_ = os.WriteFile(rep, append(b, ' '), 0o600)
	if code, _ := runCmd(t, "verify", "--report", rep, "--jwks", jwks); code == 0 {
		t.Error("a changed report verified")
	}
}

// TestCISPEntryPoint runs conformance/cisp/run as uspace-cisp's
// tools/conformance.sh calls it (Q47): without CISP_BASE_URL it refuses
// (2); against a CISP that does not answer, with the CISP contract, the
// checks fail and so does the run (1); without any contract it says so,
// nothing passes and the run is incomplete (3), which only
// CONFORMANCE_ALLOW_INCOMPLETE=1 accepts (0).
func TestCISPEntryPoint(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("bash: %v", err)
		}
		t.Skip("bash is not on PATH")
	}
	script, err := filepath.Abs(filepath.Join(labRoot, "conformance", "cisp", "run"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(env ...string) (int, string, string) {
		rep := t.TempDir()
		cmd := exec.Command(bash, script)
		cmd.Dir = t.TempDir() // not a CISP checkout
		// A name of its own: conformance/baseline/cisp.json judges the
		// real CISP's runs, not these.
		cmd.Env = append(os.Environ(), append([]string{"CONFORMANCE_REPORT=" + rep, "CISP_BASE_URL=", "CONFORMANCE_CISP_OPENAPI=",
			"CONFORMANCE_TARGET_NAME=cisp-entry-point"}, env...)...)
		out, err := cmd.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return code, string(out), rep
	}
	if code, out, _ := run(); code != 2 {
		t.Errorf("without CISP_BASE_URL: exit %d, want 2:\n%s", code, out)
	}
	if code, out, _ := run("CISP_BASE_URL=http://127.0.0.1:1"); code != 3 || !strings.Contains(out, "no contract to test against") {
		t.Errorf("without a contract: exit %d, want 3 (incomplete) and the reason:\n%s", code, out)
	}
	code, out, rep := run("CISP_BASE_URL=http://127.0.0.1:1", "CONFORMANCE_ALLOW_INCOMPLETE=1")
	if code != 0 || !strings.Contains(out, "no contract to test against") {
		t.Errorf("without a contract, incomplete accepted: exit %d, want 0 and the reason:\n%s", code, out)
	} else if rr := status(t, onlyReport(t, rep), "NAT-UNAUTH"); rr.Status != result.NotApplicable {
		t.Errorf("NAT-UNAUTH %s without a contract", rr.Status)
	}
	// Judged against the committed CISP baseline, a run in which nothing
	// could be checked is a regression: CI fails on it.
	if _, err := os.Stat(filepath.Join(labRoot, "conformance", "baseline", "cisp.json")); err == nil {
		code, out, _ := run("CISP_BASE_URL=http://127.0.0.1:1", "CONFORMANCE_TARGET_NAME=cisp")
		if code != 1 || !strings.Contains(out, "REGRESSION") {
			t.Errorf("against the CISP baseline with nothing checked: exit %d, want 1 and a regression:\n%s", code, out)
		}
	}
	dir := os.Getenv("CONFORMANCE_CONTRACTS_DIR")
	if dir == "" {
		if os.Getenv("CONFORMANCE_REQUIRE_CONTRACTS") == "1" {
			t.Fatal("CONFORMANCE_CONTRACTS_DIR is unset")
		}
		t.Log("unverified: the failing half needs the CISP contract (CONFORMANCE_CONTRACTS_DIR)")
		return
	}
	contract, _ := filepath.Abs(filepath.Join(dir, "cisp.yaml"))
	code, out, rep = run("CISP_BASE_URL=http://127.0.0.1:1", "CONFORMANCE_CISP_OPENAPI="+contract)
	if code != 1 {
		t.Errorf("against a CISP that does not answer: exit %d, want 1:\n%s", code, out)
	} else if rr := status(t, onlyReport(t, rep), "NAT-UNAUTH"); rr.Status != result.Fail {
		t.Errorf("NAT-UNAUTH %s against a CISP that does not answer", rr.Status)
	}
}

package report

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/conformance/policy"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

func load(t *testing.T) (*Catalogue, *policy.Policy) {
	t.Helper()
	cat, err := LoadCatalogue(filepath.Join("..", "requirements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Load(filepath.Join("..", "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return cat, pol
}

func TestCatalogueAndPolicyLoad(t *testing.T) {
	cat, pol := load(t)
	for _, id := range pol.Informative.Requirements {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("policy names informative %s, which is not in the catalogue", id)
		}
	}
	if !pol.IsInformative("F3411-DP") {
		t.Error("the DP suite is informative by the L-Q3 default")
	}
}

func TestBuildFoldsAndJudges(t *testing.T) {
	cat, pol := load(t)
	tg := Target{System: "cisp", Name: "cisp-test", Role: "own", BaseURL: "http://cisp.invalid"}
	outs := []result.Outcome{
		result.Passed("NAT-UNAUTH", "unauthenticated", "a", 401, ""),
		result.Passed("NAT-UNAUTH", "unauthenticated", "b", 401, ""),
		result.Skipped("NAT-UNAUTH", "unauthenticated", "c", "no 401 declared"),
		result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", ""),
		result.Skipped("ED318-WEBHOOK", "webhook", "e", "no receiver"),
	}
	r, err := Build(cat, pol, tg, outs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]result.Status{"NAT-UNAUTH": result.Pass, "NAT-INVALID": result.Fail, "ED318-WEBHOOK": result.NotApplicable, "NAT-SUCCESS": result.NotApplicable}
	for id, st := range want {
		rr, ok := r.Status(id)
		if !ok || rr.Status != st {
			t.Errorf("%s: %+v, want %s", id, rr, st)
		}
		if rr.Status != result.Pass && len(rr.Reasons) == 0 {
			t.Errorf("%s: %s without a reason", id, rr.Status)
		}
	}
	if _, ok := r.Status("F3411-DP"); ok {
		t.Error("an authority requirement in a CISP report")
	}
	if r.Summary.Verdict != VerdictFail {
		t.Errorf("verdict %s, want fail", r.Summary.Verdict)
	}
	// Without the failure nothing failed, but gate requirements did not
	// apply: incomplete, never pass.
	r, err = Build(cat, pol, tg, outs[:3])
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Verdict != VerdictIncomplete {
		t.Errorf("verdict %s, want incomplete", r.Summary.Verdict)
	}
	// An outcome for an unknown requirement, or one the system does not
	// have, is refused.
	if _, err := Build(cat, pol, tg, []result.Outcome{result.Passed("NOPE", "x", "y", 0, "")}); err == nil {
		t.Error("an unknown requirement was accepted")
	}
	if _, err := Build(cat, pol, tg, []result.Outcome{result.Passed("F3411-DP", "x", "y", 0, "")}); err == nil {
		t.Error("an authority requirement was accepted for the CISP")
	}
	if _, err := Build(cat, pol, tg, []result.Outcome{{Requirement: "NAT-UNAUTH", Check: "x", Status: result.Fail}}); err == nil {
		t.Error("a failure without a reason was accepted")
	}
}

func TestPassVerdict(t *testing.T) {
	cat, pol := load(t)
	var outs []result.Outcome
	for _, req := range cat.Requirements {
		for _, s := range req.Systems {
			if s == "authority" {
				outs = append(outs, result.Passed(req.ID, "x", "y", 200, ""))
			}
		}
	}
	r, err := Build(cat, pol, Target{System: "authority", Name: "a"}, outs)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Verdict != VerdictPass {
		t.Errorf("verdict %s, want pass", r.Summary.Verdict)
	}
	// An informative failure does not fail the verdict.
	outs = append(outs, result.Failed("F3411-DP", "x", "z", 0, "late", ""))
	r, _ = Build(cat, pol, Target{System: "authority", Name: "a"}, outs)
	if r.Summary.Verdict != VerdictPass {
		t.Errorf("an informative failure made the verdict %s", r.Summary.Verdict)
	}
}

func TestGateBothWays(t *testing.T) {
	cat, pol := load(t)
	tg := Target{System: "cisp", Name: "cisp-test"}
	base, err := Build(cat, pol, tg, []result.Outcome{
		result.Passed("NAT-UNAUTH", "unauthenticated", "a", 401, ""),
		result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewBaseline(base, "sha256:x", nil); err == nil {
		t.Fatal("a baseline accepted an untracked failure")
	}
	bl, err := NewBaseline(base, "sha256:x", map[string]string{"NAT-INVALID": "rootxkit/uspace-cisp issue (to file)"})
	if err != nil {
		t.Fatal(err)
	}
	// The same run: nothing regressed (the branch that says nothing is
	// wrong, E-02).
	fs, err := Gate(base, bl)
	if err != nil {
		t.Fatal(err)
	}
	if Regressed(fs) {
		t.Errorf("the baseline's own run regressed: %+v", fs)
	}
	// A pass that fails, a pass that stops applying, a new failure.
	for _, tc := range []struct {
		name string
		outs []result.Outcome
	}{
		{"pass to fail", []result.Outcome{result.Failed("NAT-UNAUTH", "unauthenticated", "a", 200, "open", ""), result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", "")}},
		{"pass to not applicable", []result.Outcome{result.Skipped("NAT-UNAUTH", "unauthenticated", "a", "no token"), result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", "")}},
		{"new failure", []result.Outcome{result.Passed("NAT-UNAUTH", "unauthenticated", "a", 401, ""), result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", ""), result.Failed("NAT-SUCCESS", "success", "s", 500, "500", "")}},
	} {
		r, err := Build(cat, pol, tg, tc.outs)
		if err != nil {
			t.Fatal(err)
		}
		fs, err := Gate(r, bl)
		if err != nil {
			t.Fatal(err)
		}
		if !Regressed(fs) {
			t.Errorf("%s: no regression found: %+v", tc.name, fs)
		}
	}
	// A known-failing requirement that fails on another check too: the
	// new failure is a regression (a known failure hides nothing).
	r, _ := Build(cat, pol, tg, []result.Outcome{result.Passed("NAT-UNAUTH", "unauthenticated", "a", 401, ""),
		result.Failed("NAT-INVALID", "invalid_body", "d", 200, "accepted", ""), result.Failed("NAT-INVALID", "invalid_body", "e", 200, "accepted", "")})
	if fs, _ := Gate(r, bl); !Regressed(fs) {
		t.Errorf("a new failing check inside a known failure was not a regression: %+v", fs)
	}
	// An improvement is reported, not a regression.
	r, _ = Build(cat, pol, tg, []result.Outcome{result.Passed("NAT-UNAUTH", "unauthenticated", "a", 401, ""), result.Passed("NAT-INVALID", "invalid_body", "d", 400, "")})
	fs, _ = Gate(r, bl)
	if Regressed(fs) {
		t.Errorf("an improvement regressed: %+v", fs)
	}
	improved := false
	for _, f := range fs {
		improved = improved || f.Requirement == "NAT-INVALID" && strings.Contains(f.Why, "update the baseline")
	}
	if !improved {
		t.Errorf("the improvement is not reported: %+v", fs)
	}
}

func writeKey(t *testing.T, dir string) (string, *rsa.PrivateKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "signing-key.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Kid": "lab-test"}, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, k
}

func writeJWKS(t *testing.T, dir, name string, k *rsa.PrivateKey) string {
	t.Helper()
	iss, err := auth.NewIssuer("http://lab-issuer", k, "lab-test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(iss.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignVerifyBothWays(t *testing.T) {
	dir := t.TempDir()
	keyFile, key := writeKey(t, dir)
	jwks := writeJWKS(t, dir, "jwks.json", key)
	report := filepath.Join(dir, "report.json")
	if err := os.WriteFile(report, []byte(`{"format":"conformance-report/v1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Sign(report, keyFile, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v, err := Verify(report, jwks)
	if err != nil {
		t.Fatalf("a fresh signature does not verify: %v", err)
	}
	if v.Digest != d || v.KID != "lab-test" {
		t.Errorf("verified %+v, signed %s", v, d)
	}
	// Another key's JWKS refuses it.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(report, writeJWKS(t, dir, "other.json", other)); err == nil {
		t.Error("verified with another key")
	}
	// A changed byte refuses it.
	if err := os.WriteFile(report, []byte(`{"format":"conformance-report/v1" }`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(report, jwks); err == nil {
		t.Error("a changed report verified")
	}
}

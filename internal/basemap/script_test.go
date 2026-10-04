package basemap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// TestVerifyScript runs basemap/verify.sh itself over the fixture
// bundle: once passing, read for what it says, then with a Georgian
// range file removed and with the budget lowered, each of which must
// fail the script (E-01, E-02). It starts Caddy (on PATH, or the pinned
// image in Docker), so it runs when BASEMAP_SCRIPT_TEST=1; the basemap
// workflow sets it. With the variable set and no Caddy or Docker, the
// script fails and so does the test.
func TestVerifyScript(t *testing.T) {
	if os.Getenv("BASEMAP_SCRIPT_TEST") != "1" {
		t.Skip("BASEMAP_SCRIPT_TEST=1 runs verify.sh with a file server")
	}
	f := newBundle(t)
	tmp := t.TempDir()
	inputs := filepath.Join(tmp, "inputs.yaml")
	b, err := yaml.Marshal(f.inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputs, b, 0o600); err != nil {
		t.Fatal(err)
	}
	budget := filepath.Join(tmp, "budget.txt")
	regions, err := filepath.Abs("testdata/regions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../basemap/verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := func(budgetBytes string) (string, error) {
		t.Helper()
		if err := os.WriteFile(budget, []byte("bundle "+budgetBytes+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", filepath.ToSlash(script), filepath.ToSlash(f.dir))
		cmd.Env = append(os.Environ(),
			"BASEMAP_REGIONS="+filepath.ToSlash(regions),
			"BASEMAP_INPUTS="+filepath.ToSlash(inputs),
			"BASEMAP_BUDGET="+filepath.ToSlash(budget),
			"BASEMAP_PROFILE=bundle",
		)
		out, err := cmd.CombinedOutput()
		t.Logf("verify.sh (budget %s):\n%s", budgetBytes, out)
		return string(out), err
	}

	out, err := run("1048576")
	if err != nil {
		t.Fatalf("verify.sh failed a good bundle: %v", err)
	}
	for _, want := range []string{"served by", "-> 206 Partial Content", "verify: 5 checks, 0 failed", "Nuskhuri 40/40"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify.sh output lacks %q", want)
		}
	}

	out, err = run("1000")
	if err == nil || !strings.Contains(out, "FAIL size within budget") || !strings.Contains(out, "over budget by") {
		t.Errorf("verify.sh passed a bundle over budget: %v", err)
	}

	if err := os.Remove(filepath.Join(f.dir, "fonts", fixtureStack, "11520-11775.pbf")); err != nil {
		t.Fatal(err)
	}
	out, err = run("1048576")
	if err == nil || !strings.Contains(out, "FAIL glyphs") || !strings.Contains(out, "Nuskhuri") {
		t.Errorf("verify.sh passed a bundle without Nuskhuri: %v", err)
	}
}

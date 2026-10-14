package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeDocker records every docker command line and, for "compose",
// the variables its --env-file files set (one KEY per line in
// <dir>/compose-vars); "ps" and "volume ls" print nothing (nothing left).
const fakeDocker = `#!/usr/bin/env bash
echo "$*" >> "$FAKE_DOCKER_DIR/calls"
if [ "$1" = compose ]; then
  prev=""
  for a in "$@"; do
    if [ "$prev" = --env-file ]; then sed -n 's/^\([A-Z_][A-Z0-9_]*\)=.*/\1/p' "$a" >> "$FAKE_DOCKER_DIR/compose-vars"; fi
    prev="$a"
  done
fi
exit 0
`

// demoDown runs demo-down.sh in a copy of deploy/ whose demo.env is env,
// with the fake docker first on PATH; it returns the variables compose
// was given and the output.
func demoDown(t *testing.T, env string) (map[string]bool, string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash: %v", err)
	}
	dir := t.TempDir()
	for _, f := range []string{"demo-down.sh", "compose.yaml", "systems/compose.yaml", "demo.env.example"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte(read(t, f)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "docker"), []byte(fakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.ToSlash(filepath.Join(dir, "demo-down.sh")))
	cmd.Env = append(os.Environ(), "FAKE_DOCKER_DIR="+filepath.ToSlash(shim), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	vars := map[string]bool{}
	b, _ := os.ReadFile(filepath.Join(shim, "compose-vars"))
	for _, v := range strings.Fields(string(b)) {
		vars[v] = true
	}
	return vars, string(out), err
}

// requiredVars are the ${VAR:?...} names of the two compose files:
// compose refuses to read the project, even to take it down, without
// them.
func requiredVars(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*):\?`)
	seen := map[string]bool{}
	var out []string
	for _, f := range []string{"compose.yaml", "systems/compose.yaml"} {
		for _, m := range re.FindAllStringSubmatch(read(t, f), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// A stack started with a demo.env older than the example (one without
// a variable the compose files have since come to require) can still be
// taken down: demo-down.sh fills what demo.env lacks from the example.
// It used to fail, "required variable DEMO_CADDY_IP is missing a value",
// and leave the whole stack running. The current file is the other half
// of the pair: nothing missing, nothing filled in that hides it.
func TestDemoDownWithAnOlderDemoEnv(t *testing.T) {
	example := read(t, "demo.env.example")
	var old []string
	for _, line := range strings.Split(example, "\n") {
		if strings.HasPrefix(line, "DEMO_CADDY_IP=") || strings.HasPrefix(line, "DEMO_LAB_SUBNET=") || strings.HasPrefix(line, "DEMO_LAB_IP_RANGE=") {
			continue
		}
		old = append(old, line)
	}
	for name, env := range map[string]string{"older": strings.Join(old, "\n"), "current": example} {
		vars, out, err := demoDown(t, env)
		if err != nil {
			t.Fatalf("%s demo.env: demo-down.sh failed: %v\n%s", name, err, out)
		}
		var missing []string
		for _, v := range requiredVars(t) {
			if !vars[v] {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s demo.env: compose was not given %v", name, missing)
		}
		if !strings.Contains(out, "no container or volume of project uspace-demo is left") {
			t.Errorf("%s demo.env: %s", name, out)
		}
	}
	if len(requiredVars(t)) < 5 {
		t.Fatalf("only %v required", requiredVars(t))
	}
}

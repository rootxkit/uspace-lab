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
    if [ "$prev" = --env-file ]; then
      case "$a" in /*) echo "$a" >> "$FAKE_DOCKER_DIR/absolute" ;; esac
      sed -n 's/^\([A-Z_][A-Z0-9_]*\)=.*/\1/p' "$a" >> "$FAKE_DOCKER_DIR/compose-vars"
    fi
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
	return demoDownWith(t, env, nil)
}

// demoDownWith is demoDown with setup called on the copy of deploy/
// before the script runs.
func demoDownWith(t *testing.T, env string, setup func(dir string)) (map[string]bool, string, error) {
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
	if setup != nil {
		setup(dir)
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
	if abs, _ := os.ReadFile(filepath.Join(shim, "absolute")); len(abs) > 0 {
		t.Errorf("compose was given an absolute --env-file %q: under Git Bash (MSYS_NO_PATHCONV=1) the native docker cannot open /tmp or /c/ paths", strings.TrimSpace(string(abs)))
	}
	return vars, string(out), err
}

// Every --env-file demo-down.sh hands compose is relative to deploy/,
// for both a demo.env older than the example and the current one, and
// with or without the generated passwords (demoDown checks it): on
// Windows the docker CLI is a native program, which cannot open the
// /tmp/... path mktemp gives under Git Bash once demo-down.sh switches
// path conversion off. The fill file of the older-demo.env fix was such
// a path, and demo-down.sh failed on every stack, "couldn't find env
// file: C:\tmp\tmp...". Without the passwords file the defaults file is
// handed over too, so both are run.
func TestDemoDownEnvFilesAreRelative(t *testing.T) {
	example := read(t, "demo.env.example")
	if _, out, err := demoDown(t, example); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
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

// A fault's state (scripts/chaos/lib.sh, DEMO_STATE_DIR/chaos) records
// containers stopped or cut; once demo-down has removed every container
// it describes nothing, and kept it makes the next stack's fault refuse
// to start, "system authority is already down (restore it first)" (a
// load run killed in its authority outage left one, and the next chaos
// matrix failed authority-down on it). demo-down removes it and says
// so; with none there is nothing said, and the rest of the state stays.
func TestDemoDownRemovesFaultState(t *testing.T) {
	example := read(t, "demo.env.example")
	var dir string
	_, out, err := demoDownWith(t, example, func(d string) {
		dir = d
		if err := os.MkdirAll(filepath.Join(d, "local-demo", "chaos"), 0o750); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"local-demo/chaos/system-authority.order", "local-demo/passwords.env"} {
			if err := os.WriteFile(filepath.Join(d, f), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "local-demo", "chaos")); !os.IsNotExist(err) {
		t.Errorf("the fault state is still there (%v)\n%s", err, out)
	}
	if !strings.Contains(out, "removed the fault state") || !strings.Contains(out, "system-authority.order") {
		t.Errorf("not said:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "local-demo", "passwords.env")); err != nil {
		t.Errorf("the rest of the state went too: %v", err)
	}
	_, out, err = demoDownWith(t, example, func(d string) {
		if err := os.MkdirAll(filepath.Join(d, "local-demo"), 0o750); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || strings.Contains(out, "fault state") {
		t.Errorf("without a fault state: %v\n%s", err, out)
	}
}

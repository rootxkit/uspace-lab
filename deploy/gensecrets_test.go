package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genSecrets runs systems/gen-secrets.sh with a demo.env whose state
// directory is a fresh temporary one, with extra environment and,
// when shim is not empty, that directory ahead of PATH.
func genSecrets(t *testing.T, shim string, env ...string) (state, out string, err error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash: %v", err)
	}
	dir := t.TempDir()
	state = filepath.Join(dir, "state")
	example := read(t, "demo.env.example")
	var lines []string
	for _, line := range strings.Split(example, "\n") {
		if strings.HasPrefix(line, "DEMO_STATE_DIR=") {
			line = "DEMO_STATE_DIR=" + filepath.ToSlash(state)
		}
		lines = append(lines, line)
	}
	envFile := filepath.Join(dir, "demo.env")
	if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "systems/gen-secrets.sh", filepath.ToSlash(envFile))
	cmd.Env = append(os.Environ(), env...)
	if shim != "" {
		cmd.Env = append(cmd.Env, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	b, err := cmd.CombinedOutput()
	return state, string(b), err
}

// An openssl that fails makes gen-secrets.sh fail and say so: the step,
// and openssl's own message. It used to discard openssl's stderr and
// exit 1 without a word, leaving a half-filled state directory behind.
func TestGenSecretsFailsLoudly(t *testing.T) {
	shim := t.TempDir()
	fake := "#!/usr/bin/env bash\necho \"fake openssl: cannot open the key file\" >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(shim, "openssl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	state, out, err := genSecrets(t, shim)
	t.Logf("gen-secrets.sh with a failing openssl:\n%s", out)
	if err == nil {
		t.Fatal("gen-secrets.sh exited 0 with a failing openssl")
	}
	for _, want := range []string{"gen-secrets: openssl req failed (exit 7)", "fake openssl: cannot open the key file"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q", want)
		}
	}
	if strings.Contains(out, "gen-secrets: done") {
		t.Error("a failed run reported done")
	}
	if _, err := os.Stat(filepath.Join(state, "ca", "ca.pem")); err == nil {
		t.Error("a failed run left ca/ca.pem, which the next run would keep")
	}
}

// The branch that says nothing is wrong, run for real with the
// system's openssl, and with MSYS_NO_PATHCONV=0 set as demo-up.sh sets
// it: under Git Bash any value of it switches path conversion off, and
// the native openssl could then open none of the /c/... paths. Every
// file is written, none is empty, and a second run writes nothing.
func TestGenSecretsWritesEverything(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skipf("openssl: %v", err)
	}
	state, out, err := genSecrets(t, "", "MSYS_NO_PATHCONV=0")
	t.Logf("gen-secrets.sh:\n%s", out)
	if err != nil {
		t.Fatalf("gen-secrets.sh: %v", err)
	}
	if !strings.Contains(out, "gen-secrets: done, 17 file(s) written") {
		t.Errorf("the run does not report 17 files written")
	}
	for _, f := range []string{
		"ca/ca.pem", "ca/ca.key", "tls/cert.pem", "tls/key.pem",
		"authority/token-1.pem", "authority/publication-1.pem", "authority/pii.key",
		"authority/registry-hash.key", "authority/admin.pw",
		"cisp/signing.pem", "cisp/session.pem", "cisp/secrets.key",
		"ussp/issuer.pem", "ussp/mfa.key",
		"ansp/session.pem", "ansp/secrets.key", "ansp/delivery.pem", "ansp/admin.pw",
		"passwords.env",
	} {
		st, err := os.Stat(filepath.Join(state, f))
		if err != nil || st.Size() == 0 {
			t.Errorf("%s: missing or empty (%v)", f, err)
		}
	}
	pw := read(t, filepath.Join(state, "passwords.env"))
	for _, line := range strings.Split(pw, "\n") {
		if name, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") && v == "" {
			t.Errorf("passwords.env: %s is empty", name)
		}
	}

	cmd := exec.Command("bash", "systems/gen-secrets.sh", filepath.ToSlash(filepath.Join(filepath.Dir(state), "demo.env")))
	again, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(again), "done, 0 file(s) written") {
		t.Errorf("a second run: %v\n%s", err, again)
	}
}

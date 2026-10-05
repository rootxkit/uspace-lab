package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The domain scripts against testdata/docker, a fake that keeps each
// container's state in a file: every primitive must say what it did,
// exit 0 only when the act took, and exit 1 when the fault was not (or
// could not be) put in place. Docker itself runs in the lab run
// (docs/RUNBOOKS/chaos.md) and in the lab-stack workflow.

type fakeProject struct {
	t   *testing.T
	dir string
}

func newFakeProject(t *testing.T, running ...string) *fakeProject {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	f := &fakeProject{t: t, dir: t.TempDir()}
	for _, s := range running {
		f.write(s, "running")
	}
	return f
}

func (f *fakeProject) write(svc, status string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "svc-"+svc), []byte(status+" 2026-10-05T03:00:00.000000000Z\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeProject) status(svc string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "svc-"+svc))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Fields(string(b))[0]
}

func (f *fakeProject) acts() string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "acts"))
	return string(b)
}

// run runs scripts/chaos/<script> with the fake docker first on PATH.
func (f *fakeProject) run(script string, args ...string) (string, int) {
	f.t.Helper()
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+testdata+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER="+f.dir, "CHAOS_PROJECT=p", "CHAOS_STATE="+filepath.Join(f.dir, "state"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	case err != nil:
		f.t.Fatal(err)
	}
	return out.String(), 0
}

func TestKillAndStartBothWays(t *testing.T) {
	p := newFakeProject(t, "ussp-monitor")
	out, code := p.run("hotpath.sh", "inject", "ussp-monitor")
	if code != 0 || !strings.Contains(out, "killed ussp-monitor") || p.status("ussp-monitor") != "exited" {
		t.Fatalf("inject: %d %q, status %s", code, out, p.status("ussp-monitor"))
	}
	// A second injection finds nothing to kill: exit 1, no act.
	before := p.acts()
	out, code = p.run("hotpath.sh", "inject", "ussp-monitor")
	if code != 1 || !strings.Contains(out, "nothing to kill") || p.acts() != before {
		t.Fatalf("second inject: %d %q", code, out)
	}
	out, code = p.run("hotpath.sh", "restore", "ussp-monitor")
	if code != 0 || !strings.Contains(out, "started ussp-monitor") || p.status("ussp-monitor") != "running" {
		t.Fatalf("restore: %d %q", code, out)
	}
	// Restoring what was never faulted says the fault was not in place.
	out, code = p.run("hotpath.sh", "restore", "ussp-monitor")
	if code != 1 || !strings.Contains(out, "the fault was not in place") {
		t.Fatalf("second restore: %d %q", code, out)
	}
}

func TestADockerThatSaysYesAndDoesNothingFailsTheScript(t *testing.T) {
	p := newFakeProject(t, "ussp-monitor", "ussp-nats")
	if err := os.WriteFile(filepath.Join(p.dir, "ignore"), []byte("svc-ussp-monitor\nsvc-ussp-nats\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := p.run("hotpath.sh", "inject", "ussp-monitor"); code != 1 || !strings.Contains(out, "still running after SIGKILL") {
		t.Fatalf("kill: %d %q", code, out)
	}
	if out, code := p.run("nats.sh", "inject", "ussp"); code != 1 || !strings.Contains(out, "is running after stop") {
		t.Fatalf("stop: %d %q", code, out)
	}
	if out, code := p.run("stall.sh", "inject", "ussp-monitor"); code != 1 || !strings.Contains(out, "after pause") {
		t.Fatalf("pause: %d %q", code, out)
	}
}

func TestPauseBothWays(t *testing.T) {
	p := newFakeProject(t, "ussp-monitor")
	if out, code := p.run("stall.sh", "inject", "ussp-monitor"); code != 0 || p.status("ussp-monitor") != "paused" {
		t.Fatalf("%d %q", code, out)
	}
	// A paused process is not stopped: start must refuse, unpause works.
	if out, code := p.run("hotpath.sh", "inject", "ussp-monitor"); code != 1 {
		t.Fatalf("killing a paused process: %d %q", code, out)
	}
	if out, code := p.run("stall.sh", "restore", "ussp-monitor"); code != 0 || p.status("ussp-monitor") != "running" {
		t.Fatalf("%d %q", code, out)
	}
	if _, code := p.run("stall.sh", "restore", "ussp-monitor"); code != 1 {
		t.Fatal("unpausing a running process succeeded")
	}
}

func TestSystemStopsProcessesFirstAndStartsTheReverse(t *testing.T) {
	p := newFakeProject(t, "cisp-api", "cisp-deliver", "cisp-nats", "cisp-postgres", "ussp-api")
	p.write("cisp-migrate", "exited")
	out, code := p.run("system.sh", "inject", "cisp")
	if code != 0 {
		t.Fatalf("%d %q", code, out)
	}
	acts := strings.Fields(strings.ReplaceAll(p.acts(), "\n", " "))
	want := "stop id-cisp-api stop id-cisp-deliver stop id-cisp-nats stop id-cisp-postgres"
	if strings.Join(acts, " ") != want {
		t.Fatalf("stop order %v, want %s", acts, want)
	}
	if p.status("ussp-api") != "running" || p.status("cisp-migrate") != "exited" {
		t.Fatal("another system or a one-shot was touched")
	}
	if _, code := p.run("system.sh", "inject", "cisp"); code != 1 {
		t.Fatal("a second injection over the first succeeded")
	}
	if out, code := p.run("system.sh", "restore", "cisp"); code != 0 {
		t.Fatalf("%d %q", code, out)
	}
	acts = strings.Fields(strings.ReplaceAll(p.acts(), "\n", " "))
	if got := strings.Join(acts[8:], " "); got != "start id-cisp-postgres start id-cisp-nats start id-cisp-deliver start id-cisp-api" {
		t.Fatalf("start order %s", got)
	}
	if p.status("cisp-migrate") != "exited" {
		t.Fatal("the restore started a one-shot")
	}
	if _, code := p.run("system.sh", "restore", "cisp"); code != 1 {
		t.Fatal("a restore with nothing stopped succeeded")
	}
}

func TestStackStopsAndStartsExactlyWhatRan(t *testing.T) {
	p := newFakeProject(t, "caddy", "ussp-api")
	p.write("ussp-migrate", "exited")
	if out, code := p.run("stack.sh", "inject"); code != 0 || p.status("caddy") != "exited" || p.status("ussp-api") != "exited" {
		t.Fatalf("%d %q", code, out)
	}
	if out, code := p.run("stack.sh", "restore"); code != 0 || p.status("caddy") != "running" || p.status("ussp-migrate") != "exited" {
		t.Fatalf("%d %q", code, out)
	}
	if _, code := p.run("stack.sh", "restore"); code != 1 {
		t.Fatal("a second restore succeeded")
	}
}

func TestScriptsRefuseBadInputBeforeActing(t *testing.T) {
	p := newFakeProject(t, "ussp-monitor")
	if out, code := p.run("hotpath.sh", "inject", "ussp-monitor;id"); code != 1 || !strings.Contains(out, "not a valid name") || p.acts() != "" {
		t.Fatalf("%d %q", code, out)
	}
	if _, code := p.run("hotpath.sh", "inject"); code != 2 {
		t.Fatalf("missing argument: exit %d, want 2", code)
	}
	if _, code := p.run("hotpath.sh", "explode", "ussp-monitor"); code != 2 {
		t.Fatalf("unknown action: exit %d, want 2", code)
	}
	if out, code := p.run("hotpath.sh", "inject", "ussp-ghost"); code != 1 || !strings.Contains(out, "has 0 containers") {
		t.Fatalf("missing service: %d %q", code, out)
	}
	if p.acts() != "" {
		t.Fatalf("acted on bad input: %q", p.acts())
	}
}

package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stage is a fake stack: the ussp's readiness answers 503 while its
// monitor is "down" in the fake docker, and the scripts are functions.
type stage struct {
	d      *fakeDocker
	srv    *httptest.Server
	h      *harness
	kills  atomic.Int32
	starts atomic.Int32
}

func newStage(t *testing.T, scriptDoesNothing bool) *stage {
	t.Helper()
	st := &stage{d: &fakeDocker{cs: project()}}
	st.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs, _ := st.d.Containers(r.Context(), "p")
		if strings.HasSuffix(r.URL.Path, "/ussp") && !cs["ussp-monitor"].Running() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"degraded":["monitor"],"dependencies":{"monitor":{"state":"down"}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"degraded":[],"dependencies":{"monitor":{"state":"up"}}}`)
	}))
	t.Cleanup(st.srv.Close)
	host, port, _ := net.SplitHostPort(st.srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	lab := &Lab{Project: "p", Network: "p_lab", Port: p, HTTP: st.srv.Client(),
		Hosts: map[string]string{"USSP_HOST": host, "CISP_HOST": host}}
	m := &Matrix{Probe: ProbeCfg{EveryS: 0.2, TimeoutS: 1, PreflightS: 2},
		Systems: []SystemCfg{{Name: "ussp", Host: "USSP_HOST", Path: "/r/ussp"}, {Name: "cisp", Host: "CISP_HOST", Path: "/r/cisp"}}}
	st.h = &harness{m: m, lab: lab, docker: st.d, out: io.Discard, status: map[string]*StatusLog{}}
	st.h.shell = func(_ context.Context, domain, action string, args []string) ScriptRun {
		sr := ScriptRun{Command: domain + " " + action + " " + strings.Join(args, " "), Start: time.Now().UTC()}
		if !scriptDoesNothing {
			switch action {
			case "inject":
				st.kills.Add(1)
				st.d.set(args[0], func(c *Container) { c.Status = "exited" })
			case "restore":
				st.starts.Add(1)
				st.d.set(args[0], func(c *Container) { c.Status = "running"; c.StartedAt = c.StartedAt.Add(time.Hour) })
			}
		}
		sr.Output = []string{"chaos: " + action + " done"} // a script that claims success either way
		sr.End = time.Now().UTC()
		return sr
	}
	return st
}

func monitorRow() Row {
	return Row{ID: "ussp-monitor", Domain: "hotpath", Args: []string{"ussp-monitor"}, System: "ussp", HoldS: 1, RecoverWithinS: 2,
		During: []Expect{
			{System: StringList{"ussp"}, Ready: readyLost, WithinS: 1, Why: "the fake says so"},
			{System: StringList{sysOthers}, Ready: readyAlways, Why: "others unaffected"},
		},
		After: []Expect{{System: StringList{sysAll}, Recovered: true, WithinS: 2, Why: "back"}}}
}

func TestARowWhoseFaultHappenedPasses(t *testing.T) {
	st := newStage(t, false)
	rr := st.h.runRow(context.Background(), monitorRow())
	if rr.Verdict != verdictPass {
		t.Fatalf("%s: %v", rr.Verdict, rr.Failures)
	}
	if st.kills.Load() != 1 || st.starts.Load() != 1 || !rr.Fault.Observed || !rr.Fault.Restored {
		t.Fatalf("kills %d starts %d fault %+v", st.kills.Load(), st.starts.Load(), rr.Fault)
	}
	if rr.Expect[0].AfterS["ussp"] > 1 {
		t.Fatalf("readiness lost late: %+v", rr.Expect[0])
	}
}

// The same row, the same claims of success from the script, but nothing
// happened to the container: the row fails on the fault check, and the
// expectations that depend on the fault fail with it.
func TestARowWhoseFaultNeverHappenedFails(t *testing.T) {
	st := newStage(t, true)
	rr := st.h.runRow(context.Background(), monitorRow())
	if rr.Verdict != verdictFail {
		t.Fatalf("a row with no fault passed: %+v", rr)
	}
	joined := strings.Join(rr.Failures, "\n")
	if !strings.Contains(joined, "fault: never observed in place") {
		t.Fatalf("the failure must name the missing fault:\n%s", joined)
	}
	if rr.Fault.Observed || rr.Fault.Restored {
		t.Fatalf("%+v", rr.Fault)
	}
}

func TestARowIsNotRunOnAStackThatIsNotReady(t *testing.T) {
	st := newStage(t, false)
	st.d.set("ussp-monitor", func(c *Container) { c.Status = "exited" }) // the ussp answers 503 before any fault
	rr := st.h.runRow(context.Background(), monitorRow())
	if rr.Verdict != verdictNotRun || st.kills.Load() != 0 {
		t.Fatalf("verdict %s, kills %d: %v", rr.Verdict, st.kills.Load(), rr.Failures)
	}
}

func TestARowWithAMissingTargetInjectsNothing(t *testing.T) {
	st := newStage(t, false)
	r := monitorRow()
	r.Args = []string{"ussp-ghost"}
	rr := st.h.runRow(context.Background(), r)
	if rr.Verdict != verdictNotRun || st.kills.Load() != 0 {
		t.Fatalf("verdict %s, kills %d", rr.Verdict, st.kills.Load())
	}
}

func TestTheResultIsWrittenAndReadable(t *testing.T) {
	st := newStage(t, false)
	rr := st.h.runRow(context.Background(), monitorRow())
	res := &RunResult{Format: ResultFormat, Run: "t", Rows: []*RowResult{rr}, Verdict: verdictPass}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "samples"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := res.write(dir); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, rr.SamplesFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var samples []Sample
	if err := json.NewDecoder(zr).Decode(&samples); err != nil || len(samples) != rr.Samples || rr.Samples == 0 {
		t.Fatalf("%d samples read of %d: %v", len(samples), rr.Samples, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "chaos.json"))
	if err != nil || !strings.Contains(string(b), `"format": "chaos-result/v1"`) {
		t.Fatalf("chaos.json: %v", err)
	}
	txt, err := os.ReadFile(filepath.Join(dir, "chaos.txt"))
	if err != nil || !strings.Contains(string(txt), "ussp-monitor") || !strings.Contains(string(txt), "fault seen") {
		t.Fatalf("chaos.txt: %q %v", txt, err)
	}
}

func TestTheSessionRefresherRunsAndRecordsBothWays(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	bash, _ := exec.LookPath("bash")
	for _, c := range []struct {
		cmd  string
		code int
	}{{"echo refreshed", 0}, {"echo no; exit 3", 3}} {
		r := &sessionRefresher{cmd: c.cmd, every: 20 * time.Millisecond, bash: bash, log: func(string, ...any) {}}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		done := make(chan struct{})
		go func() { r.run(ctx); close(done) }()
		ok := eventually(2*time.Second, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.Runs) > 0 })
		cancel()
		<-done
		if !ok || r.Runs[0].ExitCode != c.code {
			t.Fatalf("%q: %+v", c.cmd, r.Runs)
		}
		if _, err := json.Marshal(r); err != nil {
			t.Fatal(err)
		}
	}
}

// eventually polls f until it holds or d passes.
func eventually(d time.Duration, f func() bool) bool {
	deadline := time.Now().Add(d)
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	for !f() {
		if time.Now().After(deadline) {
			return false
		}
		<-tk.C
	}
	return true
}

// With a settle time the row keeps sampling after its claims are met,
// until the restore plus the settle; without one it stops as soon as
// they are.
func TestARowSettlesForTheReAlertBound(t *testing.T) {
	st := newStage(t, false)
	st.h.settle = 1500 * time.Millisecond
	rr := st.h.runRow(context.Background(), monitorRow())
	if rr.Verdict != verdictPass || rr.Ended.Sub(rr.Restored) < 1500*time.Millisecond {
		t.Fatalf("%s, ended %v after the restore", rr.Verdict, rr.Ended.Sub(rr.Restored))
	}
	st2 := newStage(t, false)
	rr2 := st2.h.runRow(context.Background(), monitorRow())
	if rr2.Ended.Sub(rr2.Restored) >= 1500*time.Millisecond {
		t.Fatalf("no settle, still sampled %v after the restore", rr2.Ended.Sub(rr2.Restored))
	}
}

func TestAFailedRefreshIsRetriedSooner(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	// A period of 500 ms: without the retry at most three runs fit in
	// the 1.5 s below; with a 20 ms retry after each failure, many more.
	r := &sessionRefresher{cmd: "exit 2", every: 500 * time.Millisecond, retry: 20 * time.Millisecond, bash: bash, log: func(string, ...any) {}}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	go r.run(ctx)
	r2 := func() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.Runs) }
	if !eventually(1400*time.Millisecond, func() bool { return r2() >= 5 }) {
		t.Fatalf("a failed refresh was not retried: %d run(s)", r2())
	}
}

func TestASucceedingRefreshKeepsItsPeriod(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	r := &sessionRefresher{cmd: "true", every: 500 * time.Millisecond, retry: 20 * time.Millisecond, bash: bash, log: func(string, ...any) {}}
	ctx, cancel := context.WithTimeout(context.Background(), 1400*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { r.run(ctx); close(done) }()
	<-done
	if n := len(r.Runs); n < 1 || n > 3 {
		t.Fatalf("%d runs in 1.4 s at a 500 ms period", n)
	}
}

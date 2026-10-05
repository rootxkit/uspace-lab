package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// ResultFormat names chaos.json's shape.
const ResultFormat = "chaos-result/v1"

// Exit statuses.
const (
	exitPass     = 0
	exitFail     = 1
	exitUsage    = 2
	exitNotReady = 3
)

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: chaos check|run|watch|skew [flags] (scripts/chaos/main.go)")
		return exitUsage
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "watch":
		return cmdWatch(args[1:])
	case "skew":
		return cmdSkew(args[1:])
	}
	fmt.Fprintln(os.Stderr, "chaos: unknown command", args[0])
	return exitUsage
}

func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	matrix := fs.String("matrix", "scripts/chaos/matrix.yaml", "the matrix")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	m, err := LoadMatrix(*matrix)
	if err != nil {
		fmt.Println("FAIL", err)
		return exitFail
	}
	for i := range m.Rows {
		r := &m.Rows[i]
		if r.Domain != "clock" {
			if _, err := os.Stat(filepath.Join(filepath.Dir(*matrix), r.Domain+".sh")); err != nil {
				fmt.Printf("FAIL row %s: no script for domain %s\n", r.ID, r.Domain)
				return exitFail
			}
		}
	}
	fmt.Printf("ok   %s: %d rows, %d systems, background %s; holds total %.0f s, background hold %.0f s\n",
		*matrix, len(m.Rows), len(m.Systems), firstNonEmpty(m.Background.Scenario, "none"), totalHold(m.Rows), backgroundHoldS(m.Background, m.Rows))
	return exitPass
}

func totalHold(rows []Row) float64 {
	var t float64
	for i := range rows {
		t += rows[i].HoldS
	}
	return t
}

// common flags of run, watch and skew.
type common struct {
	matrix, env, targets, project string
}

func (c *common) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.matrix, "matrix", "scripts/chaos/matrix.yaml", "the matrix")
	fs.StringVar(&c.env, "env", "deploy/demo.env", "the demo env file (hosts, port, state directory)")
	fs.StringVar(&c.targets, "targets", "targets/local-demo.yaml", "the targets file demo-seed wrote (console sessions, receiver keys)")
	fs.StringVar(&c.project, "project", "", "the compose project (default CHAOS_PROJECT, COMPOSE_PROJECT_NAME, uspace-demo)")
}

// siteLab is the sitl.env the targets file names (the origin the
// background flies from and the console viewport).
func siteLab(targets string) (*scenario.Lab, error) {
	tg, err := runner.LoadTargets(targets)
	if err != nil {
		return nil, err
	}
	p := tg.Lab
	if p == "" {
		return nil, fmt.Errorf("chaos: %s names no lab (sitl.env)", targets)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(targets), p)
	}
	return scenario.LoadLab(p)
}

func startStatus(ctx context.Context, h *harness, targets string, site *scenario.Lab) error {
	streams, err := statusStreams(targets, site, h.lab.HTTP)
	if err != nil {
		return err
	}
	for _, s := range streams {
		l := &StatusLog{System: s.system}
		h.status[s.system] = l
		go s.run(ctx, h.lab.HTTP, l)
	}
	return nil
}

//nolint:gocyclo // the run's ordered steps, each checked
func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c common
	c.bind(fs)
	rows := fs.String("rows", "", "comma-separated row ids (default every row)")
	out := fs.String("out", "", "results directory (default results/<run>-chaos)")
	runID := fs.String("run", "", "run id (default the UTC start time)")
	noBG := fs.Bool("no-background", false, "run without the background scenario (alerts are then not judged and the run cannot pass)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	m, err := LoadMatrix(c.matrix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	sel, err := m.Select(*rows)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	lab, err := LoadLab(c.env, c.project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	// The runner, the simulators and the oauth client use the default
	// client: the lab transport becomes the default for this process.
	http.DefaultTransport = lab.Transport
	bash, err := exec.LookPath("bash")
	if err != nil {
		fmt.Fprintln(os.Stderr, "chaos: bash is required for scripts/chaos/*.sh")
		return exitUsage
	}
	if *runID == "" {
		*runID = time.Now().UTC().Format("20060102T150405Z")
	}
	if *out == "" {
		*out = filepath.Join("results", *runID+"-chaos")
	}
	if err := os.MkdirAll(filepath.Join(*out, "samples"), 0o750); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	h := &harness{m: m, lab: lab, docker: cliDocker{bin: "docker"}, scripts: filepath.Dir(c.matrix), bash: bash, out: os.Stdout, status: map[string]*StatusLog{}}
	h.shell = h.runScript
	res := &RunResult{Format: ResultFormat, Run: *runID, StartedAt: time.Now().UTC(), Project: lab.Project, Systems: h.systemNames(), Pending: m.Pending}
	res.Matrix.Path, res.Matrix.Digest = filepath.ToSlash(c.matrix), fileDigest(c.matrix)
	res.Host.OS = runtime.GOOS + "/" + runtime.GOARCH
	res.Commits = labCommits()

	site, err := siteLab(c.targets)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if err := startStatus(ctx, h, c.targets, site); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	for i := range sel {
		if sel[i].Domain == "clock" {
			if h.skewRig, err = newSkewRig(c.targets, site, m.Background.Scenario); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return exitUsage
			}
		}
	}
	base, cs, err := h.preflight(ctx)
	res.Images = images(cs)
	if err != nil {
		h.say("the stack is not ready, nothing injected: %v", err)
		res.Verdict = verdictFail
		res.Failures = append(res.Failures, "preflight: "+err.Error())
		res.EndedAt = time.Now().UTC()
		_ = res.write(*out)
		return exitNotReady
	}
	h.say("stack ready: %s", readyLine(base))

	var bg *BackgroundResult
	var bgDone chan *runner.Result
	var watch *alertWatch
	var bgErr error
	var holdEnd time.Time
	if !*noBG && m.Background.Scenario != "" {
		holdS := backgroundHoldS(m.Background, sel)
		bg = &BackgroundResult{Scenario: m.Background.Scenario, HoldS: holdS}
		res.Background = bg
		path, err := writeBackground(m.Background.Scenario, m.Background.HoldStep, holdS, *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		watch = newAlertWatch(m.Background)
		bgDone = make(chan *runner.Result, 1)
		started := time.Now()
		logf, _ := os.Create(filepath.Join(*out, "background.log"))
		bgCtx, bgCancel := context.WithCancel(ctx)
		defer bgCancel()
		go func() {
			r, err := runner.Run(bgCtx, runner.Options{Scenario: path, Targets: c.targets, Vehicles: runner.VehiclesSynthetic,
				OutDir: *out, Run: *runID, Repo: runner.RepoRoot("."), OnEvent: watch.see,
				Log: slog.New(slog.NewTextHandler(logWriter(logf), nil))})
			bgErr = err
			bgDone <- r
		}()
		h.say("background %s started, hold %.0f s; waiting up to %.0f s for its %s in %s", m.Background.Scenario, holdS,
			m.Background.RaisedWithinS, m.Background.Kind, strings.Join(m.Background.Systems, ", "))
		select {
		case <-watch.ready:
			bg.RaisedAt = watch.snapshot()
			h.say("background alert raised in every system: %v", bg.RaisedAt)
		case <-time.After(time.Duration(m.Background.RaisedWithinS * float64(time.Second))):
			bg.RaisedAt = watch.snapshot()
			bg.Error = fmt.Sprintf("the standing %s was not raised in every system within %.0f s (raised: %v): no row can judge alerts, none is run", m.Background.Kind, m.Background.RaisedWithinS, bg.RaisedAt)
		case r := <-bgDone:
			summarise(r, bg)
			bg.Error = fmt.Sprintf("the background run ended before its alert was raised: %v", bgErr)
			bgDone = nil
		}
		// The hold step starts after the climb and the transit; the
		// background holds for holdS from about then.
		holdEnd = started.Add(time.Duration(holdS * float64(time.Second)))
		if bg.Error != "" {
			h.say("%s", bg.Error)
			res.Failures = append(res.Failures, "background: "+bg.Error)
			// No row runs: end the background now rather than hold for
			// nothing (its result still says what it saw).
			bgCancel()
		}
	} else {
		res.Failures = append(res.Failures, "no background: the alert claims were not judged")
	}

	var windows []window
	if bg == nil || bg.Error == "" {
		rowsStart := time.Now()
		for i := range sel {
			r := &sel[i]
			if bg != nil && time.Since(rowsStart).Seconds()+r.HoldS+r.RecoverWithinS+rowOverheadS > bg.HoldS-m.Background.RaisedWithinS {
				rr := &RowResult{ID: r.ID, Domain: r.Domain, Cite: r.Cite, Claim: r.Claim, Verdict: verdictNotRun,
					Failures: []string{"not run: the background's hold would end during the row"}}
				res.Rows = append(res.Rows, rr)
				continue
			}
			rr := h.runRow(ctx, *r)
			res.Rows = append(res.Rows, rr)
			// A row's window reaches the re-alert bound after its restore,
			// or its end if later; the next row's injection caps it.
			to := rr.Ended
			if re := rr.Restored.Add(time.Duration(m.Background.RealertWithinS * float64(time.Second))); re.After(to) {
				to = re
			}
			if n := len(windows); n > 0 && windows[n-1].to.After(rr.Injected) {
				windows[n-1].to = rr.Injected
			}
			windows = append(windows, window{row: r.ID, from: rr.Injected, to: to, restored: rr.Restored, modes: r.Alerts})
			h.say("row %s: %s%s", r.ID, strings.ToUpper(rr.Verdict), failureTail(rr.Failures))
			if ctx.Err() != nil {
				break
			}
		}
	}

	if bgDone != nil {
		h.say("rows done; waiting for the background to leave the zone and end (hold ends about %s)", holdEnd.UTC().Format(time.RFC3339))
		r := <-bgDone
		summarise(r, bg)
		if bgErr != nil {
			bg.Error = bgErr.Error()
		}
		if r != nil {
			if p, err := r.Write(*out); err == nil {
				bg.ResultFile = filepath.ToSlash(p)
			} else {
				res.Failures = append(res.Failures, "background result not written: "+err.Error())
			}
		}
		evs, dropped := watch.all()
		bg.Events, bg.EventsDropped = evs, dropped
		if r != nil {
			bg.Findings = judgeAlerts(evs, m.Background, firstClearBound(r), r.EndedAt, windows)
		}
	}
	if bg != nil {
		for _, f := range bg.Findings {
			placed := false
			for _, rr := range res.Rows {
				if rr.ID == f.Row {
					rr.AlertFindings = append(rr.AlertFindings, f)
					if !f.Allowed {
						rr.Failures = append(rr.Failures, fmt.Sprintf("alert: %s %s %s at +%.1fs into the row%s", f.System, strings.ReplaceAll(f.What, "_", " "), f.AlertID, f.OffsetS, reasonTail(f.Reason)))
						rr.Verdict = verdictFail
					}
					placed = true
				}
			}
			if !placed && !f.Allowed {
				res.Failures = append(res.Failures, fmt.Sprintf("alert: %s %s %s at %s, between rows%s", f.System, strings.ReplaceAll(f.What, "_", " "), f.AlertID, f.At.Format(time.RFC3339), reasonTail(f.Reason)))
			}
		}
		// The runner's own verdict is recorded, not judged: it pairs an
		// alert's first raise with its first clear, so an allowed stale
		// clear and re-raise reads to it as a missed clear and a false
		// alert. judgeAlerts judges the same scenario on every event.
		if bg.Verdict != "" && bg.Verdict != "PASS" {
			bg.Note = fmt.Sprintf("the runner's verdict %s (missed %d, false %d) is recorded, not judged: the rows' alert findings and the exit check judge every raise and clear", bg.Verdict, bg.Missed, bg.False)
		}
	}
	res.EndedAt = time.Now().UTC()
	res.Verdict = verdictPass
	for _, rr := range res.Rows {
		if rr.Verdict != verdictPass {
			res.Verdict = verdictFail
		}
	}
	if len(res.Failures) > 0 {
		res.Verdict = verdictFail
	}
	if err := res.write(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	fmt.Print(res.Summary())
	if res.Verdict != verdictPass {
		return exitFail
	}
	return exitPass
}

// firstClearBound is when the background aircraft left the zone (the
// "out" step's start): a clear before it is the alert lost; a clear
// after it is the expected one.
func firstClearBound(r *runner.Result) time.Time {
	if t, ok := r.Marks["out.start"]; ok {
		return t
	}
	return r.EndedAt
}

func logWriter(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

func failureTail(fs []string) string {
	if len(fs) == 0 {
		return ""
	}
	return ": " + fs[0] + map[bool]string{true: fmt.Sprintf(" (and %d more)", len(fs)-1), false: ""}[len(fs) > 1]
}

func reasonTail(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

func readyLine(s Sample) string {
	names := make([]string, 0, len(s.Ready))
	for n := range s.Ready {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+" "+describe(s.Ready[n]))
	}
	return strings.Join(parts, "; ")
}

func images(cs map[string]Container) map[string]string {
	out := map[string]string{}
	for svc := range cs {
		if c := cs[svc]; c.Image != "" {
			out[svc] = c.Image + " (" + c.ImageID + ")"
		}
	}
	return out
}

func fileDigest(p string) string {
	b, err := os.ReadFile(p) //nolint:gosec // the operator's matrix
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Commits are the code that ran (E-05).
type Commits struct {
	Lab      string `json:"lab"`
	LabDirty bool   `json:"lab_dirty"`
	Core     string `json:"core"`
}

func labCommits() Commits {
	var c Commits
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		c.Lab = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output(); err == nil {
		c.LabDirty = len(strings.TrimSpace(string(out))) > 0
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/rootxkit/uspace-core" {
				c.Core = d.Version
			}
		}
	}
	return c
}

// RunResult is chaos.json.
type RunResult struct {
	Format    string    `json:"format"`
	Run       string    `json:"run"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Host      struct {
		OS string `json:"os"`
	} `json:"host"`
	Commits Commits `json:"commits"`
	Matrix  struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
	} `json:"matrix"`
	// Pending are the matrix's figures that are the spec's defaults and
	// wait for GCAA (L-Q4).
	Pending    []string          `json:"pending,omitempty"`
	Project    string            `json:"project"`
	Systems    []string          `json:"systems"`
	Images     map[string]string `json:"images,omitempty"`
	Background *BackgroundResult `json:"background,omitempty"`
	Rows       []*RowResult      `json:"rows"`
	Verdict    string            `json:"verdict"`
	Failures   []string          `json:"failures,omitempty"`
}

func (r *RunResult) write(dir string) error {
	for _, rr := range r.Rows {
		if len(rr.samples) == 0 {
			continue
		}
		// Every sample of the row, gzipped: about 300 kB a minute of
		// JSON shrinks tenfold, so a whole run stays about a megabyte.
		name := filepath.Join("samples", rr.ID+".json.gz")
		if err := writeGzipJSON(filepath.Join(dir, name), rr.samples); err != nil {
			return err
		}
		rr.SamplesFile = filepath.ToSlash(name)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "chaos.json"), b, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "chaos.txt"), []byte(r.Summary()), 0o600)
}

func writeGzipJSON(path string, v any) error {
	f, err := os.Create(path) //nolint:gosec // the run's own directory
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Summary is the observed matrix in words (E-04: what was seen).
func (r *RunResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "chaos run %s: %s (%s to %s, project %s, lab %s, core %s)\n", r.Run, strings.ToUpper(r.Verdict),
		r.StartedAt.Format(time.RFC3339), r.EndedAt.Format(time.RFC3339), r.Project, short(r.Commits.Lab), r.Commits.Core)
	if bg := r.Background; bg != nil {
		fmt.Fprintf(&b, "background %s: verdict %s, missed %d, false %d, %d finding(s)", bg.Scenario, firstNonEmpty(bg.Verdict, "none"), bg.Missed, bg.False, len(bg.Findings))
		if bg.Error != "" {
			fmt.Fprintf(&b, "; %s", bg.Error)
		}
		b.WriteString("\n")
	}
	for _, rr := range r.Rows {
		fmt.Fprintf(&b, "%-28s %-8s", rr.ID, strings.ToUpper(rr.Verdict))
		if rr.Fault != nil {
			fh := "fault NOT observed"
			if rr.Fault.Observed {
				fh = fmt.Sprintf("fault seen +%.1fs, held %d/%d", deref(rr.Fault.FirstHeldAfterS), rr.Fault.HeldSamples, rr.Fault.DuringSamples)
			}
			if rr.Fault.Restored {
				fh += fmt.Sprintf(", restored +%.1fs", deref(rr.Fault.RestoredAfterS))
			}
			b.WriteString(" " + fh)
		}
		for _, s := range rr.Skew {
			fmt.Fprintf(&b, " [clock %+gs: left %+.1fs, %s %d]", s.SkewS, s.Measured, s.Got, s.Code)
		}
		b.WriteString("\n")
		for _, f := range rr.Failures {
			fmt.Fprintf(&b, "    - %s\n", f)
		}
	}
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "run: %s\n", f)
	}
	return b.String()
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func cmdWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	var c common
	c.bind(fs)
	dur := fs.Duration("for", 60*time.Second, "how long to watch")
	raw := fs.Bool("raw", false, "print each system's last console/status/v1 body as sent")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	m, err := LoadMatrix(c.matrix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	lab, err := LoadLab(c.env, c.project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	site, err := siteLab(c.targets)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	http.DefaultTransport = lab.Transport
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	h := &harness{m: m, lab: lab, docker: cliDocker{bin: "docker"}, out: os.Stdout, status: map[string]*StatusLog{}}
	if err := startStatus(ctx, h, c.targets, site); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	seen := map[string]int{}
	for {
		s, _ := h.sample(ctx, phaseBefore, nil, nil)
		h.say("%s", readyLine(s))
		for sys, l := range h.status {
			fs, es := l.Between(time.Time{}, time.Now().Add(time.Second))
			for _, e := range es[min(seen[sys+"e"], len(es)):] {
				h.say("  %s stream %s %s", sys, e.What, e.Error)
			}
			seen[sys+"e"] = len(es)
			if n := len(fs); n > seen[sys] {
				f := fs[n-1]
				b, _ := json.Marshal(f)
				h.say("  %s status (%d new): %s", sys, n-seen[sys], b)
				if *raw {
					l.mu.Lock()
					h.say("  %s raw: %s", sys, l.LastRaw)
					l.mu.Unlock()
				}
				seen[sys] = n
			}
		}
		if !h.tick(ctx, t) {
			return exitPass
		}
	}
}

func cmdSkew(args []string) int {
	fs := flag.NewFlagSet("skew", flag.ContinueOnError)
	var c common
	c.bind(fs)
	skew := fs.Float64("skew-s", 45, "the receiver clock's offset from this host's, seconds")
	want := fs.String("want", skewRefused, "accepted or refused")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	m, err := LoadMatrix(c.matrix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	lab, err := LoadLab(c.env, c.project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	site, err := siteLab(c.targets)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	rig, err := newSkewRig(c.targets, site, m.Background.Scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	res := runSkew(context.Background(), rig, lab.Transport, Skew{SkewS: *skew, Want: *want}, skewedClock)
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if !res.Pass {
		return exitFail
	}
	return exitPass
}

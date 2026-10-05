// Command loadgen is the load generator (WP-L8, spec 05 §7;
// docs/RUNBOOKS/load.md).
//
//	loadgen check [--paths load/paths.yaml] [--criteria load/criteria.yaml] load/tiers/*.yaml
//	loadgen run --tier load/tiers/ci.yaml [--targets targets/reference.yaml] \
//	    [--paths load/paths.yaml] [--criteria load/criteria.yaml] \
//	    [--out results/load] [--run <id>] [--duration-s N] [--host NAME] [--heap-profile FILE]
//	loadgen render results/load/<run>/report.json
//
// run drives the tier's operators, receivers and consoles against the
// target, measures every 05 §7 row it can, and writes
// <out>/<run>/report.json and report.md. It prints the verdict and the
// failures, never a figure it did not observe (E-04).
//
// Exit status: 0 the run passed; 1 it failed (a required check not
// measured, a measured check failed, or nothing measured at all); 2
// usage, configuration or setup error (nothing was run).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/load"
	"github.com/rootxkit/uspace-lab/internal/runner"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: loadgen check TIER... | loadgen run --tier FILE [flags] | loadgen render REPORT")
		return 2
	}
	switch args[0] {
	case "check":
		return check(args[1:])
	case "run":
		return runTier(args[1:])
	case "render":
		return render(args[1:])
	}
	fmt.Fprintln(os.Stderr, "loadgen: unknown command", args[0])
	return 2
}

func check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	paths := fs.String("paths", "load/paths.yaml", "the paths file")
	criteria := fs.String("criteria", "load/criteria.yaml", "the criteria file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "loadgen check: name the tier files")
		return 2
	}
	bad := 0
	for _, t := range fs.Args() {
		f, err := load.LoadFiles(t, *paths, *criteria)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", t, err)
			bad++
			continue
		}
		fmt.Printf("ok   %s: tier %s, %d aircraft, %.0f s, %d checks required\n", t, f.Tier.Tier, f.Tier.Operators.Aircraft, f.Tier.DurationS, len(f.Tier.Required))
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func runTier(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	tier := fs.String("tier", "", "the tier file (load/tiers/<name>.yaml)")
	targets := fs.String("targets", "targets/reference.yaml", "the targets file")
	paths := fs.String("paths", "load/paths.yaml", "the paths file")
	criteria := fs.String("criteria", "load/criteria.yaml", "the criteria file")
	out := fs.String("out", filepath.Join("results", "load"), "where the run's directory is made")
	runID := fs.String("run", "", "run id (default the UTC start time and the tier)")
	duration := fs.Float64("duration-s", 0, "override the tier's duration (recorded as a scale factor)")
	host := fs.String("host", "", "name the host and its size, recorded in the report (L-Q1)")
	heap := fs.String("heap-profile", "", "write a heap profile at the end of the drain (pprof)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tier == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "loadgen run: --tier is required and takes no other argument")
		return 2
	}
	files, err := load.LoadFiles(*tier, *paths, *criteria)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}
	id := *runID
	if id == "" {
		id = time.Now().UTC().Format("20060102T150405Z") + "-" + files.Tier.Tier
	}
	dir := filepath.Join(*out, id)
	// Refuse before anything starts: a run never overwrites a record.
	if _, err := os.Stat(dir); err == nil {
		fmt.Fprintf(os.Stderr, "loadgen: %s exists; choose another --run\n", dir)
		return 2
	}
	log := cli.Logger("loadgen")
	ctx, stop := cli.SignalContext()
	defer stop()
	rep, err := load.Run(ctx, load.Options{Files: files, TargetsPath: *targets, Run: id, DurationS: *duration,
		RepoRoot: runner.RepoRoot("."), HostName: *host, HeapProfile: *heap, Log: log})
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}
	if err := rep.Write(dir); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}
	summary(rep, dir)
	if ctx.Err() != nil || rep.Verdict != load.VerdictPass {
		return 1
	}
	return 0
}

func summary(rep *load.Report, dir string) {
	fmt.Printf("load %s: tier %s, %s, %d checks measured, %d not measured\n", rep.Run, rep.Tier.Tier, rep.Verdict, rep.Measured, rep.Unmeasured)
	for _, row := range rep.Rows {
		fmt.Printf("  %-22s %s\n", row.ID, row.Result)
	}
	for _, f := range rep.Failures {
		fmt.Printf("  FAIL %s\n", f)
	}
	fmt.Printf("  report: %s\n", filepath.Join(dir, "report.json"))
}

func render(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "loadgen render: name one report.json")
		return 2
	}
	rep, err := load.ReadReport(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 2
	}
	fmt.Print(rep.Markdown())
	return 0
}

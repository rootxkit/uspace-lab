// Command scenario runs the lab's scenarios (scenarios/README.md).
//
//	scenario check scenarios/*.yaml
//	scenario run --targets targets/reference.yaml [--vehicles synthetic|sitl] \
//	    [--lab sim/sitl.env] [--out results/<run>] scenarios/sc-01.yaml ...
//
// run starts the vehicles (SITL through sim/fly.py, or the synthetic
// stand-in), the simulators with the scenario's knobs and the collectors
// of each system's console stream, executes the timeline, judges what
// was observed and writes results/<run>/<scenario>.json. It prints what
// it saw, not what it expected (E-04).
//
// Exit status: 0 every scenario passed; 1 one failed; 2 usage or
// configuration error; 3 a scenario is not runnable against the targets
// (for example a USSP-only kind against the reference target).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: scenario check FILE... | scenario run --targets FILE [flags] FILE...")
		return 2
	}
	switch args[0] {
	case "check":
		return check(args[1:])
	case "run":
		return runScenarios(args[1:])
	}
	fmt.Fprintln(os.Stderr, "scenario: unknown command", args[0])
	return 2
}

func check(files []string) int {
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "scenario check: name the scenario files")
		return 2
	}
	bad := 0
	for _, f := range files {
		s, err := scenario.Load(f)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", f, err)
			bad++
			continue
		}
		fmt.Printf("ok   %s: %s (%d aircraft, %d steps, %d expectations, reference %v, policy_version %d)\n",
			f, s.ID, len(s.Aircraft), len(s.Steps), len(s.Expect), s.Reference, s.PolicyDoc.PolicyVersion)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func runScenarios(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	targets := fs.String("targets", "", "the targets file")
	vehicles := fs.String("vehicles", runner.VehiclesSynthetic, "synthetic or sitl")
	lab := fs.String("lab", "", "the sitl.env (default: the targets file's)")
	out := fs.String("out", "", "results directory (default results/<run>)")
	runID := fs.String("run", "", "run id (default the UTC start time)")
	prepare := fs.Duration("prepare", 0, "time from start to t0")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *targets == "" || fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "scenario run: --targets and at least one scenario file are required")
		return 2
	}
	id := *runID
	if id == "" {
		id = time.Now().UTC().Format("20060102T150405Z") + "-" + *vehicles
	}
	dir := *out
	if dir == "" {
		dir = filepath.Join("results", id)
	}
	log := cli.Logger("scenario")
	ctx, stop := cli.SignalContext()
	defer stop()
	status := 0
	for _, f := range fs.Args() {
		res, err := runner.Run(ctx, runner.Options{Scenario: f, Targets: *targets, Vehicles: *vehicles, Lab: *lab,
			OutDir: dir, Run: id, Prepare: *prepare, Repo: runner.RepoRoot("."), Log: log})
		if err != nil {
			fmt.Fprintf(os.Stderr, "scenario %s: %v\n", f, err)
			if errors.Is(err, runner.ErrNotRunnable) {
				if status == 0 {
					status = 3
				}
				continue
			}
			return 2
		}
		path, err := res.Write(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "scenario:", err)
			return 2
		}
		fmt.Print(res.Summary())
		fmt.Printf("  result: %s\n", path)
		if res.Verdict != "pass" {
			status = 1
		}
		if ctx.Err() != nil {
			return 1
		}
	}
	_ = context.Background
	return status
}

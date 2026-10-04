// Command demo-seed puts the demo's data into the four systems through
// their public APIs and writes the targets file of a systems run
// (internal/seed; docs/RUNBOOKS/demo.md):
//
//	demo-seed --env deploy/demo.env --lab sim/sitl.env \
//	  --steps receivers,registry,uspace,ussp,ansp,sessions \
//	  --targets targets/local-demo.yaml --secrets secrets/demo \
//	  --geoid ../../_geoids/egm2008-2_5.pgm scenarios/*.yaml
//
//	demo-seed ... --steps zones --zones sc-03-zone-entry-exit scenarios/*.yaml
//	demo-seed ... --steps sessions scenarios/*.yaml      (before each run)
//
// The scenarios given are the ones the stack is seeded for: their
// aircraft (registry rows, serial bindings), their receivers and, with
// --zones, the zones of the named ones.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/seed"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		env     = flag.String("env", "deploy/demo.env", "the demo env file (hosts, port, state directory)")
		lab     = flag.String("lab", "sim/sitl.env", "the sitl.env the fleet flies from")
		steps   = flag.String("steps", strings.Join(seed.AllSteps, ","), "steps to run, comma-separated: "+strings.Join(append(append([]string{}, seed.AllSteps...), seed.StepZones), ","))
		zones   = flag.String("zones", "", "ids of the scenarios whose zones the zones step publishes, comma-separated")
		targets = flag.String("targets", "targets/local-demo.yaml", "the targets file the sessions step writes")
		secrets = flag.String("secrets", "secrets/demo", "the directory of the secret files it names")
		geoid   = flag.String("geoid", "", "targets.geoid: the grid the systems use (R-16)")
		uspace  = flag.String("uspace-id", "LABUSP1", "identifier of the U-space airspace (at most 7 characters)")
		half    = flag.Float64("uspace-half-side-m", 10000, "half side of the U-space airspace square, metres")
		north   = flag.Float64("uspace-north-m", 0, "the U-space airspace square's centre, metres north of the origin")
		east    = flag.Float64("uspace-east-m", 0, "the U-space airspace square's centre, metres east of the origin")
		away    = flag.Float64("zones-away-north-m", 0, "with --steps zones: publish the zones this far north of their place (takes them off the area)")
		adsb    = flag.String("adsb-listen", "0.0.0.0:18092", "where the runner serves sim-adsb for the ANSP's adapter")
		// The SITL commands are the operator's (docs/RUNBOOKS/demo.md
		// gives them); without them the targets file has no sitl block
		// and only synthetic vehicles run. Only the scenario runner
		// starts the harness (INV-01, sim/tests/test_send_guard.py).
		reader  = flag.String("sitl-reader", "", "targets.sitl.reader_cmd, as a shell line (placeholders {sysid} {out_port} {max_s})")
		fly     = flag.String("sitl-fly", "", "targets.sitl.fly_cmd, as a shell line (placeholder {fly_port})")
		timeout = flag.Duration("timeout", 10*time.Minute, "bound on the whole seed")
	)
	flag.Parse()
	log := cli.Logger("demo-seed")
	var ss []*scenario.Scenario
	for _, f := range flag.Args() {
		s, err := scenario.Load(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "demo-seed:", err)
			return 2
		}
		ss = append(ss, s)
	}
	if len(ss) == 0 {
		fmt.Fprintln(os.Stderr, "demo-seed: name the scenarios the stack is seeded for")
		return 2
	}
	o := seed.Options{
		Env: *env, Lab: *lab, Scenarios: ss, Steps: split(*steps), ZoneScenarios: split(*zones),
		TargetsOut: *targets, SecretsDir: *secrets, Geoid: *geoid, USpaceID: *uspace, USpaceHalfSideM: *half,
		USpaceCenter: scenario.Offset{NorthM: *north, EastM: *east}, ZonesAwayNorthM: *away,
		ADSBListen: *adsb, SITLReader: shellWords(*reader), SITLFly: shellWords(*fly),
		Log: func(f string, a ...any) { log.Info(fmt.Sprintf(f, a...)) },
	}
	ctx, stop := cli.SignalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := seed.Run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "demo-seed:", err)
		return 1
	}
	log.Info("done", "steps", *steps)
	return 0
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// shellWords splits a command line on spaces outside double quotes (the
// SITL commands are "bash -c \"...\"").
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, started := false, false
	for _, r := range s {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case r == ' ' && !quoted:
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

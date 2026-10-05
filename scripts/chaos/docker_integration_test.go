//go:build chaosdocker

package main

// The fault checks against real docker, both ways, on the WP-L2 lab
// stack (make dss-up): .github/workflows/chaos.yml runs
//
//	CHAOS_PROJECT=uspace-lab go test -tags chaosdocker -run Docker ./scripts/chaos/
//
// Each case runs the domain script for real, then asks docker (and the
// packet filter) whether the fault is in place, before, during and after:
// a check that cannot tell a fault from its absence fails here.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func dockerLab(t *testing.T) (*Lab, Docker) {
	t.Helper()
	p := os.Getenv("CHAOS_PROJECT")
	if p == "" {
		t.Fatal("set CHAOS_PROJECT to the project make dss-up started (uspace-lab)")
	}
	// One state directory per test: a partition's restore reads what its
	// injection wrote.
	t.Setenv("CHAOS_STATE", t.TempDir())
	return &Lab{Project: p, Network: p + "_lab"}, cliDocker{bin: "docker"}
}

func script(t *testing.T, l *Lab, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{name}, args...)...)
	cmd.Env = append(os.Environ(), "CHAOS_PROJECT="+l.Project, "CHAOS_NETWORK="+l.Network)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// within polls f every half second for up to d: the bounded wait of a
// check on a state docker reaches asynchronously.
func within(d time.Duration, f func() bool) bool {
	deadline := time.Now().Add(d)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		if f() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		<-t.C
	}
}

func containers(t *testing.T, d Docker, l *Lab) map[string]Container {
	t.Helper()
	cs, err := d.Containers(context.Background(), l.Project)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestDockerStopIsSeenAndItsRestartToo(t *testing.T) {
	l, d := dockerLab(t)
	ctx := context.Background()
	before := containers(t, d, l)
	spec, err := faultSpec(&Row{ID: "issuer", Domain: "issuer", Args: []string{"lab-issuer"}}, l, before)
	if err != nil {
		t.Fatal(err)
	}
	if held, why := spec.heldNow(ctx, d, before); held {
		t.Fatalf("held before the fault: %s", why)
	}
	out := script(t, l, "issuer.sh", "inject", "lab-issuer")
	if !strings.Contains(out, "stopped lab-issuer") {
		t.Fatalf("%s", out)
	}
	if held, why := spec.heldNow(ctx, d, containers(t, d, l)); !held {
		t.Fatalf("not held after the stop: %s", why)
	}
	if ok, _ := spec.restoredNow(ctx, d, containers(t, d, l), before); ok {
		t.Fatal("restored while stopped")
	}
	script(t, l, "issuer.sh", "restore", "lab-issuer")
	if ok, why := spec.restoredNow(ctx, d, containers(t, d, l), before); !ok {
		t.Fatalf("not restored after the start: %s", why)
	}
	if held, _ := spec.heldNow(ctx, d, containers(t, d, l)); held {
		t.Fatal("still held after the restore")
	}
}

func TestDockerPauseIsSeenBothWays(t *testing.T) {
	l, d := dockerLab(t)
	ctx := context.Background()
	before := containers(t, d, l)
	spec, err := faultSpec(&Row{ID: "stall", Domain: "stall", Args: []string{"dss-crdb"}}, l, before)
	if err != nil {
		t.Fatal(err)
	}
	if held, _ := spec.heldNow(ctx, d, before); held {
		t.Fatal("held before the pause")
	}
	script(t, l, "stall.sh", "inject", "dss-crdb")
	if held, why := spec.heldNow(ctx, d, containers(t, d, l)); !held {
		t.Fatalf("pause not seen: %s", why)
	}
	script(t, l, "stall.sh", "restore", "dss-crdb")
	if ok, why := spec.restoredNow(ctx, d, containers(t, d, l), before); !ok {
		t.Fatalf("resume not seen: %s", why)
	}
}

// The partition of the DSS's datastore (system "dss": its running
// containers are dss-crdb): the filter seen in place and dropping the DSS
// server's packets, then gone.
func TestDockerPartitionFiltersAndDrops(t *testing.T) {
	l, d := dockerLab(t)
	ctx := context.Background()
	before := containers(t, d, l)
	spec, err := faultSpec(&Row{ID: "part", Domain: "partition", Args: []string{"dss"}}, l, before)
	if err != nil {
		t.Fatal(err)
	}
	if held, why := spec.heldNow(ctx, d, before); held {
		t.Fatalf("held before the partition: %s", why)
	}
	script(t, l, "partition.sh", "inject", "dss")
	defer func() {
		if ok, _ := spec.restoredNow(ctx, d, containers(t, d, l), before); !ok {
			script(t, l, "partition.sh", "restore", "dss")
		}
	}()
	if !within(30*time.Second, func() bool {
		held, _ := spec.heldNow(ctx, d, containers(t, d, l))
		return held && len(spec.partitionFailures()) == 0
	}) {
		t.Fatalf("partition not seen dropping: %v", spec.partitionFailures())
	}
	script(t, l, "partition.sh", "restore", "dss")
	if ok, why := spec.restoredNow(ctx, d, containers(t, d, l), before); !ok {
		t.Fatalf("heal not seen: %s", why)
	}
}

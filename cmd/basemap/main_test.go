package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestPlanPrintsTheExtracts(t *testing.T) {
	work := t.TempDir()
	code, out, errs := runCmd("plan", "--regions", "../../basemap/regions.yaml", "--work", work)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want the country and z13..z15:\n%s", len(lines), out)
	}
	if lines[0] != "country\t0\t12\t39.9,41,46.8,43.6\t-" {
		t.Errorf("country line %q", lines[0])
	}
	f := strings.Split(lines[3], "\t")
	if len(f) != 5 || f[0] != "cities-z15" || f[1] != "15" || f[2] != "15" || f[3] != "-" {
		t.Fatalf("z15 line %q", lines[3])
	}
	gj, err := os.ReadFile(f[4])
	if err != nil || !strings.Contains(string(gj), `"MultiPolygon"`) {
		t.Errorf("region file %s: %s %v", f[4], gj, err)
	}

	code, out, _ = runCmd("plan", "--regions", "../../basemap/regions.yaml", "--work", work, "--profile", "storybook")
	if code != 0 || out != "storybook\t0\t14\t44.77,41.68,44.83,41.73\t-\n" {
		t.Errorf("storybook plan exit %d: %q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"nope"},
		{"plan", "--work", "x"},
		{"plan", "--regions", "../../basemap/regions.yaml", "--work", "x", "--profile", "country"},
		{"source", "--tool", "noequals"},
	} {
		if code, _, _ := runCmd(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, errs := runCmd("plan", "--regions", "missing.yaml", "--work", t.TempDir()); code != 1 || !strings.Contains(errs, "missing.yaml") {
		t.Errorf("missing regions file: exit %d %s", code, errs)
	}
}

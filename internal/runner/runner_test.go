package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/scenario"
)

const root = "../.."

func opts(t *testing.T, scenario string) Options {
	t.Helper()
	return Options{
		Scenario: filepath.Join(root, "scenarios", scenario),
		Targets:  filepath.Join(root, "targets", "reference.yaml"),
		Vehicles: VehiclesSynthetic,
		OutDir:   t.TempDir(),
		Run:      "test",
		Prepare:  3 * time.Second,
		Repo:     root,
	}
}

// E-02 for the runner: a deliberately wrong expectation fails, and the
// failure lists what was observed (the alerts seen instead), not what was
// expected. The never-list fires on a real raise.
func TestWrongExpectationFailsReadably(t *testing.T) {
	if testing.Short() {
		t.Skip("a 60 s scenario")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	o := opts(t, "selftest-wrong-expectation.yaml")
	res, err := Run(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "fail" || res.Outcome.Missed != 1 {
		t.Fatalf("verdict %s, missed %d:\n%s", res.Verdict, res.Outcome.Missed, res.Summary())
	}
	miss := res.Outcome.Expectations[0]
	if miss.OK || len(miss.Seen) == 0 {
		t.Fatalf("the miss does not list what was seen: %+v", miss)
	}
	if len(res.Outcome.Never) != 1 || len(res.Outcome.Never[0].Violations) == 0 {
		t.Fatal("the never-list did not fire on the b-c conflict")
	}
	sum := res.Summary()
	for _, want := range []string{"FAIL wrong-a-and-far-b", "observed instead of wrong-a-and-far-b", "proximity", "never ussp proximity for b with c"} {
		if !strings.Contains(sum, want) {
			t.Errorf("the summary lacks %q:\n%s", want, sum)
		}
	}
	// The ledgers still balance: a failed verdict is about the alerts.
	for _, l := range res.Operators {
		if !l.Balanced || l.Sent == 0 {
			t.Errorf("ledger %+v", l)
		}
	}
	path, err := res.Write(o.OutDir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `"verdict": "fail"`) {
		t.Fatalf("result file: %v", err)
	}
}

// The presence twin: a scenario whose expectations hold passes.
func TestCorrectExpectationPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("a 90 s scenario")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res, err := Run(ctx, opts(t, "sc-22-missing-inputs-visible.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "pass" {
		t.Fatalf("%s", res.Summary())
	}
	b, err := os.ReadFile(opts(t, "sc-22-missing-inputs-visible.yaml").Scenario)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if want := "sha256:" + hex.EncodeToString(sum[:]); res.ScenarioDigest != want {
		t.Fatalf("scenario digest %q, want %q", res.ScenarioDigest, want)
	}
	if res.Commits.Core == "" || res.PolicyVersion != 1 || res.Mode.Targets != ModeReference || !strings.Contains(res.Evidence, "never evidence") {
		t.Fatalf("result metadata %+v", res.Commits)
	}
}

func TestNotRunnableIsRefusedBeforeAnythingStarts(t *testing.T) {
	_, err := Run(context.Background(), opts(t, "ussp-wp10-conformance.yaml"))
	if !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("got %v", err)
	}
	o := opts(t, "kt4-baseline.yaml")
	o.Vehicles = VehiclesSITL
	if _, err := Run(context.Background(), o); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("SITL without sitl commands: %v", err)
	}
}

// The reference target never raises unregistered (it has no registry), so
// a scenario may ask it never to, and is refused when it expects a raise.
func TestReferenceUnregisteredIsNeverOnly(t *testing.T) {
	tg := &Targets{Mode: ModeReference}
	m := scenario.Matcher{System: scenario.SystemAuthority, Kind: "unregistered"}
	never := &scenario.Scenario{ID: "never", Reference: true, Never: []scenario.Matcher{m}}
	if err := CheckRunnable(never, tg); err != nil {
		t.Fatalf("a never on unregistered: %v", err)
	}
	expect := &scenario.Scenario{ID: "expect", Reference: true, Expect: []scenario.Expect{{Name: "raised", Matcher: m}}}
	if err := CheckRunnable(expect, tg); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("an expected unregistered: %v", err)
	}
	zone := &scenario.Scenario{ID: "zone", Reference: true, Expect: []scenario.Expect{{Name: "raised",
		Matcher: scenario.Matcher{System: scenario.SystemAuthority, Kind: "zone_incursion"}}}}
	if err := CheckRunnable(zone, tg); err != nil {
		t.Fatalf("an expected zone incursion: %v", err)
	}
}

func TestExpandAndMAC(t *testing.T) {
	got := expand([]string{"x", "--sysid {sysid} --out udp:127.0.0.1:{out_port}"}, map[string]string{"sysid": "3", "out_port": "14562"})
	if got[1] != "--sysid 3 --out udp:127.0.0.1:14562" {
		t.Fatal(got)
	}
	a, b := macOf("s", "a"), macOf("s", "b")
	if a == b || !strings.HasPrefix(a, "02:") || macOf("s", "a") != a {
		t.Fatalf("%s %s", a, b)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/internal/scenario"
)

func TestCommittedMatrixValidates(t *testing.T) {
	m, err := LoadMatrix("matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Every domain of 05 §6 the brief names has a row (WP-L9 "What to
	// build"), and every row's script exists.
	want := []string{"adapter", "api", "hotpath", "stall", "nats", "timescale", "postgres", "partition",
		"system", "dss", "ansp-feed", "issuer", "clock", "stack"}
	have := map[string]bool{}
	for _, r := range m.Rows {
		have[r.Domain] = true
		if r.Domain == "clock" {
			continue
		}
		if _, err := os.Stat(r.Domain + ".sh"); err != nil {
			t.Errorf("row %s: no script %s.sh", r.ID, r.Domain)
		}
	}
	for _, d := range want {
		if !have[d] {
			t.Errorf("no row for domain %s", d)
		}
	}
	if len(m.Pending) == 0 {
		t.Error("the matrix lists no figure pending GCAA")
	}
	for _, p := range m.Pending {
		if !strings.Contains(p, "pending GCAA") {
			t.Errorf("pending entry without the words 'pending GCAA': %q", p)
		}
	}
}

func TestBackgroundScenarioLoadsAndMatchesTheMatrix(t *testing.T) {
	m, err := LoadMatrix("matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scenario.Load(filepath.Join("..", "..", m.Background.Scenario))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range sc.Steps {
		if st.ID == m.Background.HoldStep && st.Do == scenario.DoHold {
			found = true
		}
	}
	if !found {
		t.Fatalf("background has no hold step %q", m.Background.HoldStep)
	}
	for _, sys := range m.Background.Systems {
		ok := false
		for _, e := range sc.Expect {
			if e.System == sys && e.Kind == m.Background.Kind && e.Subject == m.Background.Subject {
				ok = true
			}
		}
		if !ok {
			t.Errorf("background expects no %s %s on %s", sys, m.Background.Kind, m.Background.Subject)
		}
	}
}

// mutate loads the committed matrix, applies f and validates the result.
func mutate(t *testing.T, f func(m *Matrix)) error {
	t.Helper()
	b, err := os.ReadFile("matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var m Matrix
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	f(&m)
	return m.Validate()
}

func rowByDomain(m *Matrix, d string) *Row {
	for i := range m.Rows {
		if m.Rows[i].Domain == d {
			return &m.Rows[i]
		}
	}
	return nil
}

func TestMatrixRefusesWhatCannotBeJudged(t *testing.T) {
	cases := map[string]struct {
		f    func(m *Matrix)
		want string
	}{
		"unknown domain":       {func(m *Matrix) { m.Rows[0].Domain = "meteor" }, "unknown domain"},
		"wrong arity":          {func(m *Matrix) { m.Rows[0].Args = nil }, "takes 1 argument"},
		"shell in a name":      {func(m *Matrix) { m.Rows[0].Args = []string{"x;rm -rf /"} }, "not a valid name"},
		"no claim":             {func(m *Matrix) { m.Rows[0].Claim = "" }, "cite and claim"},
		"no why":               {func(m *Matrix) { m.Rows[0].During[0].Why = "" }, "why is required"},
		"unbounded hold":       {func(m *Matrix) { m.Rows[0].HoldS = 99999 }, "hold_s"},
		"no recovery judged":   {func(m *Matrix) { m.Rows[0].After = nil }, "after needs"},
		"unprobed system":      {func(m *Matrix) { m.Rows[0].During[0].System = StringList{"mars"} }, "not probed"},
		"two kinds in one":     {func(m *Matrix) { e := &m.Rows[0].During[0]; e.Check = "nats"; e.In = []string{"down"} }, "exactly one"},
		"eventually unbounded": {func(m *Matrix) { m.Rows[0].After[0].WithinS = 0 }, "within_s"},
		"lost after the fault": {func(m *Matrix) {
			m.Rows[0].After[0] = Expect{System: StringList{"ussp"}, Ready: readyLost, WithinS: 5, Why: "x"}
		}, "belongs to during"},
		"duplicate id": {func(m *Matrix) { m.Rows[1].ID = m.Rows[0].ID }, "unique"},
		"stale_ok without why": {func(m *Matrix) {
			m.Rows[0].Alerts = map[string]string{"ussp": alertsStaleOK}
			m.Rows[0].AlertsReason = ""
		}, "alerts_reason"},
		"alerts of no system":  {func(m *Matrix) { m.Rows[0].Alerts = map[string]string{"cisp": alertsKept} }, "not a system of the background"},
		"unknown alert mode":   {func(m *Matrix) { m.Rows[0].Alerts = map[string]string{"ussp": "maybe"} }, "kept or stale_ok"},
		"wrong format":         {func(m *Matrix) { m.Format = "chaos-matrix/v0" }, "format"},
		"probe too fast":       {func(m *Matrix) { m.Probe.EveryS = 0.01 }, "every_s"},
		"host not in demo.env": {func(m *Matrix) { m.Systems[0].Host = "EXAMPLE_HOST" }, "host"},
		"no stale reasons":     {func(m *Matrix) { m.Background.StaleReasons = nil }, "stale_reasons"},
		"skew outside clock":   {func(m *Matrix) { m.Rows[0].Skew = []Skew{{SkewS: 5, Want: skewAccepted}} }, "clock domain only"},
		"clock without refusal": {func(m *Matrix) {
			rowByDomain(m, "clock").Skew = []Skew{{SkewS: -10, Want: skewAccepted}, {SkewS: -5, Want: skewAccepted}}
		}, "accepted and a refused"},
		"clock one case": {func(m *Matrix) { rowByDomain(m, "clock").Skew = []Skew{{SkewS: 45, Want: skewRefused}} }, "presence and absence"},
		"zero skew":      {func(m *Matrix) { rowByDomain(m, "clock").Skew[0].SkewS = 0 }, "non-zero"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := mutate(t, c.f)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
		})
	}
	// And the committed matrix, untouched, passes the same path.
	if err := mutate(t, func(*Matrix) {}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectRows(t *testing.T) {
	m, err := LoadMatrix("matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	all, err := m.Select("")
	if err != nil || len(all) != len(m.Rows) {
		t.Fatalf("empty selection: %d rows, %v", len(all), err)
	}
	two, err := m.Select(m.Rows[1].ID + "," + m.Rows[0].ID)
	if err != nil || len(two) != 2 || two[0].ID != m.Rows[0].ID {
		t.Fatalf("selection keeps matrix order: %+v %v", two, err)
	}
	if _, err := m.Select("no-such-row"); err == nil {
		t.Fatal("an unknown row was selected")
	}
}

func TestExpectSystemsExpand(t *testing.T) {
	all := []string{"authority", "cisp", "ussp"}
	r := &Row{System: "ussp"}
	if got := (&Expect{System: StringList{sysOthers}}).systems(all, r); strings.Join(got, ",") != "authority,cisp" {
		t.Fatalf("others: %v", got)
	}
	if got := (&Expect{System: StringList{sysAll}}).systems(all, r); len(got) != 3 {
		t.Fatalf("all: %v", got)
	}
	if got := (&Expect{System: StringList{"ussp", "ussp"}}).systems(all, r); len(got) != 1 {
		t.Fatalf("duplicates kept: %v", got)
	}
}

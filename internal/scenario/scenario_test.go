package scenario

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/core"
)

const scenariosDir = "../../scenarios"

func TestEveryCommittedScenarioLoads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(scenariosDir, "*.yaml"))
	if err != nil || len(files) < 6 {
		t.Fatalf("%d scenario files, %v", len(files), err)
	}
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		s, err := Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if _, err := Compile(s, lab, time.Unix(1_790_000_000, 0)); err != nil {
			t.Errorf("%s: compile: %v", f, err)
		}
	}
}

func load(t *testing.T, name string) *Scenario {
	t.Helper()
	s, err := Load(filepath.Join(scenariosDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRefusals(t *testing.T) {
	base := load(t, "sc-03-zone-entry-exit.yaml")
	mutate := func(f func(s *Scenario)) error {
		b, _ := yaml.Marshal(base)
		var s Scenario
		if err := yaml.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		f(&s)
		return s.Validate()
	}
	if err := mutate(func(*Scenario) {}); err != nil {
		t.Fatalf("the unchanged scenario is refused: %v", err)
	}
	cases := map[string]func(s *Scenario){
		"format":            func(s *Scenario) { s.Format = "scenario/v2" },
		"id":                func(s *Scenario) { s.ID = "Not A Slug" },
		"systems":           func(s *Scenario) { s.Systems = []string{"tower"} },
		"duration":          func(s *Scenario) { s.DurationS = 0 },
		"serial too long":   func(s *Scenario) { s.Aircraft[0].Serial = strings.Repeat("A", 21) },
		"unknown receiver":  func(s *Scenario) { s.Aircraft[0].Receivers = []string{"nope"} },
		"zone type":         func(s *Scenario) { s.Zones[0].Type = "USPACE" },
		"zone ref":          func(s *Scenario) { s.Zones[0].Lower.Ref = "FL" },
		"unknown mark":      func(s *Scenario) { s.Expect[0].Raise.After = "nowhere" },
		"window":            func(s *Scenario) { s.Expect[0].Raise.MinS, s.Expect[0].Raise.MaxS = 5, 1 },
		"unknown aircraft":  func(s *Scenario) { s.Expect[0].Aircraft = "zz" },
		"step aircraft":     func(s *Scenario) { s.Steps[0].Aircraft = []string{"zz"} },
		"knob without time": func(s *Scenario) { s.Steps = append(s.Steps, Step{Do: DoKnob, Knob: &Knob{Receiver: "down"}}) },
		"verb":              func(s *Scenario) { s.Steps[0].Do = "loop" },
		"receiver system":   func(s *Scenario) { s.Receivers[0].System = "ussp" },
		"operator system":   func(s *Scenario) { s.Aircraft[0].Operator.System = "authority" },
		"intent band":       func(s *Scenario) { s.Aircraft[0].Operator.Intent.AltUpperRelM = -50 },
		"landing after the end": func(s *Scenario) {
			at := s.DurationS - 10 // from 30 m SITL needs about 39 s
			s.Steps = append(s.Steps, Step{Do: DoLand, Aircraft: []string{"a"}, AtS: &at})
		},
		"serial not CTA for C2": func(s *Scenario) {
			s.Aircraft[0].Serial = "LABSC03A0001" // no length character: the USSP refuses it for C2
		},
		"expect intent of no": func(s *Scenario) { s.ExpectIntents = []IntentExpect{{Aircraft: "zz", Decision: "authorised"}} },
	}
	for name, f := range cases {
		if err := mutate(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// An aircraft launched outside its own intent is refused at compile
// time (it would be nonconforming from its first airborne sample), and
// one parked there is not.
func TestTakeoffOutsideTheIntentIsRefused(t *testing.T) {
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	s := load(t, "ussp-wp10-conformance.yaml")
	if _, err := Compile(s, lab, time.Unix(1_790_000_000, 0)); err != nil {
		t.Fatalf("B parked at home: %v", err)
	}
	s.Steps[0].Aircraft = append(s.Steps[0].Aircraft, "b") // B takes off at home, 600 m from its intent
	if _, err := Compile(s, lab, time.Unix(1_790_000_000, 0)); err == nil || !strings.Contains(err.Error(), "aircraft b takes off") {
		t.Fatalf("got %v", err)
	}
}

// The landing estimate against what SITL measured (LandS's comment).
func TestLandSCoversTheMeasuredLandings(t *testing.T) {
	for alt, measured := range map[float64]float64{20: 30.6, 30: 37.5, 60: 57.5} {
		if got := LandS(alt); got < measured || got > measured+6 {
			t.Errorf("LandS(%.0f) = %.1f, measured %.1f", alt, got, measured)
		}
	}
}

func TestUnknownMembersAreRefused(t *testing.T) {
	dir := t.TempDir()
	b, _ := os.ReadFile(filepath.Join(scenariosDir, "sc-22-missing-inputs-visible.yaml"))
	bad := strings.Replace(string(b), "tail_s: 3", "tail_s: 3\nsurprise: true", 1)
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	pol, _ := os.ReadFile(filepath.Join(scenariosDir, "policy", "demo.yaml"))
	_ = os.MkdirAll(filepath.Join(dir, "policy"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, "policy", "demo.yaml"), pol, 0o600)
	if _, err := Load(filepath.Join(dir, "x.yaml")); err == nil {
		t.Fatal("an unknown member was accepted")
	}
}

func TestPolicy(t *testing.T) {
	p, err := LoadPolicy(filepath.Join(scenariosDir, "policy", "demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The figures of docs/PLAN.md §7.1 L-Q4, as written there.
	if p.CPA.DHorizontalMinM != 60 || p.CPA.DVerticalMinM != 20 || p.CPA.TCPAMaxS != 60 || p.LostLinkS != 15 ||
		p.TelemetryLostS != 5 || p.HeightLimitAGLM != 120 || p.Deviation.HM != 50 || p.Deviation.VM != 15 {
		t.Fatalf("%+v", p)
	}
	c := p.AlertingConfig(true, true)
	if c.Policy.DHorizontalMinM != 60 || c.ZonePolicy.MaxHeightAGLM == nil || *c.ZonePolicy.MaxHeightAGLM != 120 || !c.SkipConflicts {
		t.Fatalf("%+v", c)
	}
	bad := *p
	bad.CPA.DHorizontalMinM = 0
	if bad.Validate() == nil {
		t.Fatal("a zero minimum was accepted (E-15)")
	}
	bad = *p
	bad.Monitor.ConditionalSeverity = string(core.SeverityCritical)
	if bad.Validate() == nil {
		t.Fatal("a critical CONDITIONAL zone was accepted (Z-10)")
	}
}

// The homes are run_sitl.sh's: these longitudes are what the script
// printed for three instances on 2026-10-03 (sim/out/instances.tsv).
func TestHomesAreRunSITLs(t *testing.T) {
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for sysid, want := range map[int]float64{1: 44.8271000, 2: 44.8274009, 3: 44.8277017} {
		if got := lab.Home(sysid).LonDeg; math.Abs(got-want) > 1e-9 {
			t.Errorf("sysid %d: lon %.7f, run_sitl.sh printed %.7f", sysid, got, want)
		}
	}
	if lab.OutPort(3) != 14562 || lab.FlyPort(3) != 14662 {
		t.Fatalf("ports %d %d", lab.OutPort(3), lab.FlyPort(3))
	}
}

func TestLabRefusesNoHome(t *testing.T) {
	if _, err := labFrom(map[string]string{}, "x"); err == nil {
		t.Fatal("a lab without SITL_HOME was accepted (INV-03)")
	}
	if _, err := labFrom(map[string]string{"SITL_HOME": "91,44,600,0"}, "x"); err == nil {
		t.Fatal("an out-of-range home was accepted")
	}
}

func TestCompilePlansAndZones(t *testing.T) {
	s := load(t, "sc-03-zone-entry-exit.yaml")
	lab, _ := LoadLab("../../sim/sitl.env.example")
	c, err := Compile(s, lab, time.Unix(1_790_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Plans["a"]
	if len(p.Steps) != 6 || p.Steps[0].Action != "arm" || p.Steps[1].Action != "takeoff" || p.Steps[2].AltRelM != 30 {
		t.Fatalf("plan %+v", p.Steps)
	}
	if len(c.Zones) != 1 || c.Zones[0].Type != core.ZoneReqAuthorization {
		t.Fatalf("zones %+v", c.Zones)
	}
	// The zone is 300 m north of the origin: a point there is inside.
	in := lab.At(Offset{NorthM: 300})
	if !c.Zones[0].BBox.Contains(in) {
		t.Fatal("the zone's box does not hold its centre")
	}
}

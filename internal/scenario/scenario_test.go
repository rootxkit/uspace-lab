package scenario

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/ed269"
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
// A nearby operator expected to be told must fly (the USSP tells only
// flying neighbours); found with B parked in a systems run of
// ussp-wp10-conformance, told nothing three times.
func TestNearbyOperatorMustFly(t *testing.T) {
	s := load(t, "ussp-wp10-conformance.yaml")
	if err := s.Validate(); err != nil {
		t.Fatalf("B flying: %v", err)
	}
	s.Steps[0].Aircraft = []string{"a"}
	s.Steps = append(s.Steps[:1], s.Steps[2:]...) // no b-hover either
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "never takes off") {
		t.Fatalf("B parked: %v", err)
	}
}

func TestTakeoffOutsideTheIntentIsRefused(t *testing.T) {
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	s := load(t, "ussp-wp10-conformance.yaml")
	if _, err := Compile(s, lab, time.Unix(1_790_000_000, 0)); err != nil {
		t.Fatalf("B over its home: %v", err)
	}
	s.Aircraft[1].Operator.Intent.Center = Offset{NorthM: 600, EastM: 50} // B takes off 600 m from its intent
	if _, err := Compile(s, lab, time.Unix(1_790_000_000, 0)); err == nil || !strings.Contains(err.Error(), "aircraft b takes off") {
		t.Fatalf("got %v", err)
	}
}

// sitlEndS is a conservative end time of an aircraft's last flight step
// as SITL flies them one after the other: 30 s to arm (the EKF settles
// after a restart), 1 m/s up, a goto at its speed plus 10 s, a hold for
// its time, a landing by LandS. A step never starts before its at_s.
func sitlEndS(s *Scenario, aircraft, untilID string) float64 {
	t, alt := 0.0, 0.0
	at := func(n, e float64) (float64, float64) { return n, e }
	pn, pe := 0.0, 0.0
	for _, st := range s.Steps {
		mine := false
		for _, a := range st.Aircraft {
			mine = mine || a == aircraft
		}
		if !mine {
			continue
		}
		if st.AtS != nil && *st.AtS > t {
			t = *st.AtS
		}
		switch st.Do {
		case DoTakeoff:
			t += 30 + st.AltRelM/1.0
			alt = st.AltRelM
		case DoGoto:
			n, e := at(st.To.NorthM, st.To.EastM)
			d := math.Hypot(n-pn, e-pe)
			if st.AltRelM > 0 {
				d = math.Max(d, math.Abs(st.AltRelM-alt))
				alt = st.AltRelM
			}
			t += d/math.Max(st.SpeedMS, 1) + 10
			pn, pe = n, e
		case DoHold:
			t += st.ForS
		case DoLand:
			t += LandS(alt)
		}
		if st.ID == untilID {
			return t
		}
	}
	return t
}

// ussp-wp10-conformance cuts A's stream to raise lost_link once A is back
// inside its volume; the first systems run cut it at +285 s while A was
// still outside (it reached back-long at +320 s), so the long
// nonconformance could not clear and the lost link fell inside it.
func TestWP10CutsTheStreamAfterTheReturn(t *testing.T) {
	s := load(t, "ussp-wp10-conformance.yaml")
	back := sitlEndS(s, "a", "back-long")
	for _, st := range s.Steps {
		if st.ID == "cut" && *st.AtS < back {
			t.Fatalf("the cut at %.0f s comes before A is back (about %.0f s)", *st.AtS, back)
		}
	}
}

// ussp-wp10-conformance's returns re-enter A's circle inside the clear
// windows: the run with the circle shrunk to 15 m and the excursion left
// at 330 m cleared 44 s after the return started, outside max_s 40.
func TestWP10ReturnsReenterInsideTheClearWindow(t *testing.T) {
	s := load(t, "ussp-wp10-conformance.yaml")
	in := s.Aircraft[0].Operator.Intent
	steps := map[string]Step{}
	for _, st := range s.Steps {
		if st.ID != "" {
			steps[st.ID] = st
		}
	}
	for _, e := range s.Expect {
		if e.Kind != "nonconformance" || e.Clear == nil {
			continue
		}
		back := steps[strings.TrimSuffix(e.Clear.After, ".start")]
		if back.Do != DoGoto {
			continue
		}
		// From the excursion point the return re-enters the circle after
		// (distance - radius) at its speed; clear_after_s and a 10 s
		// margin (acceleration, evaluation period) on top.
		var from *Offset
		for _, st := range s.Steps {
			if st.ID == back.ID {
				break
			}
			if st.Do == DoGoto && slices.Contains(st.Aircraft, "a") && st.To != nil {
				from = st.To
			}
		}
		d := math.Hypot(from.NorthM-in.Center.NorthM, from.EastM-in.Center.EastM) - in.RadiusM
		if need := d/back.SpeedMS + s.PolicyDoc.Monitor.ClearAfterS + 10; need > e.Clear.MaxS {
			t.Errorf("%s: the return re-enters after about %.0f s, after max_s %.0f", e.Name, need, e.Clear.MaxS)
		}
	}
}

// Two landings the systems run of 20261004 did not see confirmed: in
// ansp-inv02-manned the hover after a climb SITL confirms about 41 s
// after t0 must end by the landing's at_s; in sc-21 B lands where the
// ground is up to 27 m below home (sim/README.md), so up to 57 m down.
func TestLandingsTheSystemsRunSawLate(t *testing.T) {
	const climbConfirmedS = 41.5 // measured, both runs
	inv := load(t, "ansp-inv02-manned.yaml")
	var hover, land float64
	for _, st := range inv.Steps {
		switch {
		case st.ID == "hover":
			hover = st.ForS
		case st.Do == DoLand:
			land = *st.AtS
		}
	}
	if climbConfirmedS+hover > land+2 {
		t.Errorf("ansp-inv02-manned: the hover ends at about %.0f s, after the landing's at_s %.0f", climbConfirmedS+hover, land)
	}
	// authority-sc08-rid-switch had the same pattern (an 80 s hover
	// before a landing at 100 s), seen in its systems run.
	sc08 := load(t, "authority-sc08-rid-switch.yaml")
	hover, land = 0, 0
	for _, st := range sc08.Steps {
		switch {
		case st.ID == "hover":
			hover = st.ForS
		case st.Do == DoLand:
			land = *st.AtS
		}
	}
	if climbConfirmedS+hover > land+2 {
		t.Errorf("authority-sc08-rid-switch: the hover ends at about %.0f s, after the landing's at_s %.0f", climbConfirmedS+hover, land)
	}
	// B's landing there was not confirmed within 60 s; 75 s leaves margin.
	sc21 := load(t, "sc-21-slow-to-hover.yaml")
	for _, st := range sc21.Steps {
		if st.Do == DoLand && sc21.DurationS-*st.AtS < 75 {
			t.Errorf("sc-21: %.0f s for B's landing from the lower ground, under 75 s", sc21.DurationS-*st.AtS)
		}
	}
}

// SC-22 asks a deployment for what it can lack and must show. The
// authority publishes its switch state at start, so
// source_control_unknown can never show on a deployment (results/
// 20261004-systems finding 6); without terrain an AGL limit cannot be
// judged, and the warning must say so (Z-09).
func TestSC22AsksForWhatADeploymentCanLack(t *testing.T) {
	s := load(t, "sc-22-missing-inputs-visible.yaml")
	agl := map[string]bool{}
	for _, z := range s.Zones {
		if z.Lower.Ref == "AGL" || z.Upper.Ref == "AGL" {
			agl[z.ID] = true
		}
	}
	var registry, notJudged bool
	for _, e := range s.Expect {
		if e.Subject == "source_control_unknown" {
			t.Errorf("%s expects source_control_unknown, which a deployment never shows", e.Name)
		}
		registry = registry || (e.System == SystemAuthority && e.Kind == "degraded" && e.Subject == "registry_projection_absent")
		notJudged = notJudged || (e.System == SystemAuthority && e.Kind == "zone_incursion" && agl[e.Subject] &&
			e.Detail["limit_not_judged"] == true && e.Clear != nil)
	}
	if !registry {
		t.Error("no expectation that the absent registry projection is shown")
	}
	if !notJudged {
		t.Error("no zone_incursion on an AGL zone expecting limit_not_judged, raised and cleared")
	}
	// Identification is null where there is no projection: nothing may
	// call the aircraft unregistered.
	if !slices.ContainsFunc(s.Never, func(m Matcher) bool {
		return m.System == SystemAuthority && m.Kind == "unregistered" && (m.Aircraft == "" || m.Aircraft == "a")
	}) {
		t.Error("no never on an unregistered violation without a registry")
	}
}

// A scenario zone names a zone authority: ED-318 requires one, and
// uspace-authority refuses a zones import without it.
func TestZonesNameAZoneAuthority(t *testing.T) {
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	s := load(t, "sc-03-zone-entry-exit.yaml")
	gz, err := ED269Zone(s, lab, s.Zones[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(gz.ZoneAuthority) == 0 || gz.ZoneAuthority[0].Name == nil || gz.ZoneAuthority[0].Purpose == nil {
		t.Fatalf("zone authority %+v", gz.ZoneAuthority)
	}
	b, err := ed269.Export(&ed269.Document{Zones: []ed269.GeoZone{gz}, Wrapper: ed269.WrapperFeatures})
	if err != nil || !strings.Contains(string(b), `"zoneAuthority":[{`) {
		t.Fatalf("%v %s", err, b)
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

// The pairs a USSP owes (SC-01, SC-02, SC-21) each file their own
// volume, and every point either aircraft flies through, from its home
// on, lies inside its own: a USSP that deconflicts authorises only the
// first of two overlapping intents (results/20261004-systems finding 7),
// and an aircraft outside its volume is nonconforming.
func TestOwedPairsFlyInsideTheirOwnVolumes(t *testing.T) {
	lab, err := LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"sc-01-hover-inside-minima.yaml", "sc-02-head-on-and-short-return.yaml", "sc-21-slow-to-hover.yaml"} {
		s := load(t, f)
		a, b := s.AircraftByName("a"), s.AircraftByName("b")
		if a.Operator.Intent.Overlaps(b.Operator.Intent) {
			t.Errorf("%s: a's and b's intents overlap", f)
		}
		for _, ac := range []*Aircraft{a, b} {
			in := ac.Operator.Intent
			at := Offset{EastM: float64(lab.Index(ac.Sysid)) * lab.SpacingM}
			for _, st := range s.Steps {
				if st.Do != DoGoto || !slices.Contains(st.Aircraft, ac.Name) {
					continue
				}
				// Every 1 m of the straight leg (the union of boxes is not
				// convex).
				d := math.Hypot(st.To.NorthM-at.NorthM, st.To.EastM-at.EastM)
				for k := 0.0; k <= d; k++ {
					p := Offset{NorthM: at.NorthM + (st.To.NorthM-at.NorthM)*k/math.Max(d, 1), EastM: at.EastM + (st.To.EastM-at.EastM)*k/math.Max(d, 1)}
					if !in.Contains(p) {
						t.Errorf("%s: %s's leg to %s leaves its volume at %+v", f, ac.Name, st.ID, p)
						break
					}
				}
				at = *st.To
			}
		}
	}
}

// Overlap of circles and boxes; touching is not overlapping, and
// intents apart in height or time do not overlap.
func TestIntentOverlaps(t *testing.T) {
	band := func(in Intent) *Intent {
		in.AltLowerRelM, in.AltUpperRelM, in.StartsBeforeS, in.LastsS = -10, 100, 60, 3600
		return &in
	}
	circle := func(n, e, r float64) *Intent { return band(Intent{Center: Offset{NorthM: n, EastM: e}, RadiusM: r}) }
	box := func(s, n, w, e float64) *Intent {
		return band(Intent{Boxes: []Box{{SouthM: s, NorthM: n, WestM: w, EastM: e}}})
	}
	for name, c := range map[string]struct {
		a, b *Intent
		want bool
	}{
		"circles apart":        {circle(0, 0, 10), circle(0, 25, 10), false},
		"circles touching":     {circle(0, 0, 10), circle(0, 20, 10), false},
		"circles overlapping":  {circle(0, 0, 10), circle(0, 19, 10), true},
		"boxes touching":       {box(0, 10, 0, 10), box(0, 10, 10, 20), false},
		"boxes overlapping":    {box(0, 10, 0, 10), box(5, 15, 5, 15), true},
		"circle over a corner": {circle(0, 0, 10), box(7, 20, 7, 20), true},
		"circle over an edge":  {circle(0, 0, 10), box(-5, 5, 9, 20), true},
		"circle clear":         {circle(0, 0, 10), box(7.5, 20, 7.5, 20), false},
	} {
		if got := c.a.Overlaps(c.b); got != c.want || c.b.Overlaps(c.a) != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	high := circle(0, 0, 10)
	high.AltLowerRelM, high.AltUpperRelM = 100, 200
	if circle(0, 0, 10).Overlaps(high) {
		t.Error("bands that only touch overlapped")
	}
	later := circle(0, 0, 10)
	later.StartsBeforeS = -3540 // starts when the other ends
	if circle(0, 0, 10).Overlaps(later) {
		t.Error("windows that only touch overlapped")
	}
}

// A scenario a USSP owes is refused when two aircraft file overlapping
// intents, unless it expects the second refused (uspace-ussp WP-7's
// first come, first served).
func TestOverlappingIntentsAreRefusedUnlessExpected(t *testing.T) {
	s := load(t, "sc-01-hover-inside-minima.yaml")
	b := s.AircraftByName("b")
	shared := *s.AircraftByName("a").Operator.Intent
	b.Operator.Intent = &shared
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "overlaps aircraft a's") {
		t.Fatalf("one volume for both: %v", err)
	}
	s.ExpectIntents = []IntentExpect{{Aircraft: "a", Decision: "authorised"}, {Aircraft: "b", Decision: "rejected"}}
	if err := s.Validate(); err != nil {
		t.Fatalf("b expected rejected: %v", err)
	}
	if err := load(t, "ussp-wp7-authorisation.yaml").Validate(); err != nil {
		t.Fatalf("wp7: %v", err)
	}
}

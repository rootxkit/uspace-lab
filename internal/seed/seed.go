package seed

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/ed269"

	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// Steps of a seed run, in the order they run.
const (
	StepReceivers = "receivers" // the scenarios' Remote ID receivers (admin)
	StepRegistry  = "registry"  // operators and aircraft (registrar)
	StepUSpace    = "uspace"    // the U-space airspace (admin)
	StepUSSP      = "ussp"      // operators, clients, serials at the USSP
	StepANSP      = "ansp"      // the watch supervisor at the ANSP
	StepZones     = "zones"     // the zones of Options.ZoneScenarios
	StepSessions  = "sessions"  // console sessions for a run, and the targets file
)

// AllSteps is every step but zones, which a run asks for by scenario.
var AllSteps = []string{StepReceivers, StepRegistry, StepUSpace, StepUSSP, StepANSP, StepSessions}

// Options configures a seed run.
type Options struct {
	// Env is deploy/demo.env (hosts, port, state directory, images).
	Env string
	// Lab is the sitl.env the fleet flies from (origin, numbering).
	Lab       string
	Scenarios []*scenario.Scenario
	Steps     []string
	// ZoneScenarios are the ids of the scenarios whose zones StepZones
	// imports, approves and publishes.
	ZoneScenarios []string
	// TargetsOut is the targets file written by StepSessions;
	// SecretsDir the directory of the secret files it names.
	TargetsOut string
	SecretsDir string
	// Geoid is targets.geoid (the grid the systems use, R-16).
	Geoid string
	// USpaceID and USpaceHalfSideM are the U-space airspace: a square
	// about the origin.
	USpaceID        string
	USpaceHalfSideM float64
	// USpaceCenter is the square's centre, an offset from the origin
	// (zero: the origin). The USSP image of the suite has no DSS writer
	// (its WP-13), so an intent inside U-space airspace waits
	// pending_dss: the runs that need an authorised intent are flown
	// with the airspace moved off the area (docs/RUNBOOKS/demo.md).
	USpaceCenter scenario.Offset
	// ADSBListen is where the runner serves sim-adsb (feed manned-1),
	// reached by the ANSP's adapter; SITLReader and SITLFly the
	// runner's commands for a SITL vehicle (targets.sitl).
	ADSBListen string
	SITLReader []string
	SITLFly    []string
	Now        func() time.Time
	Log        func(format string, args ...any)
}

// Env reads a KEY=VALUE file (# comments), as compose's --env-file.
func Env(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own configuration
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, sc.Err()
}

// Operator clients of the targets file and the registration each one is.
var clientRegs = map[string]string{"op-a": "GEOLAB000001", "op-b": "GEOLAB000002"}

// Run seeds the systems.
//
//nolint:gocyclo // one ordered list of steps
func Run(ctx context.Context, o Options) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	env, err := Env(o.Env)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	deployDir := filepath.Dir(o.Env)
	rel := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(deployDir, p)
	}
	stateDir := rel(def(env["DEMO_STATE_DIR"], "./local-demo"))
	port, _ := strconv.Atoi(def(env["DEMO_HTTPS_PORT"], "443"))
	hosts := map[string]string{}
	for _, k := range []string{"AUTHORITY_HOST", "CISP_HOST", "USSP_HOST", "ANSP_HOST", "LAB_HOST"} {
		if env[k] == "" {
			return fmt.Errorf("seed: %s is not set in %s", k, o.Env)
		}
		hosts[k] = env[k]
	}
	c, err := NewClient(filepath.Join(stateDir, "ca", "ca.pem"), port,
		[]string{hosts["AUTHORITY_HOST"], hosts["CISP_HOST"], hosts["USSP_HOST"], hosts["ANSP_HOST"], hosts["LAB_HOST"]})
	if err != nil {
		return err
	}
	st, err := LoadState(filepath.Join(stateDir, "seed-state.json"))
	if err != nil {
		return err
	}
	// The bootstrap admins' passwords are the files the systems read.
	for sys, file := range map[string]string{"authority": "authority/admin.pw", "ansp": "ansp/admin.pw"} {
		b, err := os.ReadFile(filepath.Join(stateDir, file)) //nolint:gosec // the state directory
		if err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		st.Account(sys, "admin").Password = strings.TrimSpace(string(b))
	}
	lab, err := scenario.LoadLab(o.Lab)
	if err != nil {
		return err
	}
	auth := &Authority{C: c, Host: hosts["AUTHORITY_HOST"], St: st, Now: o.Now}
	ussp := &USSP{C: c, Host: hosts["USSP_HOST"], St: st, Wait: 90 * time.Second}
	ansp := &ANSP{C: c, Host: hosts["ANSP_HOST"], St: st, Now: o.Now}
	has := map[string]bool{}
	for _, s := range o.Steps {
		has[s] = true
	}
	var admin Session
	needAdmin := has[StepReceivers] || has[StepUSpace] || has[StepZones] || has[StepRegistry] || has[StepSessions]
	if needAdmin {
		if admin, err = auth.SignIn(ctx, "admin"); err != nil {
			return err
		}
		for user, roles := range map[string][]string{"lab-registrar": {"registrar"}, "lab-inspector": {"inspector"}} {
			if err := auth.EnsureUser(ctx, admin, user, roles); err != nil {
				return err
			}
		}
		o.Log("authority: admin signed in; lab-registrar, lab-inspector present")
	}

	if has[StepReceivers] {
		for _, rx := range receivers(o.Scenarios) {
			p := lab.At(rx.At)
			if _, err := auth.EnsureReceiver(ctx, admin, rx.ID, p.LatDeg, p.LonDeg); err != nil {
				return err
			}
			o.Log("authority: receiver %s registered, keys kept", rx.ID)
		}
	}

	if has[StepRegistry] {
		reg, err := auth.SignIn(ctx, "lab-registrar")
		if err != nil {
			return err
		}
		ops := map[string]string{}
		for _, n := range []string{"GEOLAB000001", "GEOLAB000002"} {
			id, err := auth.EnsureOperator(ctx, reg, RegistryOperator{Number: n, Name: "uspace-lab operator " + n,
				Email: strings.ToLower(n) + "@lab.uspace.test", Phone: "+995000000" + n[len(n)-3:]}, o.Now().AddDate(1, 0, 0))
			if err != nil {
				return err
			}
			ops[n] = id
		}
		for _, a := range aircraft(o.Scenarios) {
			opID := ops[a.reg]
			if opID == "" {
				return fmt.Errorf("seed: aircraft %s names operator %s, which the seed does not register", a.serial, a.reg)
			}
			if err := auth.EnsureUAS(ctx, reg, RegistryUAS{Serial: a.serial, OperatorID: opID, ClassLabel: a.class}); err != nil {
				return err
			}
		}
		o.Log("authority: %d operators and %d aircraft in the registry", len(ops), len(st.RegistryUAS))
	}

	if has[StepUSpace] {
		id := def(o.USpaceID, "LABUSP1")
		f, err := uspaceFeature(lab, id, env["DEMO_COUNTRY"], o.USpaceHalfSideM, o.USpaceCenter)
		if err != nil {
			return err
		}
		if err := auth.USpace(ctx, admin, id, f, o.Now().Add(-time.Hour), o.Now().AddDate(0, 1, 0), "uspace-lab demo U-space"); err != nil {
			return err
		}
		o.Log("authority: U-space airspace %s published", id)
	}

	if has[StepZones] && len(o.ZoneScenarios) > 0 {
		insp, err := auth.SignIn(ctx, "lab-inspector")
		if err != nil {
			return err
		}
		doc, err := zonesDoc(o.Scenarios, o.ZoneScenarios, lab)
		if err != nil {
			return err
		}
		got, err := auth.ImportZones(ctx, insp, admin, doc, o.Now().Add(-time.Hour), o.Now().AddDate(0, 1, 0))
		if err != nil {
			return err
		}
		o.Log("authority: zones published %v", got)
	}

	if has[StepUSSP] {
		for _, client := range []string{"op-a", "op-b"} {
			reg := clientRegs[client]
			op, err := ussp.EnsureOperator(ctx, reg, "uspace-lab operator "+reg, serialsOf(o.Scenarios, client))
			if err != nil {
				return err
			}
			o.Log("ussp: operator %s active, client %s, %d serials bound", reg, op.ClientID, len(op.Serials))
		}
	}

	var anspSession string
	if has[StepANSP] || has[StepSessions] {
		a, err := ansp.SignIn(ctx, "admin")
		if err != nil {
			return err
		}
		if err := ansp.EnsureUser(ctx, a, "lab-supervisor", "watch_supervisor"); err != nil {
			return err
		}
		if anspSession, err = ansp.SignIn(ctx, "lab-supervisor"); err != nil {
			return err
		}
		o.Log("ansp: lab-supervisor signed in")
	}

	if has[StepSessions] {
		insp, err := auth.SignIn(ctx, "lab-inspector")
		if err != nil {
			return err
		}
		return writeTargets(o, env, stateDir, hosts, st, admin, insp, anspSession)
	}
	return nil
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

type craft struct{ serial, reg, class string }

// aircraft is every aircraft of the scenarios, once per serial.
func aircraft(ss []*scenario.Scenario) []craft {
	seen := map[string]craft{}
	for _, s := range ss {
		for _, a := range s.Aircraft {
			c := craft{serial: a.Serial, reg: a.OperatorReg}
			if a.Operator != nil && a.Operator.Intent != nil {
				c.class = a.Operator.Intent.ClassLabel
			}
			if prev, ok := seen[a.Serial]; ok && prev.class != "" {
				c.class = prev.class
			}
			seen[a.Serial] = c
		}
	}
	out := make([]craft, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].serial < out[j].serial })
	return out
}

// serialsOf is the serials (and classes) a client streams in the
// scenarios; "default" is op-a.
func serialsOf(ss []*scenario.Scenario, client string) map[string]string {
	out := map[string]string{}
	for _, s := range ss {
		for _, a := range s.Aircraft {
			if a.Operator == nil || a.Operator.System != scenario.SystemUSSP {
				continue
			}
			c := def(a.Operator.Client, "default")
			if c == "default" {
				c = "op-a"
			}
			if c != client {
				continue
			}
			class := ""
			if a.Operator.Intent != nil {
				class = a.Operator.Intent.ClassLabel
			}
			out[a.Serial] = class
		}
	}
	return out
}

// receivers is every receiver of the scenarios, the first position seen
// of each (the pinned one; another scenario's is a counted deviation).
func receivers(ss []*scenario.Scenario) []scenario.Receiver {
	seen := map[string]bool{}
	var out []scenario.Receiver
	for _, s := range ss {
		for _, r := range s.Receivers {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// zonesDoc is the ED-269 file of the named scenarios' zones, placed by
// the lab's origin (the runner writes the same zones beside a result).
func zonesDoc(ss []*scenario.Scenario, ids []string, lab *scenario.Lab) ([]byte, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var zs []ed269.GeoZone
	for _, s := range ss {
		if !want[s.ID] {
			continue
		}
		delete(want, s.ID)
		for _, z := range s.Zones {
			gz, err := scenario.ED269Zone(s, lab, z)
			if err != nil {
				return nil, fmt.Errorf("seed: %s zone %s: %w", s.ID, z.ID, err)
			}
			zs = append(zs, gz)
		}
	}
	if len(want) > 0 {
		return nil, fmt.Errorf("seed: no scenario %v among those loaded", keys(want))
	}
	if len(zs) == 0 {
		return nil, fmt.Errorf("seed: the scenarios %v have no zones", ids)
	}
	title := "uspace-lab demo zones"
	return ed269.Export(&ed269.Document{Title: &title, Zones: zs, Wrapper: ed269.WrapperFeatures})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uspaceFeature is an ED-318 USPACE feature: a square of half side h
// about the lab's origin, from 0 m to 3000 m AMSL (every flight of the
// suite is inside it, whatever the ground).
func uspaceFeature(lab *scenario.Lab, id, country string, h float64, c scenario.Offset) ([]byte, error) {
	if h <= 0 {
		return nil, fmt.Errorf("seed: the U-space airspace's half side must be above 0")
	}
	if country == "" {
		return nil, fmt.Errorf("seed: DEMO_COUNTRY is not set")
	}
	at := func(n, e float64) core.LatLon {
		return lab.At(scenario.Offset{NorthM: c.NorthM + n, EastM: c.EastM + e})
	}
	ring := []core.LatLon{at(-h, -h), at(-h, h), at(h, h), at(h, -h), at(-h, -h)}
	var coords []string
	for _, p := range ring {
		coords = append(coords, fmt.Sprintf("[%.7f,%.7f]", p.LonDeg, p.LatDeg))
	}
	return []byte(fmt.Sprintf(`{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[%s]],`+
		`"layer":{"lower":0,"lowerReference":"AMSL","upper":3000,"upperReference":"AMSL","uom":"m"}},`+
		`"properties":{"identifier":%q,"country":%q,"name":[{"text":"uspace-lab demo U-space","lang":"en-GB"}],`+
		`"type":"USPACE","variant":"COMMON","reason":["OTHER"],`+
		`"zoneAuthority":[{"name":[{"text":"uspace-lab demo authority","lang":"en-GB"}],"purpose":"AUTHORIZATION"}]}}`,
		strings.Join(coords, ","), id, country)), nil
}

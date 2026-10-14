package seed

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/regnum"
	"github.com/rootxkit/uspace-core/serial"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

// Bounds of a load fleet the seed registers (E-10): the load harness's
// own bounds (internal/load: 20 000 aircraft, 1000 clients); receivers
// are at most one per aircraft.
const (
	MaxLoadAircraft  = 20000
	MaxLoadClients   = 1000
	MaxLoadReceivers = MaxLoadAircraft
)

// LoadFleet is a load tier's fleet as the systems must know it before a
// systems run (docs/RUNBOOKS/load.md "Systems mode"): one operator per
// client (registered at the authority, active at the USSP, one machine
// client with its aircraft bound), every aircraft in the registry under
// its operator, and the receivers registered at the authority. Digest
// names the layout; the load harness recomputes it and refuses a
// targets file seeded for another fleet.
type LoadFleet struct {
	Tier   string
	Digest string
	// ClassLabel is every aircraft's class (registered with it and
	// bound with it at the USSP, as its intents name it).
	ClassLabel string
	Clients    []LoadClient
	Receivers  []LoadReceiver
}

// LoadClient is one operator client and the serials it streams.
type LoadClient struct {
	Registration string
	Serials      []string
}

// LoadReceiver is one receiver and where it is (the centre of what it
// hears).
type LoadReceiver struct {
	ID             string
	LatDeg, LonDeg float64
}

// LoadOptions configure "loadgen seed".
type LoadOptions struct {
	// Env is deploy/demo.env (hosts, port, state directory).
	Env   string
	Fleet *LoadFleet
	// TargetsOut is the targets file written, SecretsDir the directory
	// of the secret files it names; Geoid and Lab are copied into it as
	// targets.geoid and targets.lab.
	TargetsOut string
	SecretsDir string
	Geoid      string
	Lab        string
	// Probes is deploy/systems/probes.yaml (empty: none written, and
	// the run does not read the systems' metrics or databases); Project
	// the compose project its container names are in.
	Probes  string
	Project string
	Now     func() time.Time
	Log     func(format string, args ...any)
}

var receiverID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Check refuses a fleet the systems would refuse, or that the seed
// would register twice, before anything is called: bounds, a tier and a
// digest, every registration number valid under uspace-core regnum's
// default pattern (the lab authority's default policy, pending GCAA,
// spec 08 Q5) and held once, every serial CTA-2063-A and held once
// across the fleet (compared as serial.FoldKey), every client with at
// least one aircraft, every receiver id held once and every position a
// position.
func (f *LoadFleet) Check() error {
	if f == nil {
		return fmt.Errorf("seed: no load fleet")
	}
	if f.Tier == "" || f.Digest == "" {
		return fmt.Errorf("seed: the load fleet names no tier or digest")
	}
	if len(f.Clients) == 0 || len(f.Clients) > MaxLoadClients {
		return fmt.Errorf("seed: %d load clients, want 1 to %d", len(f.Clients), MaxLoadClients)
	}
	if len(f.Receivers) > MaxLoadReceivers {
		return fmt.Errorf("seed: %d load receivers, more than %d", len(f.Receivers), MaxLoadReceivers)
	}
	v, err := regnum.NewValidator("")
	if err != nil {
		return err
	}
	regs, serials := map[string]bool{}, map[string]bool{}
	for i, c := range f.Clients {
		if err := v.Validate(c.Registration); err != nil {
			return fmt.Errorf("seed: load client %d: %w", i, err)
		}
		if regs[c.Registration] {
			return fmt.Errorf("seed: load client %d: registration %s held twice", i, c.Registration)
		}
		regs[c.Registration] = true
		if len(c.Serials) == 0 {
			return fmt.Errorf("seed: load client %d (%s) has no aircraft", i, c.Registration)
		}
		for _, sn := range c.Serials {
			if err := serial.ValidateCTA2063A(sn); err != nil {
				return fmt.Errorf("seed: load client %d: serial %q: %w", i, sn, err)
			}
			if err := serial.ValidateForClass(sn, f.ClassLabel); err != nil {
				return fmt.Errorf("seed: load client %d: serial %q: %w", i, sn, err)
			}
			k := serial.FoldKey(sn)
			if serials[k] {
				return fmt.Errorf("seed: serial %s held twice in the load fleet", sn)
			}
			serials[k] = true
			if len(serials) > MaxLoadAircraft {
				return fmt.Errorf("seed: more than %d load aircraft", MaxLoadAircraft)
			}
		}
	}
	ids := map[string]bool{}
	for _, r := range f.Receivers {
		if !receiverID.MatchString(r.ID) || ids[r.ID] {
			return fmt.Errorf("seed: load receiver id %q is not a fresh lower-case id", r.ID)
		}
		ids[r.ID] = true
		if !(r.LatDeg >= -90 && r.LatDeg <= 90 && r.LonDeg >= -180 && r.LonDeg <= 180) {
			return fmt.Errorf("seed: load receiver %s at %v, %v is not a position", r.ID, r.LatDeg, r.LonDeg)
		}
	}
	return nil
}

// SeedLoad registers a load fleet at the systems and writes the targets
// file of its systems run. Everything is checked before the first call
// (the fleet, the output paths); every value a system issues is kept in
// the seed's state, so a run resumed after a failure creates nothing
// twice. Secrets go to files in SecretsDir (0600), never to the log.
func SeedLoad(ctx context.Context, o LoadOptions) error {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if err := o.Fleet.Check(); err != nil {
		return err
	}
	if o.TargetsOut == "" || o.SecretsDir == "" || o.Geoid == "" || o.Lab == "" {
		return fmt.Errorf("seed: a load seed writes a targets file: name it, its secrets directory, the geoid and the lab file")
	}
	probes, err := loadProbes(o.Probes, o.Project)
	if err != nil {
		return err
	}
	sk, err := openStack(o.Env)
	if err != nil {
		return err
	}
	auth := &Authority{C: sk.c, Host: sk.hosts["AUTHORITY_HOST"], St: sk.st, Now: o.Now}
	ussp := &USSP{C: sk.c, Host: sk.hosts["USSP_HOST"], St: sk.st, Wait: 90 * time.Second}
	admin, err := auth.SignIn(ctx, "admin")
	if err != nil {
		return err
	}
	for user, roles := range map[string][]string{"lab-registrar": {"registrar"}, "lab-inspector": {"inspector"}} {
		if err := auth.EnsureUser(ctx, admin, user, roles); err != nil {
			return err
		}
	}
	reg, err := auth.SignIn(ctx, "lab-registrar")
	if err != nil {
		return err
	}
	for _, c := range o.Fleet.Clients {
		opID, err := auth.EnsureOperator(ctx, reg, RegistryOperator{Number: c.Registration, Name: "uspace-lab load operator " + c.Registration,
			Email: strings.ToLower(c.Registration) + "@lab.uspace.test", Phone: "+995000" + c.Registration[len(c.Registration)-6:]}, o.Now().AddDate(1, 0, 0))
		if err != nil {
			return err
		}
		for _, sn := range c.Serials {
			if err := auth.EnsureUAS(ctx, reg, RegistryUAS{Serial: sn, OperatorID: opID, ClassLabel: o.Fleet.ClassLabel}); err != nil {
				return err
			}
		}
	}
	o.Log("authority: %d load operators and their aircraft in the registry", len(o.Fleet.Clients))
	for _, r := range o.Fleet.Receivers {
		if _, err := auth.EnsureReceiver(ctx, admin, r.ID, r.LatDeg, r.LonDeg); err != nil {
			return err
		}
	}
	o.Log("authority: %d load receivers registered, keys kept", len(o.Fleet.Receivers))
	ops := make([]*USSPOperator, len(o.Fleet.Clients))
	for i, c := range o.Fleet.Clients {
		bind := make(map[string]string, len(c.Serials))
		for _, sn := range c.Serials {
			bind[sn] = o.Fleet.ClassLabel
		}
		if ops[i], err = ussp.EnsureOperator(ctx, c.Registration, "uspace-lab load operator "+c.Registration, bind); err != nil {
			return err
		}
	}
	o.Log("ussp: %d load operators active, their aircraft bound", len(ops))
	insp, err := auth.SignIn(ctx, "lab-inspector")
	if err != nil {
		return err
	}
	return writeLoadTargets(o, sk, ops, insp, probes)
}

// writeLoadTargets writes the secret files and the targets file of a
// load run (a runner.Targets with its load block), and reads the file
// back through runner.LoadTargets: a file the harness would refuse is
// an error here, not at the start of the run.
func writeLoadTargets(o LoadOptions, sk *stack, ops []*USSPOperator, insp Session, probes *runner.LoadProbes) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if err := os.MkdirAll(o.SecretsDir, 0o700); err != nil {
		return err
	}
	put := func(name, v string) (string, error) {
		p := filepath.Join(o.SecretsDir, name)
		if err := os.WriteFile(p, []byte(v+"\n"), 0o600); err != nil { //nolint:gosec // the operator names the secrets directory
			return "", err
		}
		r, err := filepath.Rel(filepath.Dir(o.TargetsOut), p)
		if err != nil {
			return "", err
		}
		return filepath.ToSlash(r), nil
	}
	h := sk.hosts
	u := func(host string) string { return fmtURL("https", host, sk.env["DEMO_HTTPS_PORT"]) }
	w := func(host string) string { return fmtURL("wss", host, sk.env["DEMO_HTTPS_PORT"]) }
	session, err := put("authority.session", insp.Token)
	if err != nil {
		return err
	}
	// DEMO_LOAD_RECEIVER_ADDRESSES (demo.env): the prefix the harness
	// gives the receivers their own addresses from, which the stack
	// trusts from the host only (deploy/systems); empty, they share the
	// host's.
	lt := &runner.LoadTarget{Tier: o.Fleet.Tier, Fleet: o.Fleet.Digest, ReceiversForwardedFor: sk.env["DEMO_LOAD_RECEIVER_ADDRESSES"],
		OperatorsForwardedFor: sk.env["DEMO_LOAD_OPERATOR_ADDRESSES"], Probes: probes}
	for i, op := range ops {
		if op == nil || op.ClientID == "" || op.ClientSecret == "" {
			return fmt.Errorf("seed: load client %d has no machine client at the USSP", i)
		}
		f, err := put(fmt.Sprintf("load-op-%04d.secret", i), op.ClientSecret)
		if err != nil {
			return err
		}
		lt.Clients = append(lt.Clients, runner.Client{ClientID: op.ClientID, SecretFile: f})
	}
	for _, r := range o.Fleet.Receivers {
		c := sk.st.Receivers[r.ID]
		if c == nil {
			return fmt.Errorf("seed: load receiver %s has no keys in the state", r.ID)
		}
		b, err := put("rx-"+r.ID+".bearer", c.BearerKey)
		if err != nil {
			return err
		}
		k, err := put("rx-"+r.ID+".hmac", c.HMACHex)
		if err != nil {
			return err
		}
		lt.Receivers = append(lt.Receivers, runner.LoadReceiver{ID: r.ID, BearerKeyFile: b, HMACKeyFile: k})
	}
	// The lab as the F3411 display provider: lab-01 of the lab issuer,
	// rid.display_provider at the USSP's audience.
	lab01, err := os.ReadFile(filepath.Join(sk.stateDir, "clients", "lab-01.secret"))
	if err != nil {
		return fmt.Errorf("seed: the lab issuer's lab-01 secret: %w", err)
	}
	dpSecret, err := put("lab-01.secret", strings.TrimSpace(string(lab01)))
	if err != nil {
		return err
	}
	lt.F3411DP = &runner.TokenCfg{TokenURL: u(h["LAB_HOST"]) + "/oauth/token", ClientID: "lab-01", SecretFile: dpSecret,
		Audience: h["USSP_HOST"], Scopes: []string{"rid.display_provider"}}
	// Every system's readiness, as the chaos matrix reads it: what a
	// load run's outage faults are judged on.
	lt.Readiness = map[string]string{
		"authority": u(h["AUTHORITY_HOST"]) + "/readyz", "cisp": u(h["CISP_HOST"]) + "/readyz",
		"ussp": u(h["USSP_HOST"]) + "/readyz", "ansp": u(h["ANSP_HOST"]) + "/readyz",
		"issuer": u(h["LAB_HOST"]) + "/healthz", "dss": u(h["LAB_HOST"]) + "/healthy",
	}
	t := runner.Targets{
		Mode: runner.ModeSystems, Name: "lab-load-" + o.Fleet.Tier,
		Images: map[string]string{"authority": sk.env["AUTHORITY_IMAGE"], "cisp": sk.env["CISP_GO_IMAGE"],
			"ussp": sk.env["USSP_GO_IMAGE"], "ansp": anspImage(sk.env)},
		Geoid: o.Geoid, Lab: relTo(o.TargetsOut, o.Lab),
		USSP: &runner.USSPTarget{BaseURL: u(h["USSP_HOST"]), TokenURL: u(h["USSP_HOST"]) + "/oauth/token", Audience: h["USSP_HOST"]},
		Authority: &runner.AuthorityTarget{BaseURL: u(h["AUTHORITY_HOST"]), PictureURL: w(h["AUTHORITY_HOST"]) + "/v1/picture/ws",
			Origin: u(h["AUTHORITY_HOST"]), SessionFile: session},
		Load: lt,
	}
	b, err := yaml.Marshal(&t)
	if err != nil {
		return err
	}
	head := fmt.Sprintf("# Written by loadgen seed (internal/seed) for tier %s (docs/RUNBOOKS/load.md).\n"+
		"# Git-ignored; the secret files it names are in %s. A console session ends\n"+
		"# after 30 minutes idle: seed again (nothing is registered twice) before a run.\n",
		o.Fleet.Tier, filepath.ToSlash(o.SecretsDir))
	if err := os.MkdirAll(filepath.Dir(o.TargetsOut), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.TargetsOut, append([]byte(head), b...), 0o600); err != nil {
		return err
	}
	if _, err := runner.LoadTargets(o.TargetsOut); err != nil {
		return fmt.Errorf("seed: the load targets file written does not load: %w", err)
	}
	o.Log("wrote %s: %d clients, %d receivers", o.TargetsOut, len(lt.Clients), len(lt.Receivers))
	return nil
}

// ProbesFormat names deploy/systems/probes.yaml.
const ProbesFormat = "lab-load-probes/v1"

// probesFile is deploy/systems/probes.yaml.
type probesFile struct {
	Format    string            `yaml:"format"`
	FetchVia  string            `yaml:"fetch_via"`
	Metrics   map[string]string `yaml:"metrics"`
	Databases []struct {
		System   string `yaml:"system"`
		Service  string `yaml:"service"`
		Database string `yaml:"database"`
	} `yaml:"databases"`
}

var composeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// loadProbes reads the probes file and names its containers in the
// compose project: fetch is docker exec into the fetch_via service's
// container and busybox wget, each database's query docker exec into
// its service's container and psql. Empty path: no probes. Everything
// is checked before the seed calls a system.
func loadProbes(path, project string) (*runner.LoadProbes, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // the lab's own file
	if err != nil {
		return nil, fmt.Errorf("seed: probes: %w", err)
	}
	var f probesFile
	if err := yaml.UnmarshalWithOptions(b, &f, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("seed: probes %s: %w", path, err)
	}
	if f.Format != ProbesFormat {
		return nil, fmt.Errorf("seed: probes %s: format %q, want %s", path, f.Format, ProbesFormat)
	}
	if project == "" {
		project = "uspace-demo"
	}
	names := append([]string{project, f.FetchVia}, slices.Collect(maps.Keys(f.Metrics))...)
	for _, d := range f.Databases {
		names = append(names, d.Service, d.Database, d.System)
	}
	for _, n := range names {
		if !composeName.MatchString(n) {
			return nil, fmt.Errorf("seed: probes %s: %q is not a compose project, service or database name", path, n)
		}
	}
	container := func(service string) string { return project + "-" + service + "-1" }
	p := &runner.LoadProbes{Fetch: []string{"docker", "exec", container(f.FetchVia), "wget", "-qO-"}, Metrics: f.Metrics}
	for _, d := range f.Databases {
		p.Databases = append(p.Databases, runner.LoadDatabase{System: d.System,
			Query: []string{"docker", "exec", container(d.Service), "psql", "-U", "postgres", "-d", d.Database, "-tAc"}})
	}
	return p, nil
}

// ConsoleSession signs user in at the authority of the stack envPath
// describes (password, then TOTP, from the seed's state) and returns the
// session: how a long load run gets a console session again when the
// authority ends the one it started with. Nothing is logged.
func ConsoleSession(ctx context.Context, envPath, user string) (string, error) {
	sk, err := openStack(envPath)
	if err != nil {
		return "", err
	}
	auth := &Authority{C: sk.c, Host: sk.hosts["AUTHORITY_HOST"], St: sk.st, Now: time.Now}
	s, err := auth.SignIn(ctx, user)
	if err != nil {
		return "", err
	}
	return s.Token, nil
}

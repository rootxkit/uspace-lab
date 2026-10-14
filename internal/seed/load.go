package seed

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/regnum"
	"github.com/rootxkit/uspace-core/serial"

	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
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
	// Peer, when set, is what the load harness's peer USSP needs of the
	// systems (systems mode, docs/RUNBOOKS/load.md).
	Peer *LoadPeer
}

// LoadPeer is the peer USSP of a load fleet as the systems must know
// it: its aircraft in the authority's registry under the first client's
// operator (the display provider identifies what it shows; they are
// never bound at the USSP, the peer serves them), an oversight area at
// the authority over the fleet and the peer (its display provider polls
// whatever has an ISA there), and a U-space airspace square (an intent
// inside it is DSS-backed, so its changes are notified to the peer).
type LoadPeer struct {
	Serials []string
	// Area is [west, south, east, north], WGS84 degrees.
	Area [4]float64
	// USpaceCentre is the square's centre (offsets from the lab origin)
	// and USpaceHalfSideM its half side.
	USpaceCentre    scenario.Offset
	USpaceHalfSideM float64
}

// LoadUSpaceID is the U-space airspace a load seed designates: the
// demo's own identifier, so the stack holds one U-space airspace,
// wherever the last seed put it.
const LoadUSpaceID = "LABUSP1"

// loadUSpaceCeilingM is the U-space airspace's ceiling above the
// origin (demo-seed's default).
const loadUSpaceCeilingM = 1500

// LoadDisplayProvider is the authority's DP client at the lab issuer
// (deploy/systems/compose.yaml, authority-dp-poller DP_CLIENT_ID).
const LoadDisplayProvider = "authority-01"

// loadPeerClient is the peer's client at the lab issuer.
const loadPeerClient = "sim-ussp-01"

// MaxLoadPeerAircraft bounds the peer's aircraft (E-10).
const MaxLoadPeerAircraft = 100

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
	if p := f.Peer; p != nil {
		if len(p.Serials) == 0 || len(p.Serials) > MaxLoadPeerAircraft {
			return fmt.Errorf("seed: %d peer aircraft, want 1 to %d", len(p.Serials), MaxLoadPeerAircraft)
		}
		for _, sn := range p.Serials {
			if err := serial.ValidateCTA2063A(sn); err != nil {
				return fmt.Errorf("seed: peer serial %q: %w", sn, err)
			}
			if err := serial.ValidateForClass(sn, f.ClassLabel); err != nil {
				return fmt.Errorf("seed: peer serial %q: %w", sn, err)
			}
			k := serial.FoldKey(sn)
			if serials[k] {
				return fmt.Errorf("seed: peer serial %s held twice in the load fleet", sn)
			}
			serials[k] = true
		}
		a := p.Area
		if !(a[0] >= -180 && a[0] < a[2] && a[2] <= 180 && a[1] >= -90 && a[1] < a[3] && a[3] <= 90) {
			return fmt.Errorf("seed: the oversight area %v is not [west, south, east, north] with west < east, south < north", a)
		}
		if !(p.USpaceHalfSideM > 0 && p.USpaceHalfSideM <= 50000) {
			return fmt.Errorf("seed: the U-space airspace's half side %v m is not in (0, 50000]", p.USpaceHalfSideM)
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
	firstOperator := ""
	for _, c := range o.Fleet.Clients {
		opID, err := auth.EnsureOperator(ctx, reg, RegistryOperator{Number: c.Registration, Name: "uspace-lab load operator " + c.Registration,
			Email: strings.ToLower(c.Registration) + "@lab.uspace.test", Phone: "+995000" + c.Registration[len(c.Registration)-6:]}, o.Now().AddDate(1, 0, 0))
		if err != nil {
			return err
		}
		if firstOperator == "" {
			firstOperator = opID
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
	if p := o.Fleet.Peer; p != nil {
		if err := seedLoadPeer(ctx, o, sk, auth, ussp, admin, reg, firstOperator, ops[0], p); err != nil {
			return err
		}
	}
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
	if p := o.Fleet.Peer; p != nil {
		if lt.Peer, err = loadPeerTarget(o, sk, p, put); err != nil {
			return err
		}
	}
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

// seedLoadPeer registers what the peer needs: its aircraft under the
// first client's operator, the oversight area, and the U-space airspace
// square, then waits until the USSP's geo-awareness (its CIS cache) puts
// the airspace over the square's centre: an intent filed before that is
// not DSS-backed. The designation's dates are the day's, so a second
// seed on the same day publishes nothing again.
func seedLoadPeer(ctx context.Context, o LoadOptions, sk *stack, auth *Authority, ussp *USSP, admin, reg Session, operatorID string, op *USSPOperator, p *LoadPeer) error {
	for _, sn := range p.Serials {
		if err := auth.EnsureUAS(ctx, reg, RegistryUAS{Serial: sn, OperatorID: operatorID, ClassLabel: o.Fleet.ClassLabel}); err != nil {
			return err
		}
	}
	if err := auth.EnsureDPView(ctx, admin, "uspace-lab load "+o.Fleet.Tier, p.Area); err != nil {
		return err
	}
	lab, err := scenario.LoadLab(o.Lab)
	if err != nil {
		return err
	}
	feature, err := uspaceFeature(lab, LoadUSpaceID, sk.env["DEMO_COUNTRY"], p.USpaceHalfSideM, p.USpaceCentre, loadUSpaceCeilingM)
	if err != nil {
		return err
	}
	from := o.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	if err := auth.USpace(ctx, admin, LoadUSpaceID, feature, from, from.AddDate(0, 1, 0), "uspace-lab load U-space"); err != nil {
		return err
	}
	c := lab.At(p.USpaceCentre)
	const d = 0.001 // about 100 m: a box well inside the square
	box := [4]float64{c.LonDeg - d, c.LatDeg - d, c.LonDeg + d, c.LatDeg + d}
	if op == nil || op.ClientID == "" || op.ClientSecret == "" {
		return fmt.Errorf("seed: no USSP machine client to read the USSP's geo-awareness with")
	}
	tokens := &oauth.ClientCredentials{TokenURL: ussp.url("/oauth/token"), ClientID: op.ClientID, ClientSecret: op.ClientSecret,
		Audience: ussp.Host, Scopes: []string{"ussp.geo"}, HTTP: ussp.C.HTTP}
	if err := ussp.WaitUSpaceAt(ctx, tokens.Token, box, LoadUSpaceID, 3*time.Minute); err != nil {
		return err
	}
	o.Log("authority: %d peer aircraft in the registry, the oversight area %v; the USSP puts U-space airspace %s over the square", len(p.Serials), p.Area, LoadUSpaceID)
	return nil
}

// loadPeerTarget is the targets file's peer block: the peer's client
// secret copied into the secrets directory, the issuer's public JWKS
// named where the issuer wrote it.
func loadPeerTarget(o LoadOptions, sk *stack, p *LoadPeer, put func(name, v string) (string, error)) (*runner.LoadPeer, error) {
	h := sk.hosts
	host, port := sk.env["LOAD_PEER_HOST"], sk.env["DEMO_LOAD_PEER_PORT"]
	if host == "" || port == "" {
		return nil, fmt.Errorf("seed: a load peer needs LOAD_PEER_HOST and DEMO_LOAD_PEER_PORT in the demo env (deploy/demo.env.example)")
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		return nil, fmt.Errorf("seed: DEMO_LOAD_PEER_PORT %q is not a port", port)
	}
	issuer := sk.env["LAB_ISSUER_URL"]
	if issuer == "" {
		return nil, fmt.Errorf("seed: LAB_ISSUER_URL is not set in the demo env")
	}
	b, err := os.ReadFile(filepath.Join(sk.stateDir, "clients", loadPeerClient+".secret"))
	if err != nil {
		return nil, fmt.Errorf("seed: the lab issuer's %s secret: %w", loadPeerClient, err)
	}
	secret, err := put(loadPeerClient+".secret", strings.TrimSpace(string(b)))
	if err != nil {
		return nil, err
	}
	// LAB_STATE_DIR is relative to deploy/, as DEMO_STATE_DIR is.
	issuerState := def(sk.env["LAB_STATE_DIR"], "./local-demo/issuer")
	if !filepath.IsAbs(issuerState) {
		issuerState = filepath.Join(sk.deployDir, issuerState)
	}
	jwks := filepath.Join(issuerState, "public", "jwks.json")
	if _, err := os.Stat(jwks); err != nil {
		return nil, fmt.Errorf("seed: the lab issuer's public JWKS: %w", err)
	}
	u := func(host string) string { return fmtURL("https", host, sk.env["DEMO_HTTPS_PORT"]) }
	return &runner.LoadPeer{
		Listen: "127.0.0.1:" + port, BaseURL: u(host), Audience: host,
		DSSRIDBase: u(h["LAB_HOST"]) + "/rid/v2", DSSUTMBase: u(h["LAB_HOST"]),
		DSS: runner.TokenCfg{TokenURL: u(h["LAB_HOST"]) + "/oauth/token", ClientID: loadPeerClient, SecretFile: secret,
			Audience: h["LAB_HOST"], Scopes: []string{"rid.service_provider", "utm.strategic_coordination"}},
		USSP: runner.TokenCfg{TokenURL: u(h["LAB_HOST"]) + "/oauth/token", ClientID: loadPeerClient, SecretFile: secret,
			Audience: h["USSP_HOST"], Scopes: []string{"utm.strategic_coordination"}},
		Issuer: issuer, JWKSFile: relTo(o.TargetsOut, jwks), DisplayProvider: LoadDisplayProvider,
		Area: p.Area, USpaceID: LoadUSpaceID, Serials: append([]string(nil), p.Serials...),
	}, nil
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
		DPCache  bool   `yaml:"dp_cache"`
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
		p.Databases = append(p.Databases, runner.LoadDatabase{System: d.System, DPCache: d.DPCache,
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

package seed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

func goodFleet() *LoadFleet {
	return &LoadFleet{Tier: "t", Digest: "sha256:ab", ClassLabel: "C2",
		Clients: []LoadClient{
			{Registration: "GEOLOAD00020001", Serials: []string{"LABXCL0002N000000", "LABXCL0002N000002"}},
			{Registration: "GEOLOAD00020002", Serials: []string{"LABXCL0002N000001"}},
		},
		Receivers: []LoadReceiver{{ID: "load-ab-0000", LatDeg: 41.7, LonDeg: 44.8}}}
}

func goodPeer() *LoadPeer {
	return &LoadPeer{Serials: []string{"LABXCP0002N000000"}, Area: [4]float64{44.8, 41.7, 44.9, 41.8}, USpaceHalfSideM: 1400}
}

// A fleet the systems would refuse, or that the seed would register
// twice, is refused by Check; the good fleet passes (the presence half).
func TestLoadFleetCheckFailsClosed(t *testing.T) {
	if err := goodFleet().Check(); err != nil {
		t.Fatalf("the good fleet: %v", err)
	}
	cases := map[string]struct {
		edit func(*LoadFleet)
		want string
	}{
		"no tier":                 {func(f *LoadFleet) { f.Tier = "" }, "no tier"},
		"no clients":              {func(f *LoadFleet) { f.Clients = nil }, "load clients"},
		"bad registration":        {func(f *LoadFleet) { f.Clients[0].Registration = "GEO-LOAD-1" }, "registration_number"},
		"registration twice":      {func(f *LoadFleet) { f.Clients[1].Registration = f.Clients[0].Registration }, "held twice"},
		"client without aircraft": {func(f *LoadFleet) { f.Clients[1].Serials = nil }, "no aircraft"},
		"serial not CTA":          {func(f *LoadFleet) { f.Clients[0].Serials[0] = "LAB-LOAD-00001" }, "serial"},
		"serial twice":            {func(f *LoadFleet) { f.Clients[1].Serials[0] = f.Clients[0].Serials[0] }, "held twice"},
		"unknown class":           {func(f *LoadFleet) { f.ClassLabel = "C9" }, "class"},
		"receiver id":             {func(f *LoadFleet) { f.Receivers[0].ID = "Load RX" }, "receiver id"},
		"receiver twice":          {func(f *LoadFleet) { f.Receivers = append(f.Receivers, f.Receivers[0]) }, "receiver id"},
		"receiver position":       {func(f *LoadFleet) { f.Receivers[0].LatDeg = 91 }, "not a position"},
		"peer without aircraft":   {func(f *LoadFleet) { f.Peer = goodPeer(); f.Peer.Serials = nil }, "peer aircraft"},
		"peer serial not CTA":     {func(f *LoadFleet) { f.Peer = goodPeer(); f.Peer.Serials[0] = "LAB-PEER-1" }, "peer serial"},
		"peer serial a fleet one": {func(f *LoadFleet) { f.Peer = goodPeer(); f.Peer.Serials[0] = f.Clients[0].Serials[0] }, "held twice"},
		"peer area inverted":      {func(f *LoadFleet) { f.Peer = goodPeer(); f.Peer.Area = [4]float64{44.9, 41.7, 44.8, 41.8} }, "oversight area"},
		"peer U-space empty":      {func(f *LoadFleet) { f.Peer = goodPeer(); f.Peer.USpaceHalfSideM = 0 }, "half side"},
	}
	withPeer := goodFleet()
	withPeer.Peer = goodPeer()
	if err := withPeer.Check(); err != nil {
		t.Fatalf("the good fleet with a peer: %v", err)
	}
	for name, c := range cases {
		f := goodFleet()
		c.edit(f)
		if err := f.Check(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
}

// SeedLoad checks the fleet and its outputs before it reads the stack's
// state or calls a system: a bad fleet stops it with the fleet's reason,
// and the same seed with the good fleet goes on to the next thing it
// needs (the lab CA, absent here).
func TestSeedLoadChecksFirst(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "demo.env")
	if err := os.WriteFile(env, []byte("AUTHORITY_HOST=a\nCISP_HOST=c\nUSSP_HOST=u\nANSP_HOST=n\nLAB_HOST=l\nDEMO_STATE_DIR="+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := LoadOptions{Env: env, TargetsOut: filepath.Join(dir, "t.yaml"), SecretsDir: filepath.Join(dir, "s"), Geoid: "constant:15.9", Lab: "sitl.env"}
	bad := goodFleet()
	bad.Clients[0].Serials[0] = "LAB-LOAD-00001"
	o.Fleet = bad
	if err := SeedLoad(context.Background(), o); err == nil || !strings.Contains(err.Error(), "serial") {
		t.Fatalf("got %v, want the refused serial", err)
	}
	o.Fleet = goodFleet()
	o.TargetsOut = ""
	if err := SeedLoad(context.Background(), o); err == nil || !strings.Contains(err.Error(), "targets file") {
		t.Fatalf("got %v, want the missing targets file", err)
	}
	o.TargetsOut = filepath.Join(dir, "t.yaml")
	if err := SeedLoad(context.Background(), o); err == nil || !strings.Contains(err.Error(), "lab CA") {
		t.Fatalf("got %v, want the seed to reach the lab CA", err)
	}
	if _, err := os.Stat(o.SecretsDir); err == nil {
		t.Fatal("a seed that stopped wrote the secrets directory")
	}
}

// The targets file the seed writes is what the load harness reads: it
// loads with runner.LoadTargets, names the clients and receivers in
// fleet order with their secret files, and holds no secret itself; the
// secrets are in their own 0600 files.
func TestLoadTargetsWrittenLoad(t *testing.T) {
	dir := t.TempDir()
	st := emptyState(t)
	st.Receivers["load-ab-0000"] = &ReceiverCreds{BearerKey: "bearer-secret", HMACHex: strings.Repeat("ab", 32)}
	if err := os.MkdirAll(filepath.Join(dir, "state", "clients"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "clients", "lab-01.secret"), []byte("lab-01-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "clients", "sim-ussp-01.secret"), []byte("peer-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "issuer", "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "issuer", "public", "jwks.json"), []byte(`{"keys":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sk := &stack{deployDir: dir, stateDir: filepath.Join(dir, "state"), env: map[string]string{"DEMO_HTTPS_PORT": "8443", "AUTHORITY_IMAGE": "a@sha256:1",
		"DEMO_LOAD_RECEIVER_ADDRESSES": "10.77.0.0/16", "DEMO_LOAD_OPERATOR_ADDRESSES": "10.78.0.0/16",
		"LOAD_PEER_HOST": "peer.test", "DEMO_LOAD_PEER_PORT": "18093", "LAB_ISSUER_URL": "https://lab.test", "LAB_STATE_DIR": "./issuer"},
		hosts: map[string]string{"AUTHORITY_HOST": "authority.test", "USSP_HOST": "ussp.test", "LAB_HOST": "lab.test", "CISP_HOST": "cisp.test", "ANSP_HOST": "ansp.test"}, st: st}
	ops := []*USSPOperator{{ClientID: "op-a", ClientSecret: "client-secret-a"}, {ClientID: "op-b", ClientSecret: "client-secret-b"}}
	fleet := goodFleet()
	fleet.Peer = goodPeer()
	o := LoadOptions{Fleet: fleet, TargetsOut: filepath.Join(dir, "targets", "load.yaml"), SecretsDir: filepath.Join(dir, "secrets"),
		Geoid: "constant:15.9", Lab: filepath.Join(dir, "sitl.env")}
	probes, err := loadProbes("../../deploy/systems/probes.yaml", "lab-x")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLoadTargets(o, sk, ops, Session{Token: "session-secret"}, probes); err != nil {
		t.Fatal(err)
	}
	tg, err := runner.LoadTargets(o.TargetsOut)
	if err != nil {
		t.Fatal(err)
	}
	lt := tg.Load
	if tg.Mode != runner.ModeSystems || lt == nil || lt.Tier != "t" || lt.Fleet != "sha256:ab" || len(lt.Clients) != 2 || lt.Clients[1].ClientID != "op-b" ||
		len(lt.Receivers) != 1 || lt.Receivers[0].ID != "load-ab-0000" {
		t.Fatalf("targets %+v load %+v", tg, lt)
	}
	if p := lt.Probes; p == nil || strings.Join(p.Fetch, " ") != "docker exec lab-x-caddy-1 wget -qO-" || p.Metrics["ussp-monitor"] != "http://ussp-monitor:8083/metrics" ||
		len(p.Databases) != 2 || strings.Join(p.Databases[0].Query, " ") != "docker exec lab-x-ussp-timescaledb-1 psql -U postgres -d ussp_timeseries -tAc" || p.Databases[1].System != "authority" {
		t.Fatalf("probes %+v", lt.Probes)
	}
	if d := lt.F3411DP; d == nil || d.TokenURL != "https://lab.test:8443/oauth/token" || d.ClientID != "lab-01" || d.Audience != "ussp.test" || d.Scopes[0] != "rid.display_provider" {
		t.Fatalf("f3411_dp %+v", lt.F3411DP)
	}
	if s, err := tg.Secret(lt.F3411DP.SecretFile); err != nil || s != "lab-01-secret" {
		t.Fatalf("lab-01 secret %q %v", s, err)
	}
	// The peer: where it listens and is reached, its clients (the secret
	// copied), the issuer's JWKS relative to the targets file.
	pe := lt.Peer
	if pe == nil || pe.Listen != "127.0.0.1:18093" || pe.BaseURL != "https://peer.test:8443" || pe.Audience != "peer.test" ||
		pe.DSSRIDBase != "https://lab.test:8443/rid/v2" || pe.DSS.Audience != "lab.test" || pe.USSP.Audience != "ussp.test" ||
		pe.DisplayProvider != "authority-01" || pe.USpaceID != "LABUSP1" || len(pe.Serials) != 1 || pe.Area != goodPeer().Area {
		t.Fatalf("peer %+v", pe)
	}
	if s, err := tg.Secret(pe.DSS.SecretFile); err != nil || s != "peer-secret" {
		t.Fatalf("peer secret %q %v", s, err)
	}
	if b, err := os.ReadFile(tg.File(pe.JWKSFile)); err != nil || string(b) != `{"keys":[]}` {
		t.Fatalf("peer JWKS %q: %v", pe.JWKSFile, err)
	}
	// Every system's readiness, for the outage faults.
	want := map[string]string{"authority": "https://authority.test:8443/readyz", "cisp": "https://cisp.test:8443/readyz",
		"ussp": "https://ussp.test:8443/readyz", "ansp": "https://ansp.test:8443/readyz",
		"issuer": "https://lab.test:8443/healthz", "dss": "https://lab.test:8443/healthy"}
	if len(lt.Readiness) != len(want) {
		t.Fatalf("readiness %v", lt.Readiness)
	}
	for k, v := range want {
		if lt.Readiness[k] != v {
			t.Errorf("readiness %s %q, want %q", k, lt.Readiness[k], v)
		}
	}
	if lt.ReceiversForwardedFor != "10.77.0.0/16" || lt.OperatorsForwardedFor != "10.78.0.0/16" {
		t.Fatalf("receivers_forwarded_for %q", lt.ReceiversForwardedFor)
	}
	if tg.USSP.BaseURL != "https://ussp.test:8443" || tg.Authority.PictureURL != "wss://authority.test:8443/v1/picture/ws" {
		t.Fatalf("endpoints %+v %+v", tg.USSP, tg.Authority)
	}
	if s, err := tg.Secret(lt.Clients[1].SecretFile); err != nil || s != "client-secret-b" {
		t.Fatalf("client secret %q %v", s, err)
	}
	if s, err := tg.Secret(lt.Receivers[0].HMACKeyFile); err != nil || s != strings.Repeat("ab", 32) {
		t.Fatalf("hmac %q %v", s, err)
	}
	text, _ := os.ReadFile(o.TargetsOut)
	for _, secret := range []string{"client-secret", "bearer-secret", "session-secret", "abab", "lab-01-secret"} {
		if strings.Contains(string(text), secret) {
			t.Fatalf("the targets file holds a secret (%s)", secret)
		}
	}
	fi, err := os.Stat(filepath.Join(o.SecretsDir, "authority.session"))
	if err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("session file %v %v", fi, err)
	}
	// A receiver the state has no keys for is an error, not a file
	// naming nothing.
	delete(st.Receivers, "load-ab-0000")
	if err := writeLoadTargets(o, sk, ops, Session{Token: "s"}, nil); err == nil || !strings.Contains(err.Error(), "no keys") {
		t.Fatalf("got %v", err)
	}
}

// The probes file is the lab's: read whole, its format named, every
// project, service and database name a plain compose name (they end up
// in a docker command line), checked before any system is called. No
// file, no probes.
func TestLoadProbesFailClosed(t *testing.T) {
	if p, err := loadProbes("", "x"); p != nil || err != nil {
		t.Fatalf("no file: %v %v", p, err)
	}
	good, err := os.ReadFile("../../deploy/systems/probes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadProbes("../../deploy/systems/probes.yaml", ""); err != nil {
		t.Fatalf("the committed file: %v", err)
	}
	dir := t.TempDir()
	for name, c := range map[string]struct{ text, project string }{
		"format":       {strings.Replace(string(good), "lab-load-probes/v1", "v0", 1), ""},
		"unknown key":  {string(good) + "\nsurprise: 1\n", ""},
		"service name": {strings.Replace(string(good), "service: ussp-timescaledb", "service: \"ussp; rm -rf\"", 1), ""},
		"project name": {string(good), "Lab X"},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".yaml")
		if err := os.WriteFile(p, []byte(c.text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProbes(p, c.project); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

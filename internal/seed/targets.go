package seed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeTargets writes the secret files and the targets file the runner
// reads for a systems run of the demo stack (targets/systems.example.yaml
// is the template; scenarios/README.md "The owed system runs").
func writeTargets(o Options, env map[string]string, stateDir string, hosts map[string]string, st *State,
	admin, inspector Session, anspSession string) error {
	if o.TargetsOut == "" || o.SecretsDir == "" {
		return fmt.Errorf("seed: the sessions step writes a targets file: name it and its secrets directory")
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
	files := map[string]string{}
	add := func(key, name, v string) error {
		p, err := put(name, v)
		files[key] = p
		return err
	}
	// The USSP's operator clients, once the ussp step made them (a run
	// of an authority-only scenario, SC-22, needs none).
	usspReady := true
	for client, reg := range clientRegs {
		op := st.USSPOperators[reg]
		if op == nil || op.ClientID == "" {
			usspReady = false
			continue
		}
		if err := add("ussp-"+client, "ussp-"+client+".secret", op.ClientSecret); err != nil {
			return err
		}
	}
	lab01, err := os.ReadFile(filepath.Join(stateDir, "clients", "lab-01.secret")) //nolint:gosec // the state directory
	if err != nil {
		return fmt.Errorf("seed: the lab issuer's lab-01 secret: %w", err)
	}
	if err := add("lab-01", "lab-01.secret", strings.TrimSpace(string(lab01))); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"authority.session": inspector.Token, "authority.csrf": inspector.CSRF,
		"authority-admin.session": admin.Token, "authority-admin.csrf": admin.CSRF,
		"ansp.session": anspSession,
	} {
		if err := add(k, k, v); err != nil {
			return err
		}
	}
	var rx strings.Builder
	for id, c := range st.Receivers {
		if err := add("rx-"+id+".bearer", "rx-"+id+".bearer", c.BearerKey); err != nil {
			return err
		}
		if err := add("rx-"+id+".hmac", "rx-"+id+".hmac", c.HMACHex); err != nil {
			return err
		}
		fmt.Fprintf(&rx, "    %s: {bearer_key_file: %s, hmac_key_file: %s}\n", id, files["rx-"+id+".bearer"], files["rx-"+id+".hmac"])
	}
	ah, uh, nh, lh := hosts["AUTHORITY_HOST"], hosts["USSP_HOST"], hosts["ANSP_HOST"], hosts["LAB_HOST"]
	u := func(h string) string { return strings.TrimSuffix(fmtURL("https", h, env["DEMO_HTTPS_PORT"]), "/") }
	w := func(h string) string { return fmtURL("wss", h, env["DEMO_HTTPS_PORT"]) }
	usspBlock := "# ussp: no operator clients yet (demo-seed --steps ussp)\n"
	if usspReady {
		a, b := st.USSPOperators[clientRegs["op-a"]], st.USSPOperators[clientRegs["op-b"]]
		usspBlock = fmt.Sprintf(`ussp:
  base_url: %s
  token_url: %s/oauth/token
  audience: %s
  clients:
    default: {client_id: %s, secret_file: %s}
    op-a: {client_id: %s, secret_file: %s}
    op-b: {client_id: %s, secret_file: %s}
`, u(uh), u(uh), uh, a.ClientID, files["ussp-op-a"], a.ClientID, files["ussp-op-a"], b.ClientID, files["ussp-op-b"])
	}
	listen := def(o.ADSBListen, "0.0.0.0:18092")
	sitlBlock := "# sitl: no commands given (demo-seed --sitl-reader, --sitl-fly): synthetic vehicles only\n"
	yamlList := func(xs []string) string {
		q := make([]string, len(xs))
		for i, x := range xs {
			q[i] = fmt.Sprintf("%q", x)
		}
		return "[" + strings.Join(q, ", ") + "]"
	}
	if len(o.SITLReader) > 0 && len(o.SITLFly) > 0 {
		sitlBlock = fmt.Sprintf("sitl:\n  reader_cmd: %s\n  fly_cmd: %s\n", yamlList(o.SITLReader), yamlList(o.SITLFly))
	}
	t := fmt.Sprintf(`# Written by cmd/demo-seed (internal/seed) for the demo stack
# (docs/RUNBOOKS/demo.md). Git-ignored (targets/local-*.yaml); the
# secret files it names are in %s, git-ignored too. Re-run
# "demo-seed --steps sessions" before a run: a console session ends after
# 30 minutes idle.
mode: systems
name: lab-demo
images:
  authority: %s
  cisp: %s
  ussp: %s
  ansp: %s (built locally from uspace-ansp %s; not published)
  dss: interuss/dss:v0.23.0@sha256:0781042bef785f6c968efd4b4e1e85854db9bf361116916d0673d2c89db91582
geoid: %s
lab: %s
%sauthority:
  base_url: %s
  picture_url: %s/v1/picture/ws
  origin: %s
  session_file: %s
  receivers:
%sansp:
  stream_url: %s/v1/manned-traffic/stream
  token:
    token_url: %s/oauth/token
    client_id: lab-01
    secret_file: %s
    audience: %s
    scopes: [ansp.traffic]
feeds:
  manned-1: {listen: %q}
requests:
  ansp:
    base_url: %s
    session_file: %s
    session_bearer: true
  authority:
    base_url: %s
    session_file: %s
    csrf_file: %s
    session_bearer: true
%sextra:
  uspace_airspace_id: %s
`, filepath.ToSlash(o.SecretsDir),
		env["AUTHORITY_IMAGE"], env["CISP_GO_IMAGE"], env["USSP_GO_IMAGE"], env["ANSP_GO_IMAGE"], env["ANSP_SOURCE_COMMIT"],
		o.Geoid, relTo(o.TargetsOut, o.Lab),
		usspBlock,
		u(ah), w(ah), u(ah), files["authority.session"], rx.String(),
		w(nh), u(lh), files["lab-01"], nh, listen,
		u(nh), files["ansp.session"],
		u(ah), files["authority-admin.session"], files["authority-admin.csrf"],
		sitlBlock, def(st.USpace, def(o.USpaceID, "LABUSP1")))
	if err := os.MkdirAll(filepath.Dir(o.TargetsOut), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.TargetsOut, []byte(t), 0o600); err != nil {
		return err
	}
	o.Log("wrote %s and %d secret files in %s", o.TargetsOut, len(files), o.SecretsDir)
	return nil
}

func fmtURL(scheme, host, port string) string {
	if port == "" || port == "443" {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

// relTo is p relative to the directory of file (the targets file names
// paths relative to itself).
func relTo(file, p string) string {
	if filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return filepath.ToSlash(p)
	}
	r, err := filepath.Rel(dir, abs)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

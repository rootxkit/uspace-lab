// Package target reads a conformance target file
// (conformance/targets/<name>.yaml): which system is under test, where
// it answers, its contract, and the credentials and fixtures the suite
// may use. Nothing in the file is a secret or a host: every such value
// is a ${VAR} reference resolved from the environment (or an env file
// the system hands over, the CISP's CONFORMANCE_ENV), so the committed
// files are the same for every deployment (INV-03, spec 06 §4).
package target

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/rootxkit/uspace-lab/conformance/national"
	"github.com/rootxkit/uspace-lab/internal/cli"
)

// Systems are the systems a target may name.
var Systems = []string{"ansp", "authority", "cisp", "ussp", "sim-ussp"}

// File is one target file after ${VAR} expansion.
type File struct {
	// System is the system under test (Systems).
	System string `yaml:"system" json:"system"`
	// Name labels the runs of this target.
	Name string `yaml:"name" json:"name"`
	// Role says what the target is for the onboarding procedure:
	// "own" (one of the five systems) or "candidate" (a third party).
	Role    string `yaml:"role" json:"role"`
	BaseURL string `yaml:"base_url" json:"base_url"`
	// Images under test, by digest (E-05); copied into the report.
	Images   map[string]string `yaml:"images" json:"images"`
	Contract struct {
		// OpenAPI is the contract file; default the aggregate's
		// api/<system>/openapi.yaml.
		OpenAPI string `yaml:"openapi" json:"openapi"`
		// Schemas is the system's schemas/ tree; default the aggregate's
		// schemas/<system>/.
		Schemas string `yaml:"schemas" json:"schemas"`
		// Overrides default conformance/national/contracts/<system>.yaml.
		Overrides string `yaml:"overrides" json:"overrides"`
	} `yaml:"contract" json:"contract"`
	// National is false for a target with no national contract (the
	// simulated peer USSP).
	National *bool             `yaml:"national" json:"national"`
	Issuers  map[string]Client `yaml:"issuers" json:"issuers"`
	// StaticTokens are ecosystem tokens handed to the suite, by name.
	StaticTokens map[string]string `yaml:"static_tokens" json:"static_tokens"`
	Session      struct {
		BearerFile string `yaml:"bearer_file" json:"bearer_file"`
	} `yaml:"session" json:"session"`
	ClientCert struct {
		CertFile string `yaml:"cert_file" json:"cert_file"`
		KeyFile  string `yaml:"key_file" json:"key_file"`
	} `yaml:"client_cert" json:"client_cert"`
	// CAFile is a PEM bundle of the roots the target's TLS chains to
	// (the lab CA of the systems stack); empty: the system roots.
	CAFile string `yaml:"ca_file" json:"ca_file"`
	// Resolve maps host names to the address the suite dials instead
	// (curl's --resolve: the systems stack's hosts at 127.0.0.1 without
	// touching the machine's resolver), "host" -> "ip".
	Resolve map[string]string `yaml:"resolve" json:"resolve"`
	// Fixtures name existing resources by path parameter name.
	Fixtures map[string]string `yaml:"fixtures" json:"fixtures"`
	// Only restricts the national checks to these operations.
	Only []string `yaml:"only" json:"only"`
	// ED318 configures the CISP's publication tests.
	ED318 *ED318 `yaml:"ed318" json:"ed318"`
	// Pages are the public pages the axe run loads (informative, L-Q10).
	Pages []string `yaml:"pages" json:"pages"`
	// Participant is the target's participant id in uss_qualifier: its
	// client id at the ecosystem issuer (decision record M24).
	Participant string `yaml:"participant" json:"participant"`
	// Interfaces are the InterUSS automated-testing interfaces the
	// system exposes, by name (rid_injection, rid_observation,
	// flight_planning) with their base URL as uss_qualifier reaches
	// them: a qualifier configuration that needs one the target lacks
	// is not applicable, with that reason
	// (conformance/uss_qualifier/coverage.yaml).
	Interfaces map[string]string `yaml:"interfaces" json:"interfaces"`
}

// Client is a client-credentials client at an issuer.
type Client struct {
	TokenURL   string `yaml:"token_url" json:"token_url"`
	ClientID   string `yaml:"client_id" json:"client_id"`
	SecretFile string `yaml:"secret_file" json:"secret_file"`
	// SecretsJSON is the lab issuer's client-secrets.json, read for
	// ClientID when SecretFile is empty.
	SecretsJSON string `yaml:"secrets_json" json:"secrets_json"`
	// Audience is the host tokens name; default the base URL's host.
	Audience string   `yaml:"audience" json:"audience"`
	Offers   []string `yaml:"offers" json:"offers"`
}

// ED318 is the CISP publication test configuration.
type ED318 struct {
	// AuthorityToken publishes zones (cis.publish:zones, sub the
	// configured authority client).
	AuthorityToken   string `yaml:"authority_token" json:"authority_token"`
	AuthorityKeyFile string `yaml:"authority_key_file" json:"authority_key_file"`
	AuthorityKID     string `yaml:"authority_kid" json:"authority_kid"`
	// ANSPToken sends the publisher heartbeat.
	ANSPToken string `yaml:"ansp_token" json:"ansp_token"`
	// ReaderToken reads (cis.read).
	ReaderToken string `yaml:"reader_token" json:"reader_token"`
	JWKSURL     string `yaml:"jwks_url" json:"jwks_url"`
	IssuerURL   string `yaml:"issuer_url" json:"issuer_url"`
	// WebhookListen and WebhookURL: where the suite's webhook receiver
	// listens and the URL the CISP reaches it at. Empty: the webhook
	// check does not apply.
	WebhookListen string `yaml:"webhook_listen" json:"webhook_listen"`
	WebhookURL    string `yaml:"webhook_url" json:"webhook_url"`
	// Publish is "true" only for a disposable stack: the publication
	// tests replace the whole zones dataset.
	Publish string `yaml:"publish" json:"publish"`
}

// Publishes reports whether the publication tests may run.
func (e *ED318) Publishes() bool {
	return e != nil && strings.EqualFold(strings.TrimSpace(e.Publish), "true")
}

// Lookup resolves a variable: the env file's value first, then the
// process environment.
type Lookup func(name string) (string, bool)

// EnvLookup reads the process environment, then extra.
func EnvLookup(extra map[string]string) Lookup {
	return func(name string) (string, bool) {
		if v, ok := extra[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
}

// MaxFileBytes bounds a target or env file.
const MaxFileBytes = 256 << 10

// ReadEnvFile reads KEY=VALUE lines (the CISP's CONFORMANCE_ENV): blank
// lines and # comments ignored, no quoting, no expansion.
func ReadEnvFile(path string) (map[string]string, error) {
	b, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), MaxFileBytes)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !envName.MatchString(k) {
			return nil, fmt.Errorf("%s:%d: not KEY=VALUE", path, n)
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

var (
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envRef  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)
)

// Load reads a target file, expands every ${VAR} and ${VAR:-default} in
// its string values with lookup, and checks it. An unset variable
// without a default is an error naming every such variable: a target
// half configured would make checks fail for the wrong reason.
func Load(path string, lookup Lookup) (*File, error) {
	b, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	missing := map[string]bool{}
	raw = expand(raw, lookup, missing)
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s: unset: %s", path, strings.Join(names, " "))
	}
	j, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// An optional value left empty (${VAR:-}) is absent, never an empty
	// identifier or URL.
	for k, v := range f.Fixtures {
		if v == "" {
			delete(f.Fixtures, k)
		}
	}
	for k, v := range f.Interfaces {
		if v == "" {
			delete(f.Interfaces, k)
		}
	}
	for k, v := range f.Resolve {
		if v == "" {
			delete(f.Resolve, k)
		} else if net.ParseIP(v) == nil {
			return nil, fmt.Errorf("%s: resolve %s: %q is not an IP address", path, k, v)
		}
	}
	for k, c := range f.Issuers {
		if c.TokenURL == "" && c.ClientID == "" {
			delete(f.Issuers, k)
		}
	}
	if err := f.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

func expand(v any, lookup Lookup, missing map[string]bool) any {
	switch t := v.(type) {
	case string:
		return envRef.ReplaceAllStringFunc(t, func(m string) string {
			g := envRef.FindStringSubmatch(m)
			if val, ok := lookup(g[1]); ok && val != "" {
				return val
			}
			if g[2] != "" {
				return g[3]
			}
			missing[g[1]] = true
			return ""
		})
	case map[string]any:
		for k, x := range t {
			t[k] = expand(x, lookup, missing)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = expand(x, lookup, missing)
		}
		return t
	}
	return v
}

func (f *File) check() error {
	known := false
	for _, s := range Systems {
		known = known || s == f.System
	}
	if !known {
		return fmt.Errorf("system %q is not one of %s", f.System, strings.Join(Systems, ", "))
	}
	if f.Name == "" {
		f.Name = f.System
	}
	switch f.Role {
	case "":
		f.Role = "own"
	case "own", "candidate":
	default:
		return fmt.Errorf("role %q is not own or candidate", f.Role)
	}
	u, err := url.Parse(f.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("base_url %q is not an http(s) URL", f.BaseURL)
	}
	for name := range f.Issuers {
		if name != national.IssuerEcosystem && name != national.IssuerOperator {
			return fmt.Errorf("issuers: %q is not ecosystem or operator", name)
		}
	}
	for name := range f.Interfaces {
		switch name {
		case "rid_injection", "rid_observation", "flight_planning":
		default:
			return fmt.Errorf("interfaces: %q is not rid_injection, rid_observation or flight_planning", name)
		}
	}
	if f.ED318 != nil {
		switch strings.ToLower(strings.TrimSpace(f.ED318.Publish)) {
		case "", "true", "false":
		default:
			return fmt.Errorf("ed318.publish %q is not true or false", f.ED318.Publish)
		}
	}
	return nil
}

// HasNational reports whether the target has a national contract.
func (f *File) HasNational() bool { return f.National == nil || *f.National }

// Host is the base URL's host without its port: the default audience
// (M18).
func (f *File) Host() string {
	u, err := url.Parse(f.BaseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// HTTPClient is the suite's client for this target, with the client
// certificate when the file names one.
func (f *File) HTTPClient() (*http.Client, bool, error) {
	if f.ClientCert.CertFile == "" && f.CAFile == "" && len(f.Resolve) == 0 {
		return national.NewHTTPClient(nil), false, nil
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, false, errors.New("the default transport is not an *http.Transport")
	}
	tr := base.Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	hasCert := false
	if f.ClientCert.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(f.ClientCert.CertFile, f.ClientCert.KeyFile)
		if err != nil {
			return nil, false, fmt.Errorf("client_cert: %w", err)
		}
		tr.TLSClientConfig.Certificates = []tls.Certificate{cert}
		hasCert = true
	}
	if f.CAFile != "" {
		pem, err := readBounded(f.CAFile)
		if err != nil {
			return nil, false, fmt.Errorf("ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, false, fmt.Errorf("ca_file %s: no certificate", f.CAFile)
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	if len(f.Resolve) > 0 {
		resolve := f.Resolve
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err == nil {
				if ip, ok := resolve[host]; ok {
					addr = net.JoinHostPort(ip, port)
				}
			}
			return dialer.DialContext(ctx, network, addr)
		}
	}
	return national.NewHTTPClient(tr), hasCert, nil
}

// Credentials builds what the national checks use.
func (f *File) Credentials(hc *http.Client, hasCert bool) (*national.TargetCredentials, error) {
	tc := &national.TargetCredentials{Clients: map[string]national.Client{}, HasClientCert: hasCert, HTTP: hc}
	for name, c := range f.Issuers {
		secret, err := c.secret()
		if err != nil {
			return nil, fmt.Errorf("issuers.%s: %w", name, err)
		}
		aud := c.Audience
		if aud == "" {
			aud = f.Host()
		}
		tc.Clients[name] = national.Client{TokenURL: c.TokenURL, ClientID: c.ClientID, Secret: secret, Audience: aud, Offers: c.Offers}
	}
	names := make([]string, 0, len(f.StaticTokens))
	for n := range f.StaticTokens {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if f.StaticTokens[n] == "" {
			continue
		}
		st, err := national.ParseStaticToken(n, f.StaticTokens[n])
		if err != nil {
			return nil, err
		}
		tc.Static = append(tc.Static, st)
	}
	if f.Session.BearerFile != "" {
		s, err := cli.ReadSecret(f.Session.BearerFile)
		if err != nil {
			return nil, fmt.Errorf("session: %w", err)
		}
		tc.SessionBearer = s
	}
	return tc, nil
}

func (c Client) secret() (string, error) {
	if c.TokenURL == "" || c.ClientID == "" {
		return "", errors.New("token_url and client_id are required")
	}
	if c.SecretFile != "" {
		return cli.ReadSecret(c.SecretFile)
	}
	if c.SecretsJSON != "" {
		return cli.ReadSecretJSON(c.SecretsJSON, c.ClientID)
	}
	return "", errors.New("secret_file or secrets_json is required")
}

// ContractPaths are the contract, schemas and overrides files for the
// target, relative paths resolved against labRoot.
func (f *File) ContractPaths(labRoot string) (openapi, schemas, overrides string) {
	openapi = f.Contract.OpenAPI
	if openapi == "" {
		openapi = filepath.Join("api", f.System, "openapi.yaml")
	}
	schemas = f.Contract.Schemas
	if schemas == "" {
		schemas = filepath.Join("schemas", f.System)
	}
	overrides = f.Contract.Overrides
	if overrides == "" {
		overrides = filepath.Join("conformance", "national", "contracts", f.System+".yaml")
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(labRoot, p)
	}
	return abs(openapi), abs(schemas), abs(overrides)
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%s is above %d bytes", path, MaxFileBytes)
	}
	return os.ReadFile(path) //nolint:gosec // the operator names the file
}

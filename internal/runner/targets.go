package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// Target modes.
const (
	ModeReference = "reference" // the runner starts internal/reftarget in process
	ModeSystems   = "systems"   // the systems' own deployments (images)
)

// Targets is a targets file: where the systems are and the credentials
// the simulators and collectors use. Secrets are read from files named
// here, never written in it; nothing here is a coordinate (the origin is
// the lab file, sim/sitl.env).
type Targets struct {
	Mode string `yaml:"mode"`
	Name string `yaml:"name"`
	// Images are the image references under test as configured (digest
	// form recommended); recorded in every result as given.
	Images map[string]string `yaml:"images"`
	// Geoid is the undulation spec the simulators use for HAE = AMSL + N
	// and the reference target for HAE - N: the same on both sides
	// (R-16).
	Geoid string `yaml:"geoid"`
	// Lab is the sitl.env the origin and the fleet numbering come from.
	Lab       string                 `yaml:"lab"`
	USSP      *USSPTarget            `yaml:"ussp"`
	Authority *AuthorityTarget       `yaml:"authority"`
	ANSP      *ANSPTarget            `yaml:"ansp"`
	Feeds     map[string]FeedTarget  `yaml:"feeds"`
	Requests  map[string]RequestAuth `yaml:"requests"`
	SITL      *SITLTarget            `yaml:"sitl"`
	Peer      *PeerTarget            `yaml:"peer"`
	Extra     map[string]any         `yaml:"extra"`
	dir       string
}

// USSPTarget is a USSP: its base URL and its issuer's operator clients.
type USSPTarget struct {
	BaseURL  string            `yaml:"base_url"`
	TokenURL string            `yaml:"token_url"`
	Audience string            `yaml:"audience"`
	Clients  map[string]Client `yaml:"clients"`
}

// Client is an operator machine client at the USSP's issuer.
type Client struct {
	ClientID   string `yaml:"client_id"`
	SecretFile string `yaml:"secret_file"`
}

// AuthorityTarget is the authority: the receiver ingest and the picture.
type AuthorityTarget struct {
	BaseURL     string                    `yaml:"base_url"`
	PictureURL  string                    `yaml:"picture_url"`
	Origin      string                    `yaml:"origin"`
	SessionFile string                    `yaml:"session_file"`
	Receivers   map[string]ReceiverTarget `yaml:"receivers"`
}

// ReceiverTarget holds a registered receiver's key files.
type ReceiverTarget struct {
	BearerKeyFile string `yaml:"bearer_key_file"`
	HMACKeyFile   string `yaml:"hmac_key_file"`
}

// ANSPTarget is the ANSP's manned-traffic stream, read with an ecosystem
// token granting ansp.traffic.
type ANSPTarget struct {
	StreamURL string   `yaml:"stream_url"`
	Token     TokenCfg `yaml:"token"`
}

// TokenCfg is a client-credentials client.
type TokenCfg struct {
	TokenURL   string   `yaml:"token_url"`
	ClientID   string   `yaml:"client_id"`
	SecretFile string   `yaml:"secret_file"`
	Audience   string   `yaml:"audience"`
	Scopes     []string `yaml:"scopes"`
}

// FeedTarget is where a lab-served feed listens, so a system can reach it.
type FeedTarget struct {
	Listen  string `yaml:"listen"`
	Issuer  string `yaml:"issuer"`
	JWKSURL string `yaml:"jwks_url"`
	// Audience of the tokens the feed accepts.
	Audience string `yaml:"audience"`
}

// RequestAuth is how a request step authenticates to a system.
type RequestAuth struct {
	BaseURL     string    `yaml:"base_url"`
	Bearer      *TokenCfg `yaml:"bearer"`
	SessionFile string    `yaml:"session_file"`
	CSRFFile    string    `yaml:"csrf_file"`
}

// SITLTarget says how to start the reader and the harness for a SITL
// vehicle. Placeholders: {sysid}, {out_port}, {fly_port}, {max_s}.
type SITLTarget struct {
	ReaderCmd []string `yaml:"reader_cmd"`
	FlyCmd    []string `yaml:"fly_cmd"`
}

// PeerTarget is where the peer USSP (cmd/sim-ussp) is, when a scenario
// drives it.
type PeerTarget struct {
	BaseURL string `yaml:"base_url"`
}

// LoadTargets reads a targets file.
func LoadTargets(path string) (*Targets, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the targets file
	if err != nil {
		return nil, fmt.Errorf("targets: %w", err)
	}
	var t Targets
	if err := yaml.UnmarshalWithOptions(b, &t, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("targets %s: %w", path, err)
	}
	t.dir = filepath.Dir(path)
	switch t.Mode {
	case ModeReference, ModeSystems:
	default:
		return nil, fmt.Errorf("targets %s: mode is reference or systems", path)
	}
	if t.Geoid == "" {
		return nil, fmt.Errorf("targets %s: geoid is required (the simulators and the ingest must use the same one, R-16)", path)
	}
	return &t, nil
}

// path resolves a file named in the targets file relative to it.
func (t *Targets) path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(t.dir, p)
}

// secret reads a secret file named in the targets file.
func (t *Targets) secret(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("targets: a secret file is not named")
	}
	b, err := os.ReadFile(t.path(p))
	if err != nil {
		return "", fmt.Errorf("targets: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func expand(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		for k, v := range vars {
			a = strings.ReplaceAll(a, "{"+k+"}", v)
		}
		out[i] = a
	}
	return out
}

package issuer

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Client is one machine client of the lab issuer (decision record M24:
// one client per calling system).
type Client struct {
	// ID is the client id and the sub of every token issued to it.
	ID string
	// Scopes are the catalogue scopes the client may request, national and
	// standard alike.
	Scopes []string
	// Audiences are the hosts the client may request a national scope
	// for. Standard scopes ignore it (M18).
	Audiences []string
}

// Registry is the validated client list.
type Registry struct {
	clients map[string]Client
	order   []string
}

// clientsFile is the shape of deploy/issuer/clients.yaml.
type clientsFile struct {
	Clients []struct {
		ID        string   `yaml:"id"`
		Scopes    []string `yaml:"scopes"`
		Audiences []string `yaml:"audiences"`
	} `yaml:"clients"`
}

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// hostPattern is a lower-case DNS name or compose service name: no
	// scheme, port, path or upper case (an aud is compared byte for byte).
	hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
	envRef      = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)
)

// MaxClientsFileBytes bounds clients.yaml.
const MaxClientsFileBytes = 64 << 10

// LoadClients parses and validates clients.yaml. An audience entry of the
// form ${VAR} is replaced by the comma-separated hosts in the environment
// variable VAR, read through lookup; an unset or empty variable is an
// error, as is anything that would make a client ambiguous: an empty or
// repeated id, a scope outside the catalogue, a reserved scope, a
// lab-only scope on another client, a repeated scope, a host that is not
// a bare lower-case host name, or a national scope with no audience.
func LoadClients(b []byte, lookup func(string) (string, bool)) (*Registry, error) {
	if len(b) > MaxClientsFileBytes {
		return nil, fmt.Errorf("clients file is larger than %d bytes", MaxClientsFileBytes)
	}
	f, err := decodeClientsFile(b)
	if err != nil {
		return nil, err
	}
	if len(f.Clients) == 0 {
		return nil, fmt.Errorf("clients file: no client")
	}
	r := &Registry{clients: make(map[string]Client, len(f.Clients))}
	for i, c := range f.Clients {
		where := fmt.Sprintf("clients[%d]", i)
		if !clientIDPattern.MatchString(c.ID) {
			return nil, fmt.Errorf("%s.id: %q is empty or not a client id", where, c.ID)
		}
		where = "client " + c.ID
		if _, dup := r.clients[c.ID]; dup {
			return nil, fmt.Errorf("%s: listed twice", where)
		}
		national := false
		for _, s := range c.Scopes {
			info, ok := catalogue[s]
			switch {
			case !ok:
				return nil, fmt.Errorf("%s: scope %q is not in the catalogue", where, s)
			case info.reserved:
				return nil, fmt.Errorf("%s: scope %q is reserved and never issued", where, s)
			case info.labOnly && c.ID != LabClientID:
				return nil, fmt.Errorf("%s: scope %q is issued to %s only", where, s, LabClientID)
			}
			if info.kind == National {
				national = true
			}
		}
		if dup := firstDuplicate(c.Scopes); dup != "" {
			return nil, fmt.Errorf("%s: scope %q listed twice", where, dup)
		}
		var auds []string
		for _, a := range c.Audiences {
			hosts, err := expandAudience(a, lookup)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", where, err)
			}
			for _, h := range hosts {
				if !slices.Contains(auds, h) {
					auds = append(auds, h)
				}
			}
		}
		if national && len(auds) == 0 {
			return nil, fmt.Errorf("%s: has a national scope but no audience", where)
		}
		r.clients[c.ID] = Client{ID: c.ID, Scopes: slices.Clone(c.Scopes), Audiences: auds}
		r.order = append(r.order, c.ID)
	}
	return r, nil
}

// decodeClientsFile decodes strictly (unknown fields refused). goccy/go-yaml
// v1.19.2 panics on some malformed input (a tag on a sequence field,
// found by FuzzLoadClients; the crasher is in testdata/fuzz): the panic
// is turned into an error so a bad file is a refusal, not a crash.
func decodeClientsFile(b []byte) (f clientsFile, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("clients file: the YAML decoder failed: %v", r)
		}
	}()
	if err := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField()).Decode(&f); err != nil {
		return f, fmt.Errorf("clients file: %w", err)
	}
	return f, nil
}

func expandAudience(entry string, lookup func(string) (string, bool)) ([]string, error) {
	raw := []string{entry}
	if m := envRef.FindStringSubmatch(entry); m != nil {
		v, ok := lookup(m[1])
		if !ok || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("audience %s: the environment variable %s is unset or empty", entry, m[1])
		}
		raw = strings.Split(v, ",")
	}
	out := make([]string, 0, len(raw))
	for _, h := range raw {
		h = strings.TrimSpace(h)
		if !hostPattern.MatchString(h) {
			return nil, fmt.Errorf("audience %q: not a bare lower-case host name (from %s)", h, entry)
		}
		out = append(out, h)
	}
	return out, nil
}

func firstDuplicate(list []string) string {
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		if seen[s] {
			return s
		}
		seen[s] = true
	}
	return ""
}

// Client returns the client with id.
func (r *Registry) Client(id string) (Client, bool) {
	c, ok := r.clients[id]
	return c, ok
}

// IDs returns the client ids in file order.
func (r *Registry) IDs() []string { return slices.Clone(r.order) }

// Grant decides a token request of client c for scopes and audience aud
// (decision record M18): every scope must be on the client's list, and
// when any of them is national, aud must be on the client's audience
// list. Standard scopes may be requested for any audience. It returns
// nil or a *RefusalError.
func (c Client) Grant(scopes []string, aud string) error {
	if len(scopes) == 0 {
		return refuse(SlugInvalidRequest, "scope", "no scope requested")
	}
	if dup := firstDuplicate(scopes); dup != "" {
		return refuse(SlugInvalidRequest, "scope", fmt.Sprintf("%s requested twice", dup))
	}
	national := ""
	for _, s := range scopes {
		info, ok := catalogue[s]
		if !ok || info.reserved || !slices.Contains(c.Scopes, s) {
			return refuse(SlugForbiddenScope, "scope", fmt.Sprintf("%s is not granted to %s", quote(s), c.ID))
		}
		if info.kind == National && national == "" {
			national = s
		}
	}
	if national != "" && !slices.Contains(c.Audiences, aud) {
		return refuse(SlugForbiddenAudience, "audience",
			fmt.Sprintf("%s may not request the national scope %s for audience %s", c.ID, national, quote(aud)))
	}
	return nil
}

// quote bounds an attacker-supplied value before it goes into a reason.
func quote(s string) string {
	const maxLen = 64
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	return fmt.Sprintf("%q", s)
}

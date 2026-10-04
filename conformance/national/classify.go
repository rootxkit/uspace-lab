package national

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Overrides is conformance/national/contracts/<system>.yaml: what the
// suite needs to know about a contract that the contract does not say
// in a form a program reads (a scope stated in prose). Every entry
// names an operation the contract has; an entry for one it does not
// have is an error, so a renamed operation cannot leave a stale entry.
type Overrides struct {
	System string `yaml:"system"`
	// Schemes maps each security scheme name to how the suite holds it.
	Schemes map[string]SchemeRule `yaml:"schemes"`
	// Unsecured is the credential of an operation with no security
	// member and no access extension: "token" (an ecosystem token whose
	// scope is listed under Scopes or Unstated) or empty, which makes
	// such an operation unclassified.
	Unsecured string `yaml:"unsecured"`
	// Scopes lists, per operation id, the scopes any one of which
	// admits a token.
	Scopes map[string][]string `yaml:"scopes"`
	// Unstated lists the token operations whose scope the contract does
	// not state (the wrong-scope check does not apply to them).
	Unstated []string `yaml:"unstated"`
	// Skip lists, per operation id and check, why a check does not
	// apply although the contract declares its status: the contract
	// itself puts another refusal first (a signature checked before
	// the body is read).
	Skip map[string]map[string]string `yaml:"skip"`
}

// SchemeRule is one security scheme as the suite holds it.
type SchemeRule struct {
	// Kind is token, session or special.
	Kind string `yaml:"kind"`
	// Issuer is ecosystem or operator for a token.
	Issuer string `yaml:"issuer"`
	// Note describes a special credential.
	Note string `yaml:"note"`
}

// MaxOverridesBytes bounds an overrides file.
const MaxOverridesBytes = 256 << 10

// LoadOverrides reads an overrides file; a missing file is an error
// (every system under test has one).
func LoadOverrides(path string) (Overrides, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Overrides{}, fmt.Errorf("overrides: %w", err)
	}
	if info.Size() > MaxOverridesBytes {
		return Overrides{}, fmt.Errorf("overrides: %s is above %d bytes", path, MaxOverridesBytes)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own configuration
	if err != nil {
		return Overrides{}, fmt.Errorf("overrides: %w", err)
	}
	var ov Overrides
	if err := yaml.UnmarshalWithOptions(b, &ov, yaml.Strict()); err != nil {
		return Overrides{}, fmt.Errorf("overrides %s: %w", path, err)
	}
	for name, r := range ov.Schemes {
		switch r.Kind {
		case "token":
			if r.Issuer != IssuerEcosystem && r.Issuer != IssuerOperator {
				return Overrides{}, fmt.Errorf("overrides %s: scheme %s: issuer %q is not ecosystem or operator", path, name, r.Issuer)
			}
		case "session":
		case "special":
			if r.Note == "" {
				return Overrides{}, fmt.Errorf("overrides %s: scheme %s: a special credential needs a note", path, name)
			}
		default:
			return Overrides{}, fmt.Errorf("overrides %s: scheme %s: kind %q is not token, session or special", path, name, r.Kind)
		}
	}
	if ov.Unsecured != "" && ov.Unsecured != "token" {
		return Overrides{}, fmt.Errorf("overrides %s: unsecured %q is not token", path, ov.Unsecured)
	}
	for id, checks := range ov.Skip {
		for check, reason := range checks {
			if !slices.Contains(CheckKinds, check) {
				return Overrides{}, fmt.Errorf("overrides %s: skip %s: %q is not a check (%s)", path, id, check, strings.Join(CheckKinds, ", "))
			}
			if strings.TrimSpace(reason) == "" {
				return Overrides{}, fmt.Errorf("overrides %s: skip %s %s: a reason is required", path, id, check)
			}
		}
	}
	return ov, nil
}

var errUnclassified = errors.New("the contract does not state its credential and the overrides do not name it")

// classify reads the operation's credential from, in order: the
// ANSP's x-auth rule, the authority's access extensions (x-scope,
// x-roles, x-session, x-receiver, x-cis-delivery, x-dp), the security
// requirements with the overrides' scheme rules, and the overrides'
// rule for an operation without security.
func (c *Contract) classify(op *Operation, raw map[string]any, ov Overrides) (Auth, error) {
	if rule, ok := raw["x-auth"].(string); ok {
		return parseXAuth(rule)
	}
	if a, ok, err := authorityAccess(raw, op.ID, ov); ok || err != nil {
		return a, err
	}
	sec, hasSec := raw["security"]
	if !hasSec {
		if root, ok := c.doc.(map[string]any); ok {
			sec, hasSec = root["security"]
		}
	}
	if !hasSec {
		if ov.Unsecured == "token" {
			t, err := scopesFor(op.ID, IssuerEcosystem, nil, ov)
			if err != nil {
				return Auth{}, err
			}
			return Auth{Token: t}, nil
		}
		return Auth{}, errUnclassified
	}
	reqs, _ := sec.([]any)
	if len(reqs) == 0 {
		return Auth{Public: true}, nil
	}
	var a Auth
	for _, r := range reqs {
		m, _ := r.(map[string]any)
		if len(m) == 0 {
			// {} in the list: anonymous access is one alternative.
			return Auth{}, errors.New("security lists an anonymous alternative beside credentials: classify it in the overrides")
		}
		for _, name := range sortedKeys(m) {
			rule, ok := ov.Schemes[name]
			if !ok {
				return Auth{}, fmt.Errorf("security scheme %s is not in the overrides' schemes", name)
			}
			var declared []string
			if l, ok := m[name].([]any); ok {
				for _, s := range l {
					if v, ok := s.(string); ok {
						declared = append(declared, v)
					}
				}
			}
			switch rule.Kind {
			case "token":
				if a.Token != nil && a.Token.Issuer != rule.Issuer {
					return Auth{}, errors.New("tokens of two issuers: classify it in the overrides")
				}
				t, err := scopesFor(op.ID, rule.Issuer, declared, ov)
				if err != nil {
					return Auth{}, err
				}
				a.Token = t
			case "session":
				a.Session = true
			case "special":
				a.Special = rule.Note
			}
		}
	}
	return a, nil
}

func scopesFor(id, issuer string, declared []string, ov Overrides) (*TokenRule, error) {
	t := &TokenRule{Issuer: issuer}
	switch {
	case len(declared) > 0:
		t.Alternatives = [][]string{declared}
	case len(ov.Scopes[id]) > 0:
		for _, s := range ov.Scopes[id] {
			t.Alternatives = append(t.Alternatives, []string{s})
		}
	case slices.Contains(ov.Unstated, id):
	default:
		return nil, errors.New("a token operation whose scope neither the contract nor the overrides (scopes or unstated) state")
	}
	return t, nil
}

// parseXAuth reads the ANSP's x-auth access rule (its api/openapi.yaml
// description): public; jws:<who>; session; session:<role>[|<role>];
// token:<scope>[+<scope>][+mtls]; alternatives joined by " or ".
func parseXAuth(rule string) (Auth, error) {
	var a Auth
	for _, alt := range strings.Split(rule, " or ") {
		alt = strings.TrimSpace(alt)
		switch {
		case alt == "public":
			if rule != "public" {
				return Auth{}, fmt.Errorf("x-auth %q: public beside another rule", rule)
			}
			return Auth{Public: true}, nil
		case alt == "session" || strings.HasPrefix(alt, "session:"):
			a.Session = true
		case strings.HasPrefix(alt, "jws:"):
			a.Special = "a compact JWS body signed by " + strings.TrimPrefix(alt, "jws:")
		case strings.HasPrefix(alt, "token:"):
			var scopes []string
			mtls := false
			for _, s := range strings.Split(strings.TrimPrefix(alt, "token:"), "+") {
				if s == "mtls" {
					mtls = true
					continue
				}
				if s == "" {
					return Auth{}, fmt.Errorf("x-auth %q: empty scope", rule)
				}
				scopes = append(scopes, s)
			}
			if len(scopes) == 0 {
				return Auth{}, fmt.Errorf("x-auth %q: a token rule without a scope", rule)
			}
			if a.Token == nil {
				a.Token = &TokenRule{Issuer: IssuerEcosystem}
			}
			a.Token.Alternatives = append(a.Token.Alternatives, scopes)
			a.Token.MTLS = a.Token.MTLS || mtls
		default:
			return Auth{}, fmt.Errorf("x-auth %q: %q is not a known rule", rule, alt)
		}
	}
	return a, nil
}

// authorityAccess reads the authority's access extensions (its
// api/openapi.yaml description): x-scope admits an ecosystem token with
// that scope and never a session; x-roles and x-session a console
// session; x-receiver a receiver's bearer key and body HMAC;
// x-cis-delivery a compact JWS body; x-dp a bearer token dp-poller
// verifies itself (scope from x-scope, else the overrides).
func authorityAccess(raw map[string]any, id string, ov Overrides) (Auth, bool, error) {
	_, roles := raw["x-roles"]
	_, session := raw["x-session"]
	scope, hasScope := raw["x-scope"].(string)
	receiver, _ := raw["x-receiver"].(bool)
	delivery, _ := raw["x-cis-delivery"].(bool)
	dp, _ := raw["x-dp"].(bool)
	switch {
	case receiver:
		return Auth{Special: "a Remote ID receiver's bearer key and the HMAC of the body"}, true, nil
	case delivery:
		return Auth{Special: "a compact JWS body signed by the CISP"}, true, nil
	case dp:
		if hasScope {
			return Auth{Token: &TokenRule{Issuer: IssuerEcosystem, Alternatives: [][]string{{scope}}}}, true, nil
		}
		t, err := scopesFor(id, IssuerEcosystem, nil, ov)
		return Auth{Token: t}, true, err
	case hasScope:
		return Auth{Token: &TokenRule{Issuer: IssuerEcosystem, Alternatives: [][]string{{scope}}}}, true, nil
	case roles || session:
		return Auth{Session: true}, true, nil
	}
	return Auth{}, false, nil
}

package national

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/rootxkit/uspace-lab/internal/oauth"
)

// ErrNoCredential is a credential the target file does not supply: the
// checks that need it do not apply (reported, never passed).
var ErrNoCredential = errors.New("the target supplies no such credential")

// Credentials hands the suite what the target accepts.
type Credentials interface {
	// Token returns a token from issuer granting every scope in scopes.
	Token(ctx context.Context, issuer string, scopes []string) (string, error)
	// WrongScope returns a valid token from issuer that grants none of
	// avoid, and the scopes it grants.
	WrongScope(ctx context.Context, issuer string, avoid []string) (string, []string, error)
	// Session returns a console or portal session to send as a bearer.
	Session(ctx context.Context) (string, error)
	// ClientCertificate reports whether requests carry a client
	// certificate (an operation bound to mTLS needs one).
	ClientCertificate() bool
}

// Client is a client-credentials client at one issuer.
type Client struct {
	TokenURL string
	ClientID string
	Secret   string
	// Audience is the host the tokens name (M18).
	Audience string
	// Offers are the scopes the client may request, in the order the
	// wrong-scope check tries them.
	Offers []string
}

// StaticToken is a token handed to the suite (the CISP's
// CONFORMANCE_ENV), with the scopes its payload names. The payload is
// read without verification and only to choose which token to send:
// the target verifies it.
type StaticToken struct {
	Name   string
	Token  string
	Scopes []string
}

// ParseStaticToken reads the scope claim of a JWT handed to the suite.
func ParseStaticToken(name, token string) (StaticToken, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return StaticToken{}, fmt.Errorf("token %s: not a compact JWT", name)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return StaticToken{}, fmt.Errorf("token %s: payload: %w", name, err)
	}
	var cl struct {
		Scope any `json:"scope"`
		Scp   any `json:"scp"`
	}
	if err := json.Unmarshal(b, &cl); err != nil {
		return StaticToken{}, fmt.Errorf("token %s: payload: %w", name, err)
	}
	st := StaticToken{Name: name, Token: token}
	for _, v := range []any{cl.Scope, cl.Scp} {
		switch s := v.(type) {
		case string:
			st.Scopes = append(st.Scopes, strings.Fields(s)...)
		case []any:
			for _, x := range s {
				if y, ok := x.(string); ok {
					st.Scopes = append(st.Scopes, y)
				}
			}
		}
	}
	sort.Strings(st.Scopes)
	return st, nil
}

// TargetCredentials is what a target file supplies.
type TargetCredentials struct {
	// Clients per issuer (ecosystem, operator).
	Clients map[string]Client
	// Static are ecosystem tokens handed to the suite.
	Static []StaticToken
	// SessionBearer is a console session, empty for none.
	SessionBearer string
	// HasClientCert is set when the HTTP client presents a certificate.
	HasClientCert bool
	HTTP          *http.Client

	mu    sync.Mutex
	cache map[string]*oauth.ClientCredentials
}

// Token implements Credentials.
func (t *TargetCredentials) Token(ctx context.Context, issuer string, scopes []string) (string, error) {
	if issuer == IssuerEcosystem {
		for _, st := range t.Static {
			if containsAll(st.Scopes, scopes) {
				return st.Token, nil
			}
		}
	}
	return t.fetch(ctx, issuer, scopes)
}

// WrongScope implements Credentials.
func (t *TargetCredentials) WrongScope(ctx context.Context, issuer string, avoid []string) (string, []string, error) {
	if issuer == IssuerEcosystem {
		for _, st := range t.Static {
			if len(st.Scopes) > 0 && !intersects(st.Scopes, avoid) {
				return st.Token, st.Scopes, nil
			}
		}
	}
	c, ok := t.Clients[issuer]
	if !ok {
		return "", nil, ErrNoCredential
	}
	for _, s := range c.Offers {
		if !slices.Contains(avoid, s) {
			tok, err := t.fetch(ctx, issuer, []string{s})
			return tok, []string{s}, err
		}
	}
	return "", nil, ErrNoCredential
}

// Session implements Credentials.
func (t *TargetCredentials) Session(context.Context) (string, error) {
	if t.SessionBearer == "" {
		return "", ErrNoCredential
	}
	return t.SessionBearer, nil
}

// ClientCertificate implements Credentials.
func (t *TargetCredentials) ClientCertificate() bool { return t.HasClientCert }

func (t *TargetCredentials) fetch(ctx context.Context, issuer string, scopes []string) (string, error) {
	c, ok := t.Clients[issuer]
	if !ok {
		return "", ErrNoCredential
	}
	key := issuer + "|" + strings.Join(scopes, " ")
	t.mu.Lock()
	if t.cache == nil {
		t.cache = map[string]*oauth.ClientCredentials{}
	}
	cc, ok := t.cache[key]
	if !ok {
		cc = &oauth.ClientCredentials{TokenURL: c.TokenURL, ClientID: c.ClientID, ClientSecret: c.Secret,
			Scopes: scopes, Audience: c.Audience, HTTP: t.HTTP}
		t.cache[key] = cc
	}
	t.mu.Unlock()
	tok, err := cc.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("token from %s for %s: %w", issuer, strings.Join(scopes, " "), err)
	}
	return tok, nil
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

package issuer

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
)

// MaxTTL is the longest token lifetime (spec 00 §6.2, 02 §1: ≤ 1 h; the
// InterUSS DSS refuses a token whose exp is more than an hour ahead).
const MaxTTL = time.Hour

// MaxRequestBytes bounds a token request body.
const MaxRequestBytes = 16 << 10

// CounterIssued counts issued tokens; refusals count refused_<slug>.
const CounterIssued = "issued"

// Config configures a Server.
type Config struct {
	// IssuerURL is the iss claim, and the issuer verifiers allow-list.
	IssuerURL string
	Key       *rsa.PrivateKey
	Kid       string
	Clients   *Registry
	Secrets   *Secrets
	// TTL of the tokens, at most MaxTTL.
	TTL time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Server is the lab issuer: POST /oauth/token, GET
// /.well-known/jwks.json, GET /healthz. Every other route is 404 or 405
// (fail closed).
type Server struct {
	cfg      Config
	iss      *auth.Issuer
	jwks     []byte
	counters core.Counters
}

// NewServer validates cfg and builds the issuer.
func NewServer(cfg Config) (*Server, error) {
	switch {
	case cfg.Clients == nil:
		return nil, errors.New("no client registry")
	case cfg.Secrets == nil:
		return nil, errors.New("no client secrets")
	case cfg.TTL < time.Second || cfg.TTL > MaxTTL:
		return nil, fmt.Errorf("token TTL %s is outside 1s..%s", cfg.TTL, MaxTTL)
	}
	if u, err := url.Parse(cfg.IssuerURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("issuer URL %q is not an absolute http(s) URL", cfg.IssuerURL)
	}
	for _, id := range cfg.Clients.IDs() {
		if _, ok := cfg.Secrets.digest[id]; !ok {
			return nil, fmt.Errorf("client %s has no secret", id)
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	iss, err := auth.NewIssuer(cfg.IssuerURL, cfg.Key, cfg.Kid)
	if err != nil {
		return nil, err
	}
	jwks, err := json.Marshal(iss.JWKS())
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, iss: iss, jwks: jwks}, nil
}

// JWKS returns the public key set.
func (s *Server) JWKS() any { return s.iss.JWKS() }

// Counters returns issued and refused_<slug>.
func (s *Server) Counters() *core.Counters { return &s.counters }

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", s.only(http.MethodPost, s.token))
	mux.HandleFunc("/.well-known/jwks.json", s.only(http.MethodGet, s.serveJWKS))
	mux.HandleFunc("/healthz", s.only(http.MethodGet, s.healthz))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		s.refuse(w, refuse(SlugNotFound, "path", "no such route"))
	})
	return mux
}

func (s *Server) only(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			s.refuse(w, refuse(SlugMethodNotAllowed, "method", r.Method+" is not allowed here"))
			return
		}
		h(w, r)
	}
}

func (s *Server) refuse(w http.ResponseWriter, r *RefusalError) {
	s.counters.Inc("refused_" + r.Slug)
	writeProblem(w, r)
}

// tokenResponse is the RFC 6749 §5.1 success body.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	req, ref := parseTokenRequest(w, r)
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	if !s.cfg.Secrets.Check(req.clientID, req.secret) {
		s.refuse(w, refuse(SlugUnauthenticated, "client", "unknown client or wrong secret"))
		return
	}
	c, ok := s.cfg.Clients.Client(req.clientID)
	if !ok {
		s.refuse(w, refuse(SlugUnauthenticated, "client", "unknown client or wrong secret"))
		return
	}
	if err := c.Grant(req.scopes, req.audience); err != nil {
		var ref *RefusalError
		if errors.As(err, &ref) {
			s.refuse(w, ref)
			return
		}
		s.refuse(w, refuse(SlugInvalidRequest, "scope", err.Error()))
		return
	}
	tok, err := s.iss.Issue(c.ID, req.audience, req.scopes, s.cfg.TTL, s.cfg.Now())
	if err != nil {
		s.refuse(w, refuse(SlugInvalidRequest, "token", err.Error()))
		return
	}
	s.counters.Inc(CounterIssued)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	//nolint:gosec // G117: the token is this endpoint's response (RFC 6749 §5.1), sent with Cache-Control no-store
	_ = json.NewEncoder(w).Encode(tokenResponse{
		AccessToken: tok,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.cfg.TTL / time.Second),
		Scope:       strings.Join(req.scopes, " "),
	})
}

type tokenRequest struct {
	clientID, secret string
	scopes           []string
	audience         string
}

// parseTokenRequest reads a client-credentials request (RFC 6749 §4.4)
// with the client authenticated by HTTP Basic or by client_id and
// client_secret in the body (§2.3.1, never both), the target named by
// audience or RFC 8707 resource (both: they must name the same host).
// Parameters come from the form body only; a repeated parameter, or a
// credential in the query string, is refused.
func parseTokenRequest(w http.ResponseWriter, r *http.Request) (tokenRequest, *RefusalError) {
	var req tokenRequest
	if r.URL.RawQuery != "" {
		return req, refuse(SlugInvalidRequest, "query", "parameters go in the form body, never the URL")
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if !strings.EqualFold(strings.TrimSpace(ct), "application/x-www-form-urlencoded") {
		return req, refuse(SlugInvalidRequest, "content-type", "must be application/x-www-form-urlencoded")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	if err := r.ParseForm(); err != nil {
		return req, refuse(SlugInvalidRequest, "body", fmt.Sprintf("unreadable or larger than %d bytes", MaxRequestBytes))
	}
	form := r.PostForm
	for name, v := range form {
		if len(v) > 1 {
			return req, refuse(SlugInvalidRequest, name, "given more than once")
		}
	}
	if gt := form.Get("grant_type"); gt != "client_credentials" {
		return req, refuse(SlugInvalidRequest, "grant_type", fmt.Sprintf("%s is not client_credentials", quote(gt)))
	}

	user, pass, basic := r.BasicAuth()
	_, bodyID := form["client_id"]
	_, bodySecret := form["client_secret"]
	switch {
	case basic && (bodyID || bodySecret):
		return req, refuse(SlugUnauthenticated, "client", "HTTP Basic and body credentials together")
	case basic:
		// RFC 6749 §2.3.1: both are form-urlencoded before Basic encoding.
		id, err1 := url.QueryUnescape(user)
		secret, err2 := url.QueryUnescape(pass)
		if err1 != nil || err2 != nil {
			return req, refuse(SlugUnauthenticated, "client", "malformed HTTP Basic credentials")
		}
		req.clientID, req.secret = id, secret
	default:
		req.clientID, req.secret = form.Get("client_id"), form.Get("client_secret")
	}
	if req.clientID == "" || req.secret == "" {
		return req, refuse(SlugUnauthenticated, "client", "no client credentials")
	}

	req.scopes = strings.Fields(form.Get("scope"))

	aud, ref := targetHost(form.Get("audience"), form.Get("resource"))
	if ref != nil {
		return req, ref
	}
	req.audience = aud
	return req, nil
}

// targetHost returns the audience: the bare host named by audience, or
// the host of the absolute URI resource (RFC 8707), lower-cased.
func targetHost(audience, resource string) (string, *RefusalError) {
	var fromAud, fromRes string
	if audience != "" {
		a := strings.ToLower(audience)
		if !hostPattern.MatchString(a) {
			return "", refuse(SlugInvalidRequest, "audience", fmt.Sprintf("%s is not a bare host name", quote(audience)))
		}
		fromAud = a
	}
	if resource != "" {
		u, err := url.Parse(resource)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Fragment != "" || u.User != nil {
			return "", refuse(SlugInvalidRequest, "resource", fmt.Sprintf("%s is not an absolute http(s) URI without fragment", quote(resource)))
		}
		h := strings.ToLower(u.Hostname())
		if !hostPattern.MatchString(h) {
			return "", refuse(SlugInvalidRequest, "resource", fmt.Sprintf("%s has no usable host", quote(resource)))
		}
		fromRes = h
	}
	switch {
	case fromAud == "" && fromRes == "":
		return "", refuse(SlugInvalidRequest, "audience", "no audience or resource")
	case fromAud != "" && fromRes != "" && fromAud != fromRes:
		return "", refuse(SlugInvalidRequest, "resource", "names a different host from audience")
	case fromAud != "":
		return fromAud, nil
	default:
		return fromRes, nil
	}
}

func (s *Server) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=300")
	_, _ = w.Write(s.jwks)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"issuer":   s.cfg.IssuerURL,
		"kid":      s.cfg.Kid,
		"clients":  len(s.cfg.Clients.IDs()),
		"counters": s.counters.Snapshot(),
	})
}

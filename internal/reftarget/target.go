package reftarget

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/alerting"
	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"
	"github.com/rootxkit/uspace-core/zones"

	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// Scopes the reference target grants and checks (the USSP's operator
// scopes, ussp openapi).
const (
	ScopeTelemetry = "ussp.telemetry"
	ScopeIntents   = "ussp.intents"
	ScopeTraffic   = "ussp.traffic"
)

// Client is an operator machine client of the reference USSP.
type Client struct {
	ID      string
	Secret  string
	Serials []string
	Scopes  []string
}

// Receiver is a Remote ID receiver of the reference authority: its
// bearer key and its HMAC secret (authority openapi receiverKey).
type Receiver struct {
	ID        string
	BearerKey string
	HMACKey   []byte
}

// Config configures the reference target.
type Config struct {
	Policy *scenario.Policy
	Zones  []*zones.Zone
	// Geoid is the undulation the ingest uses for HAE - N; nil leaves
	// every Remote ID and telemetry altitude unknown and says so in
	// degraded (SC-22).
	Geoid geoid.Undulator
	// Audience is the host the reference target's tokens are for.
	Audience  string
	Clients   []Client
	Receivers []Receiver
	// ConsoleToken stands in for a console session on the picture
	// stream and /lab/state; empty refuses every console request.
	ConsoleToken string
	// MaxLiveHz is the live rate per aircraft above which samples are
	// dropped (the USSP's 2 Hz); tests raise it to stream fast.
	MaxLiveHz float64
	Now       func() time.Time
}

// Target is the reference target: a lab stand-in for the USSP's operator
// telemetry and alert stream and for the authority's Remote ID ingest and
// picture, speaking their public contracts and judging with uspace-core's
// alerting.Monitor under the scenario's policy. It is how the scenario
// runner proves itself in CI without the systems' images (docs/
// WORKPACKAGES/WP-L5.md "fakes if no image yet"); it is never evidence
// for a system: a result against it says target "reference".
//
// What is not core's judgement is labelled: lost_link is a timer here
// (no sample for policy lost_link_s while airborne), and nonconformance,
// restriction_activated and identification are not judged at all, so a
// scenario that expects them is refused before it runs (runner).
type Target struct {
	cfg      Config
	issuer   *auth.Issuer
	verifier *auth.Verifier
	issuerID string
	started  time.Time

	mu        sync.Mutex
	ussp      *alerting.Monitor
	authority *alerting.Monitor
	alerts    *hub // alert/v1 frames, topic "flight:<id>"
	picture   *hub // violation/v1 and status, topic "picture"
	clients   map[string]Client
	serials   map[string]string // serial -> client id
	receivers map[string]Receiver
	rxOff     map[string]bool
	intents   map[string]*intent
	flights   map[string]*flight // by flight id
	active    map[string]*activeAlert
	nonces    map[string]map[string]time.Time
	seenObs   map[string]time.Time
	tx        map[string]*transmitter
	trackPos  map[string]core.LatLon
	counters  map[string]*core.Counters
}

// Counter groups.
const (
	groupTelemetry = "operator_ws"
	groupRID       = "direct_rid"
	groupUSSP      = "ussp_monitor"
	groupAuthority = "authority_monitor"
)

// New builds the target with a fresh RSA key (never stored).
func New(ctx context.Context, cfg Config) (*Target, error) {
	if cfg.Policy == nil {
		return nil, errors.New("reftarget: a policy is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("reftarget: an audience is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxLiveHz <= 0 {
		cfg.MaxLiveHz = maxLiveHz
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("reftarget: key: %w", err)
	}
	iss := "https://" + cfg.Audience + "/lab-reference-issuer"
	issuer, err := auth.NewIssuer(iss, key, "lab-ref-1")
	if err != nil {
		return nil, fmt.Errorf("reftarget: issuer: %w", err)
	}
	ver, err := auth.NewVerifier(ctx, auth.Config{
		Issuers:  map[string]auth.IssuerConfig{iss: {Keys: issuer.JWKS()}},
		Audience: cfg.Audience,
		Now:      cfg.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("reftarget: verifier: %w", err)
	}
	ucfg := cfg.Policy.AlertingConfig(false, false)
	ucfg.Zones = cfg.Zones
	acfg := cfg.Policy.AlertingConfig(true, true)
	acfg.Zones = cfg.Zones
	t := &Target{
		cfg: cfg, issuer: issuer, verifier: ver, issuerID: iss, started: cfg.Now(),
		ussp: alerting.NewMonitor(ucfg), authority: alerting.NewMonitor(acfg),
		alerts: newHub(), picture: newHub(),
		clients: map[string]Client{}, serials: map[string]string{}, receivers: map[string]Receiver{},
		rxOff: map[string]bool{}, intents: map[string]*intent{}, flights: map[string]*flight{},
		active: map[string]*activeAlert{}, nonces: map[string]map[string]time.Time{},
		seenObs: map[string]time.Time{}, tx: map[string]*transmitter{}, trackPos: map[string]core.LatLon{},
		counters: map[string]*core.Counters{},
	}
	for _, c := range cfg.Clients {
		t.clients[c.ID] = c
		for _, s := range c.Serials {
			t.serials[s] = c.ID
		}
	}
	for _, r := range cfg.Receivers {
		if len(r.HMACKey) < auth.MinReceiverKeyBytes || r.BearerKey == "" {
			return nil, fmt.Errorf("reftarget: receiver %s: a bearer key and an HMAC key of at least %d bytes", r.ID, auth.MinReceiverKeyBytes)
		}
		t.receivers[r.ID] = r
	}
	return t, nil
}

func (t *Target) countRID(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.countLocked(groupRID, name)
}

func (t *Target) countLocked(group, name string) {
	c := t.counters[group]
	if c == nil {
		c = &core.Counters{}
		t.counters[group] = c
	}
	c.Inc(name)
}

// Counters returns every counter group.
func (t *Target) Counters() map[string]map[string]uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]map[string]uint64{}
	for g, c := range t.counters {
		out[g] = c.Snapshot()
	}
	out[groupUSSP] = t.ussp.Counters().Snapshot()
	out[groupAuthority] = t.authority.Counters().Snapshot()
	return out
}

// Handler serves every route. An unknown route is 404; every route
// checks its credential before it does anything (fail closed).
func (t *Target) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", t.handleToken)
	mux.HandleFunc("GET /.well-known/jwks.json", t.handleJWKS)
	mux.HandleFunc("GET /v1/telemetry", t.handleTelemetryWS)
	mux.HandleFunc("POST /v1/telemetry/batch", t.handleTelemetryBatch)
	mux.HandleFunc("POST /v1/intents", t.handleIntentCreate)
	mux.HandleFunc("GET /v1/intents/{id}", t.handleIntentGet)
	mux.HandleFunc("PATCH /v1/intents/{id}", t.handleIntentPatch)
	mux.HandleFunc("GET /v1/alerts", t.handleAlertsWS)
	mux.HandleFunc("GET /v1/traffic", t.handleTrafficWS)
	mux.HandleFunc("POST /v1/rid/observations", t.handleObservations)
	mux.HandleFunc("GET /v1/picture/ws", t.handlePictureWS)
	mux.HandleFunc("GET /lab/state", t.handleState)
	mux.HandleFunc("POST /lab/receivers/{id}/status", t.handleReceiverSwitch)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusNotFound, "not_found", "no such route on the lab reference target")
	})
	return mux
}

// Run ticks the monitors every second until ctx ends (stale, hysteresis,
// the C-08 republish and the lost-link timer).
func (t *Target) Run(ctx context.Context) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			t.tick()
		}
	}
}

func (t *Target) tick() {
	now := t.cfg.Now()
	wall := unixS(now)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emitUSSP(t.ussp.Tick(wall), now)
	t.emitAuthority(t.authority.Tick(wall), now)
	t.lostLinkLocked(now)
	t.republishLocked(now)
}

// --- helpers -------------------------------------------------------------------

func unixS(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

func fromUnixS(s float64) time.Time {
	return time.Unix(0, int64(s*1e9)).UTC()
}

// problem writes an RFC 9457 body in the shape of problem/v1.
func problem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "https://schemas.uspace.ge/problems/" + slug,
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
		"errors": []any{},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// bearerClaims verifies the bearer token and the scope; it writes the
// refusal itself and returns ok false.
func (t *Target) bearerClaims(w http.ResponseWriter, r *http.Request, scope string) (auth.Claims, bool) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		problem(w, http.StatusUnauthorized, "unauthenticated", "a bearer token is required")
		return auth.Claims{}, false
	}
	cl, err := t.verifier.Verify(r.Context(), tok)
	if err != nil {
		problem(w, http.StatusUnauthorized, "unauthenticated", err.Error())
		return auth.Claims{}, false
	}
	if !cl.HasScope(scope) {
		problem(w, http.StatusForbidden, "forbidden", "the token does not grant "+scope)
		return auth.Claims{}, false
	}
	return cl, true
}

func (t *Target) consoleOK(r *http.Request) bool {
	if t.cfg.ConsoleToken == "" {
		return false
	}
	got := ""
	if c, err := r.Cookie("uspace_session"); err == nil {
		got = c.Value
	} else if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		got = tok
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t.cfg.ConsoleToken)) == 1
}

func (t *Target) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		problem(w, http.StatusBadRequest, "invalid_request", "parameters in the query string are refused")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" {
		problem(w, http.StatusBadRequest, "invalid_request", "client_credentials, form-encoded")
		return
	}
	id, secret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	t.mu.Lock()
	c, ok := t.clients[id]
	t.mu.Unlock()
	if !ok || subtle.ConstantTimeCompare([]byte(secret), []byte(c.Secret)) != 1 {
		problem(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong secret")
		return
	}
	if aud := r.PostForm.Get("audience"); aud != "" && aud != t.cfg.Audience {
		problem(w, http.StatusBadRequest, "invalid_target", "audience must be this host")
		return
	}
	granted := c.Scopes
	if s := r.PostForm.Get("scope"); s != "" {
		granted = nil
		for _, want := range strings.Fields(s) {
			if !contains(c.Scopes, want) {
				problem(w, http.StatusBadRequest, "invalid_scope", want+" is not granted to this client")
				return
			}
			granted = append(granted, want)
		}
	}
	const ttl = 15 * time.Minute
	tok, err := t.issuer.Issue(c.ID, t.cfg.Audience, granted, ttl, t.cfg.Now())
	if err != nil {
		problem(w, http.StatusInternalServerError, "issuer", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok, "token_type": "Bearer", "expires_in": int(ttl.Seconds()), "scope": strings.Join(granted, " "),
	})
}

func (t *Target) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, t.issuer.JWKS())
}

func (t *Target) handleState(w http.ResponseWriter, r *http.Request) {
	if !t.consoleOK(r) {
		problem(w, http.StatusUnauthorized, "unauthenticated", "console token required")
		return
	}
	t.mu.Lock()
	degraded := t.degradedLocked()
	t.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"counters": t.Counters(), "degraded": degraded, "policy_version": t.cfg.Policy.PolicyVersion,
	})
}

func (t *Target) handleReceiverSwitch(w http.ResponseWriter, r *http.Request) {
	if !t.consoleOK(r) {
		problem(w, http.StatusUnauthorized, "unauthenticated", "console token required")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		problem(w, http.StatusBadRequest, "validation", "{\"enabled\": bool}")
		return
	}
	id := r.PathValue("id")
	t.mu.Lock()
	_, ok := t.receivers[id]
	if ok {
		t.rxOff[id] = !in.Enabled
	}
	t.mu.Unlock()
	if !ok {
		problem(w, http.StatusNotFound, "not_found", "no such receiver")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": in.Enabled})
}

// degradedLocked names what this target runs without (SC-22: a missing
// input is visible, never silence).
func (t *Target) degradedLocked() []string {
	// The authority's own slugs where it has them (uspace-authority
	// internal/picture: registry_projection_absent, source_control_unknown);
	// terrain_unavailable is the reference target's.
	d := []string{"terrain_unavailable", "registry_projection_absent", "source_control_unknown"}
	if t.cfg.Geoid == nil {
		d = append(d, "geoid_unavailable")
	}
	for _, z := range t.cfg.Zones {
		if (z.Lower != nil && z.Lower.Ref == core.RefAGL) || (z.Upper != nil && z.Upper.Ref == core.RefAGL) {
			d = append(d, "zones_not_judged_agl")
			break
		}
	}
	return d
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// uuidFrom derives a UUID (version 4 bits set, as alert/v1's pattern
// wants) from a string, so a republish names the same alert.
func uuidFrom(s string) string {
	h := sha256.Sum256([]byte(s))
	h[6] = (h[6] & 0x0f) | 0x40
	h[8] = (h[8] & 0x3f) | 0x80
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func itoa(i int) string { return strconv.Itoa(i) }

// statusFrame is a console/status/v1 envelope with extras.
func (t *Target) statusFrame(producer, connID string, now time.Time, extra map[string]any, dropped uint64) []byte {
	body := map[string]any{
		"connection_id":  connID,
		"server_ts":      wire.Format(now),
		"policy_version": itoa(t.cfg.Policy.PolicyVersion),
		"stale_after_s":  t.cfg.Policy.Monitor.StaleAfterS,
		"live_max_age_s": t.cfg.Policy.Monitor.LiveMaxAgeS,
		"dropped_frames": dropped,
		"degraded":       t.degradedLocked(),
		"sources":        []any{},
	}
	for k, v := range extra {
		body[k] = v
	}
	b, _ := wire.New(wire.SchemaStatus, producer, now, now, nil, string(core.TimeSystem), false, body)
	return b
}

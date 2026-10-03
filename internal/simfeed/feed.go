// Package simfeed is the ANSP manned-traffic simulator (cmd/sim-ansp-feed,
// spec 02 F4): WS /v1/manned-traffic/stream?bbox= and GET
// /v1/manned-traffic/snapshot from a manned.Source, every frame the
// common envelope with a track/manned/v1 body (uspace-ansp's schema,
// pinned) at 1 Hz per aircraft, console/status/v1 every 2 s, the caller's
// bearer token verified (ansp.traffic). Knobs: live, stale (connected,
// positions frozen, aircraft age into state stale, SC-15) and outage
// (streams closed with 1013, new ones refused with 503 and Retry-After).
package simfeed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/manned"
	"github.com/rootxkit/uspace-lab/internal/ulid"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// Feed states (knobs).
const (
	StateLive   = "live"
	StateStale  = "stale"
	StateOutage = "outage"
)

// ScopeTraffic is the scope a caller's token must grant.
const ScopeTraffic = "ansp.traffic"

const producer = "lab/sim-ansp-feed"

// Verifier checks a bearer token and returns its scopes.
type Verifier func(ctx context.Context, token string) (scopes []string, err error)

// Config configures the feed.
type Config struct {
	Source        manned.Source
	Instance      string
	SourceClass   string
	Verify        Verifier
	StaleAfterS   float64
	LiveMaxAgeS   float64
	PolicyVersion string
	Period        time.Duration
	StatusEvery   time.Duration
	Now           func() time.Time
}

// Feed serves the stream and the snapshot.
type Feed struct {
	cfg Config

	mu       sync.Mutex
	state    string
	frozenAt time.Time
	last     map[string]*manned.Sample
	conns    map[*websocket.Conn]context.CancelFunc
	counters core.Counters
}

// New makes a feed; without a verifier every request is refused.
func New(cfg Config) (*Feed, error) {
	if cfg.Source == nil || cfg.Instance == "" {
		return nil, errors.New("simfeed: a source and an instance id are required")
	}
	if cfg.SourceClass == "" {
		cfg.SourceClass = "ads_b"
	}
	if cfg.Period <= 0 {
		cfg.Period = time.Second
	}
	if cfg.StatusEvery <= 0 {
		cfg.StatusEvery = 2 * time.Second
	}
	if cfg.StaleAfterS <= 0 || cfg.LiveMaxAgeS <= 0 || cfg.PolicyVersion == "" {
		return nil, errors.New("simfeed: stale_after_s, live_max_age_s and policy_version come from the policy file")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Feed{cfg: cfg, state: StateLive, last: map[string]*manned.Sample{}, conns: map[*websocket.Conn]context.CancelFunc{}}, nil
}

// Counters are the feed's counters.
func (f *Feed) Counters() *core.Counters { return &f.counters }

// SetState switches the feed (knob).
func (f *Feed) SetState(s string) error {
	switch s {
	case StateLive, StateStale, StateOutage:
	default:
		return errors.New("simfeed: state is live, stale or outage")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s == StateStale && f.state != StateStale {
		f.frozenAt = f.cfg.Now()
	}
	f.state = s
	if s == StateOutage {
		for c, cancel := range f.conns {
			_ = c.Close(websocket.StatusTryAgainLater, "feed outage")
			cancel()
		}
	}
	return nil
}

// Handler serves the feed's routes.
func (f *Feed) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/manned-traffic/stream", f.handleStream)
	mux.HandleFunc("GET /v1/manned-traffic/snapshot", f.handleSnapshot)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusNotFound, "not_found", "no such route")
	})
	return mux
}

func (f *Feed) authorise(w http.ResponseWriter, r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" || f.cfg.Verify == nil {
		f.counters.Inc("refused_unauthenticated")
		problem(w, http.StatusUnauthorized, "unauthenticated", "a bearer token granting ansp.traffic is required")
		return false
	}
	scopes, err := f.cfg.Verify(r.Context(), tok)
	if err != nil {
		f.counters.Inc("refused_unauthenticated")
		problem(w, http.StatusUnauthorized, "unauthenticated", err.Error())
		return false
	}
	for _, s := range scopes {
		if s == ScopeTraffic {
			return true
		}
	}
	f.counters.Inc("refused_scope")
	problem(w, http.StatusForbidden, "forbidden", "the token does not grant ansp.traffic")
	return false
}

func (f *Feed) outage(w http.ResponseWriter) bool {
	f.mu.Lock()
	out := f.state == StateOutage
	f.mu.Unlock()
	if out {
		f.counters.Inc("refused_outage")
		w.Header().Set("Retry-After", "5")
		problem(w, http.StatusServiceUnavailable, "feed_outage", "the feed is down")
	}
	return out
}

// bbox is west,south,east,north.
type bbox struct{ w, s, e, n float64 }

func parseBBox(q string) (*bbox, error) {
	if q == "" {
		return nil, nil //nolint:nilnil // no box: everything
	}
	parts := strings.Split(q, ",")
	if len(parts) != 4 || len(q) > 128 {
		return nil, errors.New("bbox is west,south,east,north")
	}
	var v [4]float64
	for i, p := range parts {
		x, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || !core.IsFinite(x) {
			return nil, errors.New("bbox is west,south,east,north")
		}
		v[i] = x
	}
	if v[1] > v[3] || v[1] < -90 || v[3] > 90 || v[0] < -180 || v[2] > 180 {
		return nil, errors.New("bbox out of range")
	}
	return &bbox{v[0], v[1], v[2], v[3]}, nil
}

func (b *bbox) contains(p core.LatLon) bool {
	if b == nil {
		return true
	}
	if p.LatDeg < b.s || p.LatDeg > b.n {
		return false
	}
	if b.w <= b.e {
		return p.LonDeg >= b.w && p.LonDeg <= b.e
	}
	return p.LonDeg >= b.w || p.LonDeg <= b.e // across the antimeridian
}

// current is the picture now: every aircraft with its state. While stale
// the source is not read and the last positions age.
func (f *Feed) current(now time.Time) []body {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == StateLive {
		at := f.cfg.Source.At(now)
		for i := range at {
			f.last[at[i].ICAO24] = &at[i]
		}
	}
	out := make([]body, 0, len(f.last))
	for id, s := range f.last {
		age := now.Sub(s.At).Seconds()
		state := StateLive
		if age > f.cfg.StaleAfterS {
			state = StateStale
		}
		if age > 10*f.cfg.StaleAfterS {
			delete(f.last, id) // aged out after it was shown stale
			continue
		}
		out = append(out, f.bodyOf(*s, state, math.Max(0, age)))
	}
	return out
}

type body struct {
	sample manned.Sample
	m      map[string]any
}

func (f *Feed) bodyOf(s manned.Sample, state string, age float64) body {
	var callsign any
	if s.Callsign != "" {
		callsign = s.Callsign
	}
	m := map[string]any{
		"icao24": s.ICAO24, "callsign": callsign, "position": map[string]any{"lat": s.Pos.LatDeg, "lng": s.Pos.LonDeg},
		"alt_pressure_m": s.AltPressureM, "alt_wgs84_m": s.AltWGS84M, "gs_ms": s.GSMS, "track_deg": s.TrackDeg,
		"vrate_ms": s.VRateMS, "emergency": s.Emergency, "squawk": s.Squawk, "source_class": f.cfg.SourceClass,
		"trust": string(core.TrustSurveillance), "source": "ansp_feed", "source_instance": f.cfg.Instance,
		"state": state, "relevant": true, "policy_version": f.cfg.PolicyVersion, "age_s": age,
	}
	return body{sample: s, m: m}
}

func (f *Feed) frame(b body, now time.Time) []byte {
	ts := b.sample.At
	out, _ := wire.New(wire.SchemaManned, producer, b.sample.At, now, &ts, string(core.TimeSourceClock), false, b.m)
	return out
}

func (f *Feed) degraded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.state {
	case StateStale:
		return []string{"manned_feed_stale"}
	case StateOutage:
		return []string{"manned_feed_down"}
	}
	return []string{}
}

func (f *Feed) adapters(now time.Time) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state
	if st == StateOutage {
		st = "down"
	}
	a := map[string]any{"id": f.cfg.Instance, "state": st, "enabled": true}
	if !f.frozenAt.IsZero() && f.state == StateStale {
		a["age_s"] = now.Sub(f.frozenAt).Seconds()
	}
	return []map[string]any{a}
}

func (f *Feed) status(connID string, now time.Time, dropped uint64) []byte {
	b := map[string]any{
		"connection_id": connID, "server_ts": wire.Format(now), "policy_version": f.cfg.PolicyVersion,
		"stale_after_s": f.cfg.StaleAfterS, "live_max_age_s": f.cfg.LiveMaxAgeS, "dropped_frames": dropped,
		"degraded": f.degraded(), "sources": []any{}, "adapters": f.adapters(now), "cis_version": nil, "cis_age_s": nil,
	}
	out, _ := wire.New(wire.SchemaStatus, producer, now, now, nil, string(core.TimeSystem), false, b)
	return out
}

func (f *Feed) snapshotBody(bb *bbox, now time.Time) map[string]any {
	manned := []json.RawMessage{}
	cur := f.current(now)
	for i := range cur {
		if bb.contains(cur[i].sample.Pos) {
			manned = append(manned, f.frame(cur[i], now))
		}
	}
	return map[string]any{
		"tracks": []any{}, "alerts": []any{}, "manned": manned, "zones_version": nil,
		"degraded": f.degraded(), "adapters": f.adapters(now), "cis_version": nil, "cis_age_s": nil, "policy_version": f.cfg.PolicyVersion,
	}
}

func (f *Feed) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if !f.authorise(w, r) || f.outage(w) {
		return
	}
	bb, err := parseBBox(r.URL.Query().Get("bbox"))
	if err != nil {
		problem(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.snapshotBody(bb, f.cfg.Now()))
}

func (f *Feed) handleStream(w http.ResponseWriter, r *http.Request) {
	if !f.authorise(w, r) || f.outage(w) {
		return
	}
	bb, err := parseBBox(r.URL.Query().Get("bbox"))
	if err != nil {
		problem(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	f.mu.Lock()
	f.conns[conn] = cancel
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.conns, conn)
		f.mu.Unlock()
		_ = conn.CloseNow()
	}()
	f.counters.Inc("streams")
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	connID := ulid.Make(f.cfg.Now())
	now := f.cfg.Now()
	snap, _ := wire.New(wire.SchemaSnapshot, producer, now, now, nil, string(core.TimeSystem), false, f.snapshotBody(bb, now))
	for _, b := range [][]byte{f.status(connID, now, 0), snap} {
		if conn.Write(ctx, websocket.MessageText, b) != nil {
			return
		}
	}
	tracks := time.NewTicker(f.cfg.Period)
	defer tracks.Stop()
	statusT := time.NewTicker(f.cfg.StatusEvery)
	defer statusT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-statusT.C:
			if conn.Write(ctx, websocket.MessageText, f.status(connID, f.cfg.Now(), 0)) != nil {
				return
			}
		case <-tracks.C:
			now := f.cfg.Now()
			cur := f.current(now)
			for i := range cur {
				if !bb.contains(cur[i].sample.Pos) {
					continue
				}
				if conn.Write(ctx, websocket.MessageText, f.frame(cur[i], now)) != nil {
					return
				}
				f.counters.Inc("frames")
			}
		}
	}
}

func problem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "https://schemas.uspace.ge/problems/" + slug, "title": http.StatusText(status), "status": status,
		"detail": detail, "errors": []any{},
	})
}

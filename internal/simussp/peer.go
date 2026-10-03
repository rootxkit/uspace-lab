// Package simussp is the peer USSP (cmd/sim-ussp): enough of a second
// U-space service provider to be the other side of S-M4 and the
// onboarding candidate of L-M4, and nothing more. It serves the standard
// shapes of uspace-core's f3411 and f3548 types and does no judgement:
//
//   - F3411 v22a Service Provider: GET /uss/flights?view= and
//     /uss/flights/{id}/details for the SITL aircraft it is given, an
//     Identification Service Area written to the DSS with this USS's
//     base URL, ISA notifications accepted;
//   - F3548-21 USS: an operational intent reference with its OVN written
//     to the DSS for each scenario intent, GET
//     /uss/v1/operational_intents/{entityid}, notifications accepted.
//
// Calls to the DSS carry a token from the lab issuer (client
// credentials); incoming calls must present one the configured verifier
// accepts with the endpoint's scope.
package simussp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/f3411"
	"github.com/rootxkit/uspace-core/f3548"
	"github.com/rootxkit/uspace-core/geodesy"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// Scopes (f3411 constants; F3548's strategic coordination scope as the
// ecosystem catalogue names it).
const (
	ScopeStrategicCoordination = "utm.strategic_coordination"
)

// TokenSource gives DSS-bound tokens.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Verifier checks an incoming bearer token and returns its scopes.
type Verifier func(ctx context.Context, token string) ([]string, error)

// Aircraft is one SITL vehicle this USS serves as a flight.
type Aircraft struct {
	Sysid      int
	Serial     string
	OperatorID string
}

// Intent is one operational intent this USS writes to the DSS.
type Intent struct {
	Center      core.LatLon
	RadiusM     float64
	AltLowerW84 float64
	AltUpperW84 float64
	Start, End  time.Time
}

// Config configures the peer.
type Config struct {
	// BaseURL is this USS's public base URL: the uss_base_url it
	// publishes for both standards.
	BaseURL string
	// DSSRIDBase is the DSS's F3411 base (InterUSS serves v22a under
	// /rid/v2); DSSUTMBase its F3548 base (paths start /dss/v1).
	DSSRIDBase string
	DSSUTMBase string
	Tokens     TokenSource
	Verify     Verifier
	// ISA is the area the Identification Service Area covers.
	ISACenter  core.LatLon
	ISARadiusM float64
	ISAAltLow  float64
	ISAAltHigh float64
	ISAFor     time.Duration
	Aircraft   []Aircraft
	Intents    []Intent
	// RecentMax bounds the positions kept per flight (E-10).
	RecentMax int
	HTTP      *http.Client
	Now       func() time.Time
}

type flight struct {
	id       string
	aircraft Aircraft
	recent   []f3411.RIDRecentAircraftPosition
	current  *f3411.RIDAircraftState
	takeoff  *core.LatLon
}

// Peer is the peer USSP.
type Peer struct {
	cfg Config

	mu       sync.Mutex
	flights  map[int]*flight
	isaID    string
	isaVer   string
	intents  map[string]*f3548.OperationalIntent
	counters core.Counters
}

// New makes the peer.
func New(cfg Config) (*Peer, error) {
	if cfg.BaseURL == "" || cfg.Tokens == nil {
		return nil, errors.New("simussp: a base URL and a token source are required")
	}
	if cfg.RecentMax <= 0 {
		cfg.RecentMax = 120
	}
	if cfg.ISAFor <= 0 {
		cfg.ISAFor = time.Hour
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	p := &Peer{cfg: cfg, flights: map[int]*flight{}, intents: map[string]*f3548.OperationalIntent{}}
	for _, a := range cfg.Aircraft {
		p.flights[a.Sysid] = &flight{id: uuidFrom("peer-flight:" + a.Serial), aircraft: a}
	}
	return p, nil
}

// Counters are the peer's counters.
func (p *Peer) Counters() *core.Counters { return &p.counters }

// ISA is the id and version of the ISA written, empty before.
func (p *Peer) ISA() (id, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isaID, p.isaVer
}

// Intents returns the operational intents written, by id.
func (p *Peer) Intents() map[string]f3548.OperationalIntent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]f3548.OperationalIntent{}
	for k, v := range p.intents {
		out[k] = *v
	}
	return out
}

// Observe takes one vehicle sample for the flight of its sysid.
func (p *Peer) Observe(s *vehicle.Sample) {
	t, ok := s.Time()
	if !ok || !s.FixOK {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fl := p.flights[s.Sysid]
	if fl == nil {
		return
	}
	if !s.Armed {
		pos := s.Pos()
		fl.takeoff = &pos
	}
	lat, lng := s.LatDeg, s.LonDeg
	alt := float32(s.AltAMSLM) // replaced below when a HAE is known
	pos := f3411.RIDAircraftPosition{Lat: &lat, Lng: &lng}
	if s.AltHAEM != nil {
		alt = float32(*s.AltHAEM)
	}
	pos.Alt = &alt
	h := float32(s.HeightTakeoffM)
	pos.Height = &f3411.RIDHeight{Distance: &h, Reference: f3411.TakeoffLocation}
	ha, va := f3411.HA10m, f3411.VA10m
	pos.AccuracyH, pos.AccuracyV = &ha, &va
	st := f3411.Ground
	if s.Armed {
		st = f3411.Airborne
	}
	if s.Status == vehicle.StatusEmergency {
		st = f3411.Emergency
	}
	speed := float32(math.Min(s.SpeedMS, f3411.MaxSpeed))
	vs := float32(math.Max(-f3411.MaxAbsVerticalSpeed, math.Min(f3411.MaxAbsVerticalSpeed, s.VSpeedMS)))
	state := f3411.RIDAircraftState{
		OperationalStatus: &st, Position: pos, Speed: &speed, SpeedAccuracy: f3411.SA1mps,
		Timestamp: f3411.Time{Format: f3411.RFC3339, Value: t}, TimestampAccuracy: 0.1, VerticalSpeed: &vs,
	}
	if s.TrackDeg != nil {
		tr := float32(*s.TrackDeg)
		state.Track = &tr
	}
	fl.current = &state
	fl.recent = append(fl.recent, f3411.RIDRecentAircraftPosition{Position: pos, Time: f3411.Time{Format: f3411.RFC3339, Value: t}})
	if len(fl.recent) > p.cfg.RecentMax {
		fl.recent = fl.recent[len(fl.recent)-p.cfg.RecentMax:]
	}
}

// --- writes to the DSS ---------------------------------------------------------

// DSSError is a refusal from the DSS.
type DSSError struct {
	Status int
	Body   string
}

func (e *DSSError) Error() string { return fmt.Sprintf("DSS answered %d: %s", e.Status, e.Body) }

// put sends body and decodes the answer into out when the DSS answers
// one of ok: F3411 v22a answers a new ISA with 200, F3548-21 a new
// operational intent reference with 201 (and its update with 200), as
// the pinned InterUSS DSS does (deploy/dss/SOURCE).
func (p *Peer) put(ctx context.Context, url string, body, out any, ok ...int) error {
	return p.call(ctx, http.MethodPut, url, body, out, ok...)
}

func (p *Peer) call(ctx context.Context, method, url string, body, out any, ok ...int) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("simussp: %w", err)
	}
	tok, err := p.cfg.Tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("simussp: token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("simussp: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("simussp: %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, f3411.MaxMessageBytes))
	if !slices.Contains(ok, resp.StatusCode) {
		return &DSSError{Status: resp.StatusCode, Body: short(rb)}
	}
	if err := json.Unmarshal(rb, out); err != nil {
		return fmt.Errorf("simussp: DSS answer: %w", err)
	}
	return nil
}

// PutISA creates the Identification Service Area in the DSS (F3411 v22a
// PUT /dss/identification_service_areas/{id}).
func (p *Peer) PutISA(ctx context.Context) error {
	now := p.cfg.Now()
	start, end := now, now.Add(p.cfg.ISAFor)
	low, high := p.cfg.ISAAltLow, p.cfg.ISAAltHigh
	r := float32(p.cfg.ISARadiusM)
	c := p.cfg.ISACenter
	body := f3411.CreateIdentificationServiceAreaParameters{
		Extents: f3411.Volume4D{
			TimeStart: &f3411.Time{Format: f3411.RFC3339, Value: start},
			TimeEnd:   &f3411.Time{Format: f3411.RFC3339, Value: end},
			Volume: f3411.Volume3D{
				AltitudeLower: &f3411.Altitude{Reference: f3411.W84, Units: f3411.AltitudeUnitsM, Value: low},
				AltitudeUpper: &f3411.Altitude{Reference: f3411.W84, Units: f3411.AltitudeUnitsM, Value: high},
				OutlineCircle: &f3411.Circle{Center: &f3411.LatLngPoint{Lat: c.LatDeg, Lng: c.LonDeg}, Radius: &f3411.Radius{Units: f3411.RadiusUnitsM, Value: r}},
			},
		},
		UssBaseUrl: p.cfg.BaseURL,
	}
	id := uuidFrom("peer-isa:" + p.cfg.BaseURL + ":" + now.Format(time.RFC3339Nano))
	var out f3411.PutIdentificationServiceAreaResponse
	if err := p.put(ctx, strings.TrimRight(p.cfg.DSSRIDBase, "/")+"/dss/identification_service_areas/"+id, body, &out, http.StatusOK); err != nil {
		p.counters.Inc("isa_write_failures")
		return err
	}
	p.counters.Inc("isa_writes")
	p.mu.Lock()
	p.isaID, p.isaVer = out.ServiceArea.Id, out.ServiceArea.Version
	p.mu.Unlock()
	return nil
}

// PutIntents writes every intent's reference (F3548 PUT
// /dss/v1/operational_intent_references/{entityid}), state Accepted, an
// implicit subscription, and keeps the OVN the DSS returns. Each write's
// key holds the OVNs the DSS gives for the references already in that
// volume (POST .../query), as F3548 requires of every intersecting
// reference: the DSS returns the OVN of this USS's own references only,
// so a reference of another USS there is still refused with 409 and its
// missing_operational_intents (fetching its OVN from that USS is not
// done here). The ids are
// new on every call, as the ISA's are: a reference already in the DSS
// can only be replaced with its OVN in the key, so a restarted peer
// reusing an id would be refused with 409.
func (p *Peer) PutIntents(ctx context.Context) error {
	run := p.cfg.Now().Format(time.RFC3339Nano)
	for i, in := range p.cfg.Intents {
		id := uuidFrom(fmt.Sprintf("peer-intent:%s:%s:%d", p.cfg.BaseURL, run, i))
		vols := []f3548.Volume4D{volume4D(in)}
		key, err := p.keyFor(ctx, vols[0])
		if err != nil {
			p.counters.Inc("intent_write_failures")
			return err
		}
		notify := false
		body := f3548.PutOperationalIntentReferenceParameters{
			Extents: vols, State: f3548.Accepted, UssBaseUrl: p.cfg.BaseURL,
			Key:             &key,
			NewSubscription: &f3548.ImplicitSubscriptionParameters{UssBaseUrl: p.cfg.BaseURL, NotifyForConstraints: &notify},
		}
		var out f3548.ChangeOperationalIntentReferenceResponse
		if err := p.put(ctx, strings.TrimRight(p.cfg.DSSUTMBase, "/")+"/dss/v1/operational_intent_references/"+id, body, &out, http.StatusOK, http.StatusCreated); err != nil {
			p.counters.Inc("intent_write_failures")
			return err
		}
		p.counters.Inc("intent_writes")
		p.mu.Lock()
		p.intents[id] = &f3548.OperationalIntent{
			Reference: out.OperationalIntentReference,
			Details:   f3548.OperationalIntentDetails{Volumes: &vols},
		}
		p.mu.Unlock()
	}
	return nil
}

// keyFor asks the DSS for the references in v and returns the OVNs it
// gives (F3548 POST /dss/v1/operational_intent_references/query).
func (p *Peer) keyFor(ctx context.Context, v f3548.Volume4D) (f3548.Key, error) {
	var out f3548.QueryOperationalIntentReferenceResponse
	q := f3548.QueryOperationalIntentReferenceParameters{AreaOfInterest: &v}
	if err := p.call(ctx, http.MethodPost, strings.TrimRight(p.cfg.DSSUTMBase, "/")+"/dss/v1/operational_intent_references/query", q, &out, http.StatusOK); err != nil {
		return nil, err
	}
	key := f3548.Key{}
	for i := range out.OperationalIntentReferences {
		if ovn := out.OperationalIntentReferences[i].Ovn; ovn != nil && *ovn != "" {
			key = append(key, *ovn)
		}
	}
	return key, nil
}

func volume4D(in Intent) f3548.Volume4D {
	r := float32(in.RadiusM)
	return f3548.Volume4D{
		TimeStart: &f3548.Time{Format: f3548.RFC3339, Value: in.Start},
		TimeEnd:   &f3548.Time{Format: f3548.RFC3339, Value: in.End},
		Volume: f3548.Volume3D{
			AltitudeLower: &f3548.Altitude{Reference: f3548.W84, Units: f3548.AltitudeUnitsM, Value: in.AltLowerW84},
			AltitudeUpper: &f3548.Altitude{Reference: f3548.W84, Units: f3548.AltitudeUnitsM, Value: in.AltUpperW84},
			OutlineCircle: &f3548.Circle{Center: &f3548.LatLngPoint{Lat: in.Center.LatDeg, Lng: in.Center.LonDeg}, Radius: &f3548.Radius{Units: f3548.RadiusUnitsM, Value: r}},
		},
	}
}

// --- the USS endpoints ---------------------------------------------------------

// Handler serves the standard USS endpoints.
func (p *Peer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /uss/flights", p.withScope(string(f3411.ScopeDisplayProvider), p.handleFlights))
	mux.HandleFunc("GET /uss/flights/{id}/details", p.withScope(string(f3411.ScopeDisplayProvider), p.handleDetails))
	mux.HandleFunc("POST /uss/identification_service_areas/{id}", p.withScope(string(f3411.ScopeServiceProvider), p.handleNotification))
	mux.HandleFunc("GET /uss/v1/operational_intents/{entityid}", p.withScope(ScopeStrategicCoordination, p.handleIntent))
	mux.HandleFunc("POST /uss/v1/operational_intents", p.withScope(ScopeStrategicCoordination, p.handleNotification))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		errorResponse(w, http.StatusNotFound, "no such endpoint")
	})
	return mux
}

func (p *Peer) withScope(scope string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" || p.cfg.Verify == nil {
			p.counters.Inc("refused_unauthenticated")
			errorResponse(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}
		scopes, err := p.cfg.Verify(r.Context(), tok)
		if err != nil {
			p.counters.Inc("refused_unauthenticated")
			errorResponse(w, http.StatusUnauthorized, err.Error())
			return
		}
		for _, s := range scopes {
			if s == scope {
				h(w, r)
				return
			}
		}
		p.counters.Inc("refused_scope")
		errorResponse(w, http.StatusForbidden, "the token does not grant "+scope)
	}
}

// errorResponse is the standards' ErrorResponse ({"message": ...}).
func errorResponse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// parseView reads lat1,lng1,lat2,lng2 and refuses a view whose diagonal
// exceeds NetMaxDisplayAreaDiagonalKm (413).
func parseView(v string) (minLat, minLng, maxLat, maxLng float64, status int, err error) {
	parts := strings.Split(v, ",")
	if len(parts) != 4 || len(v) > 128 {
		return 0, 0, 0, 0, http.StatusBadRequest, errors.New("view is lat1,lng1,lat2,lng2")
	}
	var x [4]float64
	for i, s := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || !core.IsFinite(f) {
			return 0, 0, 0, 0, http.StatusBadRequest, errors.New("view is lat1,lng1,lat2,lng2")
		}
		x[i] = f
	}
	a, b := core.LatLon{LatDeg: x[0], LonDeg: x[1]}, core.LatLon{LatDeg: x[2], LonDeg: x[3]}
	if !a.Valid() || !b.Valid() {
		return 0, 0, 0, 0, http.StatusBadRequest, errors.New("view corner out of range")
	}
	d, err := geodesy.DistanceM(a, b)
	if err != nil || d > f3411.NetMaxDisplayAreaDiagonalKm*1000 {
		return 0, 0, 0, 0, http.StatusRequestEntityTooLarge, errors.New("the view is too large")
	}
	return math.Min(x[0], x[2]), math.Min(x[1], x[3]), math.Max(x[0], x[2]), math.Max(x[1], x[3]), 0, nil
}

func (p *Peer) handleFlights(w http.ResponseWriter, r *http.Request) {
	minLat, minLng, maxLat, maxLng, status, err := parseView(r.URL.Query().Get("view"))
	if err != nil {
		errorResponse(w, status, err.Error())
		return
	}
	recentS := 0.0
	if q := r.URL.Query().Get("recent_positions_duration"); q != "" {
		if recentS, err = strconv.ParseFloat(q, 64); err != nil || recentS < 0 || recentS > f3411.NetMaxNearRealTimeDataPeriodSeconds {
			errorResponse(w, http.StatusBadRequest, "recent_positions_duration is 0..60")
			return
		}
	}
	now := p.cfg.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	flights := []f3411.RIDFlight{}
	for _, fl := range p.flights {
		if fl.current == nil {
			continue
		}
		in := false
		var recent []f3411.RIDRecentAircraftPosition
		for _, rp := range fl.recent {
			age := now.Sub(rp.Time.Value).Seconds()
			if age > f3411.NetMaxNearRealTimeDataPeriodSeconds {
				continue
			}
			lat, lng := *rp.Position.Lat, *rp.Position.Lng
			if lat >= minLat && lat <= maxLat && lng >= minLng && lng <= maxLng {
				in = true
			}
			if recentS > 0 && age <= recentS {
				recent = append(recent, rp)
			}
		}
		if !in {
			continue
		}
		cur := *fl.current
		simulated := true
		f := f3411.RIDFlight{AircraftType: f3411.Helicopter, Id: fl.id, CurrentState: &cur, Simulated: &simulated}
		if recentS > 0 {
			f.RecentPositions = &recent
		}
		flights = append(flights, f)
	}
	p.counters.Inc("flights_requests")
	writeJSON(w, f3411.GetFlightsResponse{Flights: &flights, Timestamp: f3411.Time{Format: f3411.RFC3339, Value: now}})
}

func (p *Peer) handleDetails(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fl := range p.flights {
		if fl.id != id {
			continue
		}
		serial := fl.aircraft.Serial
		d := f3411.RIDFlightDetails{Id: fl.id, UasId: &f3411.UASID{SerialNumber: &serial}}
		if fl.aircraft.OperatorID != "" {
			op := fl.aircraft.OperatorID
			d.OperatorId = &op
		}
		writeJSON(w, f3411.GetFlightDetailsResponse{Details: d})
		return
	}
	errorResponse(w, http.StatusNotFound, "no such flight")
}

func (p *Peer) handleIntent(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	oi, ok := p.intents[r.PathValue("entityid")]
	var cp f3548.OperationalIntent
	if ok {
		cp = *oi
	}
	p.mu.Unlock()
	if !ok {
		errorResponse(w, http.StatusNotFound, "no such operational intent")
		return
	}
	writeJSON(w, f3548.GetOperationalIntentDetailsResponse{OperationalIntent: cp})
}

func (p *Peer) handleNotification(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, f3411.MaxMessageBytes))
	p.counters.Inc("notifications")
	w.WriteHeader(http.StatusNoContent)
}

func uuidFrom(s string) string {
	h := sha256.Sum256([]byte(s))
	h[6] = (h[6] & 0x0f) | 0x40
	h[8] = (h[8] & 0x3f) | 0x80
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func short(b []byte) string {
	if len(b) > 200 {
		return string(b[:200])
	}
	return string(b)
}

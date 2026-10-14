package load

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/f3548"
	"github.com/rootxkit/uspace-core/geodesy"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/seed"
	"github.com/rootxkit/uspace-lab/internal/simussp"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// The peer USSP of a systems run (05 §7: "a second USSP on a local
// DSS"): the harness embeds internal/simussp, reached by the systems
// through the lab Caddy (deploy/systems, LOAD_PEER_HOST), so what it
// times is on the generator's one clock:
//
//   - F3411 DP display after SP response: the peer serves a few aircraft
//     of its own as a Service Provider (an ISA in the DSS, its
//     subscribers notified); the authority's display provider polls it
//     over the oversight area the seed added; each position's first
//     answer to the authority's poll is the SP response, and the
//     authority picture's frame that first shows that position is the
//     display;
//   - F3548: the peer holds a subscription over the U-space airspace
//     square the seed designated, so the USSP notifies it of every
//     change of the fleet's intents inside (filed, activated, ended);
//     each notification is timed from the lab's request that made the
//     change, and the details of each notified intent are asked of the
//     USSP and timed.

// PeerCfg is a tier's peer USSP (systems mode only).
type PeerCfg struct {
	// Aircraft are the peer's own flights, circling Centre at RadiusM,
	// SpeedMS, AltRelM above the origin (an altitude no walker or pair
	// uses, so they make no proximity alert).
	Aircraft int             `yaml:"aircraft" json:"aircraft"`
	Centre   scenario.Offset `yaml:"centre" json:"centre"`
	RadiusM  float64         `yaml:"radius_m" json:"radius_m"`
	SpeedMS  float64         `yaml:"speed_ms" json:"speed_ms"`
	AltRelM  float64         `yaml:"alt_rel_m" json:"alt_rel_m"`
	// USpace is the U-space airspace square the seed designates: the
	// fleet's intents inside are DSS-backed and notified to the peer.
	USpace USpaceSquare `yaml:"uspace" json:"uspace"`
}

// USpaceSquare is a square of U-space airspace about a centre, offsets
// from the lab origin.
type USpaceSquare struct {
	Centre    scenario.Offset `yaml:"centre" json:"centre"`
	HalfSideM float64         `yaml:"half_side_m" json:"half_side_m"`
}

func (p *PeerCfg) validate() error {
	switch {
	case p.Aircraft < 1 || p.Aircraft > seed.MaxLoadPeerAircraft:
		return fmt.Errorf("peer.aircraft %d is not in [1, %d]", p.Aircraft, seed.MaxLoadPeerAircraft)
	case !(p.RadiusM > 0 && p.RadiusM <= 2000):
		return fmt.Errorf("peer.radius_m %v is not in (0, 2000]", p.RadiusM)
	case !(p.SpeedMS > 0 && p.SpeedMS <= 30):
		return fmt.Errorf("peer.speed_ms %v is not in (0, 30]", p.SpeedMS)
	case !(p.AltRelM > 0 && p.AltRelM <= 500):
		return fmt.Errorf("peer.alt_rel_m %v is not in (0, 500]", p.AltRelM)
	case !(p.USpace.HalfSideM > 0 && p.USpace.HalfSideM <= 50000):
		return fmt.Errorf("peer.uspace.half_side_m %v is not in (0, 50000]", p.USpace.HalfSideM)
	}
	return nil
}

// PeerSerial is the peer's aircraft i in a fleet of clients operator
// clients: CTA-2063-A like the fleet's (AircraftSerial), P for the peer.
func PeerSerial(i, clients int) string { return fmt.Sprintf("LABXCP%04dN%06d", clients, i) }

// peerSysidBase keeps the peer's vehicle numbers apart from the fleet's.
const peerSysidBase = 1_000_000

// peerAircraft is one of the peer's own flights.
type peerAircraft struct {
	Index  int
	Serial string
	Sysid  int
}

// peerSample is the peer's aircraft a at t seconds after the start.
func (fl *Fleet) peerSample(a *peerAircraft, t float64, now time.Time) vehicle.Sample {
	p := fl.peer
	w := p.SpeedMS / p.RadiusM
	th := 2*math.Pi*float64(a.Index)/float64(p.Aircraft) + w*t
	off := scenario.Offset{NorthM: p.Centre.NorthM + p.RadiusM*math.Cos(th), EastM: p.Centre.EastM + p.RadiusM*math.Sin(th)}
	vn, ve := -p.SpeedMS*math.Sin(th), p.SpeedMS*math.Cos(th)
	pos := scenario.Move(fl.originLatLon(), off)
	track := math.Mod(math.Atan2(ve, vn)*180/math.Pi+360, 360)
	if track >= 360 {
		track = 0
	}
	amsl := fl.origin.AltAMSLM + p.AltRelM
	ts := vehicle.FormatTime(now)
	s := vehicle.Sample{Schema: vehicle.Schema, Sysid: a.Sysid, TS: &ts, LatDeg: pos.LatDeg, LonDeg: pos.LonDeg, AltAMSLM: amsl,
		HeightTakeoffM: p.AltRelM, SpeedMS: p.SpeedMS, TrackDeg: &track, VNMS: vn, VEMS: ve, Armed: true, Status: vehicle.StatusAirborne, FixOK: true}
	if fl.geo != nil {
		if n, err := fl.geo.UndulationM(pos); err == nil {
			s.AltHAEM = vehicle.F(geoid.HAEFromAMSL(amsl, n))
		}
	}
	return s
}

// inUSpace says whether aircraft a's intent can reach into the peer's
// U-space airspace square: its outline (a walker's disc, a pair
// member's box) meets the square. Such an intent is DSS-backed, and the
// USSP owes the peer a notification of each of its changes.
func (fl *Fleet) inUSpace(a *Aircraft) bool {
	if fl.peer == nil {
		return false
	}
	sq := fl.peer.USpace
	h := sq.HalfSideM
	// The outline as a box of offsets [s, w, n, e].
	var s, w, n, e float64
	switch a.Role {
	case RolePair:
		b := fl.pairBox(a)
		s, n = a.centre.NorthM-pairIntentMarginM, a.centre.NorthM+pairIntentMarginM
		w, e = a.centre.EastM+b.WestM, a.centre.EastM+b.EastM
	default:
		r := fl.paths.Walkers.RadiusM + pairIntentMarginM
		s, n, w, e = a.centre.NorthM-r, a.centre.NorthM+r, a.centre.EastM-r, a.centre.EastM+r
	}
	return n >= sq.Centre.NorthM-h && s <= sq.Centre.NorthM+h && e >= sq.Centre.EastM-h && w <= sq.Centre.EastM+h
}

// peerArea is the authority's oversight area for a run with a peer: the
// fleet's walks, the pairs' lines, the peer's circle and the U-space
// square, 500 m round, as [west, south, east, north].
func (fl *Fleet) peerArea() [4]float64 {
	s, w, n, e := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(sn, we, nn, ee float64) {
		s, w, n, e = math.Min(s, sn), math.Min(w, we), math.Max(n, nn), math.Max(e, ee)
	}
	for _, a := range fl.Aircraft {
		r := fl.paths.Walkers.RadiusM
		if a.Role == RolePair {
			r = fl.paths.Pairs.AmplitudeM
		}
		grow(a.centre.NorthM-r, a.centre.EastM-r, a.centre.NorthM+r, a.centre.EastM+r)
	}
	p := fl.peer
	grow(p.Centre.NorthM-p.RadiusM, p.Centre.EastM-p.RadiusM, p.Centre.NorthM+p.RadiusM, p.Centre.EastM+p.RadiusM)
	h := p.USpace.HalfSideM
	grow(p.USpace.Centre.NorthM-h, p.USpace.Centre.EastM-h, p.USpace.Centre.NorthM+h, p.USpace.Centre.EastM+h)
	const margin = 500
	sw := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: s - margin, EastM: w - margin})
	ne := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: n + margin, EastM: e + margin})
	return [4]float64{sw.LonDeg, sw.LatDeg, ne.LonDeg, ne.LatDeg}
}

// Bounds of the peer's bookkeeping (E-10).
const (
	peerRing           = 128 // positions kept per peer aircraft
	peerTieWindow      = 500 * time.Millisecond
	peerTieDistanceM   = 50
	maxPeerNotes       = 200_000
	maxDetailsInFlight = 4
	peerNotifyWait     = 15 * time.Second
	detailsTimeout     = 10 * time.Second
	maxListedPeer      = 20
)

// servedPos is one position of a peer aircraft: where, and when it was
// first in an answer to the display provider (zero: not yet).
type servedPos struct {
	at       time.Time
	pos      core.LatLon
	served   time.Time
	shown    bool
	occupied bool
}

// intentOps are the lab's requests that changed one intent, each when it
// was sent (the start a notification of that change is timed from).
type intentOps struct {
	aircraft string
	inUSpace bool
	filed    time.Time
	active   time.Time
	ended    time.Time
	// notified are the states a notification carried ("" deleted).
	notified map[string]bool
}

// peerRun is the peer of one run.
type peerRun struct {
	tgt      *runner.LoadPeer
	fleet    *Fleet
	usspBase string
	peer     *simussp.Peer
	ver      *auth.Verifier
	srv      *http.Server
	details  *oauth.ClientCredentials
	tokens   map[string]*oauth.ClientCredentials
	secret   string
	bySerial map[string]int
	byFlight map[string]int

	mu          sync.Mutex
	pos         [][peerRing]servedPos
	next        []int
	display     *Histogram
	dpPolls     uint64
	otherPolls  uint64
	peerFrames  uint64
	untied      uint64
	fleetTracks map[string]bool
	ops         map[string]*intentOps
	// early are notifications that arrived before the request that made
	// their change was noted (an inline DSS write answers after it
	// notifies); they are timed once it is.
	early       map[string][]simussp.Notification
	nEarly      int
	notes       uint64
	unmatched   uint64
	byState     map[string]uint64
	change      *Histogram
	detailsH    *Histogram
	detailsOK   uint64
	detailsFail uint64
	detailsLast string
	inFlight    chan struct{}
	errs        []string
	wg          sync.WaitGroup
}

// newPeerRun reads the peer's credentials and checks what was seeded
// against the tier: a refusal here starts nothing.
func newPeerRun(tg *runner.Targets, fl *Fleet, usspBase string) (*peerRun, error) {
	t := tg.Load.Peer
	if t == nil {
		return nil, fmt.Errorf("load: the tier has a peer and the targets file names none: seed again (loadgen seed)")
	}
	if len(t.Serials) != len(fl.Peer) {
		return nil, fmt.Errorf("load: the targets file's peer has %d aircraft, the tier %d: seed again", len(t.Serials), len(fl.Peer))
	}
	pr := &peerRun{tgt: t, fleet: fl, usspBase: usspBase, bySerial: map[string]int{}, byFlight: map[string]int{}, display: NewHistogram(),
		fleetTracks: map[string]bool{}, ops: map[string]*intentOps{}, early: map[string][]simussp.Notification{}, byState: map[string]uint64{}, change: NewHistogram(), detailsH: NewHistogram(),
		inFlight: make(chan struct{}, maxDetailsInFlight), tokens: map[string]*oauth.ClientCredentials{},
		pos: make([][peerRing]servedPos, len(fl.Peer)), next: make([]int, len(fl.Peer))}
	for i, a := range fl.Peer {
		if t.Serials[i] != a.Serial {
			return nil, fmt.Errorf("load: the targets file's peer aircraft %d is %s, the tier's %s: seed again", i, t.Serials[i], a.Serial)
		}
		pr.bySerial[a.Serial], pr.byFlight[simussp.FlightID(a.Serial)] = i, i
	}
	if t.Listen == "" || t.BaseURL == "" || t.Audience == "" || t.DSSRIDBase == "" || t.DSSUTMBase == "" || t.Issuer == "" || t.JWKSFile == "" || t.DisplayProvider == "" {
		return nil, fmt.Errorf("load: the targets file's peer names no listen address, base URL, audience, DSS, issuer, JWKS or display provider")
	}
	var err error
	if pr.secret, err = tg.Secret(t.DSS.SecretFile); err != nil {
		return nil, fmt.Errorf("load: the peer's client secret: %w", err)
	}
	if pr.secret == "" {
		return nil, fmt.Errorf("load: the peer's client secret file is empty")
	}
	if t.USSP.ClientID == "" || t.USSP.Audience == "" || t.USSP.TokenURL == "" {
		return nil, fmt.Errorf("load: the targets file's peer names no client for the USSP's details")
	}
	usspSecret, err := tg.Secret(t.USSP.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("load: the peer's USSP client secret: %w", err)
	}
	pr.details = &oauth.ClientCredentials{TokenURL: t.USSP.TokenURL, ClientID: t.USSP.ClientID, ClientSecret: usspSecret, Audience: t.USSP.Audience, Scopes: t.USSP.Scopes}
	keys, err := cli.ReadJWKS(tg.File(t.JWKSFile))
	if err != nil {
		return nil, fmt.Errorf("load: the peer's issuer JWKS: %w", err)
	}
	if pr.ver, err = auth.NewVerifier(context.Background(), auth.Config{Issuers: map[string]auth.IssuerConfig{t.Issuer: {Keys: keys}}, Audience: t.Audience}); err != nil {
		return nil, fmt.Errorf("load: the peer's verifier: %w", err)
	}
	return pr, nil
}

func (pr *peerRun) fail(format string, a ...any) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if len(pr.errs) < maxListed {
		pr.errs = append(pr.errs, fmt.Sprintf(format, a...))
	}
}

// tokensFor is the peer's token source for audience (cached).
func (pr *peerRun) tokensFor(aud string, scopes ...string) *oauth.ClientCredentials {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	k := aud + " " + strings.Join(scopes, " ")
	if c := pr.tokens[k]; c != nil {
		return c
	}
	c := &oauth.ClientCredentials{TokenURL: pr.tgt.DSS.TokenURL, ClientID: pr.tgt.DSS.ClientID, ClientSecret: pr.secret, Audience: aud, Scopes: scopes}
	pr.tokens[k] = c
	return c
}

// start serves the peer, feeds its aircraft until ctx ends, writes its
// ISA (notifying the display providers subscribed there) and its
// subscription over the U-space square, for until.
func (pr *peerRun) start(ctx context.Context, until time.Time, period time.Duration) error {
	fl := pr.fleet
	acs := make([]simussp.Aircraft, 0, len(fl.Peer))
	for _, a := range fl.Peer {
		acs = append(acs, simussp.Aircraft{Sysid: a.Sysid, Serial: a.Serial})
	}
	c := scenario.Move(fl.originLatLon(), fl.peer.Centre)
	n, err := fl.geo.UndulationM(c)
	if err != nil {
		return fmt.Errorf("peer: geoid: %w", err)
	}
	amsl := fl.origin.AltAMSLM + fl.peer.AltRelM
	p, err := simussp.New(simussp.Config{
		BaseURL: pr.tgt.BaseURL, DSSRIDBase: pr.tgt.DSSRIDBase, DSSUTMBase: pr.tgt.DSSUTMBase,
		Tokens: pr.tokensFor(pr.tgt.DSS.Audience, pr.tgt.DSS.Scopes...),
		Verify: func(ctx context.Context, tok string) ([]string, error) {
			cl, err := pr.ver.Verify(ctx, tok)
			return cl.Scopes, err
		},
		ISACenter: c, ISARadiusM: fl.peer.RadiusM + 200,
		ISAAltLow: geoid.HAEFromAMSL(amsl-100, n), ISAAltHigh: geoid.HAEFromAMSL(amsl+100, n),
		ISAFor: time.Until(until), Aircraft: acs, RecentMax: 64,
		OnFlights: pr.onFlights, OnNotification: pr.onNotification,
		NotifyTokens: func(aud string) simussp.TokenSource { return pr.tokensFor(aud, "rid.service_provider") },
	})
	if err != nil {
		return fmt.Errorf("peer: %w", err)
	}
	pr.peer = p
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", pr.tgt.Listen)
	if err != nil {
		return fmt.Errorf("peer: listen %s: %w", pr.tgt.Listen, err)
	}
	pr.srv = &http.Server{Handler: p.Handler(), ReadHeaderTimeout: 5 * time.Second}
	pr.wg.Add(2)
	go func() { defer pr.wg.Done(); _ = pr.srv.Serve(ln) }()
	go func() { defer pr.wg.Done(); pr.feed(ctx, period) }()
	if err := p.PutISA(ctx); err != nil {
		return fmt.Errorf("peer: ISA: %w", err)
	}
	if e := p.NotifyError(); e != "" {
		pr.fail("peer: an ISA notification failed: %s", e)
	}
	sq := fl.peer.USpace
	sc := scenario.Move(fl.originLatLon(), sq.Centre)
	sn, err := fl.geo.UndulationM(sc)
	if err != nil {
		return fmt.Errorf("peer: geoid: %w", err)
	}
	low, high := geoid.HAEFromAMSL(fl.origin.AltAMSLM-100, sn), geoid.HAEFromAMSL(fl.origin.AltAMSLM+1600, sn)
	r := float32(sq.HalfSideM * math.Sqrt2)
	now := time.Now().UTC()
	v := f3548.Volume4D{
		TimeStart: &f3548.Time{Format: f3548.RFC3339, Value: now},
		TimeEnd:   &f3548.Time{Format: f3548.RFC3339, Value: until.UTC()},
		Volume: f3548.Volume3D{
			AltitudeLower: &f3548.Altitude{Reference: f3548.W84, Units: f3548.AltitudeUnitsM, Value: low},
			AltitudeUpper: &f3548.Altitude{Reference: f3548.W84, Units: f3548.AltitudeUnitsM, Value: high},
			OutlineCircle: &f3548.Circle{Center: &f3548.LatLngPoint{Lat: sc.LatDeg, Lng: sc.LonDeg}, Radius: &f3548.Radius{Units: f3548.RadiusUnitsM, Value: r}},
		},
	}
	if err := p.PutSubscription(ctx, v); err != nil {
		return fmt.Errorf("peer: subscription: %w", err)
	}
	return nil
}

// feed hands the peer its aircraft's samples every period until ctx
// ends, and keeps each position for the display's tie.
func (pr *peerRun) feed(ctx context.Context, period time.Duration) {
	t0 := time.Now()
	tk := time.NewTicker(period)
	defer tk.Stop()
	for {
		now := time.Now()
		for i, a := range pr.fleet.Peer {
			s := pr.fleet.peerSample(a, now.Sub(t0).Seconds(), now)
			at, _ := s.Time()
			pr.mu.Lock()
			pr.pos[i][pr.next[i]] = servedPos{at: at, pos: s.Pos(), occupied: true}
			pr.next[i] = (pr.next[i] + 1) % peerRing
			pr.mu.Unlock()
			pr.peer.Observe(&s)
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// subjectOf is the verified subject of r's bearer token ("" none).
func (pr *peerRun) subjectOf(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	cl, err := pr.ver.Verify(r.Context(), tok)
	if err != nil {
		return ""
	}
	return cl.Subject
}

// onFlights notes, for the display provider's polls only, when each
// position served was first in an answer: the SP response a display is
// timed from.
func (pr *peerRun) onFlights(r *http.Request, at time.Time, served map[string]time.Time) {
	pr.recordServed(pr.subjectOf(r) == pr.tgt.DisplayProvider, at, served)
}

// recordServed notes an answer at at: dp says whether the display
// provider asked.
func (pr *peerRun) recordServed(dp bool, at time.Time, served map[string]time.Time) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if !dp {
		pr.otherPolls++
		return
	}
	pr.dpPolls++
	for id, posAt := range served {
		i, ok := pr.byFlight[id]
		if !ok {
			continue
		}
		for k := range pr.pos[i] {
			e := &pr.pos[i][k]
			if e.occupied && e.at.Equal(posAt) && e.served.IsZero() {
				e.served = at
			}
		}
	}
}

// network is a network_rid frame on the timed console: a peer
// aircraft's (source_instance the peer's owner at the DSS) is tied to
// the position it shows and its display timed; the fleet's (from the
// USSP's SP) are counted by track.
func (pr *peerRun) network(instance, trackID string, pos core.LatLon, captured, arrived time.Time) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if instance != pr.tgt.DSS.ClientID {
		if len(pr.fleetTracks) < maxTrackIDs {
			pr.fleetTracks[trackID] = true
		}
		return
	}
	pr.peerFrames++
	var best *servedPos
	bestD := time.Duration(math.MaxInt64)
	for i := range pr.pos {
		for k := range pr.pos[i] {
			e := &pr.pos[i][k]
			if !e.occupied {
				continue
			}
			d := e.at.Sub(captured)
			if d < 0 {
				d = -d
			}
			if d > peerTieWindow || d >= bestD {
				continue
			}
			if m, err := geodesy.DistanceM(e.pos, pos); err != nil || m > peerTieDistanceM {
				continue
			}
			best, bestD = e, d
		}
	}
	switch {
	case best == nil || best.served.IsZero():
		// A position the display provider was never answered, or none of
		// the peer's: the display cannot be timed, and it is counted.
		pr.untied++
	case best.shown:
	default:
		best.shown = true
		pr.display.Add(arrived.Sub(best.served))
	}
}

// filed, activated and ended note the lab's request that made each
// change, before it is sent.
func (pr *peerRun) noteOp(id string, a *Aircraft, state string, at time.Time) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	op := pr.ops[id]
	if op == nil {
		op = &intentOps{aircraft: a.Name, inUSpace: pr.fleet.inUSpace(a), notified: map[string]bool{}}
		pr.ops[id] = op
	}
	switch state {
	case string(f3548.Accepted):
		op.filed = at
	case string(f3548.Activated):
		op.active = at
	case "":
		op.ended = at
	}
	for _, n := range pr.early[id] {
		pr.timeLocked(op, n)
	}
	pr.nEarly -= len(pr.early[id])
	delete(pr.early, id)
}

// timeLocked times notification n of op's change, the first of each
// state only.
func (pr *peerRun) timeLocked(op *intentOps, n simussp.Notification) {
	from := time.Time{}
	switch n.State {
	case string(f3548.Accepted):
		from = op.filed
	case string(f3548.Activated):
		from = op.active
	case "":
		from = op.ended
	}
	if op.notified[n.State] || from.IsZero() {
		// A second notification of a state, or one before the request
		// that makes it was noted: kept for when it is.
		if !op.notified[n.State] && pr.nEarly < maxPeerNotes {
			pr.early[n.EntityID] = append(pr.early[n.EntityID], n)
			pr.nEarly++
		}
		return
	}
	op.notified[n.State] = true
	pr.change.Add(n.At.Sub(from))
}

// onNotification times an F3548 notification from the request that
// made the change it carries, and asks the USSP for the notified
// intent's details.
func (pr *peerRun) onNotification(n simussp.Notification) {
	if n.Standard != "f3548" {
		return
	}
	pr.mu.Lock()
	pr.notes++
	pr.byState[stateName(n.State)]++
	if op := pr.ops[n.EntityID]; op != nil {
		pr.timeLocked(op, n)
	} else if pr.nEarly < maxPeerNotes {
		pr.early[n.EntityID] = append(pr.early[n.EntityID], n)
		pr.nEarly++
	}
	pr.mu.Unlock()
	if n.State == "" {
		return
	}
	select {
	case pr.inFlight <- struct{}{}:
		pr.wg.Add(1)
		go func() {
			defer pr.wg.Done()
			defer func() { <-pr.inFlight }()
			pr.askDetails(n.EntityID)
		}()
	default:
		pr.mu.Lock()
		pr.detailsFail++
		pr.detailsLast = "more than 4 details requests in flight: not asked"
		pr.mu.Unlock()
	}
}

func stateName(s string) string {
	if s == "" {
		return "deleted"
	}
	return s
}

// askDetails asks the USSP for an intent's details as the peer
// (F3548 GET /uss/v1/operational_intents/{entityid}) and times the
// answer.
func (pr *peerRun) askDetails(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), detailsTimeout)
	defer cancel()
	// A request the USSP does not answer with 200 is timed at the
	// request's bound: no answer is no answer within F3548's second.
	failed := func(why string) {
		pr.mu.Lock()
		pr.detailsFail++
		pr.detailsLast = why
		pr.detailsH.Add(detailsTimeout)
		pr.mu.Unlock()
	}
	tok, err := pr.details.Token(ctx)
	if err != nil {
		failed("token: " + err.Error())
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pr.usspBase+"/uss/v1/operational_intents/"+url.PathEscape(id), http.NoBody)
	if err != nil {
		failed(err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	t0 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failed(err.Error())
		return
	}
	_ = resp.Body.Close()
	took := time.Since(t0)
	if resp.StatusCode != http.StatusOK {
		failed(fmt.Sprintf("GET /uss/v1/operational_intents/%s answered %d", id, resp.StatusCode))
		return
	}
	pr.mu.Lock()
	pr.detailsH.Add(took)
	pr.detailsOK++
	pr.mu.Unlock()
}

// awaitNotifications waits, bounded, until every change owed to the
// peer has been notified.
func (pr *peerRun) awaitNotifications(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, peerNotifyWait)
	defer cancel()
	tk := time.NewTicker(100 * time.Millisecond)
	defer tk.Stop()
	for {
		if _, missing := pr.missing(); missing == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// missing lists the changes owed to the peer and not notified: every
// filing, activation and end of an intent inside the U-space square.
func (pr *peerRun) missing() (list []string, n int) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	ids := make([]string, 0, len(pr.ops))
	for id := range pr.ops {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		op := pr.ops[id]
		if !op.inUSpace {
			continue
		}
		for _, c := range []struct {
			state string
			at    time.Time
		}{{string(f3548.Accepted), op.filed}, {string(f3548.Activated), op.active}, {"", op.ended}} {
			if c.at.IsZero() || op.notified[c.state] {
				continue
			}
			n++
			if len(list) < maxListedPeer {
				list = append(list, fmt.Sprintf("%s (%s): %s not notified", op.aircraft, id, stateName(c.state)))
			}
		}
	}
	return list, n
}

// owed counts the intents inside the U-space square and the changes the
// peer is owed for them.
func (pr *peerRun) owed() (intents, changes int) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	for _, op := range pr.ops {
		if !op.inUSpace {
			continue
		}
		intents++
		for _, at := range []time.Time{op.filed, op.active, op.ended} {
			if !at.IsZero() {
				changes++
			}
		}
	}
	return intents, changes
}

// stop deletes the peer's subscription and ISA (its subscribers are told)
// and stops serving, bounded.
func (pr *peerRun) stop() {
	if pr.peer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pr.peer.DeleteSubscription(ctx); err != nil {
		pr.fail("peer: subscription not deleted: %v", err)
	}
	if err := pr.peer.DeleteISA(ctx); err != nil {
		pr.fail("peer: ISA not deleted: %v", err)
	}
	if pr.srv != nil {
		_ = pr.srv.Shutdown(ctx)
	}
}

// PeerReport is what the report says of the peer.
type PeerReport struct {
	BaseURL         string            `json:"base_url"`
	Aircraft        []string          `json:"aircraft"`
	DisplayProvider string            `json:"display_provider"`
	DPPolls         uint64            `json:"display_provider_polls"`
	OtherPolls      uint64            `json:"other_polls"`
	PeerFrames      uint64            `json:"peer_frames"`
	Displayed       uint64            `json:"displays_timed"`
	Untied          uint64            `json:"frames_untied"`
	FleetTracks     int               `json:"fleet_network_tracks"`
	IntentsInUSpace int               `json:"intents_in_uspace"`
	ChangesOwed     int               `json:"changes_owed"`
	Notifications   uint64            `json:"notifications"`
	ByState         map[string]uint64 `json:"notifications_by_state"`
	Unmatched       uint64            `json:"notifications_unmatched"`
	Missing         []string          `json:"missing,omitempty"`
	DetailsAnswered uint64            `json:"details_answered"`
	DetailsFailed   uint64            `json:"details_failed"`
	DetailsLast     string            `json:"details_last_error,omitempty"`
	Counters        map[string]uint64 `json:"counters,omitempty"`
	Errors          []string          `json:"errors,omitempty"`
}

// Reasons the F3548 rows the peer cannot time stay "not measured".
const (
	niConflict   = "no intent of the run conflicts with another USS's: the peer files none, so no conflicting-intent notification is due"
	niConstraint = "no constraint is created during the run: no system of the lab stack publishes F3548 constraints (uss_qualifier has no constraint management at the pinned commit, WP-L7)"
)

// observe turns the peer into the DP display and F3548 rows.
func (pr *peerRun) observe(obs map[string]Observation, minSamples int) *PeerReport {
	list, missing := pr.missing()
	intents, owed := pr.owed()
	pr.mu.Lock()
	defer pr.mu.Unlock()
	// What is still early named no intent of the run, or a change it
	// never made.
	pr.unmatched = uint64(pr.nEarly)
	rep := &PeerReport{BaseURL: pr.tgt.BaseURL, Aircraft: pr.tgt.Serials, DisplayProvider: pr.tgt.DisplayProvider, DPPolls: pr.dpPolls, OtherPolls: pr.otherPolls,
		PeerFrames: pr.peerFrames, Displayed: pr.display.N(), Untied: pr.untied, FleetTracks: len(pr.fleetTracks),
		IntentsInUSpace: intents, ChangesOwed: owed, Notifications: pr.notes, ByState: map[string]uint64{}, Unmatched: pr.unmatched, Missing: list,
		DetailsAnswered: pr.detailsOK, DetailsFailed: pr.detailsFail, DetailsLast: pr.detailsLast, Errors: append([]string(nil), pr.errs...)}
	for k, v := range pr.byState {
		rep.ByState[k] = v
	}
	if pr.peer != nil {
		rep.Counters = pr.peer.Counters().Snapshot()
	}
	switch {
	case pr.dpPolls == 0:
		obs["f3411_dp_display_s"] = notMeasured("the authority's display provider never polled the peer's Service Provider (its ISA in the oversight area)")
	case pr.peerFrames == 0:
		obs["f3411_dp_display_s"] = notMeasured(fmt.Sprintf("the display provider polled the peer %d times and the timed console showed none of the peer's flights", pr.dpPolls))
	default:
		obs["f3411_dp_display_s"] = pr.display.Observe(minSamples)
	}
	if owed == 0 {
		why := notMeasured("no intent of the fleet was filed inside the U-space airspace square (peer.uspace): no change was owed to the peer")
		obs["f3548_intent_change_s"], obs["f3548_notifications_missing"], obs["f3548_details_s"] = why, why, why
	} else {
		obs["f3548_intent_change_s"] = pr.change.Observe(1)
		obs["f3548_notifications_missing"] = valueObs(float64(missing))
		if pr.detailsH.N() > 0 {
			obs["f3548_details_s"] = pr.detailsH.Observe(1)
		} else {
			obs["f3548_details_s"] = notMeasured("no notification named an intent whose details could be asked")
		}
	}
	obs["f3548_conflict_notify_s"] = notMeasured(niConflict)
	obs["f3548_constraint_notify_s"] = notMeasured(niConstraint)
	return rep
}

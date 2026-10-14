package load

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// Stream kinds.
const (
	streamPicture = "picture" // the authority's console picture
	streamTraffic = "traffic" // one flight's USSP traffic stream
)

// stream is one WebSocket a run reads, with what it saw.
type stream struct {
	name   string
	kind   string
	url    string
	header http.Header
	// headerFn, when set, gives the header of each connection instead;
	// renew asks for fresh credentials (the last connection was refused
	// as unauthorised: an upgrade answered 401, or a close 4401).
	headerFn func(ctx context.Context, renew bool) (http.Header, error)
	renew    bool
	onOpen   []byte
	aircraft *Aircraft // traffic: whose flight
	timed    bool      // its frames feed a ledger

	mu         sync.Mutex
	frames     map[string]uint64
	connects   int
	lastErr    string
	dropped    uint64 // the largest dropped_frames a status said
	lastStatus time.Time
	// lastData is the arrival of the last frame that showed a sample
	// for the first time (a track or a traffic product); status frames
	// and repeated states keep coming after the generator stops and say
	// nothing about what is still in flight.
	lastData  time.Time
	ready     chan struct{} // closed on the first status (and snapshot, when subscribing)
	readyOnce sync.Once
	needSnap  bool
	gotStatus bool
	degraded  map[string]bool
	// current is the degraded set of the latest status.
	current     []string
	evalPeriodS float64
	// breaks are the connections that ended or failed (bounded);
	// overBreaks those past the bound.
	breaks     []streamBreak
	overBreaks int
}

// streamBreak is one connection of a stream that ended or failed: when,
// why, whether it asked for fresh credentials, and how many connections
// had opened by then.
type streamBreak struct {
	at       time.Time
	err      string
	renew    bool
	connects int
}

// maxBreaks bounds the breaks kept per stream (E-10); later ones are
// counted as unexcused.
const maxBreaks = 1000

// unexcused counts the breaks that neither fell inside a fault the run
// injected (excused, nil: none) nor were a refusal of expired
// credentials followed by a connection on fresh ones, and returns the
// last of them. What a break loses shows as unshown samples in the
// ledgers either way; an unexcused one is a run error because nothing
// the run did explains it.
func (s *stream) unexcused(excused func(time.Time) bool) (n int, last string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.breaks {
		switch {
		case excused != nil && excused(b.at):
		case b.renew && s.connects > b.connects:
		default:
			n++
			last = b.err
		}
	}
	if s.overBreaks > 0 {
		n += s.overBreaks
	}
	return n, last
}

// StreamStats is what the report shows of a stream.
type StreamStats struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Aircraft string `json:"aircraft,omitempty"`
	Timed    bool   `json:"timed"`
	Connects int    `json:"connects"`
	// Breaks counts the connections that ended or failed; Unexcused
	// those outside every fault and credential renewal.
	Breaks        int               `json:"breaks"`
	Unexcused     int               `json:"breaks_unexcused"`
	Frames        map[string]uint64 `json:"frames"`
	DroppedFrames uint64            `json:"dropped_frames"`
	Degraded      []string          `json:"degraded,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
}

func (s *stream) stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StreamStats{Name: s.name, Kind: s.kind, Timed: s.timed, Connects: s.connects, Breaks: len(s.breaks) + s.overBreaks,
		Frames: map[string]uint64{}, DroppedFrames: s.dropped, LastError: s.lastErr}
	if s.aircraft != nil {
		st.Aircraft = s.aircraft.Name
	}
	for k, v := range s.frames {
		st.Frames[k] = v
	}
	for d := range s.degraded {
		st.Degraded = append(st.Degraded, d)
	}
	return st
}

// readers holds what every stream feeds.
type readers struct {
	rec     *observe.Recorder
	picture *Ledger // receiver -> authority picture (the timed console)
	traffic *Ledger // operator -> USSP traffic stream
	bySer   map[string]*Aircraft
	byMAC   map[string]*Aircraft
	// raised maps an alert id to when the sample its first frame names
	// was handed out, found when the frame arrives (the ledger keeps a
	// minute of samples, the run may last hours). Bounded (E-10).
	raisedMu sync.Mutex
	raised   map[string]time.Time
	// peers names each scripted pair member's peer.
	peers map[string]string
	// byTrack ties a picture track id to its aircraft once a frame of the
	// track named the serial; held keeps the frames of a track not tied
	// yet (the authority shows a track before it has heard the Basic
	// ID). Both bounded (E-10).
	trackMu sync.Mutex
	byTrack map[string]*Aircraft
	held    map[string][]heldFrame
	nHeld   int
	// onShown is told every sample newly shown (the faults' recovery).
	onShown func(kind string, at time.Time)
	// flags are the degraded states the streams showed for the first
	// time, with when (bounded).
	flagMu sync.Mutex
	flags  []flagSeen
}

const maxFlags = 10_000

func (rd *readers) shown(kind string, at time.Time) {
	if rd.onShown != nil {
		rd.onShown(kind, at)
	}
}

func (rd *readers) flagSeen(s *stream, flag string, at time.Time) {
	system := scenario.SystemAuthority
	if s.kind == streamTraffic {
		system = scenario.SystemUSSP
	}
	rd.flagMu.Lock()
	if len(rd.flags) < maxFlags {
		rd.flags = append(rd.flags, flagSeen{system: system, flag: flag, at: at})
	}
	rd.flagMu.Unlock()
}

// heldFrame is a picture frame waiting for its track to be identified.
type heldFrame struct {
	captured, arrived time.Time
	s                 *stream
}

// Bounds of the held frames and the track ids learned.
const (
	maxHeldPerTrack = 32
	maxHeld         = 50_000
	maxTrackIDs     = 200_000
)

const maxRaised = 100_000

// handedFor returns when the sample an alert's first frame named was
// handed out.
func (rd *readers) handedFor(alertID string) (time.Time, bool) {
	rd.raisedMu.Lock()
	defer rd.raisedMu.Unlock()
	t, ok := rd.raised[alertID]
	return t, ok
}

// noteAlert ties a traffic stream's alert to its sample the first time
// the alert is seen.
func (rd *readers) noteAlert(s *stream, e wire.Envelope) {
	var b struct {
		AlertID    string `json:"alert_id"`
		CapturedAt string `json:"captured_at"`
	}
	if s.aircraft == nil || json.Unmarshal(e.Body, &b) != nil || b.AlertID == "" {
		return
	}
	captured, err := wire.ParseTime(b.CapturedAt)
	if err != nil {
		return
	}
	rd.raisedMu.Lock()
	defer rd.raisedMu.Unlock()
	if _, seen := rd.raised[b.AlertID]; seen || len(rd.raised) >= maxRaised {
		return
	}
	// The triggering sample of a proximity alert is either aircraft's:
	// the pair is judged when the later of the two samples arrives, so
	// it is the newer of the two the alert's captured_at can show.
	best, found := time.Time{}, false
	for _, name := range []string{s.aircraft.Name, rd.peers[s.aircraft.Name]} {
		if name == "" {
			continue
		}
		if handed, ok := rd.traffic.Find(name, captured); ok && (!found || handed.After(best)) {
			best, found = handed, true
		}
	}
	if found {
		rd.raised[b.AlertID] = best
	}
}

// run reads s until ctx ends, reconnecting after a break (each break is
// counted; what it loses shows as unshown samples, never as silence).
func (rd *readers) run(ctx context.Context, s *stream) {
	for ctx.Err() == nil {
		err := rd.read(ctx, s)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.mu.Lock()
			s.lastErr = err.Error()
			if len(s.breaks) < maxBreaks {
				s.breaks = append(s.breaks, streamBreak{at: time.Now(), err: err.Error(), renew: s.renew, connects: s.connects})
			} else {
				s.overBreaks++
			}
			s.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (rd *readers) read(ctx context.Context, s *stream) error {
	header := s.header
	if s.headerFn != nil {
		h, err := s.headerFn(ctx, s.renew)
		if err != nil {
			return fmt.Errorf("%s: credentials: %w", s.name, err)
		}
		header, s.renew = h, false
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, resp, err := websocket.Dial(dctx, s.url, &websocket.DialOptions{HTTPHeader: header})
	cancel()
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			s.renew = resp.StatusCode == http.StatusUnauthorized
			return fmt.Errorf("%s: upgrade answered %d", s.name, resp.StatusCode)
		}
		return fmt.Errorf("%s: %w", s.name, err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(wire.MaxFrameBytes)
	s.mu.Lock()
	s.connects++
	s.mu.Unlock()
	if len(s.onOpen) > 0 {
		if err := conn.Write(ctx, websocket.MessageText, s.onOpen); err != nil {
			return fmt.Errorf("%s: subscribe: %w", s.name, err)
		}
	}
	st := observe.NewStreamState()
	os := observe.Stream{Name: s.name, System: scenario.SystemAuthority}
	if s.kind == streamTraffic {
		os = observe.Stream{Name: s.name, System: scenario.SystemUSSP, Aircraft: s.aircraft.Name}
	}
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// The authority closes a console whose session ended with
			// 4401 ("sign in again").
			s.renew = websocket.CloseStatus(err) == closeSignInAgain
			return fmt.Errorf("%s: %w", s.name, err)
		}
		rd.handle(s, b, time.Now(), func(b []byte, at time.Time) { rd.rec.Handle(os, st, b, at) })
	}
}

// handle reads one frame; at is its arrival on the generator's clock.
// alert hands an alert/v1 or violation/v1 to the event recorder.
func (rd *readers) handle(s *stream, b []byte, at time.Time, alert func([]byte, time.Time)) {
	e, err := wire.ParseEnvelope(b)
	s.mu.Lock()
	if s.frames == nil {
		s.frames = map[string]uint64{}
	}
	if err != nil {
		s.frames["unparseable"]++
		s.mu.Unlock()
		return
	}
	s.frames[e.Schema]++
	s.mu.Unlock()
	switch e.Schema {
	case wire.SchemaStatus:
		rd.status(s, e, at)
	case wire.SchemaSnapshot:
		s.mu.Lock()
		s.needSnap = false
		ready := s.gotStatus
		s.mu.Unlock()
		if ready {
			s.readyOnce.Do(func() { close(s.ready) })
		}
	case wire.SchemaTrack:
		if s.timed {
			rd.track(s, e, at)
		}
	case trafficProduct:
		if s.timed {
			rd.product(s, e, at)
		}
	case wire.SchemaAlert, wire.SchemaViolation:
		if e.Schema == wire.SchemaAlert {
			rd.noteAlert(s, e)
			rd.evalPeriod(s, e)
		}
		alert(b, at)
	}
}

const trafficProduct = "traffic/product/v1"

// closeSignInAgain is the close code the authority's picture ends a
// console with when its session is no longer valid.
const closeSignInAgain websocket.StatusCode = 4401

// sourceDirectRID is the track source of what a receiver heard
// (schemas/common/envelope/v1 $defs.source).
const sourceDirectRID = "direct_rid"

func (rd *readers) status(s *stream, e wire.Envelope, at time.Time) {
	var b struct {
		DroppedFrames uint64   `json:"dropped_frames"`
		Degraded      []string `json:"degraded"`
	}
	if json.Unmarshal(e.Body, &b) != nil {
		return
	}
	s.mu.Lock()
	s.dropped = max(s.dropped, b.DroppedFrames)
	s.current = append(s.current[:0], b.Degraded...)
	s.lastStatus = at
	s.gotStatus = true
	if s.degraded == nil {
		s.degraded = map[string]bool{}
	}
	var fresh []string
	for _, d := range b.Degraded {
		if !s.degraded[d] {
			fresh = append(fresh, d)
		}
		s.degraded[d] = true
	}
	ready := !s.needSnap
	s.mu.Unlock()
	for _, d := range fresh {
		rd.flagSeen(s, d, at)
	}
	if ready {
		s.readyOnce.Do(func() { close(s.ready) })
	}
}

// track reads a track/telemetry/v1 on the timed console: which aircraft
// (its serial, else the transmitter address in its track id) and when
// it was captured.
func (rd *readers) track(s *stream, e wire.Envelope, at time.Time) {
	var b struct {
		TrackID        string `json:"track_id"`
		Source         string `json:"source"`
		Identification struct {
			Serial *string `json:"serial"`
		} `json:"identification"`
	}
	captured, err := wire.ParseTime(e.CapturedAt)
	if json.Unmarshal(e.Body, &b) != nil || err != nil {
		rd.picture.untraceableFrame()
		return
	}
	if b.Source != sourceDirectRID {
		// The authority's picture also shows what its DP poller reads
		// from the USSPs (network_rid) and the manned feeds; only what
		// a receiver heard is on this ledger.
		return
	}
	var a *Aircraft
	if b.Identification.Serial != nil {
		a = rd.bySer[*b.Identification.Serial]
	}
	if a == nil {
		a = rd.bySer[b.TrackID]
	}
	if a == nil && len(b.TrackID) >= 17 {
		a = rd.byMAC[strings.ToUpper(b.TrackID[len(b.TrackID)-17:])]
	}
	rd.trackMu.Lock()
	if a == nil {
		a = rd.byTrack[b.TrackID]
	}
	if a == nil {
		// Not identified yet: hold it for the frame that names the
		// serial, within the bounds; past them it is untraceable now.
		if len(rd.held[b.TrackID]) < maxHeldPerTrack && rd.nHeld < maxHeld {
			if rd.held == nil {
				rd.held = map[string][]heldFrame{}
			}
			rd.held[b.TrackID] = append(rd.held[b.TrackID], heldFrame{captured: captured, arrived: at, s: s})
			rd.nHeld++
		} else {
			rd.picture.untraceableFrame()
		}
		rd.trackMu.Unlock()
		return
	}
	var release []heldFrame
	if b.TrackID != "" && rd.byTrack[b.TrackID] == nil && len(rd.byTrack) < maxTrackIDs {
		if rd.byTrack == nil {
			rd.byTrack = map[string]*Aircraft{}
		}
		rd.byTrack[b.TrackID] = a
		release = rd.held[b.TrackID]
		delete(rd.held, b.TrackID)
		rd.nHeld -= len(release)
	}
	rd.trackMu.Unlock()
	for _, h := range release {
		if _, ok := rd.picture.Show(a.Name, h.captured, h.arrived); ok {
			h.s.noteData(h.arrived)
			rd.shown(streamPicture, h.arrived)
		}
	}
	if _, ok := rd.picture.Show(a.Name, captured, at); ok {
		s.noteData(at)
		rd.shown(streamPicture, at)
	}
}

// closeHeld counts every frame still held, of a track never
// identified, as untraceable (the end of the run).
func (rd *readers) closeHeld() {
	rd.trackMu.Lock()
	defer rd.trackMu.Unlock()
	for id, hs := range rd.held {
		for range hs {
			rd.picture.untraceableFrame()
		}
		delete(rd.held, id)
	}
	rd.nHeld = 0
}

// currentFlags is what the streams say is degraded now: the timed
// console's latest status (picture) and the union of the traffic
// streams' latest (traffic).
func (rd *readers) currentFlags(streams []*stream) map[string][]string {
	out := map[string][]string{}
	seen := map[string]map[string]bool{streamPicture: {}, streamTraffic: {}}
	for _, s := range streams {
		if s.kind == streamPicture && !s.timed {
			continue
		}
		s.mu.Lock()
		for _, f := range s.current {
			if !seen[s.kind][f] {
				seen[s.kind][f] = true
				out[s.kind] = append(out[s.kind], f)
			}
		}
		s.mu.Unlock()
	}
	return out
}

// noteData records that a frame arriving at at showed a sample for the
// first time. A frame that repeats what was shown is not data: the
// systems go on sending a track's last state (stale) after the
// generator stops, and the drain waits for data to stop.
func (s *stream) noteData(at time.Time) {
	s.mu.Lock()
	if at.After(s.lastData) {
		s.lastData = at
	}
	s.mu.Unlock()
}

// product reads a traffic/product/v1: the subscriber's own track and its
// time of report.
func (rd *readers) product(s *stream, e wire.Envelope, at time.Time) {
	var b struct {
		Tracks []struct {
			Own          bool   `json:"own"`
			State        string `json:"state"`
			TimeOfReport string `json:"time_of_report"`
		} `json:"tracks"`
	}
	if json.Unmarshal(e.Body, &b) != nil {
		return
	}
	for _, t := range b.Tracks {
		if !t.Own || t.State != "live" {
			continue
		}
		tor, err := wire.ParseTime(t.TimeOfReport)
		if err != nil {
			rd.traffic.untraceableFrame()
			continue
		}
		if _, ok := rd.traffic.Show(s.aircraft.Name, tor, at); ok {
			s.noteData(at)
			rd.shown(streamTraffic, at)
		}
	}
}

// evalPeriod keeps the largest evaluation_period_s a proximity alert
// carried (05 §5: the CPA tick widens under load and says so).
func (rd *readers) evalPeriod(s *stream, e wire.Envelope) {
	var b struct {
		Kind   string `json:"kind"`
		Detail struct {
			EvaluationPeriodS *float64 `json:"evaluation_period_s"`
		} `json:"detail"`
	}
	if json.Unmarshal(e.Body, &b) != nil || b.Kind != "proximity" || b.Detail.EvaluationPeriodS == nil {
		return
	}
	s.mu.Lock()
	s.evalPeriodS = max(s.evalPeriodS, *b.Detail.EvaluationPeriodS)
	s.mu.Unlock()
}

// untraceableFrame counts a frame that names no aircraft of the fleet.
func (l *Ledger) untraceableFrame() {
	l.mu.Lock()
	l.untraceable++
	l.mu.Unlock()
}

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
	name     string
	kind     string
	url      string
	header   http.Header
	onOpen   []byte
	aircraft *Aircraft // traffic: whose flight
	timed    bool      // its frames feed a ledger

	mu          sync.Mutex
	frames      map[string]uint64
	connects    int
	lastErr     string
	dropped     uint64 // the largest dropped_frames a status said
	lastStatus  time.Time
	lastFrame   time.Time
	ready       chan struct{} // closed on the first status (and snapshot, when subscribing)
	readyOnce   sync.Once
	needSnap    bool
	gotStatus   bool
	degraded    map[string]bool
	evalPeriodS float64
}

// StreamStats is what the report shows of a stream.
type StreamStats struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Aircraft      string            `json:"aircraft,omitempty"`
	Timed         bool              `json:"timed"`
	Connects      int               `json:"connects"`
	Frames        map[string]uint64 `json:"frames"`
	DroppedFrames uint64            `json:"dropped_frames"`
	Degraded      []string          `json:"degraded,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
}

func (s *stream) stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StreamStats{Name: s.name, Kind: s.kind, Timed: s.timed, Connects: s.connects, Frames: map[string]uint64{}, DroppedFrames: s.dropped, LastError: s.lastErr}
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
}

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
	// the pair is judged when the later of the two samples arrives.
	best, found := time.Time{}, false
	for _, name := range []string{s.aircraft.Name, rd.peers[s.aircraft.Name]} {
		if name == "" {
			continue
		}
		if handed, ok := rd.traffic.Find(name, captured); ok && (!found || absDur(handed.Sub(captured)) < absDur(best.Sub(captured))) {
			best, found = handed, true
		}
	}
	if found {
		rd.raised[b.AlertID] = best
	}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
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
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, resp, err := websocket.Dial(dctx, s.url, &websocket.DialOptions{HTTPHeader: s.header})
	cancel()
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
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
	s.lastFrame = at
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
			rd.track(e, at)
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
	s.lastStatus = at
	s.gotStatus = true
	if s.degraded == nil {
		s.degraded = map[string]bool{}
	}
	for _, d := range b.Degraded {
		s.degraded[d] = true
	}
	ready := !s.needSnap
	s.mu.Unlock()
	if ready {
		s.readyOnce.Do(func() { close(s.ready) })
	}
}

// track reads a track/telemetry/v1 on the timed console: which aircraft
// (its serial, else the transmitter address in its track id) and when
// it was captured.
func (rd *readers) track(e wire.Envelope, at time.Time) {
	var b struct {
		TrackID        string `json:"track_id"`
		Identification struct {
			Serial *string `json:"serial"`
		} `json:"identification"`
	}
	captured, err := wire.ParseTime(e.CapturedAt)
	if json.Unmarshal(e.Body, &b) != nil || err != nil {
		rd.picture.untraceableFrame()
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
	if a == nil {
		rd.picture.untraceableFrame()
		return
	}
	rd.picture.Show(a.Name, captured, at)
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
		rd.traffic.Show(s.aircraft.Name, tor, at)
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

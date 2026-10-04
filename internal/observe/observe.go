// Package observe collects what the systems say during a scenario: the
// console frames of each system's stream (alert/v1 from a USSP's
// /v1/alerts, violation/v1 and console/status/v1 from the authority's
// picture, track/manned/v1 from the ANSP's feed), normalised into events
// with the time the runner received them. Each frame is read from the
// system's own published contract; nothing is inferred that a frame does
// not say (LESSONS E-04).
package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/wire"
)

// Phases of an event.
const (
	PhaseRaised  = "raised"
	PhaseUpdated = "updated"
	PhaseCleared = "cleared"
)

// Event kinds the runner makes out of non-alert frames.
const (
	KindDegraded    = "degraded"     // a slug in a console/status/v1 degraded[]
	KindMannedTrack = "manned_track" // a manned aircraft live (raised) or aged (cleared, reason = state)
)

// Event is one observed transition.
type Event struct {
	System     string         `json:"system"`
	Kind       string         `json:"kind"`
	AlertID    string         `json:"alert_id"`
	Phase      string         `json:"phase"`
	Reason     string         `json:"reason,omitempty"`
	Severity   string         `json:"severity,omitempty"`
	Aircraft   string         `json:"aircraft,omitempty"`
	Peer       string         `json:"peer,omitempty"`
	Subject    string         `json:"subject,omitempty"`
	ObservedAt time.Time      `json:"observed_at"`
	CapturedAt *time.Time     `json:"captured_at,omitempty"`
	RaisedAt   *time.Time     `json:"raised_at,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
	// Stream names where it was read (for the report).
	Stream string `json:"stream"`
}

// Recorder keeps every event and per-stream frame counts.
type Recorder struct {
	mu     sync.Mutex
	events []Event
	frames map[string]map[string]uint64
	errors map[string]string
	// flights maps a USSP flight id to the scenario aircraft whose alert
	// stream named it, so a peer track id resolves.
	flights map[string]string
	serials map[string]string
}

// NewRecorder makes a recorder that resolves serials to aircraft names.
func NewRecorder(serials map[string]string) *Recorder {
	return &Recorder{frames: map[string]map[string]uint64{}, errors: map[string]string{}, flights: map[string]string{}, serials: serials}
}

// Add records an event.
func (r *Recorder) Add(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *Recorder) frame(stream, schema string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frames[stream] == nil {
		r.frames[stream] = map[string]uint64{}
	}
	r.frames[stream][schema]++
}

func (r *Recorder) fail(stream string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[stream] = err.Error()
}

// Events returns the events in observation order, with peers resolved.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	for i := range out {
		if out[i].Peer != "" {
			if name, ok := r.flights[out[i].Peer]; ok {
				out[i].Peer = name
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.Before(out[j].ObservedAt) })
	return out
}

// Frames returns the frame counts per stream and schema.
func (r *Recorder) Frames() map[string]map[string]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]map[string]uint64{}
	for s, m := range r.frames {
		out[s] = map[string]uint64{}
		for k, v := range m {
			out[s][k] = v
		}
	}
	return out
}

// Errors returns the last error of each stream that failed.
func (r *Recorder) Errors() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.errors {
		out[k] = v
	}
	return out
}

// Stream is one WebSocket the runner reads.
type Stream struct {
	Name   string
	System string
	URL    string
	Header http.Header
	// Aircraft is the scenario aircraft the stream belongs to (a USSP
	// alert stream is one intent's).
	Aircraft string
	// Retry reconnects after a break until ctx ends.
	Retry time.Duration
	// OnOpen is a text frame sent on every (re)connection before
	// reading: the authority's picture sends violation/v1 only for the
	// viewport a console subscribed to (console/subscribe/v1).
	OnOpen []byte
}

// Run reads the stream until ctx ends, reconnecting after a break.
func (r *Recorder) Run(ctx context.Context, s Stream) {
	retry := s.Retry
	if retry <= 0 {
		retry = time.Second
	}
	st := &streamState{manned: map[string]string{}, degraded: map[string]bool{}, alerts: map[string]bool{}}
	for ctx.Err() == nil {
		err := r.read(ctx, s, st)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.fail(s.Name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

type streamState struct {
	manned   map[string]string
	degraded map[string]bool
	alerts   map[string]bool
}

func (r *Recorder) read(ctx context.Context, s Stream, st *streamState) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, resp, err := websocket.Dial(dctx, s.URL, &websocket.DialOptions{HTTPHeader: s.Header})
	cancel()
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return fmt.Errorf("%s: upgrade refused with %d", s.Name, resp.StatusCode)
		}
		return fmt.Errorf("%s: %w", s.Name, err)
	}
	conn.SetReadLimit(wire.MaxFrameBytes)
	defer func() { _ = conn.CloseNow() }()
	if len(s.OnOpen) > 0 {
		if err := conn.Write(ctx, websocket.MessageText, s.OnOpen); err != nil {
			return fmt.Errorf("%s: subscribe: %w", s.Name, err)
		}
	}
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		r.Handle(s, st, b, time.Now().UTC())
	}
}

// Handle normalises one frame (exported for the tests).
func (r *Recorder) Handle(s Stream, st *streamState, b []byte, at time.Time) {
	e, err := wire.ParseEnvelope(b)
	if err != nil {
		r.frame(s.Name, "unparseable")
		return
	}
	r.frame(s.Name, e.Schema)
	switch e.Schema {
	case wire.SchemaAlert:
		r.alert(s, st, e, at)
	case wire.SchemaViolation:
		r.violation(s, st, e, at)
	case wire.SchemaManned:
		r.manned(s, st, e, at)
	case wire.SchemaStatus:
		r.status(s, st, e, at)
	}
}

// NewStreamState makes the per-stream state Handle keeps.
func NewStreamState() *streamState { //nolint:revive // the state is opaque to callers
	return &streamState{manned: map[string]string{}, degraded: map[string]bool{}, alerts: map[string]bool{}}
}

func ptime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := wire.ParseTime(s)
	if err != nil {
		return nil
	}
	return &t
}

// alert reads alert/v1 (uspace-ussp schemas/alert/v1).
func (r *Recorder) alert(s Stream, st *streamState, e wire.Envelope, at time.Time) {
	var b struct {
		AlertID     string         `json:"alert_id"`
		Kind        string         `json:"kind"`
		Severity    string         `json:"severity"`
		State       string         `json:"state"`
		ClearReason *string        `json:"clear_reason"`
		FlightID    *string        `json:"flight_id"`
		CapturedAt  string         `json:"captured_at"`
		RaisedAt    string         `json:"raised_at"`
		Detail      map[string]any `json:"detail"`
	}
	if json.Unmarshal(e.Body, &b) != nil || b.AlertID == "" {
		r.frame(s.Name, "alert/v1:unreadable")
		return
	}
	if b.FlightID != nil && s.Aircraft != "" {
		r.mu.Lock()
		r.flights[*b.FlightID] = s.Aircraft
		r.mu.Unlock()
	}
	phase := b.State
	if phase == PhaseUpdated && !st.alerts[b.AlertID] {
		// The first frame of an alert this stream had not seen raised
		// (it was active on connect): count it as the raise it is.
		phase = PhaseRaised
	}
	if phase == PhaseRaised && st.alerts[b.AlertID] {
		phase = PhaseUpdated // a repeat (escalation) of a raise already recorded
	}
	st.alerts[b.AlertID] = phase != PhaseCleared
	ev := Event{System: s.System, Kind: b.Kind, AlertID: b.AlertID, Phase: phase, Severity: b.Severity, Aircraft: s.Aircraft,
		ObservedAt: at, CapturedAt: ptime(b.CapturedAt), RaisedAt: ptime(b.RaisedAt), Detail: b.Detail, Stream: s.Name}
	if b.ClearReason != nil {
		ev.Reason = *b.ClearReason
	}
	if peer, ok := b.Detail["peer"].(map[string]any); ok {
		if id, ok := peer["track_id"].(string); ok {
			ev.Peer = id
		}
	}
	if z, ok := b.Detail["zone_id"].(string); ok {
		ev.Subject = z
	}
	if phase == PhaseUpdated {
		return // updates are counted as frames, not recorded as events
	}
	r.Add(ev)
}

// violation reads violation/v1 (uspace-authority schemas/violation/v1).
func (r *Recorder) violation(s Stream, st *streamState, e wire.Envelope, at time.Time) {
	var b struct {
		ViolationID string         `json:"violation_id"`
		Kind        string         `json:"kind"`
		State       string         `json:"state"`
		Severity    string         `json:"severity"`
		TrackRef    string         `json:"track_ref"`
		Serial      *string        `json:"serial"`
		ZoneID      *string        `json:"zone_id"`
		CapturedAt  string         `json:"captured_at"`
		OpenedAt    string         `json:"opened_at"`
		ClearReason *string        `json:"clear_reason"`
		Detail      map[string]any `json:"detail"`
	}
	if json.Unmarshal(e.Body, &b) != nil || b.ViolationID == "" {
		r.frame(s.Name, "violation/v1:unreadable")
		return
	}
	phase := b.State
	if phase == PhaseUpdated && !st.alerts[b.ViolationID] {
		phase = PhaseRaised
	}
	if phase == PhaseRaised && st.alerts[b.ViolationID] {
		phase = PhaseUpdated
	}
	st.alerts[b.ViolationID] = phase != PhaseCleared
	if phase == PhaseUpdated {
		return
	}
	ev := Event{System: s.System, Kind: b.Kind, AlertID: b.ViolationID, Phase: phase, Severity: b.Severity,
		ObservedAt: at, CapturedAt: ptime(b.CapturedAt), RaisedAt: ptime(b.OpenedAt), Detail: b.Detail, Stream: s.Name}
	if b.ClearReason != nil {
		ev.Reason = *b.ClearReason
	}
	if b.Serial != nil {
		ev.Aircraft = r.serials[*b.Serial]
	}
	if ev.Aircraft == "" {
		ev.Aircraft = r.serials[b.TrackRef]
	}
	if b.ZoneID != nil {
		ev.Subject = *b.ZoneID
		if i := strings.LastIndex(ev.Subject, "/"); i >= 0 {
			ev.Subject = ev.Subject[i+1:] // country/identifier: the scenario names the identifier
		}
	}
	r.Add(ev)
}

// manned reads track/manned/v1 (uspace-ansp schemas/track/manned/v1):
// live is a raise of the aircraft's presence, stale or source_disabled
// its clear with that reason.
func (r *Recorder) manned(s Stream, st *streamState, e wire.Envelope, at time.Time) {
	var b struct {
		ICAO24 string `json:"icao24"`
		State  string `json:"state"`
	}
	if json.Unmarshal(e.Body, &b) != nil || b.ICAO24 == "" {
		r.frame(s.Name, "track/manned/v1:unreadable")
		return
	}
	prev := st.manned[b.ICAO24]
	if prev == b.State {
		return
	}
	st.manned[b.ICAO24] = b.State
	ev := Event{System: s.System, Kind: KindMannedTrack, AlertID: s.Name + ":" + b.ICAO24, Subject: b.ICAO24, ObservedAt: at,
		CapturedAt: ptime(e.CapturedAt), Stream: s.Name}
	switch {
	case b.State == "live":
		ev.Phase = PhaseRaised
	case prev == "live" || prev == "":
		ev.Phase, ev.Reason = PhaseCleared, b.State
	default:
		return
	}
	r.Add(ev)
}

// status turns a console/status/v1 degraded[] into raise and clear events
// per slug (SC-22: a missing input is visible).
func (r *Recorder) status(s Stream, st *streamState, e wire.Envelope, at time.Time) {
	var b struct {
		Degraded []string `json:"degraded"`
	}
	if json.Unmarshal(e.Body, &b) != nil {
		return
	}
	now := map[string]bool{}
	for _, d := range b.Degraded {
		now[d] = true
		if !st.degraded[d] {
			r.Add(Event{System: s.System, Kind: KindDegraded, AlertID: s.System + ":degraded:" + d, Phase: PhaseRaised, Subject: d, ObservedAt: at, Stream: s.Name})
		}
	}
	for d := range st.degraded {
		if !now[d] {
			r.Add(Event{System: s.System, Kind: KindDegraded, AlertID: s.System + ":degraded:" + d, Phase: PhaseCleared, Reason: "not_degraded", Subject: d, ObservedAt: at, Stream: s.Name})
		}
	}
	st.degraded = now
}

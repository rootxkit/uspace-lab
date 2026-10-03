package simop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// TokenSource gives the client its bearer token.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	Invalidate()
}

// Counter names (E-09). sent counts every frame written, live or
// backlog, re-sends included; the ledger compares it with the server's.
const (
	CounterSentLive       = "sent_live"
	CounterSentBacklog    = "sent_backlog"
	CounterProduced       = "produced"
	CounterQueueShed      = "queue_shed"
	CounterDroppedByKnob  = "dropped_by_knob"
	CounterDials          = "dials"
	CounterDialFailures   = "dial_failures"
	CounterRefusedUpgrade = "refused_upgrade"
	CounterStatusFrames   = "status_frames"
	CounterBadFrames      = "bad_frames"
	CounterStreamStopped  = "stream_stopped_ticks"
	// CounterResentAfterBreak counts frames written on a connection that
	// broke before acked_seq covered them, and so written again as
	// backlog: their first fate is unknown to the client.
	CounterResentAfterBreak = "resent_after_break"
)

// Transports.
const (
	TransportWS    = "ws"
	TransportBatch = "batch"
)

// Config configures one aircraft's operator client (02 F5).
type Config struct {
	BaseURL   string
	Tokens    TokenSource
	Serial    string
	Epoch     string
	Transport string
	// Period is the sample period (1 s: telemetry/v1 at 1 Hz).
	Period time.Duration
	// QueueMax and QueueMaxAge bound the queue kept while disconnected
	// (02 F5 failure column: up to 10 min). Past either bound the oldest
	// frame is shed and counted.
	QueueMax    int
	QueueMaxAge time.Duration
	DropRate    float64
	Latency     time.Duration
	Seed        uint64
	Geoid       geoid.Undulator
	OperatorPos *core.LatLon
	IntentID    string
	// RetryAfter is the reconnect delay after a failed dial.
	RetryAfter time.Duration
	HTTP       *http.Client
	Now        func() time.Time
}

type queued struct {
	frame  Frame
	queued time.Time
}

// Client streams one aircraft's telemetry to a USSP.
type Client struct {
	cfg Config
	rng *rand.Rand

	mu         sync.Mutex
	latest     *vehicle.Sample
	lastBoot   uint32
	haveLast   bool
	seq        int64
	queue      []queued // waiting to be sent (backlog when sent)
	live       []Frame  // produced while connected, not yet written
	unacked    []Frame  // written, not yet covered by acked_seq
	ackedSeq   int64
	linkWanted bool
	streaming  bool
	connected  bool
	counters   core.Counters
	server     map[string]wire.TelemetryStatus // last status per connection
	batchTotal wire.TelemetryStatus
	wake       chan struct{}
	linkChange chan struct{}
	lastErr    string
}

// New makes a client; it connects when Run starts.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.Serial == "" || cfg.Tokens == nil {
		return nil, core.Fieldf("simop", "base URL, serial and tokens are required")
	}
	if cfg.Transport == "" {
		cfg.Transport = TransportWS
	}
	if cfg.Transport != TransportWS && cfg.Transport != TransportBatch {
		return nil, core.Fieldf("transport", "%q is not ws or batch", cfg.Transport)
	}
	if cfg.Period <= 0 {
		cfg.Period = time.Second
	}
	if cfg.QueueMax <= 0 {
		cfg.QueueMax = 600
	}
	if cfg.QueueMaxAge <= 0 {
		cfg.QueueMaxAge = 10 * time.Minute
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = time.Second
	}
	if cfg.Epoch == "" {
		cfg.Epoch = fmt.Sprintf("lab-%d", time.Now().UnixNano())
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = 1
	}
	return &Client{
		cfg:        cfg,
		rng:        rand.New(rand.NewPCG(seed, 0x0bad)), //nolint:gosec // simulated loss, not security
		ackedSeq:   -1,
		linkWanted: true,
		streaming:  true,
		server:     map[string]wire.TelemetryStatus{},
		wake:       make(chan struct{}, 1),
		linkChange: make(chan struct{}, 1),
	}, nil
}

// Counters are the client's own counters.
func (c *Client) Counters() *core.Counters { return &c.counters }

// SetIntent sets the intent id the frames carry.
func (c *Client) SetIntent(id string) {
	c.mu.Lock()
	c.cfg.IntentID = id
	c.mu.Unlock()
}

// LinkDown closes the connection and keeps queueing (knob).
func (c *Client) LinkDown() { c.setLink(false) }

// LinkUp lets the client reconnect (knob).
func (c *Client) LinkUp() { c.setLink(true) }

func (c *Client) setLink(up bool) {
	c.mu.Lock()
	c.linkWanted = up
	c.mu.Unlock()
	select {
	case c.linkChange <- struct{}{}:
	default:
	}
}

// StreamStop keeps the connection and sends nothing (the USSP's lost
// link); StreamStart resumes. Knobs.
func (c *Client) StreamStop() { c.mu.Lock(); c.streaming = false; c.mu.Unlock() }

// StreamStart resumes sending.
func (c *Client) StreamStart() { c.mu.Lock(); c.streaming = true; c.mu.Unlock() }

// Run streams until ctx ends. in is the vehicle stream for this
// aircraft.
func (c *Client) Run(ctx context.Context, in <-chan vehicle.Sample) error {
	go c.consume(ctx, in)
	go c.produce(ctx)
	if c.cfg.Transport == TransportBatch {
		return c.runBatch(ctx)
	}
	for ctx.Err() == nil {
		c.mu.Lock()
		want := c.linkWanted
		c.mu.Unlock()
		if !want {
			select {
			case <-ctx.Done():
				return nil
			case <-c.linkChange:
			}
			continue
		}
		conn, err := c.dial(ctx)
		if err != nil {
			c.counters.Inc(CounterDialFailures)
			c.setErr(err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.cfg.RetryAfter):
			case <-c.linkChange:
			}
			continue
		}
		c.serve(ctx, conn)
	}
	return nil
}

func (c *Client) setErr(err error) {
	c.mu.Lock()
	c.lastErr = err.Error()
	c.mu.Unlock()
}

// LastError is the last dial or transport error, for the run's log.
func (c *Client) LastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Client) consume(ctx context.Context, in <-chan vehicle.Sample) {
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-in:
			if !ok {
				return
			}
			c.mu.Lock()
			cp := s
			c.latest = &cp
			c.mu.Unlock()
		}
	}
}

// produce turns the newest sample into one frame per period (1 Hz).
func (c *Client) produce(ctx context.Context) {
	t := time.NewTicker(c.cfg.Period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		c.tick()
	}
}

func (c *Client) tick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streaming {
		c.counters.Inc(CounterStreamStopped)
		return
	}
	s := c.latest
	if s == nil || (c.haveLast && s.TimeBootMS == c.lastBoot) {
		return
	}
	f, skip := BuildFrame(s, c.cfg.Serial, c.cfg.Epoch, c.seq, c.cfg.IntentID, c.cfg.OperatorPos, c.cfg.Geoid)
	if skip != "" {
		c.counters.Inc(skip)
		return
	}
	c.haveLast, c.lastBoot = true, s.TimeBootMS
	c.seq++
	c.counters.Inc(CounterProduced)
	if c.cfg.DropRate > 0 && c.rng.Float64() < c.cfg.DropRate {
		c.counters.Inc(CounterDroppedByKnob)
		return
	}
	if c.connected && c.linkWanted {
		c.live = append(c.live, f)
	} else {
		c.enqueueLocked(queued{frame: f, queued: c.cfg.Now()})
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) enqueueLocked(q ...queued) {
	c.queue = append(c.queue, q...)
	now := c.cfg.Now()
	for len(c.queue) > 0 && (len(c.queue) > c.cfg.QueueMax || now.Sub(c.queue[0].queued) > c.cfg.QueueMaxAge) {
		c.queue = c.queue[1:]
		c.counters.Inc(CounterQueueShed)
	}
}

func (c *Client) bearer(ctx context.Context) (http.Header, error) {
	tok, err := c.cfg.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	return http.Header{"Authorization": {"Bearer " + tok}}, nil
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	c.counters.Inc(CounterDials)
	h, err := c.bearer(ctx)
	if err != nil {
		return nil, fmt.Errorf("simop: token: %w", err)
	}
	u := strings.Replace(strings.TrimRight(c.cfg.BaseURL, "/"), "http", "ws", 1) + "/v1/telemetry"
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: h, HTTPClient: c.cfg.HTTP})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			c.counters.Inc(CounterRefusedUpgrade)
			if resp.StatusCode == http.StatusUnauthorized {
				c.cfg.Tokens.Invalidate()
			}
			return nil, fmt.Errorf("simop: upgrade refused with %d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("simop: dial: %w", err)
	}
	conn.SetReadLimit(wire.MaxFrameBytes)
	return conn, nil
}

// serve runs one connection: backlog first, then live frames; it returns
// when the connection breaks or the link knob goes down. Frames written
// but not covered by acked_seq go back to the queue as backlog (B-05).
func (c *Client) serve(ctx context.Context, conn *websocket.Conn) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	readErr := make(chan error, 1)
	go func() { readErr <- c.readStatus(cctx, conn) }()
	defer func() {
		c.mu.Lock()
		c.connected = false
		// Unacknowledged and unsent live frames are history now.
		var back []queued
		now := c.cfg.Now()
		for i := range c.unacked {
			if f := c.unacked[i]; f.Seq > c.ackedSeq {
				back = append(back, queued{frame: f, queued: now})
				c.counters.Inc(CounterResentAfterBreak)
			}
		}
		for i := range c.live {
			back = append(back, queued{frame: c.live[i], queued: now})
		}
		c.unacked, c.live = nil, nil
		c.queue = append(back, c.queue...)
		c.enqueueLocked()
		c.mu.Unlock()
	}()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		c.mu.Lock()
		want := c.linkWanted
		c.mu.Unlock()
		if !want {
			_ = conn.Close(websocket.StatusNormalClosure, "lab link knob down")
			return
		}
		if err := c.flush(cctx, conn); err != nil {
			c.setErr(err)
			_ = conn.CloseNow()
			return
		}
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "run ended")
			return
		case err := <-readErr:
			if err != nil {
				c.setErr(err)
			}
			_ = conn.CloseNow()
			return
		case <-c.wake:
		case <-c.linkChange:
		case <-poll.C:
		}
	}
}

// flush writes the queue (as backlog) and the live frames.
func (c *Client) flush(ctx context.Context, conn *websocket.Conn) error {
	for {
		c.mu.Lock()
		var f Frame
		backlog := false
		switch {
		case len(c.queue) > 0:
			f, c.queue = c.queue[0].frame, c.queue[1:]
			backlog = true
		case len(c.live) > 0:
			f, c.live = c.live[0], c.live[1:]
		default:
			c.mu.Unlock()
			return nil
		}
		f.Backlog = f.Backlog || backlog
		c.unacked = append(c.unacked, f)
		c.mu.Unlock()
		if c.cfg.Latency > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.cfg.Latency):
			}
		}
		b, err := json.Marshal(Message{Schema: SchemaTelemetry, Body: f})
		if err != nil {
			return fmt.Errorf("simop: %w", err)
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = conn.Write(wctx, websocket.MessageText, b)
		cancel()
		if err != nil {
			return fmt.Errorf("simop: write: %w", err)
		}
		if f.Backlog {
			c.counters.Inc(CounterSentBacklog)
		} else {
			c.counters.Inc(CounterSentLive)
		}
	}
}

// readStatus reads the server's console/status/v1 frames: the only thing
// the socket sends (ussp openapi). Anything else is counted and ignored.
func (c *Client) readStatus(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // the run or the connection ended: not a read failure
			}
			return fmt.Errorf("simop: read: %w", err)
		}
		e, err := wire.ParseEnvelope(b)
		if err != nil || e.Schema != wire.SchemaStatus {
			c.counters.Inc(CounterBadFrames)
			continue
		}
		var st wire.TelemetryStatus
		if err := json.Unmarshal(e.Body, &st); err != nil || st.ConnectionID == "" {
			c.counters.Inc(CounterBadFrames)
			continue
		}
		c.counters.Inc(CounterStatusFrames)
		c.mu.Lock()
		c.server[st.ConnectionID] = st
		if a, ok := st.AckedSeq[c.cfg.Serial]; ok && a > c.ackedSeq {
			c.ackedSeq = a
			kept := c.unacked[:0]
			for i := range c.unacked {
				if c.unacked[i].Seq > a {
					kept = append(kept, c.unacked[i])
				}
			}
			c.unacked = kept
		}
		c.mu.Unlock()
	}
}

// Ledger is what the client sent against what the server said it did
// with it (accepted + refused + dropped + duplicate = sent, KT-4).
type Ledger struct {
	Serial        string            `json:"serial"`
	Transport     string            `json:"transport"`
	Produced      uint64            `json:"produced"`
	SentLive      uint64            `json:"sent_live"`
	SentBacklog   uint64            `json:"sent_backlog"`
	Sent          uint64            `json:"sent"`
	DroppedByKnob uint64            `json:"dropped_by_knob"`
	QueueShed     uint64            `json:"queue_shed"`
	Pending       int               `json:"pending"`
	Accepted      uint64            `json:"accepted"`
	Refused       uint64            `json:"refused"`
	Dropped       uint64            `json:"dropped"`
	Duplicate     uint64            `json:"duplicate"`
	Backlog       uint64            `json:"backlog"`
	Outcomes      map[string]uint64 `json:"outcomes"`
	AckedSeq      int64             `json:"acked_seq"`
	LastSeq       int64             `json:"last_seq"`
	Connections   int               `json:"connections"`
	// ResentAfterBreak frames were written twice because a connection
	// broke before they were acknowledged; the server may have counted
	// the first writing on the broken connection's last, unseen status.
	ResentAfterBreak uint64 `json:"resent_after_break"`
	// Exact: sent == accepted + refused + dropped + duplicate. Balanced
	// allows the ResentAfterBreak frames whose first fate is unknown.
	Exact          bool              `json:"exact"`
	Balanced       bool              `json:"balanced"`
	ClientCounters map[string]uint64 `json:"client_counters"`
}

// Ledger reads the counters now.
func (c *Client) Ledger() Ledger {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := Ledger{
		Serial: c.cfg.Serial, Transport: c.cfg.Transport, Outcomes: map[string]uint64{},
		Produced: c.counters.Get(CounterProduced), SentLive: c.counters.Get(CounterSentLive),
		SentBacklog: c.counters.Get(CounterSentBacklog), DroppedByKnob: c.counters.Get(CounterDroppedByKnob),
		QueueShed: c.counters.Get(CounterQueueShed), Pending: len(c.queue) + len(c.live) + len(c.unacked),
		AckedSeq: c.ackedSeq, LastSeq: c.seq - 1, Connections: len(c.server),
		ClientCounters: c.counters.Snapshot(),
	}
	l.Sent = l.SentLive + l.SentBacklog
	add := func(st wire.TelemetryStatus) {
		l.Accepted += st.Accepted
		l.Refused += st.Refused
		l.Dropped += st.Dropped
		l.Backlog += st.Backlog
		for k, v := range st.Outcomes {
			l.Outcomes[k] += v
		}
	}
	for _, st := range c.server {
		add(st)
	}
	add(c.batchTotal)
	l.Duplicate = l.Outcomes["duplicate"]
	l.ResentAfterBreak = c.counters.Get(CounterResentAfterBreak)
	total := l.Accepted + l.Refused + l.Dropped + l.Duplicate
	l.Exact = l.Sent == total
	l.Balanced = l.Exact || (l.Sent > total && l.Sent-total <= l.ResentAfterBreak)
	return l
}

// Drain waits until every frame produced is written and acknowledged (or
// refused for good), or ctx ends: the condition the KT-4 ledger is read
// at (tests and the runner wait on it, never on a sleep).
func (c *Client) Drain(ctx context.Context) Ledger {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		l := c.Ledger()
		if l.Pending == 0 && l.AckedSeq >= l.LastSeq && l.Balanced {
			return l
		}
		select {
		case <-ctx.Done():
			return c.Ledger()
		case <-t.C:
		}
	}
}

// --- batch transport --------------------------------------------------------

// BatchRequest is TelemetryBatch.
type BatchRequest struct {
	SentAt string  `json:"sent_at"`
	Frames []Frame `json:"frames"`
}

// BatchResult is TelemetryBatchResult.
type BatchResult struct {
	Accepted        uint64 `json:"accepted"`
	Refused         uint64 `json:"refused"`
	Dropped         uint64 `json:"dropped"`
	Duplicate       uint64 `json:"duplicate"`
	NotAcknowledged uint64 `json:"not_acknowledged"`
	Outcomes        []struct {
		Index   int    `json:"index"`
		Outcome string `json:"outcome"`
	} `json:"outcomes"`
}

// MaxBatchFrames is the batch bound (TelemetryBatch maxItems).
const MaxBatchFrames = 2000

func (c *Client) runBatch(ctx context.Context) error {
	t := time.NewTicker(c.cfg.Period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		c.mu.Lock()
		want := c.linkWanted
		c.connected = want
		var frames []Frame
		for len(frames) < MaxBatchFrames && len(c.queue) > 0 {
			f := c.queue[0].frame
			f.Backlog = true
			frames = append(frames, f)
			c.queue = c.queue[1:]
		}
		for len(frames) < MaxBatchFrames && len(c.live) > 0 {
			frames = append(frames, c.live[0])
			c.live = c.live[1:]
		}
		c.mu.Unlock()
		if len(frames) == 0 || !want {
			c.requeue(frames)
			continue
		}
		res, err := c.postBatch(ctx, frames)
		if err != nil {
			c.setErr(err)
			c.requeue(frames)
			continue
		}
		c.mu.Lock()
		for i := range frames {
			if frames[i].Backlog {
				c.counters.Inc(CounterSentBacklog)
			} else {
				c.counters.Inc(CounterSentLive)
			}
		}
		c.batchTotal.Accepted += res.Accepted
		c.batchTotal.Refused += res.Refused
		c.batchTotal.Dropped += res.Dropped
		if c.batchTotal.Outcomes == nil {
			c.batchTotal.Outcomes = map[string]uint64{}
		}
		c.batchTotal.Outcomes["duplicate"] += res.Duplicate
		var again []queued
		for _, o := range res.Outcomes {
			c.batchTotal.Outcomes[o.Outcome]++
			if (o.Outcome == "not_acknowledged" || o.Outcome == "duplicate_pending") && o.Index >= 0 && o.Index < len(frames) {
				again = append(again, queued{frame: frames[o.Index], queued: c.cfg.Now()})
			}
		}
		// A frame sent again is counted again when it is sent; take the
		// first sending out of the balance.
		c.batchTotal.Refused += uint64(len(again))
		c.queue = append(again, c.queue...)
		if n := len(frames); n > 0 {
			c.ackedSeq = max(c.ackedSeq, frames[n-1].Seq)
		}
		c.mu.Unlock()
	}
}

func (c *Client) requeue(frames []Frame) {
	if len(frames) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	back := make([]queued, 0, len(frames))
	for i := range frames {
		back = append(back, queued{frame: frames[i], queued: c.cfg.Now()})
	}
	c.queue = append(back, c.queue...)
	c.enqueueLocked()
}

func (c *Client) postBatch(ctx context.Context, frames []Frame) (BatchResult, error) {
	var res BatchResult
	h, err := c.bearer(ctx)
	if err != nil {
		return res, err
	}
	body, err := json.Marshal(BatchRequest{SentAt: wire.Format(c.cfg.Now()), Frames: frames})
	if err != nil {
		return res, fmt.Errorf("simop: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/v1/telemetry/batch", bytes.NewReader(body))
	if err != nil {
		return res, fmt.Errorf("simop: %w", err)
	}
	req.Header = h
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return res, fmt.Errorf("simop: batch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, wire.MaxFrameBytes))
	if resp.StatusCode != http.StatusAccepted {
		if resp.StatusCode == http.StatusUnauthorized {
			c.cfg.Tokens.Invalidate()
		}
		return res, fmt.Errorf("simop: batch answered %d: %s", resp.StatusCode, shortBody(rb))
	}
	if err := json.Unmarshal(rb, &res); err != nil {
		return res, fmt.Errorf("simop: batch result: %w", err)
	}
	return res, nil
}

func shortBody(b []byte) string {
	if len(b) > 200 {
		return string(b[:200])
	}
	return string(b)
}

// ErrNoIntent is returned when an intent operation is asked for an
// aircraft without one.
var ErrNoIntent = errors.New("simop: no intent")

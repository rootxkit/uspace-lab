package simrx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"
	"github.com/rootxkit/uspace-core/odid"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// Bounds of rid/observation/v1 (authority schema).
const (
	MaxObservations = 64
	MaxBatchBytes   = 65536
)

// Transports.
const (
	TransportPack   = "pack"   // Bluetooth 5 and Wi-Fi: one message pack per Location
	TransportSingle = "single" // Bluetooth 4 legacy: each message alone
)

// Counter names (E-09).
const (
	CounterObserved       = "observations"
	CounterDroppedByKnob  = "dropped_by_knob"
	CounterSent           = "sent"
	CounterAccepted       = "accepted"
	CounterDuplicates     = "duplicates"
	CounterRefused        = "refused"
	CounterBatches        = "batches"
	CounterBatchFailures  = "batch_failures"
	CounterBacklogBatches = "backlog_batches"
	CounterBacklogShed    = "backlog_shed"
	CounterNoUTC          = "locations_without_utc"
	CounterEncodeErrors   = "encode_errors"
)

// Config configures one simulated receiver (02 F9).
type Config struct {
	BaseURL    string
	ReceiverID string
	BearerKey  string
	HMACKey    []byte
	Transport  string
	HAE        string
	Geoid      geoid.Undulator
	Position   *core.LatLon
	RSSIDBm    float64
	DropRate   float64
	Latency    time.Duration
	Seed       uint64
	// LocationPeriod and StaticPeriod are F3411's minimum rates: Location
	// every second, Basic ID, System and Operator ID every three.
	LocationPeriod time.Duration
	StaticPeriod   time.Duration
	// BatchPeriod is at most one second of reception per batch.
	BatchPeriod time.Duration
	// BacklogMax bounds the observations held while the authority is
	// unreachable (E-10); past it the oldest are shed and counted.
	BacklogMax   int
	Transmitters []Transmitter
	HTTP         *http.Client
	Now          func() time.Time
}

type txState struct {
	Transmitter
	lastLocation time.Time
	lastStatic   time.Time
	takeoff      *core.LatLon
}

type obs struct {
	Transmitter string           `json:"transmitter"`
	PayloadHex  string           `json:"payload_hex"`
	RSSIDBm     *float64         `json:"rssi_dbm"`
	RxTS        *string          `json:"rx_ts"`
	Position    *receiverPostion `json:"receiver_position,omitempty"`
}

type receiverPostion struct {
	LatDeg  float64  `json:"lat_deg"`
	LonDeg  float64  `json:"lon_deg"`
	AltHAEM *float64 `json:"alt_hae_m"`
}

// Batch is rid/observation/v1.
type Batch struct {
	ReceiverID   string `json:"receiver_id"`
	SentAtMS     int64  `json:"sent_at_ms"`
	Nonce        string `json:"nonce"`
	Backlog      bool   `json:"backlog"`
	Observations []obs  `json:"observations"`
}

// Receiver hears the vehicles' broadcasts and reports them.
type Receiver struct {
	cfg Config
	rng *mrand.Rand

	mu       sync.Mutex
	tx       map[int][]*txState
	pending  []obs
	backlog  []obs
	down     bool
	counters core.Counters
	lastErr  string
}

// New makes a receiver.
func New(cfg Config) (*Receiver, error) {
	if cfg.BaseURL == "" || cfg.ReceiverID == "" || cfg.BearerKey == "" {
		return nil, core.Fieldf("simrx", "base URL, receiver id and bearer key are required")
	}
	if len(cfg.HMACKey) < auth.MinReceiverKeyBytes {
		return nil, core.Fieldf("hmac_key", "at least %d bytes", auth.MinReceiverKeyBytes)
	}
	if cfg.Transport == "" {
		cfg.Transport = TransportPack
	}
	if cfg.Transport != TransportPack && cfg.Transport != TransportSingle {
		return nil, core.Fieldf("transport", "pack or single")
	}
	if cfg.HAE == "" {
		cfg.HAE = HAEGeoid
	}
	if cfg.LocationPeriod <= 0 {
		cfg.LocationPeriod = time.Second
	}
	if cfg.StaticPeriod <= 0 {
		cfg.StaticPeriod = 3 * time.Second
	}
	if cfg.BatchPeriod <= 0 || cfg.BatchPeriod > time.Second {
		cfg.BatchPeriod = time.Second
	}
	if cfg.BacklogMax <= 0 {
		cfg.BacklogMax = 20_000
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &Receiver{cfg: cfg, tx: map[int][]*txState{}}
	seed := cfg.Seed
	if seed == 0 {
		seed = 1
	}
	r.rng = mrand.New(mrand.NewPCG(seed, 0xfeed)) //nolint:gosec // simulated loss, not security
	for _, t := range cfg.Transmitters {
		if t.MAC == "" {
			return nil, core.Fieldf("transmitters", "sysid %d has no MAC", t.Sysid)
		}
		r.tx[t.Sysid] = append(r.tx[t.Sysid], &txState{Transmitter: t})
	}
	return r, nil
}

// Counters are the receiver's counters.
func (r *Receiver) Counters() *core.Counters { return &r.counters }

// Down stops reporting and buffers (knob); Up resumes, the buffer
// replayed as backlog.
func (r *Receiver) Down() { r.mu.Lock(); r.down = true; r.mu.Unlock() }

// Up resumes reporting.
func (r *Receiver) Up() { r.mu.Lock(); r.down = false; r.mu.Unlock() }

// SetSerial changes the serial a vehicle's transmitters broadcast (SC-10:
// a module restarted with a new serial on the same address).
func (r *Receiver) SetSerial(sysid int, serial string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tx[sysid] {
		t.Serial = serial
		t.lastStatic = time.Time{}
	}
}

// SetAddress changes a vehicle's transmitter address (SC-11 and the
// address-change knob).
func (r *Receiver) SetAddress(sysid int, mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tx[sysid] {
		t.MAC = mac
	}
}

// LastError is the last transport error.
func (r *Receiver) LastError() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Run hears samples and reports until ctx ends.
func (r *Receiver) Run(ctx context.Context, in <-chan vehicle.Sample) error {
	go r.batches(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case s, ok := <-in:
			if !ok {
				return nil
			}
			r.hear(&s)
		}
	}
}

// hear builds what the vehicle's modules broadcast for this sample.
func (r *Receiver) hear(s *vehicle.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.cfg.Now()
	for _, t := range r.tx[s.Sysid] {
		if t.takeoff == nil || !s.Armed {
			if !s.Armed {
				p := s.Pos()
				t.takeoff = &p
			}
		}
		if !t.lastLocation.IsZero() && now.Sub(t.lastLocation) < r.cfg.LocationPeriod {
			continue
		}
		t.lastLocation = now
		loc := LocationOf(s, r.cfg.HAE, r.cfg.Geoid)
		if loc.SecondsAfterHour == nil {
			r.counters.Inc(CounterNoUTC)
		}
		msgs := []odid.Message{loc}
		if t.lastStatic.IsZero() || now.Sub(t.lastStatic) >= r.cfg.StaticPeriod {
			t.lastStatic = now
			if t.Serial != "" {
				msgs = append(msgs, BasicIDOf(t.Serial))
			}
			if t.takeoff != nil {
				if sys, ok := SystemOf(s, *t.takeoff); ok {
					msgs = append(msgs, sys)
				}
			}
			if t.OperatorID != "" {
				msgs = append(msgs, OperatorIDOf(t.OperatorID))
			}
		}
		var payloads [][]byte
		if r.cfg.Transport == TransportPack {
			p, err := odid.EncodePack(msgs)
			if err != nil {
				r.counters.Inc(CounterEncodeErrors)
				r.lastErr = err.Error()
				continue
			}
			payloads = append(payloads, p)
		} else {
			for _, m := range msgs {
				b, err := odid.Encode(m)
				if err != nil {
					r.counters.Inc(CounterEncodeErrors)
					r.lastErr = err.Error()
					continue
				}
				payloads = append(payloads, b[:])
			}
		}
		for _, p := range payloads {
			r.counters.Inc(CounterObserved)
			if r.cfg.DropRate > 0 && r.rng.Float64() < r.cfg.DropRate {
				r.counters.Inc(CounterDroppedByKnob)
				continue
			}
			rx := wire.Format(now)
			o := obs{Transmitter: t.MAC, PayloadHex: hex.EncodeToString(p), RxTS: &rx}
			if r.cfg.RSSIDBm != 0 {
				v := r.cfg.RSSIDBm
				o.RSSIDBm = &v
			}
			if r.cfg.Position != nil {
				o.Position = &receiverPostion{LatDeg: r.cfg.Position.LatDeg, LonDeg: r.cfg.Position.LonDeg}
			}
			r.pending = append(r.pending, o)
		}
	}
}

func (r *Receiver) batches(ctx context.Context) {
	t := time.NewTicker(r.cfg.BatchPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.flush(ctx)
	}
}

// flush sends what is pending (live) and, when the authority answers,
// what the backlog holds.
func (r *Receiver) flush(ctx context.Context) {
	r.mu.Lock()
	live := r.pending
	r.pending = nil
	if r.down {
		r.toBacklogLocked(live)
		r.mu.Unlock()
		return
	}
	backlog := r.backlog
	r.backlog = nil
	r.mu.Unlock()
	if r.cfg.Latency > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.cfg.Latency):
		}
	}
	if ok := r.sendAll(ctx, live, false); !ok {
		// The live remainder went to the backlog; the older backlog goes
		// back in front of it, oldest first.
		r.mu.Lock()
		r.backlog = append(backlog, r.backlog...)
		r.toBacklogLocked(nil)
		r.mu.Unlock()
		return
	}
	r.sendAll(ctx, backlog, true)
}

func (r *Receiver) toBacklogLocked(o []obs) {
	r.backlog = append(r.backlog, o...)
	if over := len(r.backlog) - r.cfg.BacklogMax; over > 0 {
		r.backlog = r.backlog[over:]
		r.counters.Add(CounterBacklogShed, uint64(over))
	}
}

// sendAll posts os in batches of at most MaxObservations; on a transport
// failure or a 503 the rest go to the backlog and it returns false.
func (r *Receiver) sendAll(ctx context.Context, os []obs, backlog bool) bool {
	for len(os) > 0 {
		n := min(len(os), MaxObservations)
		chunk := os[:n]
		retry, err := r.post(ctx, chunk, backlog)
		if err != nil {
			r.mu.Lock()
			r.lastErr = err.Error()
			r.counters.Inc(CounterBatchFailures)
			if retry {
				r.toBacklogLocked(os)
				r.mu.Unlock()
				return false
			}
			r.mu.Unlock()
		}
		os = os[n:]
	}
	return true
}

// post signs and sends one batch: the HMAC-SHA256 of the exact body bytes
// under the receiver's secret in X-Report-Signature (auth.SignReport), the
// bearer key naming the receiver (authority openapi receiverKey). retry is
// true when the batch should be replayed (transport failure, 503).
func (r *Receiver) post(ctx context.Context, os []obs, backlog bool) (retry bool, err error) {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	b := Batch{ReceiverID: r.cfg.ReceiverID, SentAtMS: r.cfg.Now().UnixMilli(), Nonce: hex.EncodeToString(nonce[:]), Backlog: backlog, Observations: os}
	body, err := json.Marshal(b)
	if err != nil {
		return false, fmt.Errorf("simrx: %w", err)
	}
	if len(body) > MaxBatchBytes {
		return false, fmt.Errorf("simrx: batch of %d bytes exceeds %d", len(body), MaxBatchBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.BaseURL, "/")+"/v1/rid/observations", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("simrx: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.BearerKey)
	req.Header.Set("X-Report-Signature", auth.SignReport(r.cfg.HMACKey, body))
	r.counters.Inc(CounterBatches)
	if backlog {
		r.counters.Inc(CounterBacklogBatches)
	}
	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return true, fmt.Errorf("simrx: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusAccepted:
		var ack struct {
			Accepted   int `json:"accepted"`
			Duplicates int `json:"duplicates"`
		}
		if err := json.Unmarshal(rb, &ack); err != nil {
			return false, fmt.Errorf("simrx: ack: %w", err)
		}
		r.counters.Add(CounterSent, uint64(len(os)))
		r.counters.Add(CounterAccepted, uint64(ack.Accepted))
		r.counters.Add(CounterDuplicates, uint64(ack.Duplicates))
		return false, nil
	case resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500:
		return true, fmt.Errorf("simrx: authority answered %d: %s", resp.StatusCode, short(rb))
	default:
		r.counters.Add(CounterSent, uint64(len(os)))
		r.counters.Add(CounterRefused, uint64(len(os)))
		return false, fmt.Errorf("simrx: authority refused with %d: %s", resp.StatusCode, short(rb))
	}
}

func short(b []byte) string {
	if len(b) > 200 {
		return string(b[:200])
	}
	return string(b)
}

// Ledger is what the receiver reported and what the authority said.
type Ledger struct {
	ReceiverID    string            `json:"receiver_id"`
	Observed      uint64            `json:"observed"`
	DroppedByKnob uint64            `json:"dropped_by_knob"`
	Sent          uint64            `json:"sent"`
	Accepted      uint64            `json:"accepted"`
	Duplicates    uint64            `json:"duplicates"`
	Refused       uint64            `json:"refused"`
	BacklogShed   uint64            `json:"backlog_shed"`
	Pending       int               `json:"pending"`
	Balanced      bool              `json:"balanced"`
	Counters      map[string]uint64 `json:"counters"`
}

// Ledger reads the counters now.
func (r *Receiver) Ledger() Ledger {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := Ledger{
		ReceiverID: r.cfg.ReceiverID, Observed: r.counters.Get(CounterObserved), DroppedByKnob: r.counters.Get(CounterDroppedByKnob),
		Sent: r.counters.Get(CounterSent), Accepted: r.counters.Get(CounterAccepted), Duplicates: r.counters.Get(CounterDuplicates),
		Refused: r.counters.Get(CounterRefused), BacklogShed: r.counters.Get(CounterBacklogShed),
		Pending: len(r.pending) + len(r.backlog), Counters: r.counters.Snapshot(),
	}
	l.Balanced = l.Sent == l.Accepted+l.Duplicates+l.Refused &&
		l.Observed == l.DroppedByKnob+l.Sent+l.BacklogShed+uint64(l.Pending)
	return l
}

// Drain waits until nothing is pending, or ctx ends.
func (r *Receiver) Drain(ctx context.Context) Ledger {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		l := r.Ledger()
		if l.Pending == 0 && l.Balanced {
			return l
		}
		select {
		case <-ctx.Done():
			return r.Ledger()
		case <-t.C:
		}
	}
}

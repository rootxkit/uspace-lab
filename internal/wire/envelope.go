// Package wire holds the shapes the lab reads and writes on the systems'
// public contracts: the common envelope (schemas/common/envelope/v1),
// console/status/v1, and the pinned copies of the producing systems'
// schemas under testdata/ that the simulators' tests validate against
// (LESSONS E-03: field names from the pinned schemas, never from memory).
package wire

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rootxkit/uspace-lab/internal/ulid"
)

// TimeLayout is the envelope's timestamp: RFC 3339 UTC, milliseconds, Z.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// MaxFrameBytes bounds one frame the lab reads (E-10).
const MaxFrameBytes = 1 << 20

// Schema names the lab handles.
const (
	SchemaStatus    = "console/status/v1"
	SchemaSnapshot  = "console/snapshot/v1"
	SchemaSubscribe = "console/subscribe/v1"
	SchemaAlert     = "alert/v1"
	SchemaViolation = "violation/v1"
	SchemaManned    = "track/manned/v1"
	SchemaTrack     = "track/telemetry/v1"
	SchemaSource    = "source/status/v1"
)

// Envelope is the common envelope (04 §2) with its body left raw.
type Envelope struct {
	Schema     string          `json:"schema"`
	MsgID      string          `json:"msg_id"`
	Producer   string          `json:"producer"`
	TS         *string         `json:"ts,omitempty"`
	RxTS       string          `json:"rx_ts"`
	CapturedAt string          `json:"captured_at"`
	TimeSource string          `json:"time_source"`
	Backlog    bool            `json:"backlog"`
	Body       json.RawMessage `json:"body"`
}

// ParseEnvelope decodes a frame's envelope, refusing an oversized frame or
// one without a schema.
func ParseEnvelope(b []byte) (Envelope, error) {
	var e Envelope
	if len(b) > MaxFrameBytes {
		return e, fmt.Errorf("wire: frame of %d bytes exceeds %d", len(b), MaxFrameBytes)
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("wire: not an envelope: %w", err)
	}
	if e.Schema == "" {
		return e, fmt.Errorf("wire: envelope without schema")
	}
	return e, nil
}

// Format formats t as an envelope timestamp.
func Format(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads an envelope timestamp (any RFC 3339; the lab's own are
// milliseconds with Z).
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// New builds an envelope around body as the lab produces it.
func New(schema, producer string, captured, rx time.Time, ts *time.Time, timeSource string, backlog bool, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("wire: body: %w", err)
	}
	e := Envelope{
		Schema: schema, MsgID: ulid.Make(rx), Producer: producer, RxTS: Format(rx),
		CapturedAt: Format(captured), TimeSource: timeSource, Backlog: backlog, Body: b,
	}
	if ts != nil {
		s := Format(*ts)
		e.TS = &s
	}
	out, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("wire: envelope: %w", err)
	}
	return out, nil
}

// TelemetryStatus are the extras the USSP's telemetry socket puts in its
// console/status/v1 body (ussp openapi, WS /v1/telemetry): accepted,
// refused, dropped, outcomes, rate, backlog and acked_seq.
type TelemetryStatus struct {
	ConnectionID string            `json:"connection_id"`
	ServerTS     string            `json:"server_ts"`
	Accepted     uint64            `json:"accepted"`
	Refused      uint64            `json:"refused"`
	Dropped      uint64            `json:"dropped"`
	Outcomes     map[string]uint64 `json:"outcomes"`
	Backlog      uint64            `json:"backlog"`
	AckedSeq     map[string]int64  `json:"acked_seq"`
	Degraded     []string          `json:"degraded"`
}

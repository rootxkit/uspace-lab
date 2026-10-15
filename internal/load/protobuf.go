package load

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// 04 §1 asks whether the hot path should move from JSON to Protobuf at
// 5000 drones, "answered from the measured hot-path CPU share". The
// systems expose no profile, so the share is an estimate from what is
// measured: each hot-path process's CPU over the run
// (process_cpu_seconds_total) and its messages (the counter the probes
// file names for it, deploy/systems/probes.yaml hot_path), set against
// the cost of decoding and encoding one such message with encoding/json
// on this host, timed here on a frame the run received. It is a
// proposal's basis, never a change.

// typedTrackFrame is a track/telemetry/v1 frame as a system decodes it:
// every field typed (the systems decode into typed structs, not maps).
type typedTrackFrame struct {
	Schema     string         `json:"schema"`
	MsgID      string         `json:"msg_id"`
	Producer   string         `json:"producer"`
	TS         *string        `json:"ts"`
	RxTS       string         `json:"rx_ts"`
	CapturedAt string         `json:"captured_at"`
	TimeSource string         `json:"time_source"`
	Backlog    bool           `json:"backlog"`
	Body       typedTrackBody `json:"body"`
}

type typedTrackBody struct {
	TrackID        string `json:"track_id"`
	Trust          string `json:"trust"`
	Source         string `json:"source"`
	SourceInstance string `json:"source_instance"`
	Position       struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	} `json:"position"`
	AltWGS84M      *float64 `json:"alt_wgs84_m"`
	AltAMSLM       *float64 `json:"alt_amsl_m"`
	AltSource      string   `json:"alt_source"`
	AltPressureM   *float64 `json:"alt_pressure_m"`
	HeightM        *float64 `json:"height_m"`
	HeightRef      *string  `json:"height_ref"`
	SpeedMS        *float64 `json:"speed_ms"`
	TrackDeg       *float64 `json:"track_deg"`
	VSpeedMS       *float64 `json:"vspeed_ms"`
	AccuracyHM     *float64 `json:"accuracy_h_m"`
	AccuracyVM     *float64 `json:"accuracy_v_m"`
	Status         *string  `json:"status"`
	Emergency      bool     `json:"emergency"`
	Identification struct {
		Status                string  `json:"status"`
		Reason                string  `json:"reason"`
		Serial                *string `json:"serial"`
		OperatorReg           *string `json:"operator_reg"`
		RegisteredOperatorReg *string `json:"registered_operator_reg"`
		Mismatch              bool    `json:"mismatch"`
		RegistryUASID         *string `json:"registry_uas_id"`
		Basis                 string  `json:"basis"`
	} `json:"identification"`
	FlightID *string `json:"flight_id"`
	IntentID *string `json:"intent_id"`
	Cell     string  `json:"cell,omitempty"`
}

// jsonCost is the median time of decoding frame into its typed form and
// encoding it again, over rounds of n.
func jsonCost(frame []byte, n, rounds int) (time.Duration, error) {
	var f typedTrackFrame
	if err := json.Unmarshal(frame, &f); err != nil {
		return 0, err
	}
	if _, err := json.Marshal(&f); err != nil {
		return 0, err
	}
	per := make([]time.Duration, 0, rounds)
	for range rounds {
		t0 := time.Now()
		for range n {
			var g typedTrackFrame
			_ = json.Unmarshal(frame, &g)
			_, _ = json.Marshal(&g)
		}
		per = append(per, time.Since(t0)/time.Duration(n))
	}
	sort.Slice(per, func(i, j int) bool { return per[i] < per[j] })
	return per[len(per)/2], nil
}

// HotPathShare is one hot-path process's estimated JSON share.
type HotPathShare struct {
	Process string  `json:"process"`
	Series  string  `json:"messages_series"`
	CPUS    float64 `json:"cpu_s"`
	Msgs    float64 `json:"messages"`
	// CPUPerMsgUS is the process's CPU per message, JSONPerMsgUS the
	// harness's decode and encode of one frame, both in microseconds;
	// Share their ratio (at most 1: a share above it means the process
	// does less than a full decode and encode per message).
	CPUPerMsgUS  float64 `json:"cpu_per_message_us"`
	JSONPerMsgUS float64 `json:"json_per_message_us"`
	Share        float64 `json:"json_share"`
}

// Protobuf is the 04 §1 estimate.
type Protobuf struct {
	FrameBytes int            `json:"frame_bytes"`
	JSONUS     float64        `json:"json_per_message_us"`
	Processes  []HotPathShare `json:"processes"`
	Note       string         `json:"note"`
}

// protobufEstimate sets each hot-path process's CPU and messages over
// the run (cpu and msgs, the probes' rises) against frame's JSON cost.
func protobufEstimate(frame []byte, hot map[string]string, rise map[string]map[string]float64) *Protobuf {
	pb := &Protobuf{FrameBytes: len(frame)}
	if len(frame) == 0 {
		pb.Note = "no track frame was received to time"
		return pb
	}
	cost, err := jsonCost(frame, 2000, 5)
	if err != nil {
		pb.Note = "the frame received does not decode as track/telemetry/v1: " + err.Error()
		return pb
	}
	pb.JSONUS = round3(float64(cost) / float64(time.Microsecond))
	for _, proc := range sortedKeysOf(hot) {
		r, ok := rise[proc]
		if !ok {
			continue
		}
		hs := HotPathShare{Process: proc, Series: hot[proc], CPUS: round3(r[metricCPU]), Msgs: r[hot[proc]], JSONPerMsgUS: pb.JSONUS}
		if hs.Msgs > 0 && hs.CPUS > 0 {
			hs.CPUPerMsgUS = round3(hs.CPUS * 1e6 / hs.Msgs)
			hs.Share = round3(pb.JSONUS / hs.CPUPerMsgUS)
		}
		pb.Processes = append(pb.Processes, hs)
	}
	pb.Note = fmt.Sprintf("an estimate: one encoding/json decode and encode of a %d-byte track frame on this host against each process's CPU per message over the run; the systems expose no profile", len(frame))
	return pb
}

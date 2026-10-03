package simop

import (
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// SchemaTelemetry is the frame schema of WS /v1/telemetry (ussp
// openapi: {"schema": "telemetry/v1", "body": TelemetryFrame}).
const SchemaTelemetry = "telemetry/v1"

// Position is TelemetryPosition (lat, lng).
type Position struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// OperatorPosition is TelemetryOperatorPosition.
type OperatorPosition struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	AltWGS84M *float64 `json:"alt_wgs84_m"`
}

// Frame is the telemetry/v1 body (uspace-ussp schemas/telemetry/v1,
// pinned under internal/wire/testdata/ussp and validated in the tests).
// Every required member is written, null when unknown; the trust class
// and the source are never sent (06 T11: a sample carrying either is
// refused).
type Frame struct {
	TS                 string            `json:"ts"`
	Serial             string            `json:"serial"`
	Seq                int64             `json:"seq"`
	Epoch              string            `json:"epoch"`
	Backlog            bool              `json:"backlog"`
	End                bool              `json:"end,omitempty"`
	IntentID           *string           `json:"intent_id"`
	Position           Position          `json:"position"`
	AltWGS84M          *float64          `json:"alt_wgs84_m"`
	AltPressureM       *float64          `json:"alt_pressure_m"`
	HeightM            *float64          `json:"height_m"`
	HeightRef          *string           `json:"height_ref"`
	SpeedMS            *float64          `json:"speed_ms"`
	TrackDeg           *float64          `json:"track_deg"`
	VSpeedMS           *float64          `json:"vspeed_ms"`
	Status             string            `json:"status"`
	Emergency          bool              `json:"emergency"`
	OperatorPosition   *OperatorPosition `json:"operator_position"`
	AccuracyH          string            `json:"accuracy_h"`
	AccuracyV          string            `json:"accuracy_v"`
	TimestampAccuracyS *float64          `json:"timestamp_accuracy_s"`
}

// Message is one client-to-server WebSocket message.
type Message struct {
	Schema string `json:"schema"`
	Body   Frame  `json:"body"`
}

// The F3411 v22a enumerations the frame carries (the schema's enums).
const (
	statusGround     = "Ground"
	statusAirborne   = "Airborne"
	statusEmergency  = "Emergency"
	heightRefTakeoff = "TakeoffLocation"
	// With a 3D fix the reader's GPS is taken as better than 10 m, the
	// predecessor's figure for SITL (sitl_remote_id.py); without one the
	// accuracy is unknown.
	accuracyH10m     = "HA10m"
	accuracyV10m     = "VA10m"
	accuracyVUnknown = "VAUnknown"
)

// BuildFrame is the telemetry/v1 body of one vehicle sample, or ok false
// when the sample cannot be one: no UTC from the vehicle yet (ts is the
// vehicle's time and is required), or no 3D fix. The HAE is AMSL + N
// through the configured geoid (R-16; SITL's own alt_ellipsoid is its
// AMSL), null without a geoid or when the reader marks the altitude
// invalid (S-36).
func BuildFrame(s *vehicle.Sample, serial, epoch string, seq int64, intentID string, op *core.LatLon, g geoid.Undulator) (Frame, string) {
	if s.TS == nil {
		return Frame{}, skipNoUTC
	}
	if !s.FixOK {
		return Frame{}, skipNoFix
	}
	f := Frame{
		TS:           *s.TS,
		Serial:       serial,
		Seq:          seq,
		Epoch:        epoch,
		Position:     Position{Lat: s.LatDeg, Lng: s.LonDeg},
		AltPressureM: s.AltPressureM,
		HeightM:      vehicle.F(s.HeightTakeoffM),
		SpeedMS:      vehicle.F(s.SpeedMS),
		TrackDeg:     s.TrackDeg,
		VSpeedMS:     vehicle.F(s.VSpeedMS),
		AccuracyH:    accuracyH10m,
		AccuracyV:    accuracyV10m,
	}
	ref := heightRefTakeoff
	f.HeightRef = &ref
	if intentID != "" {
		id := intentID
		f.IntentID = &id
	}
	switch s.Status {
	case vehicle.StatusEmergency:
		f.Status, f.Emergency = statusEmergency, true
	case vehicle.StatusAirborne:
		f.Status = statusAirborne
	default:
		f.Status = statusGround
	}
	if g != nil && !s.AltInvalid {
		if n, err := g.UndulationM(s.Pos()); err == nil {
			f.AltWGS84M = vehicle.F(geoid.HAEFromAMSL(s.AltAMSLM, n))
		}
	}
	if s.AltInvalid {
		f.AccuracyV = accuracyVUnknown
	}
	if op != nil {
		f.OperatorPosition = &OperatorPosition{Lat: op.LatDeg, Lng: op.LonDeg}
	}
	return f, ""
}

// Reasons a sample is not sent (counted).
const (
	skipNoUTC = "skipped_no_utc"
	skipNoFix = "skipped_no_fix"
)

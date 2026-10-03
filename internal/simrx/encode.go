package simrx

import (
	"math"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"
	"github.com/rootxkit/uspace-core/odid"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// ODID enumeration values the broadcast uses. Each is the MAVLink
// OPEN_DRONE_ID enumeration of the same name in pymavlink's common.xml
// (MAV_ODID_*), which mirrors ASTM F3411; read there, not remembered,
// and pinned in encode_test.go against uspace-core's decoder.
const (
	// MAV_ODID_HOR_ACC_10_METER, MAV_ODID_VER_ACC_10_METER,
	// MAV_ODID_SPEED_ACC_1_METERS_PER_SECOND, MAV_ODID_TIME_ACC_0_1_SECOND.
	horizAccuracy10m = 10
	vertAccuracy10m  = 4
	speedAccuracy1ms = 3
	tsAccuracy0p1s   = 1
	// MAV_ODID_UA_TYPE_HELICOPTER_OR_MULTIROTOR.
	uaTypeMultirotor = 2
	// MAV_ODID_OPERATOR_LOCATION_TYPE_TAKEOFF, MAV_ODID_OPERATOR_ID_TYPE_CAA.
	operatorLocationTakeoff = 0
	operatorIDTypeCAA       = 0
)

// odidEpoch is the System message's epoch: seconds since 2019-01-01 UTC.
var odidEpoch = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

// Transmitter is one Remote ID module on a vehicle: its radio address and
// what it broadcasts. An empty Serial sends no Basic ID (heard as an
// unidentified transmitter, S-32); an empty OperatorID no Operator ID.
type Transmitter struct {
	Sysid      int
	MAC        string
	Serial     string
	OperatorID string
}

// HAE sources (R-16).
const (
	HAEGeoid = "geoid" // AMSL + N through the configured geoid
	HAEGPS   = "gps"   // the vehicle's GPS_RAW_INT.alt_ellipsoid (SITL: its AMSL)
)

// LocationOf is the ODID Location of a vehicle sample. HAE per hae (nil
// when it cannot be had, or when the reader marks the altitude invalid,
// S-36); the timestamp unknown while the vehicle has sent no UTC (R-16).
func LocationOf(s *vehicle.Sample, hae string, g geoid.Undulator) odid.Location {
	loc := odid.Location{
		Status:            odid.StatusGround,
		DirectionDeg:      s.TrackDeg,
		SpeedHorizontalMS: vehicle.F(s.SpeedMS),
		SpeedVerticalMS:   vehicle.F(s.VSpeedMS),
		LatDeg:            vehicle.F(s.LatDeg),
		LonDeg:            vehicle.F(s.LonDeg),
		AltBaroM:          s.AltPressureM,
		HeightReference:   odid.HeightOverTakeoff,
		HeightM:           vehicle.F(s.HeightTakeoffM),
		HorizAccuracy:     horizAccuracy10m,
		VertAccuracy:      vertAccuracy10m,
		SpeedAccuracy:     speedAccuracy1ms,
	}
	switch s.Status {
	case vehicle.StatusEmergency:
		loc.Status = odid.StatusEmergency
	case vehicle.StatusAirborne:
		loc.Status = odid.StatusAirborne
	}
	if !s.AltInvalid {
		switch hae {
		case HAEGPS:
			loc.AltHAEM = s.AltHAEM
		default:
			if g != nil {
				if n, err := g.UndulationM(s.Pos()); err == nil {
					loc.AltHAEM = vehicle.F(geoid.HAEFromAMSL(s.AltAMSLM, n))
				}
			}
		}
	}
	if loc.AltHAEM == nil {
		loc.VertAccuracy = 0
	}
	if t, ok := s.Time(); ok {
		sah := float64(t.Minute()*60+t.Second()) + float64(t.Nanosecond())/1e9
		sah = math.Floor(sah*10) / 10
		loc.SecondsAfterHour = &sah
		loc.TSAccuracy = tsAccuracy0p1s
	}
	return loc
}

// SystemOf is the System message: the take-off location as the operator
// location, and the vehicle's UTC; ok false while UTC is unknown (R-16:
// no System message until the vehicle has sent UTC).
func SystemOf(s *vehicle.Sample, takeoff core.LatLon) (odid.System, bool) {
	t, ok := s.Time()
	if !ok {
		return odid.System{}, false
	}
	return odid.System{
		OperatorLocationType: operatorLocationTakeoff,
		OperatorLatDeg:       vehicle.F(takeoff.LatDeg),
		OperatorLonDeg:       vehicle.F(takeoff.LonDeg),
		AreaCount:            1,
		TimestampS:           uint32(t.Sub(odidEpoch).Seconds()),
	}, true
}

// BasicIDOf and OperatorIDOf are the static identity messages.
func BasicIDOf(serial string) odid.BasicID {
	return odid.BasicID{IDType: odid.IDTypeSerial, UAType: uaTypeMultirotor, UAID: serial}
}

// OperatorIDOf is the Operator ID message.
func OperatorIDOf(id string) odid.OperatorID {
	return odid.OperatorID{OperatorIDType: operatorIDTypeCAA, OperatorID: id}
}

// Package manned is the lab's manned traffic: aircraft flown along
// straight legs placed by a scenario's offsets, or replayed from a
// recording file (testdata/adsb/*.jsonl). sim-ansp-feed serves it as the
// ANSP's track/manned/v1 stream and sim-adsb as a readsb aircraft.json.
//
// A recording holds the units a receiver reports (feet, knots, feet per
// minute, as dump1090's README-json.md names them, pinned in
// uspace-ansp internal/manned/dump1090/SOURCE); the conversions to SI are
// here, once.
package manned

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"
)

// Exact unit conversions: the international foot (core.FeetToMetres) and
// the international nautical mile of 1852 m.
const (
	KnotToMS       = 1852.0 / 3600.0
	FeetPerMinToMS = core.FeetToMetres / 60.0
)

// Sample is one manned aircraft at one moment, SI units.
type Sample struct {
	ICAO24       string
	Callsign     string
	Pos          core.LatLon
	AltPressureM *float64
	AltWGS84M    *float64
	GSMS         *float64
	TrackDeg     *float64
	VRateMS      *float64
	Squawk       *string
	Emergency    *bool
	// At is when the position was valid.
	At time.Time
}

// Source gives every aircraft's latest sample at a moment.
type Source interface {
	At(t time.Time) []Sample
}

var icaoPattern = regexp.MustCompile(`^[0-9a-f]{6}$`)

// Leg is an aircraft flying from one point to another at a constant
// speed and pressure altitude, starting Start after the run began. It
// is listed from its start until it reaches the end.
type Leg struct {
	ICAO24       string
	Callsign     string
	From, To     core.LatLon
	SpeedMS      float64
	AltPressureM float64
	AltWGS84M    *float64
	Start        time.Duration
}

// Legs is a set of legs over a run starting at T0.
type Legs struct {
	T0   time.Time
	Legs []Leg
}

// At places every leg that is under way at t.
func (l *Legs) At(t time.Time) []Sample {
	var out []Sample
	for _, leg := range l.Legs {
		el := t.Sub(l.T0) - leg.Start
		if el < 0 {
			continue
		}
		dist, bearing, _, err := geodesy.Inverse(leg.From, leg.To)
		if err != nil {
			continue
		}
		flown := leg.SpeedMS * el.Seconds()
		if flown > dist {
			continue
		}
		p := geodesy.Destination(leg.From, bearing, flown)
		gs, trk, vr := leg.SpeedMS, math.Mod(bearing+360, 360), 0.0
		if trk >= 360 {
			trk = 0
		}
		alt := leg.AltPressureM
		out = append(out, Sample{
			ICAO24: leg.ICAO24, Callsign: leg.Callsign, Pos: p, AltPressureM: &alt, AltWGS84M: leg.AltWGS84M,
			GSMS: &gs, TrackDeg: &trk, VRateMS: &vr, At: t,
		})
	}
	return out
}

// Record is one line of a recording (readsb units).
type Record struct {
	TS          float64  `json:"t_s"`
	ICAO24      string   `json:"icao24"`
	Callsign    string   `json:"callsign,omitempty"`
	LatDeg      float64  `json:"lat"`
	LonDeg      float64  `json:"lon"`
	AltBaroFt   *float64 `json:"alt_baro_ft"`
	AltGeomFt   *float64 `json:"alt_geom_ft"`
	GSKt        *float64 `json:"gs_kt"`
	TrackDeg    *float64 `json:"track_deg"`
	BaroRateFPM *float64 `json:"baro_rate_fpm"`
	Squawk      *string  `json:"squawk"`
}

// Recording replays records by their offset from T0, looping when Loop.
// An aircraft is listed while its latest record is no older than
// MaxGap.
type Recording struct {
	T0      time.Time
	Loop    bool
	MaxGap  time.Duration
	records []Record // by TS
	spanS   float64
}

// MaxRecordingLines bounds a recording (E-10).
const MaxRecordingLines = 1_000_000

// LoadRecording reads a .jsonl recording.
func LoadRecording(path string) (*Recording, error) {
	f, err := os.Open(path) //nolint:gosec // the scenario names the recording
	if err != nil {
		return nil, fmt.Errorf("recording: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ReadRecording(f)
}

// ReadRecording parses a recording, refusing a line that is not a record.
func ReadRecording(r io.Reader) (*Recording, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	rec := &Recording{MaxGap: 10 * time.Second}
	n := 0
	for sc.Scan() {
		n++
		if n > MaxRecordingLines {
			return nil, fmt.Errorf("recording: more than %d lines", MaxRecordingLines)
		}
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("recording line %d: %w", n, err)
		}
		p := core.LatLon{LatDeg: r.LatDeg, LonDeg: r.LonDeg}
		if !icaoPattern.MatchString(r.ICAO24) || !p.Valid() || !core.IsFinite(r.TS) || r.TS < 0 {
			return nil, fmt.Errorf("recording line %d: icao24 six lower-case hex digits, a valid position, t_s >= 0", n)
		}
		rec.records = append(rec.records, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("recording: %w", err)
	}
	if len(rec.records) == 0 {
		return nil, fmt.Errorf("recording: empty")
	}
	sort.SliceStable(rec.records, func(i, j int) bool { return rec.records[i].TS < rec.records[j].TS })
	rec.spanS = rec.records[len(rec.records)-1].TS
	return rec, nil
}

// At lists each aircraft's latest record at t.
func (r *Recording) At(t time.Time) []Sample {
	el := t.Sub(r.T0).Seconds()
	if el < 0 {
		return nil
	}
	if r.Loop && r.spanS > 0 {
		el = math.Mod(el, r.spanS+1)
	}
	latest := map[string]Record{}
	for _, rec := range r.records {
		if rec.TS > el {
			break
		}
		latest[rec.ICAO24] = rec
	}
	out := make([]Sample, 0, len(latest))
	for _, rec := range latest {
		age := time.Duration((el - rec.TS) * float64(time.Second))
		if age > r.MaxGap {
			continue
		}
		s := Sample{ICAO24: rec.ICAO24, Callsign: rec.Callsign, Pos: core.LatLon{LatDeg: rec.LatDeg, LonDeg: rec.LonDeg},
			TrackDeg: rec.TrackDeg, Squawk: rec.Squawk, At: t.Add(-age)}
		s.AltPressureM = scale(rec.AltBaroFt, core.FeetToMetres)
		s.AltWGS84M = scale(rec.AltGeomFt, core.FeetToMetres)
		s.GSMS = scale(rec.GSKt, KnotToMS)
		s.VRateMS = scale(rec.BaroRateFPM, FeetPerMinToMS)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ICAO24 < out[j].ICAO24 })
	return out
}

func scale(v *float64, f float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v * f
	return &x
}

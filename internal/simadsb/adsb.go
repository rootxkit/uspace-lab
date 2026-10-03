// Package simadsb is the e-conspicuity receiver simulator (cmd/sim-adsb,
// docs/PLAN.md L-Q5 default): it serves the manned traffic as a readsb /
// dump1090 aircraft.json over HTTP for the USSP's and the ANSP's
// readers. The keys and units are those dump1090's README-json.md names
// (feet, knots, feet per minute; uspace-ansp pins the document and its
// commit in internal/manned/dump1090/SOURCE); the tests feed the output
// to the same key list.
package simadsb

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/manned"
)

// Item is one aircraft.json entry: only keys README-json.md documents,
// each omitted when unknown, as the document says readsb does.
type Item struct {
	Hex       string   `json:"hex"`
	Flight    *string  `json:"flight,omitempty"`
	AltBaro   *float64 `json:"alt_baro,omitempty"`
	AltGeom   *float64 `json:"alt_geom,omitempty"`
	GS        *float64 `json:"gs,omitempty"`
	Track     *float64 `json:"track,omitempty"`
	BaroRate  *float64 `json:"baro_rate,omitempty"`
	Squawk    *string  `json:"squawk,omitempty"`
	Emergency string   `json:"emergency"`
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	SeenPos   float64  `json:"seen_pos"`
	Seen      float64  `json:"seen"`
	MLAT      []string `json:"mlat"`
	TISB      []string `json:"tisb"`
}

// Document is aircraft.json.
type Document struct {
	Now      float64 `json:"now"`
	Messages uint64  `json:"messages"`
	Aircraft []Item  `json:"aircraft"`
}

// emergencyNone is dump1090's emergency value for no emergency
// (emergency_enum_string, uspace-ansp SOURCE).
const emergencyNone = "none"

// Server serves aircraft.json at Path.
type Server struct {
	Source manned.Source
	Path   string
	Now    func() time.Time

	mu       sync.Mutex
	frozen   bool
	last     map[string]*manned.Sample
	down     bool
	messages atomic.Uint64
}

// Freeze stops new positions (stale knob); Thaw resumes.
func (s *Server) Freeze() { s.mu.Lock(); s.frozen = true; s.mu.Unlock() }

// Thaw resumes positions.
func (s *Server) Thaw() { s.mu.Lock(); s.frozen = false; s.mu.Unlock() }

// SetDown makes the file unavailable (503) or available.
func (s *Server) SetDown(down bool) { s.mu.Lock(); s.down = down; s.mu.Unlock() }

// Document builds aircraft.json at now.
func (s *Server) Document(now time.Time) Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]*manned.Sample{}
	}
	if !s.frozen {
		at := s.Source.At(now)
		for i := range at {
			s.last[at[i].ICAO24] = &at[i]
		}
	}
	doc := Document{Now: float64(now.UnixMilli()) / 1000, Aircraft: []Item{}}
	for _, a := range s.last {
		seen := math.Max(0, now.Sub(a.At).Seconds())
		if seen > 300 {
			continue
		}
		it := Item{Hex: a.ICAO24, Lat: a.Pos.LatDeg, Lon: a.Pos.LonDeg, SeenPos: round1(seen), Seen: round1(seen),
			Emergency: emergencyNone, Squawk: a.Squawk, Track: a.TrackDeg, MLAT: []string{}, TISB: []string{}}
		if a.Callsign != "" {
			f := fmt.Sprintf("%-8s", a.Callsign)
			it.Flight = &f
		}
		it.AltBaro = div(a.AltPressureM, core.FeetToMetres)
		it.AltGeom = div(a.AltWGS84M, core.FeetToMetres)
		it.GS = div(a.GSMS, manned.KnotToMS)
		it.BaroRate = div(a.VRateMS, manned.FeetPerMinToMS)
		if a.Emergency != nil && *a.Emergency {
			it.Emergency = "general"
		}
		doc.Aircraft = append(doc.Aircraft, it)
		s.messages.Add(1)
	}
	doc.Messages = s.messages.Load()
	return doc
}

func div(v *float64, by float64) *float64 {
	if v == nil {
		return nil
	}
	x := math.Round(*v/by*10) / 10
	return &x
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// ServeHTTP serves the file.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := s.Path
	if path == "" {
		path = "/data/aircraft.json"
	}
	if r.Method != http.MethodGet || r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Document(now()))
}

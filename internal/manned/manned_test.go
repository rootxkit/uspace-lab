package manned

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"
)

func TestLegFliesAndEnds(t *testing.T) {
	t0 := time.Unix(1_790_000_000, 0)
	from := core.LatLon{LatDeg: 41.7, LonDeg: 44.8}
	to := geodesy.Destination(from, 90, 1000)
	l := &Legs{T0: t0, Legs: []Leg{{ICAO24: "f0a001", From: from, To: to, SpeedMS: 50, AltPressureM: 600, Start: 10 * time.Second}}}
	if len(l.At(t0.Add(5*time.Second))) != 0 {
		t.Fatal("listed before its start")
	}
	s := l.At(t0.Add(20 * time.Second))
	if len(s) != 1 {
		t.Fatal("not listed under way")
	}
	d, _ := geodesy.DistanceM(from, s[0].Pos)
	if math.Abs(d-500) > 0.5 || math.Abs(*s[0].TrackDeg-90) > 0.01 || *s[0].GSMS != 50 {
		t.Fatalf("at %.1f m, track %v", d, *s[0].TrackDeg)
	}
	if len(l.At(t0.Add(31*time.Second))) != 0 {
		t.Fatal("listed after the end of its leg")
	}
}

func TestRecordingReplay(t *testing.T) {
	rec, err := LoadRecording("../../testdata/adsb/synthetic-approach.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	rec.T0 = time.Unix(1_790_000_000, 0)
	s := rec.At(rec.T0.Add(60 * time.Second))
	if len(s) != 2 {
		t.Fatalf("%d aircraft at 60 s", len(s))
	}
	a := s[0]
	// 2300 ft is 701.04 m exactly; 140 kt is 72.0222 m/s.
	if a.ICAO24 != "f0b001" || math.Abs(*a.AltPressureM-2300*0.3048) > 1e-9 || math.Abs(*a.GSMS-140*1852.0/3600) > 1e-9 {
		t.Fatalf("%+v", a)
	}
	if math.Abs(*a.VRateMS-(-700*0.3048/60)) > 1e-12 {
		t.Fatal("vertical rate conversion")
	}
	// Past the end (no loop) every aircraft ages out after MaxGap.
	if len(rec.At(rec.T0.Add(200*time.Second))) != 0 {
		t.Fatal("aircraft listed long after their last record")
	}
}

func TestRecordingRefusals(t *testing.T) {
	for _, bad := range []string{
		"",
		`{"t_s":0,"icao24":"F0B001","lat":41,"lon":44}`,
		`{"t_s":0,"icao24":"f0b001","lat":91,"lon":44}`,
		`{"t_s":-1,"icao24":"f0b001","lat":41,"lon":44}`,
		`not json`,
	} {
		if _, err := ReadRecording(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

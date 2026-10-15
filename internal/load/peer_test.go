package load

import (
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy"
	"github.com/rootxkit/uspace-core/serial"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simussp"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

const peerBlock = `peer: {aircraft: 3, centre: {north_m: 1000, east_m: 1000}, radius_m: 150, speed_ms: 5, alt_rel_m: 145,
  uspace: {centre: {north_m: 2000, east_m: 2000}, half_side_m: 400}}
`

// peerFleet is the small tier with a peer: its U-space square holds
// the first walker (at 2000, 2000) and no other.
func peerFleet(t *testing.T) *Fleet {
	t.Helper()
	tp, pp := writeFiles(t, smallTier+peerBlock, smallPaths(repo("scenarios/policy/demo.yaml")))
	f, err := LoadFiles(tp, pp, repo("load/criteria.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	geo, _, err := geoidx.Load("constant:15.9")
	if err != nil {
		t.Fatal(err)
	}
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 490}, geo)
	if err != nil {
		t.Fatal(err)
	}
	return fl
}

func newTestPeer(fl *Fleet) *peerRun {
	pr := &peerRun{tgt: &runner.LoadPeer{DisplayProvider: "authority-01", DSS: runner.TokenCfg{ClientID: "sim-ussp-01"}}, fleet: fl,
		bySerial: map[string]int{}, byFlight: map[string]int{}, display: NewHistogram(), fleetTracks: map[string]bool{},
		ops: map[string]*intentOps{}, early: map[string][]simussp.Notification{}, byState: map[string]uint64{},
		change: NewHistogram(), detailsH: NewHistogram(), inFlight: make(chan struct{}), // no details asked here
		pos: make([][peerRing]servedPos, len(fl.Peer)), next: make([]int, len(fl.Peer))}
	for i, a := range fl.Peer {
		pr.bySerial[a.Serial], pr.byFlight[simussp.FlightID(a.Serial)] = i, i
	}
	return pr
}

// The peer's aircraft are CTA-2063-A serials of the fleet's class,
// apart from every fleet serial, and they circle where the tier says, at
// its altitude; the layout's digest names the peer, so a fleet seeded
// without it (or with another) is refused. A peer altitude a walker or
// the pairs use is refused at load.
func TestPeerFleetIsLaidOut(t *testing.T) {
	fl := peerFleet(t)
	if len(fl.Peer) != 3 {
		t.Fatalf("%d peer aircraft", len(fl.Peer))
	}
	seen := map[string]bool{}
	for _, a := range fl.Aircraft {
		seen[serial.FoldKey(a.Serial)] = true
	}
	t0 := time.Now()
	for _, a := range fl.Peer {
		if err := serial.ValidateCTA2063A(a.Serial); err != nil {
			t.Fatalf("%s: %v", a.Serial, err)
		}
		if err := serial.ValidateForClass(a.Serial, LoadClassLabel); err != nil {
			t.Fatalf("%s: %v", a.Serial, err)
		}
		if seen[serial.FoldKey(a.Serial)] {
			t.Fatalf("%s is a fleet serial too", a.Serial)
		}
		s := fl.peerSample(a, 10, t0)
		c := scenario.Move(fl.originLatLon(), fl.peer.Centre)
		d, err := distance(c, s.Pos())
		if err != nil || d < 149 || d > 151 || s.AltAMSLM != 490+145 || s.AltHAEM == nil {
			t.Fatalf("%s at %v m from the centre, %v m AMSL", a.Serial, d, s.AltAMSLM)
		}
	}
	plain := peerless(t)
	if plain.Digest() == fl.Digest() {
		t.Fatal("the digest does not name the peer")
	}
	tp, pp := writeFiles(t, smallTier+strings.Replace(peerBlock, "alt_rel_m: 145", "alt_rel_m: 100", 1), smallPaths(repo("scenarios/policy/demo.yaml")))
	if _, err := LoadFiles(tp, pp, repo("load/criteria.yaml")); err == nil || !strings.Contains(err.Error(), "vertical minimum") {
		t.Fatalf("a peer at 100 m (the pairs at 115 m): %v", err)
	}
	sf := fl.SeedFleet("test")
	if sf.Peer == nil || len(sf.Peer.Serials) != 3 || sf.Peer.USpaceHalfSideM != 400 || !(sf.Peer.Area[0] < sf.Peer.Area[2] && sf.Peer.Area[1] < sf.Peer.Area[3]) {
		t.Fatalf("seed fleet peer %+v", sf.Peer)
	}
	if err := sf.Check(); err != nil {
		t.Fatalf("the seed refuses the peer fleet: %v", err)
	}
}

func peerless(t *testing.T) *Fleet {
	t.Helper()
	f := loadSmall(t)
	geo, _, _ := geoidx.Load("constant:15.9")
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 490}, geo)
	if err != nil {
		t.Fatal(err)
	}
	return fl
}

func distance(a, b core.LatLon) (float64, error) { return geodesy.DistanceM(a, b) }

// Only the intents whose outline meets the U-space square are owed to
// the peer: the first walker's, not the next one's 700 m on, nor the
// pairs' 3 km south.
func TestInUSpace(t *testing.T) {
	fl := peerFleet(t)
	var in, out []string
	for _, a := range fl.Aircraft {
		if fl.inUSpace(a) {
			in = append(in, a.Name)
		} else {
			out = append(out, a.Name)
		}
	}
	if len(in) != 1 || in[0] != "ac-00002" || len(out) != len(fl.Aircraft)-1 {
		t.Fatalf("inside %v, outside %v", in, out)
	}
	if peerless(t).inUSpace(fl.Aircraft[2]) {
		t.Fatal("a fleet without a peer has a U-space square")
	}
}

// The display is timed from the first answer to the display provider
// that held a position to the first frame showing it; an answer to
// anyone else times nothing, a frame of a position never answered to
// the display provider is counted untied, and the fleet's own network
// tracks are counted apart.
func TestPeerDisplayIsTimedFromTheDisplayProvidersAnswer(t *testing.T) {
	fl := peerFleet(t)
	pr := newTestPeer(fl)
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	a := fl.Peer[0]
	for k := range 3 {
		s := fl.peerSample(a, float64(k), t0.Add(time.Duration(k)*time.Second))
		at, _ := s.Time()
		pr.pos[0][k] = servedPos{at: at, pos: s.Pos(), occupied: true}
	}
	id := simussp.FlightID(a.Serial)
	served := func(k int) map[string]time.Time { return map[string]time.Time{id: pr.pos[0][k].at} }
	// Position 0 answered to another USS first, then to the display
	// provider 300 ms after it was made; position 1 only to another.
	pr.recordServed(false, t0.Add(100*time.Millisecond), served(0))
	pr.recordServed(true, t0.Add(300*time.Millisecond), served(0))
	pr.recordServed(false, t0.Add(1300*time.Millisecond), served(1))
	// The frames: position 0 at 1.1 s, again at 2.1 s (a repeat), and
	// position 1 at 2.2 s; one of the fleet's.
	pr.network("sim-ussp-01", "t-peer", pr.pos[0][0].pos, pr.pos[0][0].at, t0.Add(1100*time.Millisecond))
	pr.network("sim-ussp-01", "t-peer", pr.pos[0][0].pos, pr.pos[0][0].at, t0.Add(2100*time.Millisecond))
	pr.network("sim-ussp-01", "t-peer", pr.pos[0][1].pos, pr.pos[0][1].at, t0.Add(2200*time.Millisecond))
	pr.network("ussp-DEV01-01", "t-fleet", core.LatLon{LatDeg: 41.7, LonDeg: 44.8}, t0, t0)
	obs := map[string]Observation{}
	rep := pr.observe(obs, 1)
	o := obs["f3411_dp_display_s"]
	if !o.Measured || o.N != 1 || *o.Max != 0.8 {
		t.Fatalf("display %+v, want one display of 0.8 s", o)
	}
	if rep.Untied != 1 || rep.PeerFrames != 3 || rep.FleetTracks != 1 {
		t.Fatalf("report %+v", rep)
	}
	// Never polled: not measured, and why.
	none := newTestPeer(fl)
	obs = map[string]Observation{}
	none.observe(obs, 1)
	if obs["f3411_dp_display_s"].Measured || !strings.Contains(obs["f3411_dp_display_s"].Reason, "never polled") {
		t.Fatalf("an unpolled peer: %+v", obs["f3411_dp_display_s"])
	}
}

// Each change of an intent inside the U-space square is timed from the
// lab's request that made it to its notification, a notification that
// arrives before its request was noted is timed once it is, a change
// never notified is counted missing, and one naming no intent of the
// run is unmatched. Outside the square nothing is owed; with nothing
// owed the rows are not measured.
func TestPeerNotificationsAreTimedAndCounted(t *testing.T) {
	fl := peerFleet(t)
	pr := newTestPeer(fl)
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	inside, outside := fl.Aircraft[2], fl.Aircraft[3]
	pr.noteOp("i-in", inside, "Accepted", t0)
	pr.noteOp("i-out", outside, "Accepted", t0)
	pr.onNotification(simussp.Notification{At: t0.Add(400 * time.Millisecond), Standard: "f3548", EntityID: "i-in", State: "Accepted"})
	// The activation's notification before its request is noted.
	pr.onNotification(simussp.Notification{At: t0.Add(2 * time.Second), Standard: "f3548", EntityID: "i-in", State: "Activated"})
	pr.noteOp("i-in", inside, "Activated", t0.Add(1500*time.Millisecond))
	pr.noteOp("i-in", inside, "", t0.Add(10*time.Second))
	pr.onNotification(simussp.Notification{At: t0.Add(3 * time.Second), Standard: "f3548", EntityID: "nobody", State: "Accepted"})
	pr.onNotification(simussp.Notification{At: t0.Add(3 * time.Second), Standard: "f3411", EntityID: "isa"})
	pr.wg.Wait()
	obs := map[string]Observation{}
	rep := pr.observe(obs, 1)
	if o := obs["f3548_intent_change_s"]; !o.Measured || o.N != 2 || *o.Max != 0.5 {
		t.Fatalf("intent change %+v, want 0.4 s and 0.5 s", o)
	}
	if o := obs["f3548_notifications_missing"]; !o.Measured || *o.Value != 1 || len(rep.Missing) != 1 || !strings.Contains(rep.Missing[0], "deleted not notified") {
		t.Fatalf("missing %+v %v", o, rep.Missing)
	}
	if rep.IntentsInUSpace != 1 || rep.ChangesOwed != 3 || rep.Unmatched != 1 || rep.Notifications != 3 {
		t.Fatalf("report %+v", rep)
	}
	if obs["f3548_conflict_notify_s"].Measured || obs["f3548_constraint_notify_s"].Measured {
		t.Fatal("the conflict and constraint rows were measured")
	}
	nothing := newTestPeer(fl)
	nothing.noteOp("i-out", outside, "Accepted", t0)
	obs = map[string]Observation{}
	nothing.observe(obs, 1)
	if obs["f3548_intent_change_s"].Measured || obs["f3548_notifications_missing"].Measured {
		t.Fatalf("nothing owed, measured: %+v", obs)
	}
}

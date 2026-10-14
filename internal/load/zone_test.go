package load

import (
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// zoneTier is the small tier with one zone aircraft through a zone
// clear of every other path.
const zoneBlock = `zone: {aircraft: 1, centre: {north_m: -1500, east_m: 6000}, side_m: 300, spacing_m: 100, amplitude_m: 600,
  speed_ms: 10, alt_rel_m: 170, type: CONDITIONAL, raise_margin_s: 3, clear_within_s: 30}
`

func zoneFleet(t *testing.T, tier string) *Fleet {
	t.Helper()
	tp, pp := writeFiles(t, tier, smallPaths(repo("scenarios/policy/demo.yaml")))
	f, err := LoadFiles(tp, pp, repo("load/criteria.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	geo, _, _ := geoidx.Load("constant:15.9")
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 490}, geo)
	if err != nil {
		t.Fatal(err)
	}
	return fl
}

// The zone aircraft is the fleet's last, watched, flying its line
// through the zone: its entries are the samples first inside after one
// outside, each with the first sample outside after it. A zone that
// meets another path, or that its line never leaves, is refused.
func TestZoneAircraftAndEntries(t *testing.T) {
	long := strings.Replace(smallTier, "duration_s: 16", "duration_s: 200", 1)
	fl := zoneFleet(t, long+zoneBlock)
	a := fl.Aircraft[len(fl.Aircraft)-1]
	if a.Role != RoleZone || !a.Watched || a.AltRelM != 170 {
		t.Fatalf("the last aircraft %+v", a)
	}
	// From -600 m at 10 m/s the west edge (-150 m) is crossed at 45 s,
	// the east edge (+150 m) left at 76 s; back at 165 s.
	if len(fl.ZoneExpected) != 2 || fl.ZoneExpected[0].EntryS != 45 || fl.ZoneExpected[0].ExitS != 76 || fl.ZoneExpected[1].EntryS != 165 {
		t.Fatalf("entries %+v", fl.ZoneExpected)
	}
	if !fl.ZoneExpected[0].RaiseDue || !fl.ZoneExpected[0].ClearDue || fl.ZoneExpected[1].ClearDue {
		t.Fatalf("due %+v", fl.ZoneExpected)
	}
	for k := range int64(200) {
		s := fl.Sample(a, k, time.Now(), time.Now())
		e, _ := fl.zone.zoneEast(float64(k))
		in := fl.zone.inside(e)
		c := scenario.Move(fl.originLatLon(), fl.zone.Centre)
		if d, err := distance(c, s.Pos()); err != nil || (in && d > 220) || (!in && d < 149) {
			t.Fatalf("sample %d at %v m from the zone's centre, inside %v", k, d, in)
		}
	}
	if sz := fl.seedZone(); sz == nil || sz.Zone.ID != LoadZoneID || sz.Zone.Square.SideM != 300 {
		t.Fatalf("seed zone %+v", sz)
	}
	for name, block := range map[string]string{
		"on the walkers": strings.Replace(zoneBlock, "north_m: -1500, east_m: 6000", "north_m: 2000, east_m: 2000", 1),
		"never left":     strings.Replace(zoneBlock, "amplitude_m: 600", "amplitude_m: 100", 1),
	} {
		tp, pp := writeFiles(t, long+block, smallPaths(repo("scenarios/policy/demo.yaml")))
		if _, err := LoadFiles(tp, pp, repo("load/criteria.yaml")); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// An entry's raise on time is timed in ticks from the entry's sample
// and its clear found; a raise too late is a miss and a false alarm, a
// clear never seen is a missed clear, and a raise outside every entry is
// a false alarm.
func TestJudgeZone(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(seconds(s)) }
	entries := []ZoneEntry{{Aircraft: "z", K: 45, EntryS: 45, ExitS: 76, RaiseDue: true, ClearDue: true}}
	handed := func(string, int64) (time.Time, bool) { return at(45.02), true }
	ev := func(phase, id string, s float64) observe.Event {
		return observe.Event{System: scenario.SystemUSSP, Kind: "zone_incursion", AlertID: id, Phase: phase, Aircraft: "z", Subject: LoadZoneID, ObservedAt: at(s)}
	}
	good := judgeZone(entries, []observe.Event{ev(observe.PhaseRaised, "a1", 45.62), ev(observe.PhaseCleared, "a1", 79)}, t0, 1, 3, 30, handed)
	if good.Raised != 1 || good.Cleared != 1 || good.Missed+good.MissedClears+good.Unexpected != 0 || good.Ticks < 0.59 || good.Ticks > 0.61 {
		t.Fatalf("on time: %+v", good)
	}
	late := judgeZone(entries, []observe.Event{ev(observe.PhaseRaised, "a1", 52)}, t0, 1, 3, 30, handed)
	if late.Missed != 1 || late.Unexpected != 1 || late.Raised != 0 {
		t.Fatalf("too late: %+v", late)
	}
	noClear := judgeZone(entries, []observe.Event{ev(observe.PhaseRaised, "a1", 46)}, t0, 1, 3, 30, handed)
	if noClear.MissedClears != 1 {
		t.Fatalf("no clear: %+v", noClear)
	}
	stray := judgeZone(entries, []observe.Event{ev(observe.PhaseRaised, "a1", 46), ev(observe.PhaseCleared, "a1", 77), ev(observe.PhaseRaised, "a2", 100)}, t0, 1, 3, 30, handed)
	if stray.Unexpected != 1 || stray.Raised != 1 {
		t.Fatalf("a stray raise: %+v", stray)
	}
	// Two ticks of a widened monitor make the same raise one tick late.
	slow := judgeZone(entries, []observe.Event{ev(observe.PhaseRaised, "a1", 46.02)}, t0, 2, 3, 30, handed)
	if slow.Ticks < 0.49 || slow.Ticks > 0.51 {
		t.Fatalf("a 2 s tick: %+v", slow)
	}
}

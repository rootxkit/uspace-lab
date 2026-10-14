package load

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// With pairs.inner_gap_m a pair's members turn back short of each other:
// each stays on its own side of the line's centre, they are inner_gap_m
// apart at every crossing time and never closer, and each pair member's
// intent is a box on its own side that holds every sample of its path
// and does not touch the other's. The USSP rejected the second of two
// pair intents that shared the line (intent_filed_first), so a pair
// could not both fly in a systems run.
func TestPairsWithAnInnerGapKeepTheirIntentsApart(t *testing.T) {
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 30
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	a, b := fl.Aircraft[0], fl.Aircraft[1]
	pr := f.Paths.Pairs
	ba, bb := fl.pairBox(a), fl.pairBox(b)
	if !(ba.EastM < bb.WestM) {
		t.Fatalf("the boxes touch: %+v %+v", ba, bb)
	}
	period := 4 * pr.AmplitudeM / pr.SpeedMS
	for k := 0; k <= int(period*10); k++ {
		ts := float64(k) / 10
		ea, _ := fl.pairPos(a, ts)
		eb, _ := fl.pairPos(b, ts)
		if ea > -pr.InnerGapM/2+1e-9 || eb < pr.InnerGapM/2-1e-9 {
			t.Fatalf("at %v s a member left its side: %v %v", ts, ea, eb)
		}
		if eb-ea < pr.InnerGapM-1e-9 {
			t.Fatalf("at %v s the members are %v m apart", ts, eb-ea)
		}
		if ea < ba.WestM || ea > ba.EastM || eb < bb.WestM || eb > bb.EastM {
			t.Fatalf("at %v s a member left its intent: %v in %+v, %v in %+v", ts, ea, ba, eb, bb)
		}
	}
	for _, c := range []float64{pr.FirstCrossingS, pr.FirstCrossingS + 2*pr.AmplitudeM/pr.SpeedMS} {
		ea, va := fl.pairPos(a, c)
		eb, vb := fl.pairPos(b, c)
		if math.Abs(eb-ea-pr.InnerGapM) > 1e-6 || va != -vb {
			t.Fatalf("at the crossing %v s: %v %v (%v %v)", c, ea, eb, va, vb)
		}
	}
	// The intents filed are those boxes.
	req, err := (&run{fleet: fl, files: f, geo: geoidx.Constant(15.9), opt: Options{Run: "t"}}).intentRequest(a, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Volumes) != 1 || req.Volumes[0].Volume.OutlinePolygon == nil || req.Volumes[0].Volume.OutlineCircle != nil {
		t.Fatalf("pair intent %+v", req.Volumes)
	}
}

// inner_gap_m must leave the crossing inside the policy's horizontal
// minimum, or no alert would be due; and a systems run refuses pairs
// that share their line (inner_gap_m 0): the USSP would reject one of
// each pair's intents.
func TestPairInnerGapFailsClosed(t *testing.T) {
	policy := repo("scenarios/policy/demo.yaml")
	for gap, want := range map[string]string{"60": "inner_gap_m", "-5": "inner_gap_m"} {
		tp, pp := writeFiles(t, smallTier, strings.Replace(smallPaths(policy), "first_crossing_s: 6,", "first_crossing_s: 6, inner_gap_m: "+gap+",", 1))
		if _, err := LoadFiles(tp, pp, repo("load/criteria.yaml")); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("inner_gap_m %s: %v", gap, err)
		}
	}
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 0
	base, n := counting(t)
	p := systemsTargets(t, f, base, nil)
	if _, err := Run(context.Background(), Options{Files: f, TargetsPath: p, Run: "x", RepoRoot: repo(".")}); err == nil || !strings.Contains(err.Error(), "inner_gap_m") {
		t.Fatalf("a systems run with crossing pairs: %v", err)
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests sent", n.Load())
	}
}

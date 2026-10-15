package load

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/readiness"
)

// sampleAt is one outage sample: every system ready but those in down,
// the checks given, the flags given.
func sampleAt(at time.Time, down []string, checks map[string]map[string]string, details map[string]map[string]string, flags map[string][]string) outageSample {
	s := outageSample{at: at, ready: map[string]readiness.Readiness{}, flags: flags}
	for _, sys := range outageSystems {
		r := readiness.Readiness{Code: http.StatusOK, Checks: checks[sys], Details: details[sys]}
		for _, d := range down {
			if d == sys {
				r.Code = 0
				r.Err = "connection refused"
			}
		}
		s.ready[sys] = r
	}
	return s
}

// Each kind of claim is met by what it claims and unmet without it (the
// pairs E-01 asks for): the system lost, everyone else ready, a check
// degraded in time, an age that grows with the hold, a stream's flag,
// and the recovery after the restore.
func TestOutageClaimsAreJudgedBothWays(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	restored := t0.Add(300 * time.Second)
	f := &Fault{Row: FaultOutage, System: "cisp", HoldS: 300, RecoverWithinS: 90, Expect: []OutageExpect{
		{System: "cisp", Ready: "lost", WithinS: 15, Why: "w"},
		{System: "others", Ready: "always", Why: "w"},
		{System: "ansp", Check: "cisp", In: []string{"degraded", "down"}, WithinS: 90, Why: "w"},
		{System: "ussp", Check: "cis", AgeGrows: true, Why: "w"},
		{Stream: streamTraffic, Flag: "cis_stale", WithinS: 60, Why: "w"},
	}}
	// As claimed: the CISP down from 4 s, the ANSP's check degraded at
	// 40 s, the USSP's CIS age growing from 3 s to 300 s, the traffic
	// stream flagged at 20 s; everything back 30 s after the restore.
	var good, bad []outageSample
	for s := 0.0; s <= 420; s += 2 {
		at := t0.Add(seconds(s))
		var down []string
		if s >= 4 && s <= 300 {
			down = []string{"cisp"}
		}
		if s > 300 && s < 330 {
			down = []string{"cisp"}
		}
		anspState := "ok"
		if s >= 40 && s <= 300 {
			anspState = "degraded"
		}
		age := 3 + s
		if s > 300 {
			age = 2
		}
		var flags map[string][]string
		if s >= 20 && s <= 300 {
			flags = map[string][]string{streamTraffic: {"manned", "cis_stale"}}
		}
		good = append(good, sampleAt(at, down,
			map[string]map[string]string{"ansp": {"cisp": anspState}, "ussp": {"cis": "ok"}},
			map[string]map[string]string{"ussp": {"cis": "zones:0, age " + fmtNum(age) + " s"}}, flags))
		// Otherwise: the CISP never seen down, the USSP down too, the
		// ANSP's check never degraded, the age stuck, no flag, and the
		// CISP never back.
		bdown := []string{}
		if s >= 100 && s <= 300 {
			bdown = []string{"ussp"}
		}
		if s > 300 {
			bdown = []string{"cisp"}
		}
		bad = append(bad, sampleAt(at, bdown,
			map[string]map[string]string{"ansp": {"cisp": "ok"}, "ussp": {"cis": "ok"}},
			map[string]map[string]string{"ussp": {"cis": "zones:0, age 3 s"}}, nil))
	}
	for name, c := range map[string]struct {
		samples []outageSample
		met     bool
	}{"as claimed": {good, true}, "otherwise": {bad, false}} {
		res := append(judgeOutage(f, c.samples, t0, restored), judgeRecovered(f, c.samples, restored))
		if len(res) != len(f.Expect)+1 {
			t.Fatalf("%s: %d results", name, len(res))
		}
		for _, r := range res {
			if r.Met != c.met || r.Observed == "" {
				t.Errorf("%s: %s: met %v (%s), want %v", name, r.Expect.String(), r.Met, r.Observed, c.met)
			}
		}
	}
	// A check that degrades only after its bound is late, not met.
	late := []outageSample{
		sampleAt(t0, []string{"cisp"}, map[string]map[string]string{"ansp": {"cisp": "ok"}}, nil, nil),
		sampleAt(t0.Add(120*time.Second), []string{"cisp"}, map[string]map[string]string{"ansp": {"cisp": "down"}}, nil, nil),
	}
	if r := judgeOutage(&Fault{Row: FaultOutage, System: "cisp", HoldS: 300, Expect: f.Expect[2:3]}, late, t0, restored); r[0].Met {
		t.Errorf("a check degraded at 120 s met a 90 s bound: %s", r[0].Observed)
	}
	// No sample at all: nothing is met.
	if r := judgeOutage(f, nil, t0, restored); r[0].Met || !strings.Contains(r[0].Observed, "no sample") {
		t.Errorf("no samples: %+v", r[0])
	}
}

// The plan's outages are checked before the run: an outage without
// claims, a claim of no kind or two, an unknown system, a bound past the
// hold, claims on another row, and a next fault inside an outage's
// recovery are refused. The committed 60-minute plan loads.
func TestOutagePlansFailClosed(t *testing.T) {
	scripts := repo("scripts/chaos")
	plan, err := LoadFaults(repo("load/faults/60min.yaml"), scripts, 3600)
	if err != nil {
		t.Fatalf("load/faults/60min.yaml: %v", err)
	}
	if !plan.hasOutage() {
		t.Fatal("the 60-minute plan takes no system down")
	}
	const base = `format: lab-load-faults/v1
faults:
  - row: outage
    at_s: 100
    hold_s: 300
    domain: system
    args: [cisp]
    system: cisp
    recover_within_s: 90
    expect:
      - {system: cisp, ready: lost, within_s: 15, why: "down"}
  - {row: restart, at_s: 500, hold_s: 5, domain: hotpath, args: [ussp-monitor], system: ussp}
`
	if _, err := LoadFaults(writePlan(t, base), scripts, 3600); err != nil {
		t.Fatalf("the base plan: %v", err)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"no claim": {`    expect:
      - {system: cisp, ready: lost, within_s: 15, why: "down"}
`, "", "needs 1 to"},
		"two kinds":        {`ready: lost, within_s`, `ready: lost, check: cis, in: [down], within_s`, "names no check"},
		"no kind":          {`ready: lost, `, ``, "one of ready, check or stream"},
		"unknown system":   {`{system: cisp, ready`, `{system: radar, ready`, "is one of"},
		"bound past hold":  {`within_s: 15`, `within_s: 400`, "within_s"},
		"no why":           {`, why: "down"`, ``, "why is required"},
		"claims elsewhere": {`ussp-monitor], system: ussp}`, `ussp-monitor], system: ussp, expect: [{system: ussp, ready: lost, why: x}]}`, "only an outage"},
		"inside recovery":  {`at_s: 500`, `at_s: 450`, "restored and settled"},
		"check of all":     {`{system: cisp, ready: lost, within_s: 15, why: "down"}`, `{system: all, check: cis, age_grows: true, why: "down"}`, "a check names one system"},
	} {
		text := strings.Replace(base, c.old, c.new, 1)
		if text == base {
			t.Fatalf("%s: the replacement changed nothing", name)
		}
		if _, err := LoadFaults(writePlan(t, text), scripts, 3600); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// An outage fault is sampled from its injection through its recovery
// window, judged into outage_unflagged, and a run whose plan takes no
// system down does not measure the row. Without a probe the outage's
// claims are unmet, never passed.
func TestOutageFaultsAreSampledAndCounted(t *testing.T) {
	var cispDown atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/cisp") && cispDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ready","checks":{}}`))
	}))
	t.Cleanup(srv.Close)
	urls := map[string]string{}
	for _, sys := range outageSystems {
		urls[sys] = srv.URL + "/" + sys
	}
	f := Fault{Row: FaultOutage, AtS: 0.01, HoldS: 0.3, Domain: "system", Args: []string{"cisp"}, System: "cisp", RecoverWithinS: 0.3,
		Expect: []OutageExpect{{System: "cisp", Ready: "lost", WithinS: 0.2, Why: "w"}, {System: "others", Ready: "always", Why: "w"}}}
	for _, withProbe := range []bool{true, false} {
		cispDown.Store(false)
		fr := newFaultRunner(&FaultPlan{Format: FaultsFormat, Faults: []Fault{f}}, "scripts")
		fr.every = 20 * time.Millisecond
		if withProbe {
			fr.probe = &outageProbe{urls: urls, hc: srv.Client()}
		}
		fr.run = func(_ context.Context, _ string, args ...string) (string, error) {
			cispDown.Store(args[0] == "inject")
			return "chaos: done", nil
		}
		fr.execute(context.Background(), time.Now(), func(string, ...any) {})
		obs := map[string]Observation{}
		recs := fr.observe(obs, time.Now(), nil, 0, 0, nil)
		o := obs["outage_unflagged"]
		if !o.Measured {
			t.Fatalf("probe %v: outage_unflagged not measured: %s", withProbe, o.Reason)
		}
		want := 0.0
		if !withProbe {
			want = 3 // the two claims and the recovery: nothing sampled
		}
		if *o.Value != want {
			t.Errorf("probe %v: outage_unflagged %v, want %v (%+v)", withProbe, *o.Value, want, recs[0].Outage)
		}
		if withProbe && recs[0].OutageSamples < 10 {
			t.Errorf("only %d samples", recs[0].OutageSamples)
		}
	}
	none := newFaultRunner(&FaultPlan{Format: FaultsFormat, Faults: []Fault{{Row: FaultRestart, AtS: 0.01, HoldS: 0.01, Domain: "hotpath", Args: []string{"x"}, System: "ussp"}}}, "scripts")
	none.run = func(context.Context, string, ...string) (string, error) { return "", nil }
	none.execute(context.Background(), time.Now(), func(string, ...any) {})
	obs := map[string]Observation{}
	none.observe(obs, time.Now(), nil, 0, 0, nil)
	if obs["outage_unflagged"].Measured {
		t.Error("a plan without an outage measured the outage row")
	}
}

// The age a check states is read from the systems' words for it.
func TestDetailAge(t *testing.T) {
	for d, want := range map[string]float64{
		"zones:0,uspace_airspace:0,restrictions:0, age 18 s": 18,
		"last_success_age_s=4; feed_cursor=0, feed_age_s=4":  4,
		"age_s=2.5":            2.5,
		"ok (age 9 s); x":      9,
		"mapped: true":         -1,
		"down since 02:57:17Z": -1,
	} {
		got, ok := detailAge(d)
		if want < 0 {
			if ok {
				t.Errorf("%q: read age %v, want none", d, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q: %v %v, want %v", d, got, ok, want)
		}
	}
}

package load

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// systemsTargets writes a systems targets file for f's fleet as loadgen
// seed would, its secrets beside it, every endpoint at base; edit
// changes the text before it is written.
func systemsTargets(t *testing.T, f *Files, base string, edit func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	lab, err := scenario.LoadLab(repo("sim/sitl.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	fl, err := NewFleet(f, lab.Origin, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, v string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mode: systems\nname: test\ngeoid: \"constant:15.9\"\nlab: %s\n", filepath.ToSlash(repo("sim/sitl.env.example")))
	fmt.Fprintf(&b, "ussp: {base_url: %s, token_url: %s/oauth/token, audience: ussp.test}\n", base, base)
	fmt.Fprintf(&b, "authority: {base_url: %s, picture_url: %s/v1/picture/ws, origin: %s, session_file: %s}\n",
		base, strings.Replace(base, "http", "ws", 1), base, put("session", "console-session"))
	sf := fl.SeedFleet(f.Tier.Tier)
	fmt.Fprintf(&b, "load:\n  tier: %s\n  fleet: %s\n  clients:\n", sf.Tier, sf.Digest)
	for i := range sf.Clients {
		fmt.Fprintf(&b, "    - {client_id: op-%d, secret_file: %s}\n", i, put(fmt.Sprintf("op-%d", i), fmt.Sprintf("secret-%d", i)))
	}
	fmt.Fprintf(&b, "  receivers:\n")
	for _, r := range sf.Receivers {
		fmt.Fprintf(&b, "    - {id: %s, bearer_key_file: %s, hmac_key_file: %s}\n", r.ID,
			put(r.ID+".bearer", "bearer-"+r.ID), put(r.ID+".hmac", hex.EncodeToString(make([]byte, 32))))
	}
	text := b.String()
	if edit != nil {
		text = edit(text)
	}
	p := filepath.Join(dir, "targets.yaml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// counting answers 503 to everything and counts the requests.
func counting(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// A systems targets file that cannot drive this fleet is refused before
// anything is sent: no load block (not seeded), seeded for another tier,
// for another fleet layout, with a client or a receiver missing, with
// another receiver's id, a secret file missing or an HMAC key that is
// not 32 bytes. Every refusal names the reason and the systems saw no
// request. The matching file is the other half: the run starts and
// talks to the systems.
func TestSystemsTargetsFailClosed(t *testing.T) {
	f := loadSmall(t)
	f.Paths.Pairs.InnerGapM = 30
	base, n := counting(t)
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"not seeded":       {func(s string) string { head, _, _ := strings.Cut(s, "load:"); return head }, "no load block"},
		"another tier":     {func(s string) string { return strings.Replace(s, "tier: test", "tier: ci", 1) }, "seeded for tier"},
		"another fleet":    {func(s string) string { return strings.Replace(s, "fleet: sha256:", "fleet: sha256:0", 1) }, "seeded for fleet"},
		"client missing":   {func(s string) string { return strings.Replace(s, "    - {client_id: op-1,", "    #", 1) }, "operator clients"},
		"receiver missing": {func(s string) string { return s[:strings.LastIndex(s, "    - {id:")] }, "receivers"},
		"another receiver": {func(s string) string { return strings.Replace(s, "-0001,", "-0009,", 1) }, "in the fleet"},
		"secret missing":   {func(s string) string { return strings.Replace(s, "secret_file: op-0", "secret_file: absent", 1) }, "no secret"},
		"short hmac key":   {func(s string) string { return strings.Replace(s, ".hmac}", ".bearer}", 1) }, "32 bytes"},
		"no ussp":          {func(s string) string { return strings.Replace(s, "ussp: {", "ussp_off: {", 1) }, "ussp_off"},
	}
	for name, c := range cases {
		p := systemsTargets(t, f, base, c.edit)
		_, err := Run(context.Background(), Options{Files: f, TargetsPath: p, Run: "x", RepoRoot: repo(".")})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("a refused targets file sent %d requests to the systems", n.Load())
	}
	p := systemsTargets(t, f, base, nil)
	rep, err := Run(context.Background(), Options{Files: f, TargetsPath: p, Run: "x", RepoRoot: repo("."), ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("the matching targets file was refused: %v", err)
	}
	if n.Load() == 0 || rep.Target.Mode != "systems" || !strings.Contains(rep.Target.Evidence, "seeded for this fleet") {
		t.Fatalf("%d requests, target %+v", n.Load(), rep.Target)
	}
	if rep.Verdict != VerdictFail {
		t.Fatalf("a run against systems answering 503 passed")
	}
	for _, e := range rep.Errors {
		for _, s := range []string{"secret-0", "bearer-load", "console-session"} {
			if strings.Contains(e, s) {
				t.Fatalf("an error carries a secret: %s", e)
			}
		}
	}
}

// The receiver ledger takes only what a receiver heard: the systems'
// picture also shows the same aircraft from the authority's DP poller
// (network_rid), whose frames would otherwise be tied to the receiver's
// samples and timed as if a receiver had sent them. A direct_rid frame
// is shown (the presence half); a network_rid one is neither shown nor
// untraceable.
func TestPictureLedgerTakesDirectRIDOnly(t *testing.T) {
	a := &Aircraft{Name: "ac-00000", Serial: AircraftSerial(0, 1)}
	rd := &readers{picture: NewLedger(), traffic: NewReceiptLedger(), bySer: map[string]*Aircraft{a.Serial: a}, byMAC: map[string]*Aircraft{}}
	s := &stream{name: "console-00", kind: streamPicture, timed: true, ready: make(chan struct{})}
	captured := time.Now().Add(-time.Second).UTC()
	rd.picture.Hand(a.Name, captured)
	frame := func(source string) []byte {
		b, err := wire.New(wire.SchemaTrack, "authority", captured, captured, nil, "gnss", false, map[string]any{
			"track_id": "t-" + source, "source": source, "identification": map[string]any{"serial": a.Serial}})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rd.handle(s, frame("network_rid"), time.Now(), func([]byte, time.Time) {})
	if c := rd.picture.Counts(); c.Shown != 0 || c.Untraceable != 0 {
		t.Fatalf("a network_rid frame counted: %+v", c)
	}
	rd.handle(s, frame("direct_rid"), time.Now(), func([]byte, time.Time) {})
	if c := rd.picture.Counts(); c.Shown != 1 || c.Untraceable != 0 {
		t.Fatalf("the direct_rid frame: %+v", c)
	}
}

// trackFrame is a track/telemetry/v1 on the picture, captured at
// captured, from source with the identification given.
func trackFrame(t *testing.T, trackID, source string, serial *string, captured time.Time) []byte {
	t.Helper()
	b, err := wire.New(wire.SchemaTrack, "authority", captured, captured, nil, "gnss", false, map[string]any{
		"track_id": trackID, "source": source, "identification": map[string]any{"serial": serial}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A frame that shows no new sample says nothing about what is still in
// flight: the systems' picture and traffic streams go on sending the
// last state of a track (stale) after the generator stops, and the
// drain, which waited for three seconds without data, never settled
// (drain_s spent on every systems run). Only a frame that shows a
// sample for the first time is data; a repeat is not (the absence
// half), a new sample is (presence).
func TestOnlyANewSampleIsData(t *testing.T) {
	a := &Aircraft{Name: "ac-00000", Serial: AircraftSerial(0, 1)}
	rd := &readers{picture: NewLedger(), traffic: NewReceiptLedger(), bySer: map[string]*Aircraft{a.Serial: a}, byMAC: map[string]*Aircraft{}}
	s := &stream{name: "console-00", kind: streamPicture, timed: true, ready: make(chan struct{})}
	captured := time.Now().Add(-time.Second).UTC()
	rd.picture.Hand(a.Name, captured)
	sn := a.Serial
	first := time.Now()
	rd.handle(s, trackFrame(t, "t1", sourceDirectRID, &sn, captured), first, func([]byte, time.Time) {})
	if !s.lastData.Equal(first) {
		t.Fatalf("a new sample did not count as data: %v", s.lastData)
	}
	rd.handle(s, trackFrame(t, "t1", sourceDirectRID, &sn, captured), first.Add(5*time.Second), func([]byte, time.Time) {})
	rd.handle(s, trackFrame(t, "t1", "network_rid", &sn, captured), first.Add(6*time.Second), func([]byte, time.Time) {})
	if !s.lastData.Equal(first) {
		t.Fatalf("a repeated or other-source frame counted as data: %v", s.lastData)
	}
}

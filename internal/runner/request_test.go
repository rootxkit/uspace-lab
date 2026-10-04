package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simop"
)

// A request step carries the console session the way the system reads
// it: with session_bearer as Authorization: Bearer beside the cookie
// (the ANSP and the authority read the bearer; found by the WP-L6
// systems run, where the cookie alone was answered 401), without it as
// the cookie alone; and its headers with the run's substitutions (the
// ANSP's Idempotency-Key).
func TestRequestCarriesTheSessionAsTheSystemReadsIt(t *testing.T) {
	type seen struct{ auth, cookie, csrf, idem string }
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("X-CSRF-Token"), r.Header.Get("Idempotency-Key")}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"r1"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	for name, v := range map[string]string{"s": "the-session", "c": "the-csrf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(bearer bool) *run {
		return &run{
			opt:      Options{Run: "run-7"},
			captures: map[string]string{},
			tg: &Targets{dir: dir, Requests: map[string]RequestAuth{
				"ansp": {BaseURL: srv.URL, SessionFile: "s", CSRFFile: "c", SessionBearer: bearer},
			}},
		}
	}
	q := &scenario.Request{System: "ansp", Method: http.MethodPost, Path: "/v1/restrictions", Expect: http.StatusCreated,
		Headers: map[string]string{"Idempotency-Key": "lab-${run}-plan"}, Capture: map[string]string{"rid": "id"}}

	r := mk(true)
	if _, err := r.request(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if got.auth != "Bearer the-session" || got.cookie != "uspace_session=the-session; uspace_csrf=the-csrf" ||
		got.csrf != "the-csrf" || got.idem != "lab-run-7-plan" {
		t.Fatalf("session_bearer: %+v", got)
	}
	if r.captures["rid"] != "r1" {
		t.Fatalf("captures %v", r.captures)
	}
	// Absence beside the presence: without session_bearer no bearer.
	if _, err := mk(false).request(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if got.auth != "" || got.cookie == "" {
		t.Fatalf("cookie only: %+v", got)
	}
}

// A header's lab placeholders are filled as a body's are: ${time:S} as
// the RFC 3339 time t0 + S seconds and ${lat:N,E}/${lng:N,E} as the
// offset's number. Found by wp12, whose Idempotency-Key went out as the
// literal "${time:0}", the same on every execution.
func TestRequestFillsTheLabPlaceholdersInHeaders(t *testing.T) {
	var idem, where string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idem, where = r.Header.Get("Idempotency-Key"), r.Header.Get("X-Lab-Where")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	lab, err := scenario.LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	r := &run{opt: Options{Run: "run-7"}, captures: map[string]string{}, lab: lab, t0: t0,
		tg: &Targets{Requests: map[string]RequestAuth{"ansp": {BaseURL: srv.URL}}}}
	q := &scenario.Request{System: "ansp", Method: http.MethodPost, Path: "/v1/restrictions", Expect: http.StatusCreated,
		Headers: map[string]string{"Idempotency-Key": "lab-${run}-plan-${time:30}", "X-Lab-Where": "${lat:0,0},${lng:0,0}"}}
	if _, err := r.request(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if want := "lab-run-7-plan-2026-10-04T09:00:30Z"; idem != want {
		t.Fatalf("Idempotency-Key %q, want %q", idem, want)
	}
	o := lab.At(scenario.Offset{})
	if want := strconv.FormatFloat(o.LatDeg, 'f', 7, 64) + "," + strconv.FormatFloat(o.LonDeg, 'f', 7, 64); where != want {
		t.Fatalf("X-Lab-Where %q, want %q", where, want)
	}
}

// A request still holding a ${...} the runner could not fill (a capture
// an earlier step never made, an extra the targets file lacks, a
// malformed placeholder) is refused before it is sent, naming where it
// stands, instead of reaching the system as literal text. Beside it the
// same request filled is sent.
func TestRequestRefusesAnUnfilledPlaceholder(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	lab, err := scenario.LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	r := &run{opt: Options{Run: "run-7"}, captures: map[string]string{"rid": "r1"}, lab: lab, t0: time.Unix(1_791_100_000, 0),
		tg: &Targets{Requests: map[string]RequestAuth{"ansp": {BaseURL: srv.URL}}, Extra: map[string]any{"air": "ua-1"}}}
	filled := func() *scenario.Request {
		return &scenario.Request{System: "ansp", Method: http.MethodPost, Path: "/v1/restrictions/${rid}/end",
			Headers: map[string]string{"Idempotency-Key": "lab-${run}-${time:0}"},
			Body:    map[string]any{"airspace": "${extra:air}", "at": "${time:5}", "lat": "${lat:0,0}"}}
	}
	if _, err := r.request(context.Background(), filled()); err != nil || hits.Load() != 1 {
		t.Fatalf("filled: hits %d, %v", hits.Load(), err)
	}
	for name, spoil := range map[string]func(q *scenario.Request){
		"path":       func(q *scenario.Request) { q.Path = "/v1/restrictions/${restriction_id}/end" },
		"header":     func(q *scenario.Request) { q.Headers["Idempotency-Key"] = "lab-${time:soon}" },
		"header key": func(q *scenario.Request) { q.Headers["X-${run_id}"] = "v" },
		"body":       func(q *scenario.Request) { q.Body["airspace"] = "${extra:missing}" },
		"body lat":   func(q *scenario.Request) { q.Body["lat"] = "${lat:north}" },
	} {
		q := filled()
		spoil(q)
		_, err := r.request(context.Background(), q)
		if err == nil || !strings.Contains(err.Error(), "unfilled placeholder") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("a request with an unfilled placeholder was sent: %d hits", n)
	}
}

// The runner ends the intents it left open (accepted or activated) and
// records the answer; a rejected intent is not touched. Without it a
// run's intent outlives the run and the next run's intent over the same
// volume is refused as filed second (uspace-ussp WP-7 runbook, step 2).
func TestRunEndsTheIntentsItLeftOpen(t *testing.T) {
	var ended []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
		case r.Method == http.MethodPatch && r.Header.Get("Authorization") == "Bearer tok":
			ended = append(ended, filepath.Base(r.URL.Path))
			_, _ = w.Write([]byte(`{"intent_id":"` + filepath.Base(r.URL.Path) + `","decision":"authorised","state":"ended"}`))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "op.secret"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &run{
		tg: &Targets{Mode: ModeSystems, dir: dir, USSP: &USSPTarget{BaseURL: srv.URL, TokenURL: srv.URL + "/oauth/token", Audience: "u",
			Clients: map[string]Client{"default": {ClientID: "op", SecretFile: "op.secret"}}}},
		sc: &scenario.Scenario{Aircraft: []scenario.Aircraft{
			{Name: "a", Operator: &scenario.Operator{System: scenario.SystemUSSP}},
			{Name: "b", Operator: &scenario.Operator{System: scenario.SystemUSSP}},
			{Name: "c", Operator: &scenario.Operator{System: scenario.SystemUSSP}},
		}},
		log: slog.New(slog.DiscardHandler),
	}
	res := &Result{Intents: []IntentRecord{
		{Aircraft: "a", IntentID: "i-a", Decision: "authorised", State: "activated"},
		{Aircraft: "b", IntentID: "i-b", Decision: "rejected", State: "rejected"},
		{Aircraft: "c", IntentID: "i-c", Decision: "authorised", State: "accepted"},
	}}
	r.endIntents(context.Background(), res)
	if len(ended) != 2 || ended[0] != "i-a" || ended[1] != "i-c" {
		t.Fatalf("ended %v", ended)
	}
	if res.Intents[0].Ended != "ended" || res.Intents[1].Ended != "" || res.Intents[2].Ended != "ended" {
		t.Fatalf("%+v", res.Intents)
	}
}

type fixedToken string

func (f fixedToken) Token(context.Context) (string, error) { return string(f), nil }
func (fixedToken) Invalidate()                             {}

// The intent record keeps the volumes the request filed, as sent, so a
// decision is tied to the airspace asked for; a request that could not
// be built files nothing and records none.
func TestIntentRecordKeepsTheFiledVolumes(t *testing.T) {
	var filed simop.IntentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/intents" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&filed); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"intent_id":"i-a","decision":"authorised","state":"activated"}`))
	}))
	defer srv.Close()
	req := simop.IntentRequest{ClientRef: "r", Volumes: []simop.Volume4D{{Volume: simop.Volume3D{
		OutlineCircle: &simop.Circle{Center: simop.Point{Lat: 41.7, Lng: 44.8}, Radius: simop.Radius{Value: 150, Units: "M"}},
		AltitudeLower: simop.IntentAltitude{Value: 500, Reference: "W84", Units: "M"},
		AltitudeUpper: simop.IntentAltitude{Value: 560, Reference: "W84", Units: "M"},
	}}}}
	ins := &simop.Intents{BaseURL: srv.URL, Tokens: fixedToken("tok")}
	rec := fileIntent(context.Background(), ins, "a", req, nil)
	if rec.Error != "" || rec.IntentID != "i-a" || rec.State != "activated" {
		t.Fatalf("%+v", rec)
	}
	got, _ := json.Marshal(rec.Volumes)
	sent, _ := json.Marshal(filed.Volumes)
	if len(rec.Volumes) != 1 || !bytes.Equal(got, sent) {
		t.Fatalf("recorded %s, filed %s", got, sent)
	}
	if b, _ := json.Marshal(rec); !strings.Contains(string(b), `"volumes":[{`) {
		t.Fatalf("the record does not carry the volumes: %s", b)
	}
	failed := fileIntent(context.Background(), ins, "b", simop.IntentRequest{}, os.ErrNotExist)
	if failed.Error == "" || failed.Volumes != nil {
		t.Fatalf("%+v", failed)
	}
}

// A systems targets file without a USSP runs a scenario that streams to
// none (SC-22, the authority alone) instead of panicking on the missing
// USSP, and refuses, by name, one whose aircraft streams to a USSP.
func TestOperatorsWithoutAUSSPInTheTargets(t *testing.T) {
	r := &run{tg: &Targets{Mode: ModeSystems}, sc: &scenario.Scenario{Aircraft: []scenario.Aircraft{{Name: "a"}}}}
	if err := r.startOperators(context.Background(), context.Background(), nil); err != nil {
		t.Fatalf("no operator aircraft: %v", err)
	}
	r.sc.Aircraft[0].Operator = &scenario.Operator{System: scenario.SystemUSSP}
	err := r.startOperators(context.Background(), context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "has no ussp") {
		t.Fatalf("an operator aircraft: %v", err)
	}
	r.endIntents(context.Background(), &Result{Intents: []IntentRecord{{Aircraft: "a", IntentID: "i", State: "activated"}}})
}

// An intent's client_ref is new per execution and always valid: the
// same run id again (the same results directory) must not reuse one,
// which a USSP answers 409 idempotency_conflict (found re-running
// ussp-wp10-conformance under its run id on the WP-L6 systems stack).
func TestClientRefIsNewPerExecutionAndValid(t *testing.T) {
	t0 := time.Unix(1_791_100_000, 0)
	a := clientRef("20261004-systems", "ussp-wp10-conformance", "a", t0)
	b := clientRef("20261004-systems", "ussp-wp10-conformance", "a", t0.Add(400*time.Second))
	if a == b {
		t.Fatalf("one reference for two executions: %s", a)
	}
	long := clientRef(strings.Repeat("r", 40), strings.Repeat("s", 40), "a", t0)
	for _, ref := range []string{a, b, long} {
		if len(ref) > 64 || !clientRefPattern.MatchString(ref) {
			t.Errorf("invalid client_ref %q", ref)
		}
	}
}

// Without git where the runner runs, the lab commit comes from the
// binary's VCS stamp; with git, git's answer stands.
func TestLabCommitFromTheBuildWhenGitCannotBeAsked(t *testing.T) {
	st := []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}}
	c := withBuildVCS(Commits{Lab: "unknown"}, st)
	if c.Lab != "abc123" || !c.LabDirty {
		t.Fatalf("%+v", c)
	}
	if c := withBuildVCS(Commits{Lab: "def456"}, st); c.Lab != "def456" || c.LabDirty {
		t.Fatalf("git's answer overridden: %+v", c)
	}
}

// The picture subscription is a console/subscribe/v1 (schemas/common/
// console/subscribe/v1: a [west, south, east, north] box and known
// layers) about the origin, with the alerts layer.
func TestPictureSubscribeIsValid(t *testing.T) {
	lab, err := scenario.LoadLab("../../sim/sitl.env.example")
	if err != nil {
		t.Fatal(err)
	}
	b := (&run{lab: lab}).pictureSubscribe()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	body := m["body"].(map[string]any)
	bb := body["bbox"].([]any)
	if m["schema"] != "console/subscribe/v1" || len(bb) != 4 || bb[1].(float64) >= bb[3].(float64) {
		t.Fatalf("%s", b)
	}
	for _, l := range body["layers"].([]any) {
		if l != "tracks" && l != "manned" && l != "alerts" && l != "zones" {
			t.Fatalf("unknown layer %v", l)
		}
	}
	if !(bb[0].(float64) < lab.Origin.LonDeg && lab.Origin.LonDeg < bb[2].(float64)) || !strings.Contains(string(b), `"alerts"`) {
		t.Fatalf("%s", b)
	}
}

package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/scenario"
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

package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

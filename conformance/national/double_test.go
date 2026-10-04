package national

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

// The double's one existing thing and its ETag.
const (
	thingID   = "01K6P0A1B2C3D4E5F6G7H8J9KM"
	thingETag = `"thing:1"`
)

// faults switch the double's deliberate defects on, one per test.
type faults struct {
	// missingErrors leaves errors[] out of every 400 problem body.
	missingErrors bool
	// pii puts a name and an e-mail in the registry answer.
	pii bool
	// openRead answers GET /v1/things without a token (fail open).
	openRead bool
	// plain401 answers 401 as text/plain.
	plain401 bool
	// emptyErrors answers a 400 with errors: [].
	emptyErrors bool
	// ignoreIfMatch applies a PATCH whatever its If-Match.
	ignoreIfMatch bool
	// badSuccess answers GET /v1/things/{id} with a body the schema refuses.
	badSuccess bool
	// consoleOff answers the console operation 503, before any check.
	consoleOff bool
}

// double serves the fixture contract correctly unless a fault is set.
func double(t *testing.T, f faults) *httptest.Server {
	t.Helper()
	problem := func(w http.ResponseWriter, status int, slug string, errs []map[string]string) {
		if status == http.StatusUnauthorized && f.plain401 {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("no credential"))
			return
		}
		body := map[string]any{"type": "https://schemas.uspace.ge/problems/" + slug, "title": slug, "status": status}
		if status != http.StatusBadRequest || !f.missingErrors {
			if errs == nil {
				errs = []map[string]string{}
			}
			if status == http.StatusBadRequest && f.emptyErrors {
				errs = []map[string]string{}
			}
			body["errors"] = errs
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	ok := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	// scopes is the scopes of a "tok:a,b" bearer; session the bearer
	// "session-ok".
	auth := func(r *http.Request) (scopes []string, session, present bool) {
		h := r.Header.Get("Authorization")
		if h == "" {
			return nil, false, false
		}
		v := strings.TrimPrefix(h, "Bearer ")
		if v == "session-ok" {
			return nil, true, true
		}
		if s, found := strings.CutPrefix(v, "tok:"); found {
			return strings.Split(s, ","), false, true
		}
		return nil, false, true
	}
	needScope := func(w http.ResponseWriter, r *http.Request, scope string, sessionOK bool) bool {
		sc, sess, present := auth(r)
		switch {
		case !present:
			problem(w, http.StatusUnauthorized, "unauthenticated", nil)
			return false
		case sess && sessionOK:
			return true
		case slices.Contains(sc, scope):
			return true
		}
		problem(w, http.StatusForbidden, "forbidden", nil)
		return false
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { ok(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/things", func(w http.ResponseWriter, r *http.Request) {
		if f.openRead && r.Header.Get("Authorization") == "" {
			ok(w, 200, map[string]any{"things": []any{}})
			return
		}
		if needScope(w, r, "things.read", false) {
			ok(w, 200, map[string]any{"things": []any{map[string]string{"id": thingID, "name": "lamp"}}})
		}
	})
	mux.HandleFunc("POST /v1/things", func(w http.ResponseWriter, r *http.Request) {
		if !needScope(w, r, "things.write", false) {
			return
		}
		if r.Header.Get("Idempotency-Key") == "" {
			problem(w, 400, "invalid_request", []map[string]string{{"field": "Idempotency-Key", "reason": "required"}})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			problem(w, 400, "invalid_request", []map[string]string{{"field": "$", "reason": "not JSON"}})
			return
		}
		if n, _ := body["name"].(string); n == "" {
			problem(w, 400, "invalid_request", []map[string]string{{"field": "$.name", "reason": "required"}})
			return
		}
		ok(w, 201, map[string]string{"id": thingID, "name": body["name"].(string)})
	})
	mux.HandleFunc("GET /v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !needScope(w, r, "things.read", true) {
			return
		}
		if r.PathValue("id") != thingID {
			problem(w, 404, "not_found", nil)
			return
		}
		if f.badSuccess {
			ok(w, 200, map[string]string{"id": "not-a-ulid"})
			return
		}
		ok(w, 200, map[string]string{"id": thingID, "name": "lamp"})
	})
	mux.HandleFunc("PATCH /v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !needScope(w, r, "things.write", false) {
			return
		}
		if r.PathValue("id") != thingID {
			problem(w, 404, "not_found", nil)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			problem(w, 400, "invalid_request", []map[string]string{{"field": "$", "reason": "not JSON"}})
			return
		}
		if n, has := body["name"]; has {
			if s, _ := n.(string); s == "" {
				problem(w, 400, "invalid_request", []map[string]string{{"field": "$.name", "reason": "a string"}})
				return
			}
		}
		if r.Header.Get("If-Match") != thingETag && !f.ignoreIfMatch {
			problem(w, 412, "precondition_failed", nil)
			return
		}
		ok(w, 200, map[string]string{"id": thingID, "name": "lantern"})
	})
	mux.HandleFunc("GET /v1/registry/validate", func(w http.ResponseWriter, r *http.Request) {
		if !needScope(w, r, "registry.validate", false) {
			return
		}
		if r.URL.Query().Get("registration") == "" {
			problem(w, 400, "invalid_request", []map[string]string{{"field": "registration", "reason": "required"}})
			return
		}
		ans := map[string]any{"status": "valid"}
		if f.pii {
			ans["holder"] = map[string]string{"name": "A. Person", "email": "a.person@example.invalid"}
		}
		ok(w, 200, ans)
	})
	mux.HandleFunc("GET /v1/console/me", func(w http.ResponseWriter, r *http.Request) {
		if f.consoleOff {
			problem(w, http.StatusServiceUnavailable, "console_unavailable", nil)
			return
		}
		_, sess, present := auth(r)
		if !present {
			problem(w, 401, "unauthenticated", nil)
			return
		}
		if !sess {
			problem(w, 403, "forbidden", nil)
			return
		}
		ok(w, 200, map[string]string{"role": "viewer"})
	})
	mux.HandleFunc("POST /v1/receivers/observations", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, 401, "unauthenticated", nil)
	})
	// A body in a media type other than JSON (a compact JWS, as the
	// ANSP's receiveCisNotification): a request without its declared
	// Content-Type is refused 415 before any credential is looked at,
	// as a real system does.
	mux.HandleFunc("POST /v1/receivers/frames", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/jose" {
			problem(w, 415, "unsupported_media_type", nil)
			return
		}
		problem(w, 401, "unauthenticated", nil)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeCreds mints the double's tokens: "tok:<scopes>".
type fakeCreds struct {
	session bool
	offers  []string
}

func (fakeCreds) Token(_ context.Context, _ string, scopes []string) (string, error) {
	return "tok:" + strings.Join(scopes, ","), nil
}

func (f fakeCreds) WrongScope(_ context.Context, _ string, avoid []string) (string, []string, error) {
	for _, s := range f.offers {
		if !slices.Contains(avoid, s) {
			return "tok:" + s, []string{s}, nil
		}
	}
	return "", nil, ErrNoCredential
}

func (f fakeCreds) Session(context.Context) (string, error) {
	if !f.session {
		return "", ErrNoCredential
	}
	return "session-ok", nil
}

func (fakeCreds) ClientCertificate() bool { return false }

func fixtureRunner(t *testing.T, srv *httptest.Server, creds Credentials) *Runner {
	t.Helper()
	ov := Overrides{System: "fixture", Schemes: map[string]SchemeRule{
		"ecosystemToken": {Kind: "token", Issuer: IssuerEcosystem},
		"consoleSession": {Kind: "session"},
		"sessionCookie":  {Kind: "session"},
		"receiverKey":    {Kind: "special", Note: "a receiver key"},
	}}
	b, err := os.ReadFile(filepath.Join("testdata", "fixture.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadContract("fixture", b, ov, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prob, err := CompileProblem(filepath.Join("..", "..", "schemas", "common"))
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{Contract: c, BaseURL: srv.URL, Creds: creds, HTTP: NewHTTPClient(nil),
		Fixtures: map[string]string{"id": thingID}, Problem: prob}
}

var piiRule = NoPIIRule{Requirement: "REG-NOPII", Operation: "validateRegistry", Fields: []string{"name", "email", "phone", "address"}}

func runAll(t *testing.T, r *Runner) []result.Outcome {
	t.Helper()
	out := r.Run(context.Background())
	out = append(out, r.RunNoPII(context.Background(), []NoPIIRule{piiRule})...)
	for _, o := range out {
		if err := o.Validate(); err != nil {
			t.Errorf("invalid outcome: %v", err)
		}
	}
	return out
}

func byRequirement(out []result.Outcome) map[string][]result.Outcome {
	m := map[string][]result.Outcome{}
	for _, o := range out {
		m[o.Requirement] = append(m[o.Requirement], o)
	}
	return m
}

func dump(t *testing.T, out []result.Outcome) {
	t.Helper()
	for _, o := range out {
		t.Logf("%-16s %-18s %-14s %3d %-50s %s %s", o.Requirement, o.Check, o.Status, o.HTTPStatus, o.Subject, o.Reason, o.Detail)
	}
}

// TestDoublePasses is the branch that says nothing is wrong (E-02): the
// correct double passes every requirement, and the checks that cannot
// apply say why instead of passing.
func TestDoublePasses(t *testing.T) {
	r := fixtureRunner(t, double(t, faults{}), fakeCreds{session: true, offers: []string{"police.query"}})
	out := runAll(t, r)
	for req, os := range byRequirement(out) {
		st, reasons := result.Fold(os)
		if st != result.Pass {
			dump(t, out)
			t.Errorf("%s: %s %v, want pass", req, st, reasons)
		}
	}
	// Every kind ran at least once and passed.
	for _, check := range append(slices.Clone(CheckKinds), CheckNoPII) {
		found := false
		for _, o := range out {
			found = found || o.Check == check && o.Status == result.Pass
		}
		if !found {
			dump(t, out)
			t.Errorf("no passing %s check", check)
		}
	}
	// The receiver operation is checked for 401 and nothing else.
	for _, o := range out {
		if (strings.HasPrefix(o.Subject, "postObservations ") || strings.HasPrefix(o.Subject, "postFrames ")) && o.Check != CheckUnauthenticated && o.Status != result.NotApplicable {
			t.Errorf("receiver operation: %s %s", o.Check, o.Status)
		}
	}
}

// TestDoubleFaults: each deliberate defect fails its requirement and
// only its requirement (E-01: the presence half of TestDoublePasses).
func TestDoubleFaults(t *testing.T) {
	cases := []struct {
		name string
		f    faults
		want string
	}{
		{"missing errors[]", faults{missingErrors: true}, ReqInvalidBody},
		{"empty errors[]", faults{emptyErrors: true}, ReqInvalidBody},
		{"personal data in registry/validate", faults{pii: true}, "REG-NOPII"},
		{"a read open without a token", faults{openRead: true}, ReqUnauthenticated},
		{"a 401 that is not a problem body", faults{plain401: true}, ReqUnauthenticated},
		{"a stale If-Match applied", faults{ignoreIfMatch: true}, ReqPrecondition},
		{"a success body the schema refuses", faults{badSuccess: true}, ReqSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureRunner(t, double(t, tc.f), fakeCreds{session: true, offers: []string{"police.query"}})
			out := runAll(t, r)
			for req, os := range byRequirement(out) {
				st, _ := result.Fold(os)
				if req == tc.want && st != result.Fail {
					dump(t, out)
					t.Errorf("%s: %s, want fail", req, st)
				}
				if req != tc.want && st == result.Fail {
					dump(t, out)
					t.Errorf("%s: fail, want only %s to fail", req, tc.want)
				}
			}
		})
	}
}

// TestMissingCredentialsAreNotApplicable: without a session or a token
// for a wrong scope the checks that need them say so; none passes.
func TestMissingCredentialsAreNotApplicable(t *testing.T) {
	r := fixtureRunner(t, double(t, faults{}), fakeCreds{})
	out := runAll(t, r)
	for _, o := range out {
		if o.Check == CheckWrongScope && o.Status != result.NotApplicable {
			t.Errorf("%s %s: %s without a wrong-scope token", o.Check, o.Subject, o.Status)
		}
		if strings.HasPrefix(o.Subject, "getConsoleMe ") && o.Check == CheckSuccess {
			if o.Status != result.NotApplicable || !strings.Contains(o.Reason, "session") {
				t.Errorf("getConsoleMe success without a session: %s %q", o.Status, o.Reason)
			}
		}
	}
}

// TestNoFixtureNoPrecondition: without the existing thing named, the
// stale If-Match check does not apply (it never guesses a resource).
func TestNoFixtureNoPrecondition(t *testing.T) {
	r := fixtureRunner(t, double(t, faults{}), fakeCreds{session: true, offers: []string{"police.query"}})
	r.Fixtures = nil
	for _, o := range r.Run(context.Background()) {
		if o.Check == CheckPrecondition && o.Status != result.NotApplicable {
			t.Errorf("%s: %s without a fixture", o.Subject, o.Status)
		}
	}
}

// TestUnavailableIsNotApplicable: an operation the target has switched
// off (503 with a problem body, declared) leaves its refusals
// unobserved: not applicable with that reason, never a pass, and never
// a failure of the refusal.
func TestUnavailableIsNotApplicable(t *testing.T) {
	r := fixtureRunner(t, double(t, faults{consoleOff: true}), fakeCreds{session: true, offers: []string{"police.query"}})
	seen := false
	for _, o := range r.Run(context.Background()) {
		if !strings.HasPrefix(o.Subject, "getConsoleMe ") {
			continue
		}
		switch o.Check {
		case CheckUnauthenticated:
			seen = true
			if o.Status != result.NotApplicable || !strings.Contains(o.Reason, "unavailable") {
				t.Errorf("unauthenticated on a 503 operation: %s %q", o.Status, o.Reason)
			}
		case CheckSuccess:
			if o.Status != result.Fail {
				t.Errorf("success on a 503 operation: %s, want fail", o.Status)
			}
		}
	}
	if !seen {
		t.Error("no unauthenticated check of getConsoleMe")
	}
}

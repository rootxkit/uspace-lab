package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The committed fixture is the test operator of uspace-ussp WP-21: a
// number the authority's registry admits (no hyphen) and the eight
// TEST-WP21-* serials its test-interfaces process flies under.
func TestTestOperatorsFixture(t *testing.T) {
	f, err := LoadTestOperators("../../conformance/fixtures/test-operators.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Operators) != 1 {
		t.Fatalf("%d operators", len(f.Operators))
	}
	op := f.Operators[0]
	if op.RegistrationNumber != "GEOTESTWP210001" || op.ValidDays != 365 {
		t.Fatalf("operator %+v", op)
	}
	var want []string
	for i := 1; i <= 8; i++ {
		want = append(want, fmt.Sprintf("TEST-WP21-%04d", i))
	}
	if !slices.Equal(op.Serials, want) {
		t.Fatalf("serials %v, want %v", op.Serials, want)
	}
}

const baseTestOperators = `format: lab-test-operators/v1
operators:
  - registration_number: GEOTESTWP210001
    name: test operator
    email: t@example.test
    phone: "+995000000901"
    valid_days: 365
    serials: [TEST-1, TEST-2]
`

func writeTestOperators(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test-operators.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Every refusal beside the acceptance of the base file it differs from
// by one edit (E-01), each past its bound where it has one (E-10). The
// first is what the WP-21 fixture's number GEO-TEST-WP21-0001 was: the
// authority refuses a registration number with a hyphen.
func TestLoadTestOperatorsRefusals(t *testing.T) {
	if _, err := LoadTestOperators(writeTestOperators(t, baseTestOperators)); err != nil {
		t.Fatalf("base refused: %v", err)
	}
	many := "format: lab-test-operators/v1\noperators:\n"
	for i := 0; i <= MaxTestOperators; i++ {
		many += fmt.Sprintf("  - {registration_number: GEOTEST%08d, name: n, email: e, phone: p, valid_days: 1}\n", i)
	}
	var sns []string
	for i := 0; i <= MaxTestOperatorSerials; i++ {
		sns = append(sns, "S"+strconv.Itoa(i))
	}
	second := strings.Replace(strings.SplitN(baseTestOperators, "operators:\n", 2)[1], "TEST-1, TEST-2", "TEST-3", 1)
	for _, tc := range []struct{ name, body, want string }{
		{"number with a hyphen", strings.Replace(baseTestOperators, "GEOTESTWP210001", "GEO-TEST-WP21-0001", 1), "registration_number"},
		{"number too short", strings.Replace(baseTestOperators, "GEOTESTWP210001", "GEOTEST", 1), "registration_number"},
		{"format", strings.Replace(baseTestOperators, "lab-test-operators/v1", "lab-test-operators/v2", 1), "format"},
		{"unknown member", baseTestOperators + "    class: C2\n", "class"},
		{"no operator", "format: lab-test-operators/v1\noperators: []\n", "no operator"},
		{"too many operators", many, "more than"},
		{"operator twice", baseTestOperators + second, "listed twice"},
		{"empty name", strings.Replace(baseTestOperators, "name: test operator", `name: " "`, 1), "name is empty"},
		{"valid_days 0", strings.Replace(baseTestOperators, "valid_days: 365", "valid_days: 0", 1), "valid_days"},
		{"valid_days past the bound", strings.Replace(baseTestOperators, "valid_days: 365", "valid_days: "+strconv.Itoa(MaxTestOperatorDays+1), 1), "valid_days"},
		{"serial twice, other case", strings.Replace(baseTestOperators, "TEST-1, TEST-2", "TEST-1, test-1", 1), "also"},
		{"empty serial", strings.Replace(baseTestOperators, "TEST-1, TEST-2", `TEST-1, ""`, 1), "empty"},
		{"serial with white space", strings.Replace(baseTestOperators, "TEST-1, TEST-2", `"TEST 1"`, 1), "white space"},
		{"serial too long", strings.Replace(baseTestOperators, "TEST-1, TEST-2", strings.Repeat("A", 21), 1), "longer"},
		{"too many serials", strings.Replace(baseTestOperators, "TEST-1, TEST-2", strings.Join(sns, ", "), 1), "more than"},
		{"file past the bound", baseTestOperators + "#" + strings.Repeat("x", MaxTestOperatorsBytes) + "\n", "bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadTestOperators(writeTestOperators(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
	if _, err := LoadTestOperators(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("an absent file was accepted")
	}
}

// fakeRegistry is the authority's registry console API as the seed
// calls it: operators and aircraft created once, a second creation 409.
type fakeRegistry struct {
	mu        sync.Mutex
	operators map[string]map[string]any // number -> body
	ids       map[string]string         // number -> id
	uas       map[string]map[string]any // serial -> body
	creates   int
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/registry/operators":
		n, _ := body["registration_number"].(string)
		if _, ok := f.operators[n]; ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.creates++
		f.operators[n] = body
		f.ids[n] = fmt.Sprintf("%032d", len(f.ids)+1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": f.ids[n]})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/registry/operators":
		n := r.URL.Query().Get("number")
		_ = json.NewEncoder(w).Encode(map[string]any{"operators": []map[string]string{{"id": f.ids[n]}}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/registry/uas":
		sn, _ := body["serial"].(string)
		if _, ok := f.uas[sn]; ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.creates++
		f.uas[sn] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "u" + sn})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func authorityAt(t *testing.T, h http.Handler, st *State) *Authority {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return &Authority{C: &Client{HTTP: srv.Client(), Port: port}, Host: u.Hostname(), St: st}
}

func emptyState(t *testing.T) *State {
	t.Helper()
	st, err := LoadState(filepath.Join(t.TempDir(), "seed-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The registry step registers the test operator valid for its
// valid_days from the run and every serial under it, unlabelled; a
// second run (the state kept) creates nothing, and a run on a fresh
// state against the same registry finds the rows (409) without failing.
func TestEnsureTestOperatorsRegistersOnce(t *testing.T) {
	f, err := LoadTestOperators("../../conformance/fixtures/test-operators.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeRegistry{operators: map[string]map[string]any{}, ids: map[string]string{}, uas: map[string]map[string]any{}}
	a := authorityAt(t, reg, emptyState(t))
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	n, err := a.EnsureTestOperators(context.Background(), Session{Token: "t", CSRF: "c"}, f, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 || len(reg.uas) != 8 || len(reg.operators) != 1 {
		t.Fatalf("%d aircraft reported, %d registered, %d operators", n, len(reg.uas), len(reg.operators))
	}
	op := reg.operators["GEOTESTWP210001"]
	if op == nil || op["valid_until"] != "2027-10-13T12:00:00Z" || op["source"] != "manual" || op["operator_type"] != "natural" {
		t.Fatalf("operator registered as %v", op)
	}
	for _, sn := range f.Operators[0].Serials {
		u := reg.uas[sn]
		if u == nil || u["operator_id"] != reg.ids["GEOTESTWP210001"] {
			t.Fatalf("aircraft %s registered as %v", sn, u)
		}
		if _, labelled := u["class_label"]; labelled {
			t.Fatalf("aircraft %s registered with a class label", sn)
		}
	}
	creates := reg.creates
	if _, err := a.EnsureTestOperators(context.Background(), Session{}, f, now); err != nil || reg.creates != creates {
		t.Fatalf("second run: %v, %d creations after %d", err, reg.creates, creates)
	}
	st2 := emptyState(t)
	a.St = st2
	if n, err := a.EnsureTestOperators(context.Background(), Session{}, f, now); err != nil || n != 8 || reg.creates != creates {
		t.Fatalf("fresh state: %d aircraft, %v, %d creations after %d", n, err, reg.creates, creates)
	}
	if st2.RegistryOperators["GEOTESTWP210001"] != reg.ids["GEOTESTWP210001"] {
		t.Fatalf("fresh state kept operator id %q", st2.RegistryOperators["GEOTESTWP210001"])
	}
}

// A refused registration stops the step with the authority's answer.
func TestEnsureTestOperatorsRefused(t *testing.T) {
	f, err := LoadTestOperators(writeTestOperators(t, baseTestOperators))
	if err != nil {
		t.Fatal(err)
	}
	a := authorityAt(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"validation","errors":[{"field":"registration_number"}]}`))
	}), emptyState(t))
	if _, err := a.EnsureTestOperators(context.Background(), Session{}, f, time.Now()); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("got %v, want the 400", err)
	}
}

// Run reads and checks the test operators file before anything else
// of the run (the lab CA, the state, any system): a refused file stops
// it with the file's reason, and the same run with the base file goes
// on to the next thing it needs (the CA, absent here).
func TestRunChecksTestOperatorsFirst(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "demo.env")
	if err := os.WriteFile(env, []byte("AUTHORITY_HOST=a\nCISP_HOST=c\nUSSP_HOST=u\nANSP_HOST=n\nLAB_HOST=l\nDEMO_STATE_DIR="+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := writeTestOperators(t, strings.Replace(baseTestOperators, "GEOTESTWP210001", "GEO-TEST-WP21-0001", 1))
	err := Run(context.Background(), Options{Env: env, TestOperators: bad, Steps: []string{StepRegistry}})
	if err == nil || !strings.Contains(err.Error(), "registration_number") {
		t.Fatalf("got %v, want the refused number", err)
	}
	good := writeTestOperators(t, baseTestOperators)
	err = Run(context.Background(), Options{Env: env, TestOperators: good, Steps: []string{StepRegistry}})
	if err == nil || !strings.Contains(err.Error(), "lab CA") {
		t.Fatalf("got %v, want the run to reach the lab CA", err)
	}
}

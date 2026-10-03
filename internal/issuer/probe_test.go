package issuer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/auth"
)

var probeArea = ProbeArea{LatDeg: 1.5, LngDeg: 2.5, RadiusM: 300, AltLowerWGS84M: 0, AltUpperWGS84M: 3000, Window: 10 * time.Minute}

// The probe's body, field by field and constant by constant, as read from
// the InterUSS DSS at the pinned commit (pkg/api/scdv1/types.gen.go and
// pkg/scd/models/conversions.go; deploy/dss/SOURCE). A renamed field
// here would make the DSS answer 400 and the proof fail for the wrong
// reason.
func TestProbeBodyFieldNames(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	b, err := probeArea.body(now)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"area_of_interest":{"volume":{"outline_circle":{"center":{"lng":2.5,"lat":1.5},"radius":{"value":300,"units":"M"}},` +
		`"altitude_lower":{"value":0,"reference":"W84","units":"M"},"altitude_upper":{"value":3000,"reference":"W84","units":"M"}},` +
		`"time_start":{"value":"2026-10-03T12:00:00Z","format":"RFC3339"},"time_end":{"value":"2026-10-03T12:10:00Z","format":"RFC3339"}}}`
	if string(b) != want {
		t.Fatalf("body\n%s\nwant\n%s", b, want)
	}
}

// fakeDSS answers the query route like the DSS: 401 without a valid
// token for its own host, 403 without the scope, 200 with a list. The
// flaws make it answer wrongly in one way each, so the probe is shown to
// catch each one (E-01, E-02).
type fakeDSS struct {
	open        bool // answers 200 without a token
	anyAudience bool // accepts a token for another host
	noList      bool // 200 without the list
}

func (d fakeDSS) server(t *testing.T, f *fixture) *httptest.Server {
	t.Helper()
	var own, wrong *auth.Verifier
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != QueryPath {
			http.NotFound(w, r)
			return
		}
		var q dssQuery
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ok := d.open
		if tok, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); found && !ok {
			cl, err := own.Verify(r.Context(), tok)
			if err != nil && d.anyAudience {
				cl, err = wrong.Verify(r.Context(), tok)
			}
			if err == nil {
				if !cl.HasScope(ProbeScope) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				ok = true
			}
		}
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if d.noList {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"operational_intent_references":[]}`))
	}))
	t.Cleanup(hs.Close)
	u, _ := url.Parse(hs.URL)
	own = f.verifier(t, u.Hostname())
	wrong = f.verifier(t, "not-the-dss.invalid")
	return hs
}

func runProbe(t *testing.T, f *fixture, d fakeDSS) (string, error) {
	t.Helper()
	dss := d.server(t, f)
	var out bytes.Buffer
	err := Probe(context.Background(), ProbeConfig{
		IssuerURL: f.http.URL, DSSURL: dss.URL, ClientID: "sim-ussp-01", Secret: f.secrets["sim-ussp-01"],
		Area: probeArea, WrongAudience: "not-the-dss.invalid", Out: &out,
	})
	return out.String(), err
}

// The probe passes against a DSS that behaves, and prints three ok lines
// naming the codes it saw.
func TestProbePassesAgainstACorrectDSS(t *testing.T) {
	f := newFixture(t)
	out, err := runProbe(t, f, fakeDSS{})
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	if strings.Count(out, "ok ") != 3 || !strings.Contains(out, "HTTP 401 (want 401)") || !strings.Contains(out, "HTTP 200 (want 200), 0 operational intent references") {
		t.Fatalf("output:\n%s", out)
	}
}

// Each way the DSS can answer wrongly makes the probe fail and name the
// step: an open DSS (the 200 would prove nothing), a DSS that ignores the
// audience, a 200 without the list.
func TestProbeFailsOnEachWrongAnswer(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name string
		d    fakeDSS
		step string
	}{
		{"open DSS", fakeDSS{open: true}, "without a token"},
		{"audience ignored", fakeDSS{anyAudience: true}, "with aud not-the-dss.invalid"},
		{"no list", fakeDSS{noList: true}, "scope " + ProbeScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runProbe(t, f, tc.d)
			if err == nil {
				t.Fatalf("probe passed:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.step) || !strings.Contains(out, "FAILED") {
				t.Fatalf("error %v does not name %q\n%s", err, tc.step, out)
			}
		})
	}
}

// The probe refuses to start on configuration that would make a proof
// meaningless, beside the configuration that runs.
func TestProbeConfigRefusals(t *testing.T) {
	if err := probeArea.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mut := range []func(*ProbeArea){
		func(a *ProbeArea) { a.LatDeg = 91 },
		func(a *ProbeArea) { a.LngDeg = -181 },
		func(a *ProbeArea) { a.RadiusM = 0 },
		func(a *ProbeArea) { a.AltUpperWGS84M = a.AltLowerWGS84M },
		func(a *ProbeArea) { a.Window = 0 },
	} {
		a := probeArea
		mut(&a)
		if a.Validate() == nil {
			t.Fatalf("area %+v accepted", a)
		}
	}
	err := Probe(context.Background(), ProbeConfig{DSSURL: "http://dss:8082", Area: probeArea, WrongAudience: "dss"})
	if err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("wrong audience equal to the DSS host accepted: %v", err)
	}
}

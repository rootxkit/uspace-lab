package issuer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The token endpoint never panics and answers 200 only to a request that
// authenticates a client; everything else is a 400, 401 or 403 with a
// problem body.
func FuzzTokenRequest(f *testing.F) {
	fx := newFixture(f)
	f.Add("application/x-www-form-urlencoded", fx.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss").Encode(), "")
	f.Add("application/x-www-form-urlencoded", "grant_type=client_credentials&scope=cis.read&resource=https://cisp/", "Basic bGFiLTAxOng=")
	f.Add("application/x-www-form-urlencoded", "grant_type=client_credentials&audience=%ff&scope=%20%20", "")
	f.Add("text/plain", "a=b", "Bearer x")
	h := fx.srv.Handler()
	f.Fuzz(func(t *testing.T, ctype, body, authz string) {
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
		req.Header.Set("Content-Type", ctype)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		switch w.Code {
		case http.StatusOK:
			if !strings.Contains(body, "secret-of-") && !strings.HasPrefix(authz, "Basic ") {
				t.Fatalf("200 without credentials: %q %q", body, authz)
			}
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			if w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("refusal without a problem body: %d", w.Code)
			}
		default:
			t.Fatalf("status %d", w.Code)
		}
	})
}

// LoadClients never panics, and whatever it accepts obeys the rules it
// enforces: known, unreserved scopes, dp.observe on lab-01 only, bare
// hosts, an audience wherever there is a national scope.
func FuzzLoadClients(f *testing.F) {
	f.Add([]byte(baseClients))
	f.Add([]byte("clients:\n  - id: x\n    scopes: [cis.read]\n    audiences: [a]\n"))
	f.Add([]byte("clients: [{id: lab-01, scopes: [dp.observe], audiences: ['${A}']}]"))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := LoadClients(b, func(string) (string, bool) { return "h1,h2", true })
		if err != nil {
			return
		}
		for _, id := range r.IDs() {
			c, _ := r.Client(id)
			national := false
			for _, s := range c.Scopes {
				info, ok := catalogue[s]
				if !ok || info.reserved || (info.labOnly && id != LabClientID) {
					t.Fatalf("accepted %s for %s", s, id)
				}
				national = national || info.kind == National
			}
			for _, a := range c.Audiences {
				if !hostPattern.MatchString(a) {
					t.Fatalf("accepted host %q", a)
				}
			}
			if national && len(c.Audiences) == 0 {
				t.Fatalf("%s: national scope without audience", id)
			}
		}
	})
}

// targetHost never panics and returns either a bare host or a refusal.
func FuzzTargetHost(f *testing.F) {
	f.Add("dss", "")
	f.Add("", "https://dss:8082/dss/v1")
	f.Add("DSS", "http://dss/")
	f.Add("a", "http://[::1]/")
	f.Fuzz(func(t *testing.T, aud, res string) {
		h, ref := targetHost(aud, res)
		if ref == nil && !hostPattern.MatchString(h) {
			t.Fatalf("targetHost(%q, %q) = %q", aud, res, h)
		}
	})
}

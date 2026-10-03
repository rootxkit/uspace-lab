package issuer

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/auth"
)

// audiencesFor returns the audiences to try for client c and scope s: a
// national scope every allowed audience; a standard scope a host on no
// list (discovered peer) plus the client's first audience.
func audiencesFor(c Client, s string) []string {
	k, _ := ScopeKind(s)
	if k == National {
		return c.Audiences
	}
	out := []string{"peer-uss.discovered.test"}
	if len(c.Audiences) > 0 {
		out = append(out, c.Audiences[0])
	}
	return out
}

// Every client and scope combination of deploy/issuer/clients.yaml gets
// a token that uspace-core's auth.Verifier, configured with this issuer's
// JWKS endpoint, accepts with the requested audience, sub, scope and
// kid, an exp at most an hour after iat and a jti. Each client's full
// scope set in one token too. Half the requests authenticate with HTTP
// Basic, half in the body. WP-L2 done-when: the issuer's tokens verify
// with core's verifier for every client.
func TestEveryClientScopeVerifiesWithCore(t *testing.T) {
	f := newFixture(t)
	verifiers := map[string]*auth.Verifier{}
	verifierFor := func(aud string) *auth.Verifier {
		if v, ok := verifiers[aud]; ok {
			return v
		}
		v := f.verifier(t, aud)
		verifiers[aud] = v
		return v
	}
	n := 0
	check := func(c Client, scopes []string, aud string) {
		t.Helper()
		form := f.tokenForm(c.ID, strings.Join(scopes, " "), aud)
		var basic *[2]string
		if n%2 == 1 {
			form.Del("client_id")
			form.Del("client_secret")
			basic = &[2]string{c.ID, f.secrets[c.ID]}
		}
		n++
		tok := f.accessToken(t, f.post(t, form, basic))
		cl, err := verifierFor(aud).Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("%s %v aud %s: core refused the token: %v", c.ID, scopes, aud, err)
		}
		switch {
		case cl.Issuer != testIssuerURL, cl.Subject != c.ID, cl.Audience != aud, cl.KeyID != "20261003-1", cl.JTI == "":
			t.Fatalf("%s %v aud %s: claims %+v", c.ID, scopes, aud, cl)
		case !slices.Equal(cl.Scopes, scopes):
			t.Fatalf("%s: scopes %v, want %v", c.ID, cl.Scopes, scopes)
		case cl.IssuedAt.IsZero() || cl.ExpiresAt.Sub(cl.IssuedAt) > time.Hour:
			t.Fatalf("%s: iat %s exp %s", c.ID, cl.IssuedAt, cl.ExpiresAt)
		}
	}
	clients := 0
	for _, id := range f.reg.IDs() {
		c, _ := f.reg.Client(id)
		clients++
		for _, s := range c.Scopes {
			for _, aud := range audiencesFor(c, s) {
				check(c, []string{s}, aud)
			}
		}
		if len(c.Scopes) > 0 {
			check(c, c.Scopes, c.Audiences[0])
		}
	}
	if clients < 6 || n < 50 {
		t.Fatalf("only %d clients and %d tokens checked; the clients file shrank", clients, n)
	}
	if got := f.srv.Counters().Get(CounterIssued); got != uint64(n) {
		t.Fatalf("issued counter %d, want %d", got, n)
	}
	t.Logf("%d tokens for %d clients verified with core's auth.Verifier", n, clients)
}

// A token is for one audience: core's verifier for another host refuses
// it, beside the acceptance for its own (E-01).
func TestTokenIsBoundToItsAudience(t *testing.T) {
	f := newFixture(t)
	tok := f.accessToken(t, f.post(t, f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"), nil))
	if _, err := f.verifier(t, "dss").Verify(context.Background(), tok); err != nil {
		t.Fatalf("own audience refused: %v", err)
	}
	_, err := f.verifier(t, "ussp").Verify(context.Background(), tok)
	var te *auth.TokenError
	if !errors.As(err, &te) || te.Claim != "aud" {
		t.Fatalf("other audience: %v, want an aud refusal", err)
	}
}

// Every refusal beside the acceptance it differs from by one thing
// (LESSONS E-01). Each refusal is a problem/v1 body with its slug, and
// increments refused_<slug>.
func TestRefusalsBesideAcceptances(t *testing.T) {
	f := newFixture(t)
	type mod func(url.Values) (url.Values, *[2]string)
	same := func(v url.Values) (url.Values, *[2]string) { return v, nil }
	set := func(k, v string) mod {
		return func(form url.Values) (url.Values, *[2]string) { form.Set(k, v); return form, nil }
	}
	cases := []struct {
		name      string
		base      url.Values
		accept    mod
		refuse    mod
		status    int
		slug      string
		wantField string
	}{
		{"wrong secret", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("client_secret", "not-the-secret-0123456789"), 401, SlugUnauthenticated, "client"},
		{"unknown client", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("client_id", "sim-ussp-02"), 401, SlugUnauthenticated, "client"},
		{"another client's secret", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("client_secret", f.secrets["lab-01"]), 401, SlugUnauthenticated, "client"},
		{"no credentials", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, func(v url.Values) (url.Values, *[2]string) { v.Del("client_id"); v.Del("client_secret"); return v, nil },
			401, SlugUnauthenticated, "client"},
		{"Basic and body together", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			func(v url.Values) (url.Values, *[2]string) {
				v.Del("client_id")
				v.Del("client_secret")
				return v, &[2]string{"sim-ussp-01", f.secrets["sim-ussp-01"]}
			},
			func(v url.Values) (url.Values, *[2]string) {
				return v, &[2]string{"sim-ussp-01", f.secrets["sim-ussp-01"]}
			},
			401, SlugUnauthenticated, "client"},
		{"standard scope not granted to the client", f.tokenForm("ansp-01", "utm.constraint_management", "dss"),
			same, set("scope", "utm.strategic_coordination"), 403, SlugForbiddenScope, "scope"},
		{"scope outside the catalogue", f.tokenForm("lab-01", "cis.read", "cisp"),
			same, set("scope", "cis.write"), 403, SlugForbiddenScope, "scope"},
		{"reserved scope", f.tokenForm("lab-01", "cis.publish:restrictions", "cisp"),
			same, set("scope", "cis.publish:ats_data"), 403, SlugForbiddenScope, "scope"},
		{"USSP-issuer scope", f.tokenForm("lab-01", "cis.read", "cisp"),
			same, set("scope", "ussp.intents"), 403, SlugForbiddenScope, "scope"},
		{"retired rid.observe", f.tokenForm("lab-01", "rid.display_provider", "authority"),
			same, set("scope", "rid.observe"), 403, SlugForbiddenScope, "scope"},
		{"dp.observe to a client other than lab-01", f.tokenForm("lab-01", "dp.observe", "authority"),
			same, func(v url.Values) (url.Values, *[2]string) {
				v.Set("client_id", "authority-01")
				v.Set("client_secret", f.secrets["authority-01"])
				return v, nil
			}, 403, SlugForbiddenScope, "scope"},
		{"one scope of several not granted", f.tokenForm("sim-ussp-01", "utm.strategic_coordination rid.service_provider", "dss"),
			same, set("scope", "utm.strategic_coordination utm.availability_arbitration"), 403, SlugForbiddenScope, "scope"},
		{"national scope for an audience not on the list", f.tokenForm("ansp-01", "cis.publish:restrictions", "cisp.lab.test"),
			same, set("audience", "ussp"), 403, SlugForbiddenAudience, "audience"},
		{"national scope mixed with a standard one, audience off the list", f.tokenForm("sim-ussp-01", "cis.read utm.strategic_coordination", "cisp"),
			same, set("audience", "dss"), 403, SlugForbiddenAudience, "audience"},
		{"national scope via resource for a host off the list", f.tokenForm("authority-01", "cis.publish:zones", "cisp"),
			func(v url.Values) (url.Values, *[2]string) {
				v.Del("audience")
				v.Set("resource", "https://cisp.lab.test/v1/zones")
				return v, nil
			},
			func(v url.Values) (url.Values, *[2]string) {
				v.Del("audience")
				v.Set("resource", "https://authority/v1")
				return v, nil
			},
			403, SlugForbiddenAudience, "audience"},
		{"no scope", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("scope", ""), 400, SlugInvalidRequest, "scope"},
		{"scope repeated in the list", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("scope", "utm.strategic_coordination utm.strategic_coordination"), 400, SlugInvalidRequest, "scope"},
		{"wrong grant type", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("grant_type", "password"), 400, SlugInvalidRequest, "grant_type"},
		{"no audience or resource", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, func(v url.Values) (url.Values, *[2]string) { v.Del("audience"); return v, nil }, 400, SlugInvalidRequest, "audience"},
		{"audience that is a URL", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, set("audience", "https://dss"), 400, SlugInvalidRequest, "audience"},
		{"resource naming another host than audience", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			set("resource", "http://dss:8082/dss/v1"), set("resource", "http://ussp/"), 400, SlugInvalidRequest, "resource"},
		{"resource that is not absolute", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			func(v url.Values) (url.Values, *[2]string) {
				v.Del("audience")
				v.Set("resource", "https://dss/")
				return v, nil
			},
			func(v url.Values) (url.Values, *[2]string) {
				v.Del("audience")
				v.Set("resource", "dss/dss/v1")
				return v, nil
			},
			400, SlugInvalidRequest, "resource"},
		{"repeated parameter", f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"),
			same, func(v url.Values) (url.Values, *[2]string) { v.Add("audience", "dss"); return v, nil }, 400, SlugInvalidRequest, "audience"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc, accBasic := tc.accept(cloneForm(tc.base))
			f.accessToken(t, f.post(t, acc, accBasic))

			before := f.srv.Counters().Get("refused_" + tc.slug)
			ref, refBasic := tc.refuse(cloneForm(tc.base))
			r := f.post(t, ref, refBasic)
			if r.status != tc.status {
				t.Fatalf("HTTP %d, want %d: %s", r.status, tc.status, r.body)
			}
			if slug := checkProblem(t, r); slug != tc.slug {
				t.Fatalf("slug %s, want %s", slug, tc.slug)
			}
			if !strings.Contains(string(r.body), `"field":"`+tc.wantField+`"`) {
				t.Fatalf("problem does not name field %s: %s", tc.wantField, r.body)
			}
			if got := f.srv.Counters().Get("refused_" + tc.slug); got != before+1 {
				t.Fatalf("refused_%s went %d -> %d", tc.slug, before, got)
			}
			if strings.Contains(string(r.body), "secret-of-") {
				t.Fatalf("a refusal echoes a secret: %s", r.body)
			}
		})
	}
}

func cloneForm(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = slices.Clone(vs)
	}
	return out
}

// Transport-level refusals beside their acceptances: the query string,
// the content type, the body size, the method and unknown routes.
func TestRequestShapeRefusals(t *testing.T) {
	f := newFixture(t)
	good := f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss").Encode()
	send := func(method, path, ctype, body string) response {
		req, err := http.NewRequest(method, f.http.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		return do(t, req)
	}
	form := "application/x-www-form-urlencoded"
	if r := send(http.MethodPost, "/oauth/token", form+"; charset=utf-8", good); r.status != 200 {
		t.Fatalf("acceptance: HTTP %d %s", r.status, r.body)
	}
	for _, tc := range []struct {
		name, method, path, ctype, body string
		status                          int
		slug                            string
	}{
		{"credentials in the query string", http.MethodPost, "/oauth/token?client_secret=x", form, good, 400, SlugInvalidRequest},
		{"JSON body", http.MethodPost, "/oauth/token", "application/json", good, 400, SlugInvalidRequest},
		{"body over the bound", http.MethodPost, "/oauth/token", form, good + "&pad=" + strings.Repeat("a", MaxRequestBytes), 400, SlugInvalidRequest},
		{"GET on the token endpoint", http.MethodGet, "/oauth/token", "", "", 405, SlugMethodNotAllowed},
		{"POST on the JWKS", http.MethodPost, "/.well-known/jwks.json", form, "", 405, SlugMethodNotAllowed},
		{"unknown route", http.MethodGet, "/oauth/authorize", "", "", 404, SlugNotFound},
	} {
		r := send(tc.method, tc.path, tc.ctype, tc.body)
		if r.status != tc.status {
			t.Fatalf("%s: HTTP %d, want %d: %s", tc.name, r.status, tc.status, r.body)
		}
		if slug := checkProblem(t, r); slug != tc.slug {
			t.Fatalf("%s: slug %s, want %s", tc.name, slug, tc.slug)
		}
	}
	for _, path := range []string{"/.well-known/jwks.json", "/healthz"} {
		if r := send(http.MethodGet, path, "", ""); r.status != 200 {
			t.Fatalf("GET %s: HTTP %d", path, r.status)
		}
	}
}

// The JWKS publishes one RS256 key with use sig and the kid, and the
// health answer reports the counters (E-02: the success path says what
// it did).
func TestJWKSAndHealth(t *testing.T) {
	f := newFixture(t)
	r := do(t, mustReq(t, http.MethodGet, f.http.URL+"/.well-known/jwks.json"))
	for _, want := range []string{`"kid":"20261003-1"`, `"use":"sig"`, `"alg":"RS256"`, `"kty":"RSA"`} {
		if !strings.Contains(string(r.body), want) {
			t.Fatalf("JWKS lacks %s: %s", want, r.body)
		}
	}
	if strings.Contains(string(r.body), `"d":`) {
		t.Fatalf("JWKS carries the private exponent: %s", r.body)
	}
	f.post(t, f.tokenForm("sim-ussp-01", "utm.strategic_coordination", "dss"), nil)
	h := do(t, mustReq(t, http.MethodGet, f.http.URL+"/healthz"))
	if h.status != 200 || !strings.Contains(string(h.body), `"issued":1`) {
		t.Fatalf("healthz: %d %s", h.status, h.body)
	}
}

func mustReq(t testing.TB, method, u string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, u, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// The TTL bound: an hour is accepted, more is refused at start (the DSS
// refuses a token whose exp is more than an hour ahead).
func TestTTLBound(t *testing.T) {
	reg := labRegistry(t)
	plain := map[string]string{}
	for _, id := range reg.IDs() {
		plain[id] = "secret-of-" + id + "-0123456789"
	}
	sec, err := NewSecrets(plain)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{IssuerURL: testIssuerURL, Key: sharedKey(t), Kid: "k-1", Clients: reg, Secrets: sec, TTL: MaxTTL}
	if _, err := NewServer(cfg); err != nil {
		t.Fatalf("1h refused: %v", err)
	}
	cfg.TTL = MaxTTL + time.Second
	if _, err := NewServer(cfg); err == nil {
		t.Fatal("a TTL over an hour was accepted")
	}
	cfg.TTL = MaxTTL
	delete(plain, "cisp-01")
	sec2, err := NewSecrets(plain)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Secrets = sec2
	if _, err := NewServer(cfg); err == nil || !strings.Contains(err.Error(), "cisp-01") {
		t.Fatalf("a client without a secret was accepted: %v", err)
	}
}

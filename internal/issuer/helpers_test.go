package issuer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/internal/contracts"
)

// testIssuerURL is the iss of test tokens. The verifier fetches the JWKS
// from the test server's own /.well-known/jwks.json, not from here.
const testIssuerURL = "http://lab-issuer.test"

// Lab hosts the clients file's ${VAR} entries expand to in tests.
var testEnv = map[string]string{
	"LAB_AUTHORITY_AUDIENCES": "authority,authority.lab.test",
	"LAB_CISP_AUDIENCES":      "cisp,cisp.lab.test",
	"LAB_ANSP_AUDIENCES":      "ansp",
	"LAB_USSP_AUDIENCES":      "ussp",
}

func testLookup(name string) (string, bool) {
	v, ok := testEnv[name]
	return v, ok
}

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// sharedKey is one 2048-bit key for the whole package (generation is the
// slow part of these tests).
func sharedKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

func labRegistry(t testing.TB) *Registry {
	t.Helper()
	b, err := os.ReadFile("../../deploy/issuer/clients.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := LoadClients(b, testLookup)
	if err != nil {
		t.Fatalf("deploy/issuer/clients.yaml: %v", err)
	}
	return r
}

// fixture is a running issuer over the lab clients file with known
// secrets.
type fixture struct {
	srv     *Server
	http    *httptest.Server
	secrets map[string]string
	reg     *Registry
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	reg := labRegistry(t)
	plain := map[string]string{}
	for _, id := range reg.IDs() {
		plain[id] = "secret-of-" + id + "-0123456789"
	}
	sec, err := NewSecrets(plain)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{IssuerURL: testIssuerURL, Key: sharedKey(t), Kid: "20261003-1", Clients: reg, Secrets: sec, TTL: 50 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &fixture{srv: srv, http: hs, secrets: plain, reg: reg}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// post sends form to /oauth/token; basic sets HTTP Basic credentials.
func (f *fixture) post(t testing.TB, form url.Values, basic *[2]string) response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.http.URL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic != nil {
		req.SetBasicAuth(basic[0], basic[1])
	}
	return do(t, req)
}

func do(t testing.TB, req *http.Request) response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// tokenForm is a valid body-credentials request.
func (f *fixture) tokenForm(client, scope, aud string) url.Values {
	return url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {client},
		"client_secret": {f.secrets[client]},
		"scope":         {scope},
		"audience":      {aud},
	}
}

func (f *fixture) accessToken(t testing.TB, r response) string {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("token request: HTTP %d: %s", r.status, r.body)
	}
	var tr tokenResponse
	if err := json.Unmarshal(r.body, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.TokenType != "Bearer" || tr.AccessToken == "" || tr.ExpiresIn <= 0 || tr.ExpiresIn > int64(MaxTTL/time.Second) {
		t.Fatalf("token response %+v", tr)
	}
	if got := r.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
	return tr.AccessToken
}

// verifier returns core's verifier for aud, trusting this issuer through
// its JWKS endpoint (the path every system uses).
func (f *fixture) verifier(t testing.TB, aud string) *auth.Verifier {
	t.Helper()
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuers:  map[string]auth.IssuerConfig{testIssuerURL: {JWKSURL: f.http.URL + "/.well-known/jwks.json"}},
		Audience: aud,
	})
	if err != nil {
		t.Fatalf("core verifier for %s: %v", aud, err)
	}
	return v
}

var (
	problemOnce   sync.Once
	problemSchema interface{ Validate(any) error }
	errProblem    error
)

// checkProblem validates body against schemas/common/problem/v1 and
// returns its slug.
func checkProblem(t testing.TB, r response) string {
	t.Helper()
	problemOnce.Do(func() {
		schemas, err := contracts.LoadSchemas("../../schemas/common")
		if err != nil {
			errProblem = err
			return
		}
		compiled, err := contracts.Compile(schemas)
		if err != nil {
			errProblem = err
			return
		}
		problemSchema = compiled["problem/v1"]
	})
	if errProblem != nil || problemSchema == nil {
		t.Fatalf("problem/v1 schema: %v", errProblem)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type %q, want application/problem+json", ct)
	}
	var doc any
	if err := json.Unmarshal(r.body, &doc); err != nil {
		t.Fatalf("problem body: %v", err)
	}
	if err := problemSchema.Validate(doc); err != nil {
		t.Fatalf("problem body does not validate against problem/v1: %v\n%s", err, r.body)
	}
	typ, _ := doc.(map[string]any)["type"].(string)
	return strings.TrimPrefix(typ, ProblemTypeBase)
}

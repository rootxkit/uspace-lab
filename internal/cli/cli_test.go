package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The issuer's client-secrets.json gives the named client's secret, and
// refuses a client it has no secret for (E-01: both paths).
func TestReadSecretJSON(t *testing.T) {
	p := write(t, "client-secrets.json", `{"sim-ussp-01": " s3cret-of-sixteen-chars\n", "lab-01": "other-secret-value-1"}`)
	got, err := ReadSecretJSON(p, "sim-ussp-01")
	if err != nil || got != "s3cret-of-sixteen-chars" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, id := range []string{"nobody", ""} {
		if _, err := ReadSecretJSON(p, id); err == nil {
			t.Errorf("client %q: no error", id)
		}
	}
	if _, err := ReadSecretJSON(write(t, "bad.json", "not json"), "sim-ussp-01"); err == nil {
		t.Error("a malformed file: no error")
	}
	if _, err := ReadSecretJSON(write(t, "big.json", `{"a":"`+strings.Repeat("x", MaxStateFileBytes)+`"}`), "a"); err == nil {
		t.Error("an oversized file: no error")
	}
}

// A JWKS file as the issuer writes it gives its key; an empty set is
// refused.
func TestReadJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k, err := jwk.Import(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Set(jwk.KeyIDKey, "20261003-1"); err != nil {
		t.Fatal(err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(k); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadJWKS(write(t, "jwks.json", string(b)))
	if err != nil || got.Len() != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if kk, ok := got.Key(0); !ok || kk == nil {
		t.Fatal("no key 0")
	} else if kid, _ := kk.KeyID(); kid != "20261003-1" {
		t.Fatalf("kid %q", kid)
	}
	if _, err := ReadJWKS(write(t, "empty.json", `{"keys":[]}`)); err == nil {
		t.Error("an empty set: no error")
	}
	if _, err := ReadJWKS(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("an absent file: no error")
	}
}

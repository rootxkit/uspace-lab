package issuer

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

// The state key survives a restart: the second start loads the same key
// and kid instead of generating one (safety state persists, and the DSS,
// which reads the public key once, keeps trusting the issuer). After the
// key file is removed, the next key gets the next counter.
func TestStateKeyPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	k1, kid1, created, err := LoadOrCreateStateKey(dir, day)
	if err != nil || !created {
		t.Fatalf("first start: created=%v err=%v", created, err)
	}
	if kid1 != "20261003-1" {
		t.Fatalf("kid %s, want 20261003-1", kid1)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, StateKeyFile))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode %v (%v), want 0600", st.Mode().Perm(), err)
		}
	}
	k2, kid2, created, err := LoadOrCreateStateKey(dir, day.Add(48*time.Hour))
	if err != nil || created {
		t.Fatalf("restart: created=%v err=%v", created, err)
	}
	if kid2 != kid1 || !k1.Equal(k2) {
		t.Fatalf("restart changed the key: %s -> %s", kid1, kid2)
	}
	if err := os.Remove(filepath.Join(dir, StateKeyFile)); err != nil {
		t.Fatal(err)
	}
	k3, kid3, created, err := LoadOrCreateStateKey(dir, day)
	if err != nil || !created || kid3 != "20261003-2" || k3.Equal(k1) {
		t.Fatalf("regenerated: kid %s created %v err %v", kid3, created, err)
	}
}

// A staging key file: the kid comes from its Kid header or --kid; a
// disagreement or no kid at all is refused, beside the acceptances.
func TestLoadKeyFileKid(t *testing.T) {
	dir := t.TempDir()
	key := sharedKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.pem")
	withKid := filepath.Join(dir, "kid.pem")
	pkcs1 := filepath.Join(dir, "pkcs1.pem")
	write := func(p string, b *pem.Block) {
		if err := os.WriteFile(p, pem.EncodeToMemory(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(plain, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
	write(withKid, &pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Kid": "staging-1"}, Bytes: der})
	write(pkcs1, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	if _, kid, err := LoadKeyFile(withKid, ""); err != nil || kid != "staging-1" {
		t.Fatalf("header kid: %s %v", kid, err)
	}
	if _, kid, err := LoadKeyFile(plain, "staging-2"); err != nil || kid != "staging-2" {
		t.Fatalf("flag kid: %s %v", kid, err)
	}
	if _, kid, err := LoadKeyFile(pkcs1, "staging-3"); err != nil || kid != "staging-3" {
		t.Fatalf("PKCS#1: %s %v", kid, err)
	}
	if _, _, err := LoadKeyFile(plain, ""); err == nil || !strings.Contains(err.Error(), "no kid") {
		t.Fatalf("no kid accepted: %v", err)
	}
	if _, _, err := LoadKeyFile(withKid, "other"); err == nil {
		t.Fatal("conflicting kids accepted")
	}
	if _, _, err := LoadKeyFile(filepath.Join(dir, "absent.pem"), "x"); err == nil {
		t.Fatal("a missing key file was accepted")
	}
}

// The public key file parses the way the InterUSS DSS's
// FromFileKeyResolver parses -public_key_files at the pinned commit
// (pem.Decode, x509.ParsePKIXPublicKey, *rsa.PublicKey), and is the
// issuer's key.
func TestWritePublicIsWhatTheDSSReads(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := WritePublic(dir, &sharedKey(t).PublicKey, f.srv.JWKS()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, PublicDir, PublicKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("no PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := parsed.(*rsa.PublicKey)
	if !ok || !pub.Equal(&sharedKey(t).PublicKey) {
		t.Fatal("public key file is not the issuer's RSA key")
	}
	j, err := os.ReadFile(filepath.Join(dir, PublicDir, PublicJWKS))
	if err != nil || !strings.Contains(string(j), `"kid": "20261003-1"`) {
		t.Fatalf("jwks.json: %v %s", err, j)
	}
}

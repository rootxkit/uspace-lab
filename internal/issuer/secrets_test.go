package issuer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// First start generates a secret per client and reports them (to print
// once); a restart generates nothing and returns the same secrets; a new
// client gets a secret without touching the others.
func TestSecretsGeneratedOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"lab-01", "sim-ussp-01"}
	s1, created, err := LoadOrCreateSecrets(dir, ids)
	if err != nil || len(created) != 2 {
		t.Fatalf("first start: created %v err %v", created, err)
	}
	s2, created, err := LoadOrCreateSecrets(dir, ids)
	if err != nil || len(created) != 0 {
		t.Fatalf("restart: created %v err %v", created, err)
	}
	for _, id := range ids {
		if s1[id] != s2[id] || len(s1[id]) < 40 {
			t.Fatalf("%s: secret changed or short", id)
		}
	}
	s3, created, err := LoadOrCreateSecrets(dir, append(ids, "ansp-01"))
	if err != nil || len(created) != 1 || created[0] != "ansp-01" || s3["lab-01"] != s1["lab-01"] {
		t.Fatalf("new client: created %v err %v", created, err)
	}
	got, err := ReadSecret(dir, "sim-ussp-01")
	if err != nil || got != s1["sim-ussp-01"] {
		t.Fatalf("ReadSecret: %v", err)
	}
	if _, err := ReadSecret(dir, "nobody-01"); err == nil {
		t.Fatal("ReadSecret of an unknown client succeeded")
	}
}

// A secrets file naming a client clients.yaml no longer lists stops the
// start (a stale credential is never kept silently), beside the start
// that accepts it while the client is listed.
func TestSecretsFileWithUnknownClient(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadOrCreateSecrets(dir, []string{"a-01", "b-01"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateSecrets(dir, []string{"a-01"}); err == nil || !strings.Contains(err.Error(), "b-01") {
		t.Fatalf("stale client accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateSecretsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateSecrets(dir, []string{"a-01"}); err == nil {
		t.Fatal("a corrupt secrets file was accepted")
	}
}

// LESSONS B-14: an empty or duplicated credential is a startup error,
// beside the set that differs by one entry and is accepted.
func TestNewSecretsRefusesEmptyAndDuplicate(t *testing.T) {
	ok := map[string]string{"a-01": "0123456789abcdef-a", "b-01": "0123456789abcdef-b"}
	s, err := NewSecrets(ok)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Check("a-01", ok["a-01"]) || s.Check("a-01", ok["b-01"]) || s.Check("c-01", ok["a-01"]) || s.Check("a-01", "") {
		t.Fatal("Check answers wrongly")
	}
	if _, err := NewSecrets(map[string]string{"a-01": "", "b-01": ok["b-01"]}); err == nil {
		t.Fatal("empty secret accepted")
	}
	if _, err := NewSecrets(map[string]string{"a-01": ok["a-01"], "b-01": ok["a-01"]}); err == nil {
		t.Fatal("shared secret accepted")
	}
}

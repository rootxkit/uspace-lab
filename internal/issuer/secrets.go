package issuer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// MaxSecretsFileBytes bounds client-secrets.json.
const MaxSecretsFileBytes = 64 << 10

// Secrets holds the client secrets as SHA-256 digests, compared in
// constant time.
type Secrets struct {
	digest map[string][32]byte
}

// NewSecrets builds the set from plain secrets. An empty secret, or one
// secret shared by two clients, is an error (LESSONS B-14: an empty or
// duplicated credential is a startup error).
func NewSecrets(plain map[string]string) (*Secrets, error) {
	s := &Secrets{digest: make(map[string][32]byte, len(plain))}
	owner := make(map[[32]byte]string, len(plain))
	ids := make([]string, 0, len(plain))
	for id := range plain {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(plain[id]) < 16 {
			return nil, fmt.Errorf("client %s: secret is empty or shorter than 16 characters", id)
		}
		d := sha256.Sum256([]byte(plain[id]))
		if other, dup := owner[d]; dup {
			return nil, fmt.Errorf("clients %s and %s share a secret", other, id)
		}
		owner[d] = id
		s.digest[id] = d
	}
	return s, nil
}

// dummy keeps the comparison time the same for an unknown client.
var dummy = sha256.Sum256([]byte("lab-issuer: no such client"))

// Check reports whether secret is the secret of client id. It compares
// digests in constant time whether or not the client exists.
func (s *Secrets) Check(id, secret string) bool {
	want, ok := s.digest[id]
	if !ok {
		want = dummy
	}
	got := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1 && ok
}

// LoadOrCreateSecrets reads dir/client-secrets.json, generates a secret
// for every client in ids that has none, and writes the file back (mode
// 0600) when it generated one. It returns the plain secrets and the ids
// whose secret it generated, so the caller prints those once. A file
// naming a client that is not in ids is an error: the client list
// changed, and a stale credential is never kept silently.
func LoadOrCreateSecrets(dir string, ids []string) (plain map[string]string, created []string, err error) {
	path := filepath.Join(dir, StateSecretsFile)
	plain = map[string]string{}
	b, err := os.ReadFile(path) //nolint:gosec // inside the configured state directory
	switch {
	case err == nil:
		if len(b) > MaxSecretsFileBytes {
			return nil, nil, fmt.Errorf("%s is larger than %d bytes", path, MaxSecretsFileBytes)
		}
		if err := json.Unmarshal(b, &plain); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, nil, err
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	for id := range plain {
		if !known[id] {
			return nil, nil, fmt.Errorf("%s names client %s, which clients.yaml no longer lists; remove its line", path, id)
		}
	}
	for _, id := range ids {
		if _, ok := plain[id]; ok {
			continue
		}
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		plain[id] = base64.RawURLEncoding.EncodeToString(raw[:])
		created = append(created, id)
	}
	if len(created) > 0 {
		out, err := json.MarshalIndent(plain, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		if err := writeFileAtomic(path, append(out, '\n'), 0o600); err != nil {
			return nil, nil, err
		}
	}
	return plain, created, nil
}

// ReadSecret returns the secret of client id from dir/client-secrets.json.
func ReadSecret(dir, id string) (string, error) {
	path := filepath.Join(dir, StateSecretsFile)
	b, err := os.ReadFile(path) //nolint:gosec // inside the configured state directory
	if err != nil {
		return "", err
	}
	if len(b) > MaxSecretsFileBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", path, MaxSecretsFileBytes)
	}
	var plain map[string]string
	if err := json.Unmarshal(b, &plain); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	s, ok := plain[id]
	if !ok || s == "" {
		return "", fmt.Errorf("%s has no secret for %s", path, id)
	}
	return s, nil
}

package issuer

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/auth"
)

// KeyBits is the size of the RSA keys the issuer generates.
const KeyBits = 3072

// Files in the state directory. Everything under it is git-ignored
// (deploy/local/, spec 06 §4).
const (
	StateKeyFile     = "signing-key.pem"
	StateKidLog      = "kids.log"
	StateSecretsFile = "client-secrets.json"
	// PublicDir holds what other containers may read: the public key in
	// the PKIX PEM form the InterUSS DSS's -public_key_files expects, and
	// the JWKS for verifiers configured with static keys.
	PublicDir     = "public"
	PublicKeyFile = "issuer-public.pem"
	PublicJWKS    = "jwks.json"
)

// kidHeader is the PEM header carrying the kid of a generated key.
const kidHeader = "Kid"

var kidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// LoadKeyFile reads an RSA private key (PKCS#8 or PKCS#1 PEM) for a
// stable deployment key. The kid is the key's Kid PEM header or kid; when
// both are given they must agree, and one of them must be.
func LoadKeyFile(path, kid string) (*rsa.PrivateKey, string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the key file
	if err != nil {
		return nil, "", fmt.Errorf("key file: %w", err)
	}
	return parseKeyPEM(b, kid)
}

func parseKeyPEM(b []byte, kid string) (*rsa.PrivateKey, string, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, "", errors.New("key file: no PEM block")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("key file: %w", err)
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, "", errors.New("key file: not an RSA key")
		}
		key = rk
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("key file: %w", err)
		}
		key = k
	default:
		return nil, "", fmt.Errorf("key file: PEM block %q is not a private key", block.Type)
	}
	header := block.Headers[kidHeader]
	switch {
	case header != "" && kid != "" && header != kid:
		return nil, "", fmt.Errorf("key file: its kid %q differs from the configured kid %q", header, kid)
	case kid == "":
		kid = header
	}
	if !kidPattern.MatchString(kid) {
		return nil, "", errors.New("key file: no kid (give --kid, or a Kid PEM header)")
	}
	if key.N.BitLen() < auth.MinRSABits {
		return nil, "", fmt.Errorf("key file: %d bits, shorter than %d", key.N.BitLen(), auth.MinRSABits)
	}
	return key, kid, nil
}

// LoadOrCreateStateKey returns the key kept in dir, generating and saving
// it (mode 0600) when absent, so the key and therefore every verifier's
// trust survives a restart of the issuer. A generated key's kid is the
// UTC date and a counter of the keys this state directory has generated
// (kids.log), e.g. 20261003-1.
func LoadOrCreateStateKey(dir string, now time.Time) (key *rsa.PrivateKey, kid string, created bool, err error) {
	path := filepath.Join(dir, StateKeyFile)
	b, err := os.ReadFile(path) //nolint:gosec // inside the configured state directory
	switch {
	case err == nil:
		key, kid, err = parseKeyPEM(b, "")
		return key, kid, false, err
	case !errors.Is(err, fs.ErrNotExist):
		return nil, "", false, fmt.Errorf("state key: %w", err)
	}
	n, err := countLines(filepath.Join(dir, StateKidLog))
	if err != nil {
		return nil, "", false, err
	}
	kid = fmt.Sprintf("%s-%d", now.UTC().Format("20060102"), n+1)
	key, err = rsa.GenerateKey(rand.Reader, KeyBits)
	if err != nil {
		return nil, "", false, fmt.Errorf("state key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", false, fmt.Errorf("state key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{kidHeader: kid}, Bytes: der})
	if err := writeFileAtomic(path, pemBytes, 0o600); err != nil {
		return nil, "", false, err
	}
	if err := appendLine(filepath.Join(dir, StateKidLog), kid+" "+now.UTC().Format(time.RFC3339)); err != nil {
		return nil, "", false, err
	}
	return key, kid, true, nil
}

// GenerateKey returns a fresh key that lives only in memory, kid = the
// UTC date and counter 1.
func GenerateKey(now time.Time) (*rsa.PrivateKey, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, KeyBits)
	if err != nil {
		return nil, "", err
	}
	return key, now.UTC().Format("20060102") + "-1", nil
}

// WritePublic writes the public key (PKIX PEM) and the JWKS into
// dir/public, each replaced atomically.
func WritePublic(dir string, key *rsa.PublicKey, jwks any) error {
	pub := filepath.Join(dir, PublicDir)
	if err := os.MkdirAll(pub, 0o755); err != nil { //nolint:gosec // the public directory is meant to be readable
		return err
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(pub, PublicKeyFile), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		return err
	}
	j, err := json.MarshalIndent(jwks, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(pub, PublicJWKS), append(j, '\n'), 0o644)
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func countLines(path string) (int, error) {
	f, err := os.Open(path) //nolint:gosec // inside the configured state directory
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // inside the configured state directory
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

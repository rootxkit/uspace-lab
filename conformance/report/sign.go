package report

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/internal/issuer"
)

// Signature files written beside a report.
const (
	// DigestSuffix holds "sha256:<hex>  report.json".
	DigestSuffix = ".sha256"
	// SignatureSuffix holds the RFC 7797 detached JWS
	// (<protected>..<signature>) over the report's exact bytes.
	SignatureSuffix = ".jws"
)

// Digest is "sha256:<hex>" of b.
func Digest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Sign writes path.sha256 and path.jws for the report bytes at path,
// signed with the lab issuer's key (its signing-key.pem; the kid is the
// file's Kid header unless kid names it). It returns the digest.
func Sign(path, keyFile, kid string, now time.Time) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the report just written
	if err != nil {
		return "", err
	}
	key, kid, err := issuer.LoadKeyFile(keyFile, kid)
	if err != nil {
		return "", fmt.Errorf("signing key: %w", err)
	}
	sig, err := auth.SignDetached(auth.SigningKey{KID: kid, Key: key}, b, now)
	if err != nil {
		return "", fmt.Errorf("signing: %w", err)
	}
	d := Digest(b)
	if err := os.WriteFile(path+DigestSuffix, //nolint:gosec // beside the report the caller named
		[]byte(d+"  report.json\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(path+SignatureSuffix, //nolint:gosec // beside the report the caller named
		[]byte(sig+"\n"), 0o600); err != nil {
		return "", err
	}
	return d, nil
}

// Verified is what Verify established.
type Verified struct {
	Digest   string
	KID      string
	IssuedAt time.Time
}

// Verify checks path.jws against the report bytes at path with a key
// of the JWKS file (the lab issuer's public/jwks.json), and path.sha256
// against the bytes. A report whose bytes changed after signing, a
// signature by a key the JWKS does not hold, or a header other than
// RS256 with b64 false and crit ["b64"] is refused. The signature's age
// is reported, not judged: a report is evidence long after it is made.
func Verify(path, jwksFile string) (Verified, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the report
	if err != nil {
		return Verified{}, err
	}
	sigB, err := os.ReadFile(path + SignatureSuffix) //nolint:gosec // beside the report
	if err != nil {
		return Verified{}, fmt.Errorf("signature: %w", err)
	}
	sig := strings.TrimSpace(string(sigB))
	hdr, err := auth.ParseDetachedHeader(sig)
	if err != nil {
		return Verified{}, fmt.Errorf("signature header: %w", err)
	}
	if hdr.Alg != "RS256" || hdr.B64 || !slices.Contains(hdr.Crit, "b64") || hdr.KID == "" {
		return Verified{}, fmt.Errorf("signature header: alg %s, b64 %v, crit %v, kid %q: want RS256, b64 false, crit [b64], a kid", hdr.Alg, hdr.B64, hdr.Crit, hdr.KID)
	}
	protected, signature, ok := strings.Cut(sig, "..")
	if !ok {
		return Verified{}, errors.New("signature: not a detached JWS (<protected>..<signature>)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return Verified{}, fmt.Errorf("signature: %w", err)
	}
	jb, err := os.ReadFile(jwksFile) //nolint:gosec // the operator names the JWKS
	if err != nil {
		return Verified{}, fmt.Errorf("jwks: %w", err)
	}
	set, err := jwk.Parse(jb)
	if err != nil {
		return Verified{}, fmt.Errorf("jwks: %w", err)
	}
	k, found := set.LookupKeyID(hdr.KID)
	if !found {
		return Verified{}, fmt.Errorf("jwks: no key %q", hdr.KID)
	}
	var pub rsa.PublicKey
	if err := jwk.Export(k, &pub); err != nil {
		return Verified{}, fmt.Errorf("jwks: key %q: %w", hdr.KID, err)
	}
	h := sha256.New()
	h.Write([]byte(protected))
	h.Write([]byte("."))
	h.Write(b)
	if err := rsa.VerifyPKCS1v15(&pub, crypto.SHA256, h.Sum(nil), raw); err != nil {
		return Verified{}, errors.New("signature: does not verify over the report's bytes")
	}
	d := Digest(b)
	if db, err := os.ReadFile(path + DigestSuffix); err == nil { //nolint:gosec // beside the report
		if got := strings.Fields(string(db)); len(got) == 0 || got[0] != d {
			return Verified{}, fmt.Errorf("digest file says %v, the report is %s", got, d)
		}
	}
	return Verified{Digest: d, KID: hdr.KID, IssuedAt: hdr.IssuedAt}, nil
}

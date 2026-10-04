// Package seed puts the demo's data into the four systems through their
// public APIs only (docs/WORKPACKAGES/WP-L6.md, make demo): console
// accounts with their TOTP enrolments, the registry rows, the Remote ID
// receivers, the U-space airspace and the scenarios' zones at the
// authority; the operators, their machine clients and serial bindings at
// the USSP; a watch supervisor at the ANSP. It writes the targets file
// the scenario runner reads (targets/local-demo.yaml) and the secret
// files it names (secrets/demo/), never a secret into the repository.
//
// Every value the systems issue (a client secret, a receiver key, a
// registration's id) is kept in the state directory so a second run
// changes nothing that exists; a TOTP secret is kept so a session can be
// opened again (sessions end after 30 minutes idle, so `sessions` is run
// before each scenario run).
package seed

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP is HMAC-SHA1, which the systems use
	"crypto/tls"
	"crypto/x509"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is an HTTP client for the lab hosts: TLS from the lab CA, and
// every lab host dialled at 127.0.0.1 on the published port, so nothing
// on the machine (hosts file, trust store) is changed.
type Client struct {
	HTTP *http.Client
	Port int
}

// NewClient builds the client. hosts are the names dialled at 127.0.0.1.
func NewClient(caFile string, port int, hosts []string) (*Client, error) {
	pem, err := os.ReadFile(caFile) //nolint:gosec // the operator names the CA file
	if err != nil {
		return nil, fmt.Errorf("seed: lab CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("seed: %s holds no certificate", caFile)
	}
	lab := map[string]bool{}
	for _, h := range hosts {
		lab[strings.ToLower(h)] = true
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, _, err := net.SplitHostPort(addr)
			if err == nil && lab[strings.ToLower(h)] {
				addr = net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
			}
			return d.DialContext(ctx, network, addr)
		},
	}
	return &Client{HTTP: &http.Client{Transport: tr, Timeout: 30 * time.Second}, Port: port}, nil
}

// URL is https://host[:port]path.
func (c *Client) URL(host, path string) string {
	if c.Port == 443 {
		return "https://" + host + path
	}
	return fmt.Sprintf("https://%s:%d%s", host, c.Port, path)
}

// Auth is how a request authenticates.
type Auth struct {
	Bearer string
	// Cookie is a console session sent as the uspace_session cookie with
	// a CSRF pair (the authority's console API, M21).
	Cookie string
	CSRF   string
	Header map[string]string
}

// Answer is a response read whole.
type Answer struct {
	Status int
	Body   []byte
}

// JSON decodes the body into v.
func (a Answer) JSON(v any) error {
	if err := json.Unmarshal(a.Body, v); err != nil {
		return fmt.Errorf("seed: answer %d is not the JSON expected: %w: %s", a.Status, err, short(a.Body))
	}
	return nil
}

func (a Answer) String() string { return fmt.Sprintf("%d %s", a.Status, short(a.Body)) }

// Do sends one request. body is JSON-encoded unless it is []byte (sent
// as application/octet-stream) or url.Values-like string (form).
func (c *Client) Do(ctx context.Context, method, url string, au Auth, body any) (Answer, error) {
	var r io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r, ctype = bytes.NewReader(b), "application/octet-stream"
	case Form:
		r, ctype = strings.NewReader(string(b)), "application/x-www-form-urlencoded"
	default:
		j, err := json.Marshal(b)
		if err != nil {
			return Answer{}, err
		}
		r, ctype = bytes.NewReader(j), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return Answer{}, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	if au.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+au.Bearer)
	}
	if au.Cookie != "" {
		req.Header.Set("Cookie", "uspace_session="+au.Cookie+"; uspace_csrf="+au.CSRF)
		req.Header.Set("X-CSRF-Token", au.CSRF)
	}
	for k, v := range au.Header {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Answer{}, fmt.Errorf("seed: %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Answer{}, err
	}
	return Answer{Status: resp.StatusCode, Body: b}, nil
}

// Form is an application/x-www-form-urlencoded body.
type Form string

// Expect is Do, failing unless the answer has one of the statuses.
func (c *Client) Expect(ctx context.Context, method, url string, au Auth, body any, what string, status ...int) (Answer, error) {
	a, err := c.Do(ctx, method, url, au, body)
	if err != nil {
		return a, err
	}
	for _, s := range status {
		if a.Status == s {
			return a, nil
		}
	}
	return a, fmt.Errorf("seed: %s answered %s (want %v)", what, a, status)
}

func short(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

// TOTP is the RFC 6238 code (HMAC-SHA1, 6 digits, 30 s) of a base32
// secret at t; the systems' enrolments are that (authority MFAEnrolment,
// ANSP WP-11).
func TOTP(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secret, " ", ""), "=")))
	if err != nil {
		return "", fmt.Errorf("seed: TOTP secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", code), nil
}

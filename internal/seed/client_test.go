package seed

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// limited answers 429 with Retry-After for the first refusals requests,
// then 201; it records every body it was sent.
type limited struct {
	mu       sync.Mutex
	refusals int
	retry    string
	bodies   []string
	status   int
}

func (l *limited) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bodies = append(l.bodies, string(b))
	if len(l.bodies) <= l.refusals {
		if l.retry != "" {
			w.Header().Set("Retry-After", l.retry)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"https://schemas.uspace.ge/problems/rate_limited"}`))
		return
	}
	w.WriteHeader(l.status)
}

func limitedClient(t *testing.T, h http.Handler) (*Client, string, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var waits []time.Duration
	c := &Client{HTTP: srv.Client(), Wait: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}}
	return c, srv.URL, &waits
}

// A 429 is a request the system did not act on: the client waits what
// Retry-After says and sends the same request again, so a seed of a
// thousand aircraft is not stopped by the systems' request budgets.
func TestRateLimitedRequestIsSentAgain(t *testing.T) {
	l := &limited{refusals: 2, retry: "3", status: http.StatusCreated}
	c, base, waits := limitedClient(t, l)
	ans, err := c.Do(context.Background(), http.MethodPost, base+"/v1/registry/uas", Auth{}, map[string]string{"serial": "S1"})
	if err != nil || ans.Status != http.StatusCreated {
		t.Fatalf("%v %v", ans, err)
	}
	if len(l.bodies) != 3 || l.bodies[0] != l.bodies[2] || !strings.Contains(l.bodies[2], `"S1"`) {
		t.Fatalf("bodies %q", l.bodies)
	}
	if len(*waits) != 2 || (*waits)[0] != 3*time.Second {
		t.Fatalf("waits %v", *waits)
	}
}

// Bounded both ways: a Retry-After past the bound is waited for only up
// to it, a missing or unreadable one is a second, and a system that
// never lets the request through ends in its own 429 after
// MaxRateLimitedTries, not a loop.
func TestRateLimitedRetriesAreBounded(t *testing.T) {
	l := &limited{refusals: 1 << 30, retry: "86400", status: http.StatusCreated}
	c, base, waits := limitedClient(t, l)
	ans, err := c.Do(context.Background(), http.MethodPost, base+"/x", Auth{}, map[string]string{})
	if err != nil || ans.Status != http.StatusTooManyRequests || len(l.bodies) != MaxRateLimitedTries {
		t.Fatalf("%v %v after %d requests", ans, err, len(l.bodies))
	}
	for _, w := range *waits {
		if w != MaxRateLimitedWait {
			t.Fatalf("waited %v, bound %v", w, MaxRateLimitedWait)
		}
	}
	l2 := &limited{refusals: 1, retry: "soon", status: http.StatusOK}
	c2, base2, waits2 := limitedClient(t, l2)
	if ans, err := c2.Do(context.Background(), http.MethodGet, base2+"/x", Auth{}, nil); err != nil || ans.Status != http.StatusOK || (*waits2)[0] != time.Second {
		t.Fatalf("%v %v waits %v", ans, err, *waits2)
	}
}

// Only a 429 is sent again: any other refusal is the answer.
func TestOtherRefusalsAreNotRetried(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	n := 0
	c, base, waits := limitedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++; h(w, r) }))
	if ans, err := c.Do(context.Background(), http.MethodPost, base+"/x", Auth{}, map[string]string{}); err != nil || ans.Status != http.StatusServiceUnavailable || n != 1 || len(*waits) != 0 {
		t.Fatalf("%v %v, %d requests, waits %v", ans, err, n, *waits)
	}
}

// A wait cut short by the context ends the request with that error.
func TestRateLimitedWaitEndsWithTheContext(t *testing.T) {
	l := &limited{refusals: 5, retry: "1", status: http.StatusOK}
	srv := httptest.NewServer(l)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{HTTP: srv.Client(), Wait: func(context.Context, time.Duration) error { cancel(); return context.Canceled }}
	if _, err := c.Do(ctx, http.MethodGet, srv.URL, Auth{}, nil); err == nil || len(l.bodies) != 1 {
		t.Fatalf("%v after %d requests", err, len(l.bodies))
	}
}

// LabTransport dials every lab host of demo.env at 127.0.0.1 on the
// published port and trusts the lab CA only: a request to a lab host
// reaches the stack (presence), the same request without the transport
// is refused by TLS, and an env file without a port or a host is
// refused before any transport is made.
func TestLabTransportReachesTheStack(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ca"), 0o700); err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca", "ca.pem"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	write := func(body string) string {
		p := filepath.Join(dir, fmt.Sprintf("demo-%d.env", len(body)))
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// example.com is a name the test server's certificate holds.
	hosts := "AUTHORITY_HOST=a.test\nCISP_HOST=c.test\nUSSP_HOST=example.com\nANSP_HOST=n.test\nLAB_HOST=l.test\n"
	env := write(hosts + "DEMO_STATE_DIR=" + filepath.ToSlash(dir) + "\nDEMO_HTTPS_PORT=" + port + "\n")
	tr, err := LabTransport(env, 64)
	if err != nil {
		t.Fatal(err)
	}
	if tr.MaxIdleConnsPerHost != 64 {
		t.Fatalf("idle connections per host %d", tr.MaxIdleConnsPerHost)
	}
	resp, err := (&http.Client{Transport: tr}).Get("https://example.com:" + port + "/v1/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp, err := (&http.Client{}).Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("the test server is trusted without the lab CA")
	}
	for name, body := range map[string]string{
		"bad port": hosts + "DEMO_STATE_DIR=" + filepath.ToSlash(dir) + "\nDEMO_HTTPS_PORT=https\n",
		"no host":  "AUTHORITY_HOST=a.test\nDEMO_STATE_DIR=" + filepath.ToSlash(dir) + "\n",
	} {
		if _, err := LabTransport(write(body), 4); err == nil {
			t.Errorf("%s: a transport was made", name)
		}
	}
}

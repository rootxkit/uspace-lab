package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenCachedAndRenewedAtHalfLife(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" ||
			r.PostForm.Get("client_id") != "op-1" || r.PostForm.Get("client_secret") != "s3cret" ||
			r.PostForm.Get("scope") != "ussp.telemetry ussp.intents" || r.URL.RawQuery != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":60,"scope":"ussp.telemetry"}`))
	}))
	defer srv.Close()
	now := time.Unix(1_790_000_000, 0)
	c := &ClientCredentials{TokenURL: srv.URL, ClientID: "op-1", ClientSecret: "s3cret", Scopes: []string{"ussp.telemetry", "ussp.intents"}, Now: func() time.Time { return now }}
	for range 3 {
		if tok, err := c.Token(context.Background()); err != nil || tok != "tok" {
			t.Fatal(tok, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls, want 1 (cached)", calls.Load())
	}
	now = now.Add(31 * time.Second)
	if _, err := c.Token(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatalf("not renewed at half-life: %d calls, %v", calls.Load(), err)
	}
}

func TestRefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	c := &ClientCredentials{TokenURL: srv.URL, ClientID: "x", ClientSecret: "y"}
	_, err := c.Token(context.Background())
	var oe *Error
	if !errors.As(err, &oe) || oe.Status != http.StatusUnauthorized {
		t.Fatalf("got %v", err)
	}
}

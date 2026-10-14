package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// tlsClient is a seed client of srv, a TLS test server on 127.0.0.1.
func tlsClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	return &Client{HTTP: srv.Client(), Port: port, Wait: func(context.Context, time.Duration) error { return nil }}
}

// The USSP's CIS version of the U-space airspace is read from its
// readiness, and the wait ends when it moves, or fails, naming what it
// last saw, when it does not: the run's intents would not be DSS-backed.
func TestWaitForTheUSSPsCIS(t *testing.T) {
	var mu sync.Mutex
	// The CIS takes version 2 on the second read after moves is set.
	moves, reads := false, 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		version := "1"
		if moves {
			if reads++; reads >= 2 {
				version = "2"
			}
		}
		_, _ = fmt.Fprintf(w, `{"dependencies":{"cis":{"state":"up","detail":"zones:0,uspace_airspace:%s,restrictions:0, age 3 s"}}}`, version)
	}))
	t.Cleanup(srv.Close)
	u := &USSP{C: tlsClient(t, srv), Host: "127.0.0.1"}
	ctx := context.Background()
	if v, err := u.CISUSpace(ctx); err != nil || v != "1" {
		t.Fatalf("version %q %v", v, err)
	}
	err := u.WaitCISUSpace(ctx, "1", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "still holds U-space airspace version 1") {
		t.Fatalf("an unchanged CIS: %v", err)
	}
	mu.Lock()
	moves = true
	mu.Unlock()
	if err := u.WaitCISUSpace(ctx, "1", 10*time.Second); err != nil {
		t.Fatalf("a CIS that took the new version: %v", err)
	}
}

// The oversight area is added once: a view with the label and the box
// is kept, any other is added beside it.
func TestEnsureDPView(t *testing.T) {
	var mu sync.Mutex
	views := []map[string]any{{"label": "kept", "bbox": []float64{44.8, 41.7, 44.9, 41.8}}}
	posts := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"views": views})
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			var v map[string]any
			_ = json.Unmarshal(b, &v)
			views = append(views, v)
			posts++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	a := &Authority{C: tlsClient(t, srv), Host: "127.0.0.1"}
	ctx := context.Background()
	if err := a.EnsureDPView(ctx, Session{Token: "s"}, "kept", [4]float64{44.8, 41.7, 44.9, 41.8}); err != nil || posts != 0 {
		t.Fatalf("an existing view: %v, %d posts", err, posts)
	}
	if err := a.EnsureDPView(ctx, Session{Token: "s"}, "kept", [4]float64{44.8, 41.7, 45.0, 41.8}); err != nil || posts != 1 {
		t.Fatalf("another box: %v, %d posts", err, posts)
	}
	if err := a.EnsureDPView(ctx, Session{Token: "s"}, "kept", [4]float64{44.8, 41.7, 45.0, 41.8}); err != nil || posts != 1 {
		t.Fatalf("the same again: %v, %d posts", err, posts)
	}
}

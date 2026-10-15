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

// The wait for the USSP's geo-awareness ends when it puts the airspace
// in the box, asked with the operator's token, and fails, naming what it
// last saw, when it does not: the run's intents would not be DSS-backed.
func TestWaitForTheUSSPsGeoAwareness(t *testing.T) {
	var mu sync.Mutex
	// The USSP holds the airspace from the second read after moves.
	moves, reads := false, 0
	var auths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auths = append(auths, r.Header.Get("Authorization")+" "+r.URL.Query().Get("bbox"))
		ids := `[]`
		if moves {
			if reads++; reads >= 2 {
				ids = `[{"identifier":"LABUSP1"}]`
			}
		}
		_, _ = fmt.Fprintf(w, `{"cis_version":"zones:0,uspace_airspace:2,restrictions:0","uspace_airspaces":%s,"zones":[]}`, ids)
	}))
	t.Cleanup(srv.Close)
	u := &USSP{C: tlsClient(t, srv), Host: "127.0.0.1"}
	ctx := context.Background()
	tok := func(context.Context) (string, error) { return "op-token", nil }
	box := [4]float64{44.8, 41.7, 44.81, 41.71}
	err := u.WaitGeoAt(ctx, tok, box, GeoUSpace, "LABUSP1", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "does not put uspace_airspaces LABUSP1") || !strings.Contains(err.Error(), "uspace_airspace:2") {
		t.Fatalf("an airspace the USSP does not hold: %v", err)
	}
	mu.Lock()
	moves = true
	mu.Unlock()
	if err := u.WaitGeoAt(ctx, tok, box, GeoUSpace, "LABUSP1", 10*time.Second); err != nil {
		t.Fatalf("an airspace the USSP took: %v", err)
	}
	if auths[0] != "Bearer op-token 44.8000000,41.7000000,44.8100000,41.7100000" {
		t.Fatalf("asked with %q", auths[0])
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

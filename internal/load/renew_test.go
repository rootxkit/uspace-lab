package load

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// A stream's credentials expire during a long run: the authority closes
// the picture with 4401 "sign in again" after its session ends, the USSP
// answers an upgrade with an expired token 401. The stream asks for
// fresh credentials on the next connection after either, and keeps the
// ones it has otherwise (asking for new ones on every reconnect would
// sign in a console for every network blip). A 60-minute systems run
// reconnected its consoles 368 times on a dead session instead.
func TestStreamsRenewCredentialsWhenRefused(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer old-401" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conns.Add(1)
		if r.Header.Get("Authorization") == "Bearer old" {
			_ = c.Close(4401, "sign in again")
			return
		}
		b, _ := wire.New(wire.SchemaStatus, "authority", time.Now(), time.Now(), nil, "system", false, map[string]any{"dropped_frames": 0})
		_ = c.Write(r.Context(), websocket.MessageText, b)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, start := range []string{"old", "old-401"} {
		var renewals []bool
		cur := start
		s := &stream{name: "console-00", kind: streamPicture, url: url, ready: make(chan struct{}),
			headerFn: func(_ context.Context, renew bool) (http.Header, error) {
				renewals = append(renewals, renew)
				if renew {
					cur = "fresh"
				}
				return http.Header{"Authorization": {"Bearer " + cur}}, nil
			}}
		rd := &readers{rec: observe.NewRecorder(nil), picture: NewLedger(), traffic: NewReceiptLedger()}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); rd.run(ctx, s) }()
		select {
		case <-s.ready:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: never opened with fresh credentials: %v", start, renewals)
		}
		cancel()
		<-done
		if len(renewals) != 2 || renewals[0] || !renewals[1] {
			t.Fatalf("%s: renewals %v, want one refusal then one renewal", start, renewals)
		}
	}
}

// The consoles share one session: when the authority ends it, every
// console is refused at once, and only the first to ask signs in again;
// the others take that session. Ten consoles each signing in thrashed
// the 100-tier run (a sign-in waits for a fresh TOTP step, and the
// consoles kept asking).
func TestConsolesShareARenewedSession(t *testing.T) {
	signIns := 0
	r := &run{opt: Options{RenewSession: func(context.Context) (string, error) {
		signIns++
		return "session-" + string(rune('0'+signIns)), nil
	}}, systems: &systemsTarget{session: "session-0", origin: "https://authority.test"}}
	if err := r.startSystems(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	a, b := r.pictureHeaderFor(), r.pictureHeaderFor()
	for _, h := range []func(context.Context, bool) (http.Header, error){a, b} {
		if hd, err := h(context.Background(), false); err != nil || hd.Get("Cookie") != "uspace_session=session-0" {
			t.Fatalf("start: %v %v", hd, err)
		}
	}
	ha, _ := a(context.Background(), true)
	hb, _ := b(context.Background(), true)
	if signIns != 1 || ha.Get("Cookie") != "uspace_session=session-1" || hb.Get("Cookie") != ha.Get("Cookie") {
		t.Fatalf("%d sign-ins, a %v, b %v", signIns, ha, hb)
	}
	// Refused again on the renewed session: a second sign-in.
	if hb2, _ := b(context.Background(), true); signIns != 2 || hb2.Get("Cookie") != "uspace_session=session-2" {
		t.Fatalf("%d sign-ins, %v", signIns, hb2)
	}
}

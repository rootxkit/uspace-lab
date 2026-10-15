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
		// The refusal is recorded as a break, and excused: a connection
		// on fresh credentials followed it.
		if n, _ := s.unexcused(nil); n != 0 || s.stats().Breaks != 1 {
			t.Fatalf("%s: %d breaks, %d unexcused, want one excused", start, s.stats().Breaks, n)
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

// A stream that breaks is a run error unless something the run did
// explains it: a fault it injected (a restarted picture-ws closes every
// console), or expired credentials refused and replaced by a connection
// on fresh ones (a 60-minute run outlives a token). A break outside both
// still fails the run, and so does a refusal that was never followed by
// a connection. Before, any second connection failed the run, so every
// fault plan that restarted a stream's process failed it too.
func TestStreamBreaksAreExcusedOnlyByAFaultOrARenewal(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	inFault := func(at time.Time) bool {
		return !at.Before(t0.Add(10*time.Second)) && at.Before(t0.Add(20*time.Second))
	}
	cases := []struct {
		name    string
		breaks  []streamBreak
		conns   int
		excused func(time.Time) bool
		want    int
	}{
		{"none", nil, 1, inFault, 0},
		{"inside a fault", []streamBreak{{at: t0.Add(12 * time.Second), err: "closed", connects: 1}}, 2, inFault, 0},
		{"outside every fault", []streamBreak{{at: t0.Add(30 * time.Second), err: "closed", connects: 1}}, 2, inFault, 1},
		{"no fault plan", []streamBreak{{at: t0.Add(12 * time.Second), err: "closed", connects: 1}}, 2, nil, 1},
		{"renewed and connected again", []streamBreak{{at: t0.Add(30 * time.Second), err: "4401", renew: true, connects: 1}}, 2, nil, 0},
		{"renewal never connected", []streamBreak{{at: t0.Add(30 * time.Second), err: "401", renew: true, connects: 1}}, 1, nil, 1},
	}
	for _, c := range cases {
		s := &stream{breaks: c.breaks, connects: c.conns}
		if n, last := s.unexcused(c.excused); n != c.want || (n > 0 && last == "") {
			t.Errorf("%s: %d unexcused (last %q), want %d", c.name, n, last, c.want)
		}
	}
	over := &stream{connects: 1, overBreaks: 3}
	if n, _ := over.unexcused(inFault); n != 3 {
		t.Errorf("breaks past the bound: %d unexcused, want 3", n)
	}
}

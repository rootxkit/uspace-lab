package load

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// A console subscribed to the whole area of 1000 aircraft is sent a
// snapshot of every track, past wire.MaxFrameBytes: the first recorded
// 1000 run lost every console to "message too big" on each snapshot and
// reconnected them some 240 times each. A console's bound is its
// fleet's snapshot: such a snapshot is read and makes the stream ready;
// one past the bound still breaks the stream, and a stream with no
// bound of its own keeps wire.MaxFrameBytes.
func TestAConsoleReadsItsFleetsSnapshot(t *testing.T) {
	if snapshotLimit(100) != wire.MaxFrameBytes || snapshotLimit(1000) != 1000*snapshotTrackBytes {
		t.Fatalf("limits %d, %d", snapshotLimit(100), snapshotLimit(1000))
	}
	// 1000 tracks of about 2 KB each: 2 MB, inside 1000's bound.
	track := strings.Repeat("x", 2000)
	tracks := make([]map[string]string, 1000)
	for i := range tracks {
		tracks[i] = map[string]string{"pad": track}
	}
	snap, err := wire.New(wire.SchemaSnapshot, "authority", time.Now(), time.Now(), nil, "system", false,
		map[string]any{"tracks": tracks, "alerts": []any{}, "manned": []any{}, "zones_version": nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) <= wire.MaxFrameBytes || len(snap) > snapshotLimit(1000) {
		t.Fatalf("snapshot of %d bytes", len(snap))
	}
	status, _ := wire.New(wire.SchemaStatus, "authority", time.Now(), time.Now(), nil, "system", false, map[string]any{"dropped_frames": 0})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Write(r.Context(), websocket.MessageText, status)
		_ = c.Write(r.Context(), websocket.MessageText, snap)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, c := range []struct {
		name  string
		limit int
		ready bool
	}{{"the fleet's bound", snapshotLimit(1000), true}, {"no bound of its own", 0, false}, {"a smaller fleet's", snapshotLimit(300), false}} {
		s := &stream{name: "console-00", kind: streamPicture, url: url, needSnap: true, ready: make(chan struct{}), readLimit: c.limit}
		rd := &readers{rec: observe.NewRecorder(nil), picture: NewLedger(), traffic: NewReceiptLedger()}
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() { errc <- rd.read(ctx, s) }()
		select {
		case <-s.ready:
			if !c.ready {
				t.Errorf("%s: a %d-byte snapshot read", c.name, len(snap))
			}
			cancel()
			<-errc
		case err := <-errc:
			cancel()
			if c.ready || err == nil || !strings.Contains(err.Error(), "message too big") {
				t.Errorf("%s: %v", c.name, err)
			}
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatalf("%s: neither ready nor broken", c.name)
		}
	}
	// The frame parsed under the same bound (an envelope past it is
	// unparseable, not a snapshot).
	s := &stream{name: "c", needSnap: true, ready: make(chan struct{})}
	rd := &readers{}
	rd.handle(s, snap, time.Now(), nil)
	if s.frames["unparseable"] != 1 || !s.needSnap {
		t.Fatalf("parsed past the default bound: %v", s.frames)
	}
	s.readLimit = snapshotLimit(1000)
	rd.handle(s, snap, time.Now(), nil)
	if s.frames[wire.SchemaSnapshot] != 1 || s.needSnap {
		t.Fatalf("not parsed within the fleet's bound: %v", s.frames)
	}
}

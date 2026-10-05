package reftarget

import (
	"sync"
	"sync/atomic"

	"github.com/rootxkit/uspace-core/core"
)

// hub fans frames out to WebSocket subscribers with bounded queues. A
// subscriber whose queue is full loses the frame and the loss is counted
// on it (its next status frame carries dropped_frames), never blocking
// the monitor.
type hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

type sub struct {
	c       chan []byte
	filter  func(topic string) bool
	dropped atomic.Uint64
	// view is the console's subscription (console/subscribe/v1): which
	// layers, inside which box. Nil until the client subscribes; track
	// frames go only to a subscriber whose view holds the track.
	view atomic.Pointer[view]
}

// view is one console/subscribe/v1 body.
type view struct {
	west, south, east, north float64
	tracks                   bool
}

// holds reports whether p is inside the box (west > east crosses the
// antimeridian, RFC 7946 §5.2).
func (v *view) holds(p core.LatLon) bool {
	if p.LatDeg < v.south || p.LatDeg > v.north {
		return false
	}
	if v.west <= v.east {
		return p.LonDeg >= v.west && p.LonDeg <= v.east
	}
	return p.LonDeg >= v.west || p.LonDeg <= v.east
}

// subQueue bounds one subscriber's queue (E-10).
const subQueue = 256

func newHub() *hub { return &hub{subs: map[*sub]struct{}{}} }

func (h *hub) add(filter func(string) bool) *sub {
	s := &sub{c: make(chan []byte, subQueue), filter: filter}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *hub) remove(s *sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

func (h *hub) publish(topic string, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.filter != nil && !s.filter(topic) {
			continue
		}
		s.offer(frame)
	}
}

func (s *sub) offer(frame []byte) {
	select {
	case s.c <- frame:
	default:
		s.dropped.Add(1)
	}
}

// wants reports whether any subscriber takes topic, so a publisher can
// skip building a frame nobody reads (the per-sample frames of a load
// run).
func (h *hub) wants(topic string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.filter == nil || s.filter(topic) {
			return true
		}
	}
	return false
}

// publishEach hands every subscriber of topic its own frame (one that
// carries the subscriber's dropped_frames).
func (h *hub) publishEach(topic string, build func(s *sub) []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.filter != nil && !s.filter(topic) {
			continue
		}
		s.offer(build(s))
	}
}

// publishTrack hands a track frame to every subscriber whose view holds
// p; build is called once, and only when someone takes it.
func (h *hub) publishTrack(p core.LatLon, build func() []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var frame []byte
	for s := range h.subs {
		v := s.view.Load()
		if v == nil || !v.tracks || !v.holds(p) {
			continue
		}
		if frame == nil {
			frame = build()
		}
		s.offer(frame)
	}
}

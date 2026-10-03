package reftarget

import (
	"sync"
	"sync/atomic"
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
		select {
		case s.c <- frame:
		default:
			s.dropped.Add(1)
		}
	}
}

package vehicle

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/rootxkit/uspace-core/core"
)

// Counter names of the stream (E-09).
const (
	CounterLines        = "lines"
	CounterInvalidLines = "invalid_lines"
	CounterPublished    = "published"
)

// Bus fans the vehicle stream out to its consumers (the simulators and
// the runner's own record). Each subscriber has a bounded queue (E-10);
// a sample a full queue cannot take is dropped for that subscriber and
// counted, never blocking the others or the reader.
type Bus struct {
	mu       sync.Mutex
	subs     []*Sub
	counters core.Counters
}

// Sub is one consumer's view of the stream.
type Sub struct {
	Name    string
	C       chan Sample
	dropped atomic.Uint64
	sysids  map[int]bool
}

// Dropped is how many samples this subscriber's full queue refused.
func (s *Sub) Dropped() uint64 { return s.dropped.Load() }

// NewBus makes an empty bus.
func NewBus() *Bus { return &Bus{} }

// Counters are the bus's counters.
func (b *Bus) Counters() *core.Counters { return &b.counters }

// Subscribe adds a consumer with a queue of buf samples. With sysids, it
// receives only those vehicles.
func (b *Bus) Subscribe(name string, buf int, sysids ...int) *Sub {
	if buf < 1 {
		buf = 1
	}
	s := &Sub{Name: name, C: make(chan Sample, buf)}
	if len(sysids) > 0 {
		s.sysids = map[int]bool{}
		for _, id := range sysids {
			s.sysids[id] = true
		}
	}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s
}

// Publish hands s to every subscriber that wants it.
func (b *Bus) Publish(s Sample) {
	b.counters.Inc(CounterPublished)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		if sub.sysids != nil && !sub.sysids[s.Sysid] {
			continue
		}
		select {
		case sub.C <- s:
		default:
			sub.dropped.Add(1)
		}
	}
}

// Close closes every subscriber's channel; Publish must not be called
// afterwards.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		close(sub.C)
	}
	b.subs = nil
}

// ReadLines reads NDJSON from r until EOF or ctx ends, publishing each
// valid line. Invalid lines are counted and reported through onInvalid
// (which the caller rate-limits); a line longer than MaxLineBytes ends
// the read with an error, since the stream is then not sim/vehicle/v1.
func ReadLines(ctx context.Context, r io.Reader, publish func(Sample), counters *core.Counters, onInvalid func(error)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024), MaxLineBytes+1)
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the run ended
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		counters.Inc(CounterLines)
		s, err := ParseLine(line)
		if err != nil {
			counters.Inc(CounterInvalidLines)
			if onInvalid != nil {
				onInvalid(err)
			}
			continue
		}
		publish(s)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("vehicle: reading lines: %w", err)
	}
	return nil
}

// ListenUDP reads datagrams of NDJSON lines (sim/mav_reader.py --emit)
// on addr until ctx ends. It only listens.
func ListenUDP(ctx context.Context, addr string, publish func(Sample), counters *core.Counters, onInvalid func(error)) error {
	pc, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("vehicle: listen %s: %w", addr, err)
	}
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	buf := make([]byte, 65535)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // the listener was closed because the run ended
			}
			return fmt.Errorf("vehicle: read %s: %w", addr, err)
		}
		if err := ReadLines(ctx, bytes.NewReader(buf[:n]), publish, counters, onInvalid); err != nil && onInvalid != nil {
			onInvalid(err)
		}
	}
}

package load

import (
	"sync"
	"time"
)

// MatchWindow is how far a frame's captured_at may be from the sample it
// shows when the target places a sample at the source's own time (the
// authority believes a Remote ID broadcast time, to a tenth): the
// samples are a period apart (1 s), so half a period is unambiguous.
const MatchWindow = 500 * time.Millisecond

// ReceiptSkew is how far after a frame's captured_at the sample it shows
// may have been handed out when the target places samples at receipt
// (the USSP's operator telemetry): only the two clocks' difference,
// which in one process is none.
const ReceiptSkew = 50 * time.Millisecond

// ringSize bounds what is kept per aircraft and path (E-10): a frame
// later than ringSize periods is untraceable, and counted so.
const ringSize = 64

// sent is one sample handed to a client.
type sent struct {
	at   time.Time
	seen bool
}

// ring is the recent samples of one aircraft on one path.
type ring struct {
	buf  [ringSize]sent
	next int
	n    int
}

// Ledger keeps, per aircraft and path, the samples handed out and which
// a console has shown, and times the first showing of each.
//
// A frame is tied to the sample it shows by its captured_at. Where the
// target places a sample at the source's time, that is the nearest
// sample within MatchWindow. Where it places at receipt, captured_at is
// after the hand-out by the transport's delay, however long: the sample
// is the newest one handed out by captured_at (+ ReceiptSkew). Matching
// a receipt time to the nearest sample would tie a frame that arrived
// more than half a period late to the next sample, or to none, and the
// slowest frames would drop out of the percentiles.
type Ledger struct {
	mu      sync.Mutex
	receipt bool
	rings   map[string]*ring
	hist    *Histogram
	// handed counts samples handed out; shown those shown at least once;
	// unshown those that left the ring (or the run ended) never shown.
	handed, shown, unshown, repeats, untraceable uint64
}

// NewLedger makes an empty ledger for a path whose target places a
// sample at the source's time.
func NewLedger() *Ledger { return &Ledger{rings: map[string]*ring{}, hist: NewHistogram()} }

// NewReceiptLedger makes an empty ledger for a path whose target places
// a sample at its receipt.
func NewReceiptLedger() *Ledger {
	return &Ledger{receipt: true, rings: map[string]*ring{}, hist: NewHistogram()}
}

// matchLocked is the index of the sample of r a frame captured at
// captured shows, or -1.
func (l *Ledger) matchLocked(r *ring, captured time.Time) int {
	best := -1
	if l.receipt {
		limit := captured.Add(ReceiptSkew)
		for i := range r.n {
			if !r.buf[i].at.After(limit) && (best < 0 || r.buf[i].at.After(r.buf[best].at)) {
				best = i
			}
		}
		return best
	}
	bestD := MatchWindow + 1
	for i := range r.n {
		d := r.buf[i].at.Sub(captured)
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	if bestD > MatchWindow {
		return -1
	}
	return best
}

// Hand records that aircraft's sample was handed out at at.
func (l *Ledger) Hand(aircraft string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rings[aircraft]
	if r == nil {
		r = &ring{}
		l.rings[aircraft] = r
	}
	if r.n == ringSize && !r.buf[r.next].seen {
		l.unshown++
	}
	r.buf[r.next] = sent{at: at}
	r.next = (r.next + 1) % ringSize
	r.n = min(r.n+1, ringSize)
	l.handed++
}

// Show records that a frame for aircraft captured at captured arrived at
// arrived. The first showing of a sample is timed; a later one is a
// repeat; a frame no sample matches is untraceable. It returns the
// latency when it timed one.
func (l *Ledger) Show(aircraft string, captured, arrived time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rings[aircraft]
	if r == nil {
		l.untraceable++
		return 0, false
	}
	best := l.matchLocked(r, captured)
	if best < 0 {
		l.untraceable++
		return 0, false
	}
	s := &r.buf[best]
	lat := arrived.Sub(s.at)
	if lat < 0 {
		// The frame arrived before the sample it was tied to was handed
		// out: the tie is wrong, and the sample stays unshown.
		l.hist.Add(lat) // counted as negative, never timed
		return 0, false
	}
	if s.seen {
		l.repeats++
		return 0, false
	}
	s.seen = true
	l.shown++
	l.hist.Add(lat)
	return lat, true
}

// Find returns when the sample of aircraft a frame captured at captured
// shows was handed out, without marking it.
func (l *Ledger) Find(aircraft string, captured time.Time) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rings[aircraft]
	if r == nil {
		return time.Time{}, false
	}
	if i := l.matchLocked(r, captured); i >= 0 {
		return r.buf[i].at, true
	}
	return time.Time{}, false
}

// LedgerCounts are a ledger's totals.
type LedgerCounts struct {
	Handed      uint64 `json:"handed"`
	Shown       uint64 `json:"shown"`
	Unshown     uint64 `json:"unshown"`
	Repeats     uint64 `json:"repeats"`
	Untraceable uint64 `json:"untraceable"`
	Negative    uint64 `json:"negative_latencies"`
}

// Close counts every sample still in a ring and never shown as unshown
// and returns the totals; call it once, after the drain.
func (l *Ledger) Close() LedgerCounts {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.rings {
		for i := range r.n {
			if !r.buf[i].seen {
				l.unshown++
				r.buf[i].seen = true
			}
		}
	}
	return l.countsLocked()
}

// Counts returns the totals so far.
func (l *Ledger) Counts() LedgerCounts {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.countsLocked()
}

func (l *Ledger) countsLocked() LedgerCounts {
	return LedgerCounts{Handed: l.handed, Shown: l.shown, Unshown: l.unshown, Repeats: l.repeats, Untraceable: l.untraceable, Negative: l.hist.Negative()}
}

// Histogram returns the latency histogram (read after the run).
func (l *Ledger) Histogram() *Histogram { return l.hist }

package main

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// A "kept" alert is judged by the absence of a clear and of a second
// raise, and an absence is evidence only while the stream that would
// carry them is talking: a console stream that dropped, or was refused
// on reconnection, sends no clear either. Each background system's
// console stream sends console/status/v1 on connect and every 2 s
// (spec 04 §1), so a silence longer than background.max_silence_s is a
// stream that was not heard, and the alert claim over it is unproven.

// maxSilences bounds the silences kept per stream; past it the run
// counts them and the judgement fails closed (dropped).
const maxSilences = 1000

// silence is one stretch of a stream without a frame: From is the last
// frame before it, To the first after it (or the end of the run).
type silence struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Reread is the system's open alerts re-sent within max_silence_s of
	// To: a console/snapshot/v1 (the authority's picture) or the active
	// alert/v1 or violation/v1 frames a stream sends on connect (the
	// USSP's alert stream), so an alert lost or raised anew while the
	// stream was down shows there.
	Reread bool `json:"reread"`
}

// StreamLiveness is what one background console stream said, as frames.
type StreamLiveness struct {
	System   string    `json:"system"`
	Frames   uint64    `json:"frames"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	Silences []silence `json:"silences,omitempty"`
	Dropped  int       `json:"silences_dropped,omitempty"`
}

// liveWatch records, per stream of a background system, every silence
// longer than maxSilence.
type liveWatch struct {
	mu         sync.Mutex
	maxSilence time.Duration
	want       map[string]bool
	streams    map[string]*StreamLiveness
}

func newLiveWatch(bg Background) *liveWatch {
	w := &liveWatch{maxSilence: time.Duration(bg.MaxSilenceS * float64(time.Second)), want: map[string]bool{}, streams: map[string]*StreamLiveness{}}
	for _, s := range bg.Systems {
		w.want[s] = true
	}
	return w
}

// frame sees one received frame (runner.Options.OnFrame).
func (w *liveWatch) frame(f observe.Frame) {
	if !w.want[f.System] {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	l := w.streams[f.Stream]
	if l == nil {
		l = &StreamLiveness{System: f.System, First: f.At, Last: f.At}
		w.streams[f.Stream] = l
	}
	l.Frames++
	if f.At.Sub(l.Last) > w.maxSilence {
		if len(l.Silences) < maxSilences {
			l.Silences = append(l.Silences, silence{From: l.Last, To: f.At})
		} else {
			l.Dropped++
		}
	}
	if f.At.After(l.Last) {
		l.Last = f.At
	}
	if rereads(f.Schema) && len(l.Silences) > 0 {
		g := &l.Silences[len(l.Silences)-1]
		if d := f.At.Sub(g.To); d >= 0 && d <= w.maxSilence {
			g.Reread = true
		}
	}
}

// rereads is a frame that carries a system's open alerts.
func rereads(schema string) bool {
	return schema == wire.SchemaSnapshot || schema == wire.SchemaAlert || schema == wire.SchemaViolation
}

// all is a copy of every stream's record.
func (w *liveWatch) all() map[string]StreamLiveness {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]StreamLiveness, len(w.streams))
	for k, v := range w.streams {
		c := *v
		c.Silences = append([]silence(nil), v.Silences...)
		out[k] = c
	}
	return out
}

// judgeLiveness reads the background streams against the rows' windows,
// from the first row's injection to outStart (the aircraft leaving the
// zone; the exit is judged on the clear it must see):
//   - a background system with no stream, or a stream with no frame, is
//     silent through every window;
//   - a silence longer than background.max_silence_s that overlaps a
//     window by more than that is the row's finding, allowed only when
//     the row names the system in streams_down (the fault takes its
//     console stream down), the silence lies inside the window, and the
//     stream came back re-sending the open alerts (Reread);
//   - one between rows is a finding of no row;
//   - a stream that never came back is silent to the end of the run.
func judgeLiveness(streams map[string]StreamLiveness, bg Background, outStart, end time.Time, windows []window) []AlertFinding {
	if len(windows) == 0 {
		return nil
	}
	maxSilence := time.Duration(bg.MaxSilenceS * float64(time.Second))
	var out []AlertFinding
	names := make([]string, 0, len(streams))
	for n := range streams {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, sys := range bg.Systems {
		var mine []string
		for _, n := range names {
			if streams[n].System == sys && streams[n].Frames > 0 {
				mine = append(mine, n)
			}
		}
		if len(mine) == 0 {
			for _, w := range windows {
				out = append(out, AlertFinding{System: sys, What: "stream_silent", At: w.from, Row: w.row,
					Reason: "no frame from any console stream of the system: nothing it said about the alert was heard"})
			}
			continue
		}
		for _, n := range mine {
			l := streams[n]
			gaps := append([]silence(nil), l.Silences...)
			if l.First.After(windows[0].from) {
				gaps = append([]silence{{From: windows[0].from, To: l.First}}, gaps...)
			}
			if end.Sub(l.Last) > maxSilence {
				gaps = append(gaps, silence{From: l.Last, To: end})
			}
			if l.Dropped > 0 {
				out = append(out, AlertFinding{System: sys, What: "stream_silent", At: l.Last,
					Reason: fmt.Sprintf("stream %s: %d silences past the %d kept were not judged", n, l.Dropped, maxSilences)})
			}
			for _, g := range gaps {
				if g.To.Sub(g.From) <= maxSilence || !g.To.After(windows[0].from) || !g.From.Before(outStart) {
					continue
				}
				placed := false
				for _, w := range windows {
					from, to := later(g.From, w.from), earlier(g.To, w.to)
					if to.Sub(from) <= maxSilence {
						continue
					}
					placed = true
					f := AlertFinding{System: sys, What: "stream_silent", At: g.From, Row: w.row,
						OffsetS: round1(g.From.Sub(w.from).Seconds())}
					inside := !g.From.Before(w.from) && !g.To.After(w.to)
					switch {
					case !slices.Contains(w.streamsDown, sys):
						f.Reason = fmt.Sprintf("stream %s silent %.1f s (the row does not take it down): a kept alert over it is unproven", n, g.To.Sub(g.From).Seconds())
					case !inside:
						f.Reason = fmt.Sprintf("stream %s silent %.1f s, past the row's window", n, g.To.Sub(g.From).Seconds())
					case !g.Reread:
						f.Reason = fmt.Sprintf("stream %s silent %.1f s and back without re-sending the open alerts: the alert after it was not re-read", n, g.To.Sub(g.From).Seconds())
					default:
						f.What, f.Allowed = "stream_down", true
						f.Reason = fmt.Sprintf("stream %s down %.1f s with the fault, back with the open alerts re-sent", n, g.To.Sub(g.From).Seconds())
					}
					out = append(out, f)
				}
				if !placed {
					out = append(out, AlertFinding{System: sys, What: "stream_silent", At: g.From,
						Reason: fmt.Sprintf("stream %s silent %.1f s between rows", n, g.To.Sub(g.From).Seconds())})
				}
			}
		}
	}
	return out
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

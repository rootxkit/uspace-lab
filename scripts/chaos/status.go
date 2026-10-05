package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

// maxStatusFrames bounds the console/status/v1 frames kept per stream;
// past it the oldest are dropped and counted (bounded everything).
const maxStatusFrames = 20000

// StatusFrame is one console/status/v1 a system's console stream sent,
// with the members the chaos assertions read (05 §6 "with age shown"):
// degraded[], the per-source states, and the ages and states a system
// adds (Appendix C extras). Members a system does not send stay absent.
type StatusFrame struct {
	At       time.Time          `json:"at"`
	Degraded []string           `json:"degraded"`
	Sources  map[string]string  `json:"sources,omitempty"` // "source/instance" ("*" for the type) -> state
	Ages     map[string]float64 `json:"ages,omitempty"`    // projection_age_s, cis_age_s, ...
	NATS     string             `json:"nats,omitempty"`
	DPState  string             `json:"dp_state,omitempty"`
	Dropped  *float64           `json:"dropped_frames,omitempty"`
}

// StreamEvent is a console stream connecting, failing or closing.
type StreamEvent struct {
	At    time.Time `json:"at"`
	What  string    `json:"what"` // connected, failed, closed
	Error string    `json:"error,omitempty"`
}

// StatusLog is what one system's console stream said.
type StatusLog struct {
	mu      sync.Mutex
	System  string
	Frames  []StatusFrame
	Events  []StreamEvent
	Dropped uint64
	// LastRaw is the last status body as sent, bounded (chaos watch --raw).
	LastRaw []byte
}

func (s *StatusLog) addFrame(f StatusFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Frames) >= maxStatusFrames {
		s.Frames = s.Frames[1:]
		s.Dropped++
	}
	s.Frames = append(s.Frames, f)
}

func (s *StatusLog) addEvent(e StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Events) >= maxStatusFrames {
		s.Events = s.Events[1:]
	}
	s.Events = append(s.Events, e)
}

// Between returns the frames and events in [from, to).
func (s *StatusLog) Between(from, to time.Time) ([]StatusFrame, []StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var fs []StatusFrame
	for _, f := range s.Frames {
		if !f.At.Before(from) && f.At.Before(to) {
			fs = append(fs, f)
		}
	}
	var es []StreamEvent
	for _, e := range s.Events {
		if !e.At.Before(from) && e.At.Before(to) {
			es = append(es, e)
		}
	}
	return fs, es
}

// parseStatus reads a console/status/v1 body.
func parseStatus(body json.RawMessage, at time.Time) (StatusFrame, error) {
	var b map[string]json.RawMessage
	if err := json.Unmarshal(body, &b); err != nil {
		return StatusFrame{}, fmt.Errorf("console/status/v1 body: %w", err)
	}
	f := StatusFrame{At: at, Degraded: []string{}}
	if d, ok := b["degraded"]; ok {
		_ = json.Unmarshal(d, &f.Degraded)
	}
	sort.Strings(f.Degraded)
	if s, ok := b["sources"]; ok {
		// source/status/v1 entries: source (the type) and
		// source_instance (null for the type as a whole, keyed "*").
		var srcs []struct {
			Source   string  `json:"source"`
			Instance *string `json:"source_instance"`
			State    string  `json:"state"`
		}
		if json.Unmarshal(s, &srcs) == nil && len(srcs) > 0 {
			f.Sources = map[string]string{}
			for _, x := range srcs {
				inst := "*"
				if x.Instance != nil && *x.Instance != "" {
					inst = *x.Instance
				}
				f.Sources[x.Source+"/"+inst] = x.State
			}
		}
	}
	for k, v := range b {
		if !strings.HasSuffix(k, "_age_s") {
			continue
		}
		var n float64
		if json.Unmarshal(v, &n) == nil {
			if f.Ages == nil {
				f.Ages = map[string]float64{}
			}
			f.Ages[k] = n
		}
	}
	if n, ok := b["nats"]; ok {
		var s string
		var o struct {
			State string `json:"state"`
		}
		if json.Unmarshal(n, &s) == nil {
			f.NATS = s
		} else if json.Unmarshal(n, &o) == nil {
			f.NATS = o.State
		}
	}
	if d, ok := b["dp_state"]; ok {
		_ = json.Unmarshal(d, &f.DPState)
	}
	if d, ok := b["dropped_frames"]; ok {
		var n float64
		if json.Unmarshal(d, &n) == nil {
			f.Dropped = &n
		}
	}
	return f, nil
}

// statusStream is one console stream to read.
type statusStream struct {
	system string
	url    string
	// header is built on every (re)connection: a token may have expired.
	header func(ctx context.Context) (http.Header, error)
	onOpen []byte
}

// statusStreams are the console streams of the targets file the harness
// can read without a scenario: the authority's picture (the console
// session the seed opened) and the ANSP's manned-traffic stream (the
// lab-01 client). The USSP's console streams are per flight and the
// background run reads them.
func statusStreams(targetsPath string, lab *scenario.Lab, hc *http.Client) ([]statusStream, error) {
	tg, err := runner.LoadTargets(targetsPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(targetsPath)
	secret := func(p string) (string, error) {
		if p == "" {
			return "", fmt.Errorf("chaos: %s names no secret file", targetsPath)
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		b, err := os.ReadFile(p) //nolint:gosec // a secret file the operator's targets file names
		if err != nil {
			return "", fmt.Errorf("chaos: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	var out []statusStream
	if a := tg.Authority; a != nil && a.PictureURL != "" {
		sw := lab.At(scenario.Offset{NorthM: -10000, EastM: -10000})
		ne := lab.At(scenario.Offset{NorthM: 10000, EastM: 10000})
		sub, _ := json.Marshal(map[string]any{"schema": wire.SchemaSubscribe, "body": map[string]any{
			"bbox": []float64{sw.LonDeg, sw.LatDeg, ne.LonDeg, ne.LatDeg}, "layers": []string{"tracks", "manned", "alerts"},
		}})
		sessionFile, origin := a.SessionFile, a.Origin
		out = append(out, statusStream{system: scenario.SystemAuthority, url: a.PictureURL, onOpen: sub,
			header: func(context.Context) (http.Header, error) {
				s, err := secret(sessionFile)
				if err != nil {
					return nil, err
				}
				return http.Header{"Cookie": {"uspace_session=" + s}, "Origin": {origin}}, nil
			}})
	}
	if a := tg.ANSP; a != nil && a.StreamURL != "" {
		tc := a.Token
		out = append(out, statusStream{system: scenario.SystemANSP, url: a.StreamURL,
			header: func(ctx context.Context) (http.Header, error) {
				s, err := secret(tc.SecretFile)
				if err != nil {
					return nil, err
				}
				cc := &oauth.ClientCredentials{TokenURL: tc.TokenURL, ClientID: tc.ClientID, ClientSecret: s, Audience: tc.Audience, Scopes: tc.Scopes, HTTP: hc}
				tok, err := cc.Token(ctx)
				if err != nil {
					return nil, err
				}
				return http.Header{"Authorization": {"Bearer " + tok}}, nil
			}})
	}
	return out, nil
}

// run reads the stream until ctx ends, reconnecting a second after every
// break; every connection, failure and close is recorded with its time,
// because a console stream that stops answering is itself an observation.
func (s statusStream) run(ctx context.Context, hc *http.Client, log *StatusLog) {
	for ctx.Err() == nil {
		err := s.once(ctx, hc, log)
		if ctx.Err() != nil {
			return
		}
		e := StreamEvent{At: time.Now().UTC(), What: "closed"}
		if err != nil {
			e.What, e.Error = "failed", shortErr(err)
		}
		log.addEvent(e)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (s statusStream) once(ctx context.Context, hc *http.Client, log *StatusLog) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h, err := s.header(dctx)
	if err != nil {
		return err
	}
	conn, resp, err := websocket.Dial(dctx, s.url, &websocket.DialOptions{HTTPClient: hc, HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return fmt.Errorf("upgrade refused with %d", resp.StatusCode)
		}
		return err
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(wire.MaxFrameBytes)
	log.addEvent(StreamEvent{At: time.Now().UTC(), What: "connected"})
	if len(s.onOpen) > 0 {
		if err := conn.Write(ctx, websocket.MessageText, s.onOpen); err != nil {
			return fmt.Errorf("subscribe: %w", err)
		}
	}
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		at := time.Now().UTC()
		e, err := wire.ParseEnvelope(b)
		if err != nil || e.Schema != wire.SchemaStatus {
			continue
		}
		f, err := parseStatus(e.Body, at)
		if err != nil {
			continue
		}
		log.addFrame(f)
		log.mu.Lock()
		log.LastRaw = append(log.LastRaw[:0], e.Body[:min(len(e.Body), 8192)]...)
		log.mu.Unlock()
	}
}

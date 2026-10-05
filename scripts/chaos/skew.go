package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simrx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// skewTolerance is how far the measured offset of a sent batch may be
// from the configured one for the fault to count as injected: the send
// itself takes a few milliseconds.
const skewTolerance = time.Second

// SkewResult is one clock case: what left (sent_at_ms against this
// host's clock at the moment of sending, read from the request itself)
// and what the authority answered.
type SkewResult struct {
	SkewS    float64 `json:"skew_s"`
	Want     string  `json:"want"`
	Measured float64 `json:"measured_skew_s"`
	// Injected is the batch seen leaving with the configured offset.
	Injected bool   `json:"injected"`
	Code     int    `json:"code"`
	Answer   string `json:"answer,omitempty"`
	Got      string `json:"got"` // accepted, refused, refused_other, unavailable, none
	Pass     bool   `json:"pass"`
	Note     string `json:"note,omitempty"`
}

// capture is an http.RoundTripper that records the first rid/observation
// batch it carries: its sent_at_ms, this host's clock when it left, and
// the answer. Never the bearer key or the signature (secrets never
// printed).
type capture struct {
	next http.RoundTripper
	mu   sync.Mutex
	done chan struct{}
	once sync.Once

	sentAtMS int64
	wallMS   int64
	code     int
	answer   string
	err      string
}

func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, simrx.MaxBatchBytes+1))
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	wall := time.Now().UnixMilli()
	resp, err := c.next.RoundTrip(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return resp, err // only the first batch is the case's
	default:
	}
	var sent struct {
		SentAtMS int64 `json:"sent_at_ms"`
	}
	_ = json.Unmarshal(body, &sent)
	c.sentAtMS, c.wallMS = sent.SentAtMS, wall
	if err != nil {
		c.err = shortErr(err)
	} else {
		c.code = resp.StatusCode
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(rb))
		c.answer = bounded(strings.TrimSpace(string(rb)))
	}
	c.once.Do(func() { close(c.done) })
	return resp, err
}

// skewRig is what every clock case needs from the targets file.
type skewRig struct {
	baseURL    string
	receiverID string
	bearer     string
	hmacKey    []byte
	position   core.LatLon
	home       vehicle.Home
	geoidSpec  string
}

// newSkewRig reads the receiver's keys and position (the background
// scenario's receiver, at its place) from the targets file.
func newSkewRig(targetsPath string, lab *scenario.Lab, bgScenario string) (*skewRig, error) {
	tg, err := runner.LoadTargets(targetsPath)
	if err != nil {
		return nil, err
	}
	if tg.Authority == nil || tg.Authority.BaseURL == "" {
		return nil, fmt.Errorf("chaos: %s has no authority", targetsPath)
	}
	sc, err := scenario.Load(bgScenario)
	if err != nil {
		return nil, err
	}
	if len(sc.Receivers) == 0 {
		return nil, fmt.Errorf("chaos: %s has no receiver for the clock row", bgScenario)
	}
	rx := sc.Receivers[0]
	keys, ok := tg.Authority.Receivers[rx.ID]
	if !ok {
		return nil, fmt.Errorf("chaos: %s has no keys for receiver %s", targetsPath, rx.ID)
	}
	read := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(targetsPath), p)
		}
		b, err := os.ReadFile(p) //nolint:gosec // a secret file the operator's targets file names
		if err != nil {
			return "", fmt.Errorf("chaos: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	bearer, err := read(keys.BearerKeyFile)
	if err != nil {
		return nil, err
	}
	hk, err := read(keys.HMACKeyFile)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(hk)
	if err != nil {
		return nil, fmt.Errorf("chaos: the HMAC key file of %s is not hex", rx.ID)
	}
	return &skewRig{baseURL: tg.Authority.BaseURL, receiverID: rx.ID, bearer: bearer, hmacKey: key,
		position: lab.At(rx.At), home: lab.Origin, geoidSpec: tg.Geoid}, nil
}

// skewedClock is the receiver's clock for a case: this host's, offset.
func skewedClock(offset time.Duration) func() time.Time {
	return func() time.Time { return time.Now().Add(offset) }
}

// runSkew sends one batch from a receiver whose clock is s.SkewS seconds
// off this host's (clock makes that clock from the offset), from a
// transmitter on the ground at the receiver (no serial, so it is no
// registered aircraft), and judges the answer. The offset is measured
// on the batch as it left, so a clock that was not skewed cannot pass.
func runSkew(ctx context.Context, rig *skewRig, base http.RoundTripper, s Skew, clock func(time.Duration) func() time.Time) SkewResult {
	res := SkewResult{SkewS: s.SkewS, Want: s.Want, Got: "none"}
	g, _, err := geoidx.Load(rig.geoidSpec)
	if err != nil {
		res.Note = "geoid: " + err.Error()
		return res
	}
	offset := time.Duration(s.SkewS * float64(time.Second))
	cp := &capture{next: base, done: make(chan struct{})}
	mac := macFor(fmt.Sprintf("chaos-clock/%g", s.SkewS))
	rec, err := simrx.New(simrx.Config{BaseURL: rig.baseURL, ReceiverID: rig.receiverID, BearerKey: rig.bearer, HMACKey: rig.hmacKey,
		Geoid: g, Position: &rig.position, BatchPeriod: 200 * time.Millisecond, BacklogMax: 64,
		Transmitters: []simrx.Transmitter{{Sysid: 250, MAC: mac}},
		HTTP:         &http.Client{Transport: cp, Timeout: 10 * time.Second},
		Now:          clock(offset)})
	if err != nil {
		res.Note = err.Error()
		return res
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	in := make(chan vehicle.Sample, 1)
	ts := clock(offset)().UTC().Format(vehicle.TimeLayout)
	in <- vehicle.Sample{Schema: vehicle.Schema, Sysid: 250, TS: &ts, LatDeg: rig.position.LatDeg, LonDeg: rig.position.LonDeg,
		AltAMSLM: rig.home.AltAMSLM, Status: vehicle.StatusGround, FixOK: true}
	go func() { _ = rec.Run(rctx, in) }()
	select {
	case <-cp.done:
	case <-rctx.Done():
		res.Note = "no batch left within 15 s: " + rec.LastError()
		return res
	}
	cancel()
	cp.mu.Lock()
	defer cp.mu.Unlock()
	res.Measured = round1(float64(cp.sentAtMS-cp.wallMS) / 1000)
	res.Injected = cp.sentAtMS != 0 && math.Abs(float64(cp.sentAtMS-cp.wallMS)-float64(offset.Milliseconds())) <= float64(skewTolerance.Milliseconds())
	res.Code, res.Answer = cp.code, cp.answer
	switch {
	case cp.err != "":
		res.Got, res.Note = "unavailable", cp.err
	case cp.code == http.StatusAccepted:
		res.Got = skewAccepted
	case cp.code >= 400 && cp.code < 500:
		// Only the authority's skew problem is the refusal the case is
		// about: a 4xx for anything else (a bearer, a signature) is
		// another refusal and proves nothing about the clock rule.
		if pt := problemType(cp.answer); s.Problem != "" && pt == s.Problem {
			res.Got = skewRefused
		} else {
			res.Got = skewRefusedOther
			res.Note = fmt.Sprintf("refused with problem type %q, not %q", pt, s.Problem)
		}
	default:
		res.Got = "unavailable"
	}
	res.Pass = res.Injected && res.Got == s.Want
	if !res.Injected {
		res.Note = strings.TrimSpace(res.Note + fmt.Sprintf(" the batch left %.1f s off this host's clock, not %g s: the fault did not happen", res.Measured, s.SkewS))
	}
	return res
}

// skewRefusedOther is a 4xx that is not the skew problem.
const skewRefusedOther = "refused_other"

// problemType is the type member of a problem body, "" when the answer
// is not one.
func problemType(answer string) string {
	var p struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(answer), &p) != nil {
		return ""
	}
	return p.Type
}

func macFor(seed string) string {
	h := sha256.Sum256([]byte(seed))
	h[0] = 0x02
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", h[0], h[1], h[2], h[3], h[4], h[5])
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// ingest is the authority's rule from 06 T2: a body more than 30 s from
// the ingest clock is refused, anything else accepted.
func ingest(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			SentAtMS     int64 `json:"sent_at_ms"`
			Observations []any `json:"observations"`
		}
		body, _ := io.ReadAll(r.Body)
		if json.Unmarshal(body, &b) != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if d := time.Since(time.UnixMilli(b.SentAtMS)); d > 30*time.Second || d < -30*time.Second {
			http.Error(w, `{"type":"https://schemas.uspace.ge/problems/skew"}`, http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accepted":`+strconv.Itoa(len(b.Observations))+`,"duplicates":0}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rig(url string) *skewRig {
	return &skewRig{baseURL: url, receiverID: "rx", bearer: "b", hmacKey: bytes.Repeat([]byte{7}, 32),
		position: core.LatLon{LatDeg: 41.7, LonDeg: 44.8}, home: vehicle.Home{LatDeg: 41.7, LonDeg: 44.8, AltAMSLM: 500}, geoidSpec: "constant:0"}
}

func TestClockSkewBothWays(t *testing.T) {
	srv := ingest(t)
	in := runSkew(context.Background(), rig(srv.URL), http.DefaultTransport, Skew{SkewS: -10, Want: skewAccepted}, skewedClock)
	if !in.Pass || !in.Injected || in.Got != skewAccepted || in.Measured > -9 || in.Measured < -11 {
		t.Fatalf("inside the window: %+v", in)
	}
	out := runSkew(context.Background(), rig(srv.URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, skewedClock)
	if !out.Pass || !out.Injected || out.Got != skewRefused || out.Code != http.StatusUnauthorized {
		t.Fatalf("outside the window: %+v", out)
	}
	// The same case judged with the wrong expectation fails.
	wrong := runSkew(context.Background(), rig(srv.URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewAccepted}, skewedClock)
	if wrong.Pass {
		t.Fatalf("%+v", wrong)
	}
}

// unskewed is a receiver clock that ignores the configured offset: the
// fault was asked for and never happened.
func unskewed(time.Duration) func() time.Time { return time.Now }

func TestASkewThatNeverLeftFailsWhateverTheAnswer(t *testing.T) {
	// Accepted, for a case that wants refused: fails twice over.
	res := runSkew(context.Background(), rig(ingest(t).URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, unskewed)
	if res.Injected || res.Pass || res.Got != skewAccepted {
		t.Fatalf("an unskewed batch counted: %+v", res)
	}
	// A server that refuses everything gives the answer the case wants;
	// the fault still did not happen, so the case fails.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"`+skewProblem+`"}`, http.StatusUnauthorized)
	}))
	defer refusing.Close()
	res = runSkew(context.Background(), rig(refusing.URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, unskewed)
	if res.Got != skewRefused || res.Injected || res.Pass {
		t.Fatalf("a refusal without the fault passed: %+v", res)
	}
}

// skewProblem is the authority's problem type for a skewed body
// (uspace-authority internal/receivers/protocol.go SlugSkew).
const skewProblem = "https://schemas.uspace.ge/problems/skew"

// A refusal counts only when it is the skew refusal: a skewed batch the
// authority refuses for another reason (a bad bearer, a bad signature)
// says nothing about its clock rule.
func TestOnlyTheSkewProblemIsARefusal(t *testing.T) {
	for name, body := range map[string]string{
		"another problem": `{"type":"https://schemas.uspace.ge/problems/unauthorized"}`,
		"no problem body": "refused",
	} {
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, body, http.StatusUnauthorized)
		}))
		res := runSkew(context.Background(), rig(other.URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, skewedClock)
		other.Close()
		if res.Pass || res.Got == skewRefused || !res.Injected {
			t.Errorf("%s: a 401 that is not the skew problem counted: %+v", name, res)
		}
	}
	res := runSkew(context.Background(), rig(ingest(t).URL), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, skewedClock)
	if !res.Pass || res.Got != skewRefused {
		t.Fatalf("the skew problem did not count: %+v", res)
	}
}

func TestASkewCaseWithNoAnswerIsNotAVerdict(t *testing.T) {
	srv := ingest(t)
	url := srv.URL
	srv.Close()
	res := runSkew(context.Background(), rig(url), http.DefaultTransport, Skew{SkewS: 45, Want: skewRefused, Problem: skewProblem}, skewedClock)
	if res.Pass || res.Got == skewRefused {
		t.Fatalf("an unreachable authority counted as a refusal: %+v", res)
	}
}

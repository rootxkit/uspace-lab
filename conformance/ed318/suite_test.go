package ed318

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	coreed318 "github.com/rootxkit/uspace-core/ed318"

	"github.com/rootxkit/uspace-lab/conformance/national"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

var area = FixtureArea{CentreLatDeg: 41.7151, CentreLonDeg: 44.8271, HalfSideM: 300, UpperAGLM: 120, UpperAMSLFt: 2500}

// TestFixtureIsED318: the publication fixture is read by uspace-core's
// strict ED-318 reader and builds as zones, and the invalid one is
// refused naming the removed field.
func TestFixtureIsED318(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	b, err := Fixture(area, now, "A123456", "E123456")
	if err != nil {
		t.Fatal(err)
	}
	fc, probs := coreed318.Parse(b, coreed318.Limits{})
	if probs != nil && len(probs.List) > 0 {
		t.Fatalf("fixture refused: %v", probs)
	}
	if _, err := coreed318.ToZones(fc, coreed318.NOAADaylight{}); err != nil {
		t.Fatalf("fixture does not build as zones: %v", err)
	}
	for i, want := range []bool{true, false} {
		f := fc.Features[i]
		got, err := coreed318.Applies(f.Properties.LimitedApplicability, now, centroid(f.Geometry), coreed318.NOAADaylight{})
		if err != nil || got != want {
			t.Errorf("feature %d applies %v (%v), want %v", i, got, err, want)
		}
	}
	bad, err := InvalidFixture(area, now, "A123456", "E123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, probs := coreed318.Parse(bad, coreed318.Limits{}); probs == nil || !strings.Contains(probs.Error(), "features[0].properties.type") {
		t.Errorf("the invalid fixture is not refused at features[0].properties.type: %v", probs)
	}
}

// cispFaults are the CISP double's deliberate defects.
type cispFaults struct {
	noMark           bool // applies_at does not annotate
	keepAll          bool // at= drops nothing
	ignoreNoneMatch  bool // 200 instead of 304
	acceptStale      bool // a stale If-Match is applied
	acceptInvalid    bool // an invalid body is stored
	webhookDelay     time.Duration
	wrongWebhookKey  bool // deliveries signed by a key the JWKS does not hold
	staleAfterBeat   bool // the publisher is stale right after a heartbeat
	noChangeRecorded bool // the change feed never lists the publication
}

type cispDouble struct {
	t      *testing.T
	f      cispFaults
	srv    *httptest.Server
	issuer string
	key    *rsa.PrivateKey
	other  *rsa.PrivateKey
	jwks   []byte

	mu       sync.Mutex
	version  int64
	body     []byte
	changes  []map[string]any
	subs     map[string]*sub
	lastBeat map[string]time.Time
}

type sub struct {
	callback string
	status   string
}

func newCISPDouble(t *testing.T, f cispFaults) *cispDouble {
	t.Helper()
	d := &cispDouble{t: t, f: f, subs: map[string]*sub{}, lastBeat: map[string]time.Time{}}
	var err error
	if d.key, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if d.other, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(d.jwks)
	})
	mux.HandleFunc("GET /v1/zones", d.getZones)
	mux.HandleFunc("GET /v1/zones/versions", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		var vs []map[string]int64
		for v := int64(1); v <= d.version; v++ {
			vs = append(vs, map[string]int64{"version": v})
		}
		writeJSON(w, 200, "application/json", map[string]any{"versions": vs})
	})
	mux.HandleFunc("GET /v1/uspace_airspace", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, 404, "no_version", nil)
	})
	mux.HandleFunc("PUT /v1/publications/zones", d.put)
	mux.HandleFunc("GET /v1/changes", d.getChanges)
	mux.HandleFunc("POST /v1/subscriptions", d.subscribe)
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		s, ok := d.subs[r.PathValue("id")]
		st := ""
		if ok {
			st = s.status
		}
		d.mu.Unlock()
		if !ok {
			problem(w, 404, "not_found", nil)
			return
		}
		writeJSON(w, 200, "application/json", map[string]string{"id": r.PathValue("id"), "status": st})
	})
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		delete(d.subs, r.PathValue("id"))
		d.mu.Unlock()
		writeJSON(w, 200, "application/json", map[string]string{"id": r.PathValue("id"), "status": "deleted"})
	})
	mux.HandleFunc("POST /v1/publishers/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.lastBeat[tokenSubject(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))] = time.Now().UTC()
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/status", d.status)
	d.srv = httptest.NewServer(mux)
	t.Cleanup(d.srv.Close)
	d.issuer = d.srv.URL
	iss, err := auth.NewIssuer(d.issuer, d.key, "cisp-test")
	if err != nil {
		t.Fatal(err)
	}
	if d.jwks, err = json.Marshal(iss.JWKS()); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeJSON(w http.ResponseWriter, status int, ct string, v any) {
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, status int, slug string, errs []map[string]string) {
	if errs == nil {
		errs = []map[string]string{}
	}
	writeJSON(w, status, "application/problem+json", map[string]any{
		"type": "https://schemas.uspace.ge/problems/" + slug, "title": slug, "status": status, "errors": errs})
}

func (d *cispDouble) etag() string { return fmt.Sprintf(`"zones:%d"`, d.version) }

func (d *cispDouble) getZones(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	version, body, etag := d.version, d.body, d.etag()
	d.mu.Unlock()
	if version == 0 {
		problem(w, 404, "no_version", nil)
		return
	}
	q := r.URL.Query()
	at, appliesAt := q.Get("at"), q.Get("applies_at")
	if at != "" && appliesAt != "" {
		problem(w, 400, "filter_conflict", []map[string]string{{"field": "applies_at", "reason": "with at"}})
		return
	}
	if at == "" && appliesAt == "" {
		if r.Header.Get("If-None-Match") == etag && !d.f.ignoreNoneMatch {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("X-CIS-Signature", "eyJhbGciOiJSUzI1NiJ9..sig")
		w.Header().Set("Content-Type", "application/geo+json")
		_, _ = w.Write(body)
		return
	}
	when := at + appliesAt
	instant, err := time.Parse(time.RFC3339, when)
	if err != nil || !strings.ContainsAny(when[10:], "Z+-") {
		problem(w, 400, "invalid_request", []map[string]string{{"field": "at", "reason": "RFC 3339 with an offset"}})
		return
	}
	fc, _ := coreed318.Parse(body, coreed318.Limits{})
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	feats, _ := doc["features"].([]any)
	var kept []any
	for i := range fc.Features {
		f := &fc.Features[i]
		applies, aerr := coreed318.Applies(f.Properties.LimitedApplicability, instant, centroid(f.Geometry), coreed318.NOAADaylight{})
		mark := "not_applicable"
		switch {
		case aerr != nil:
			mark = "unknown"
		case applies:
			mark = "applies"
		}
		fm := feats[i].(map[string]any)
		props := fm["properties"].(map[string]any)
		if appliesAt != "" {
			if !d.f.noMark {
				props["extendedProperties"] = map[string]any{"cis_applicability": mark}
			}
			kept = append(kept, fm)
			continue
		}
		if mark == "not_applicable" && !d.f.keepAll {
			continue
		}
		if mark == "unknown" {
			props["extendedProperties"] = map[string]any{"cis_applicability": mark}
		}
		kept = append(kept, fm)
	}
	doc["features"] = kept
	filter := "at"
	if appliesAt != "" {
		filter = "applies_at"
	}
	w.Header().Set("X-CIS-Filtered", filter)
	w.Header().Set("ETag", etag)
	writeJSON(w, 200, "application/geo+json", doc)
}

func (d *cispDouble) put(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-JWS-Signature") == "" {
		problem(w, 403, "signature", nil)
		return
	}
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	d.mu.Lock()
	defer d.mu.Unlock()
	im := r.Header.Get("If-Match")
	if im == "" {
		problem(w, 428, "precondition_required", nil)
		return
	}
	if im != d.etag() && !d.f.acceptStale {
		w.Header().Set("ETag", d.etag())
		problem(w, 412, "precondition_failed", nil)
		return
	}
	if _, probs := coreed318.Parse(b, coreed318.Limits{}); probs != nil && len(probs.List) > 0 && !d.f.acceptInvalid {
		var errs []map[string]string
		for _, p := range probs.List {
			errs = append(errs, map[string]string{"field": p.Field, "reason": p.Reason})
		}
		problem(w, 400, "invalid_publication", errs)
		return
	}
	d.version++
	d.body = b
	cursor := len(d.changes) + 1
	change := map[string]any{"schema": "cis/change/v1", "msg_id": strconv.Itoa(cursor), "dataset": "zones", "version": d.version, "reason": "publication"}
	if !d.f.noChangeRecorded {
		d.changes = append(d.changes, change)
	}
	for id, s := range d.subs {
		if s.status == "active" {
			go d.deliver(id, s.callback, change, d.f.webhookDelay)
		}
	}
	w.Header().Set("ETag", d.etag())
	writeJSON(w, 201, "application/json", map[string]any{"dataset": "zones", "version": d.version, "etag": d.etag()})
}

func (d *cispDouble) getChanges(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []map[string]any{}
	next := since
	for i := since; i < len(d.changes); i++ {
		out = append(out, d.changes[i])
		next = i + 1
	}
	writeJSON(w, 200, "application/json", map[string]any{"changes": out, "next": next})
}

func (d *cispDouble) subscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallbackURL string `json:"callback_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id := newID("S")
	d.mu.Lock()
	d.subs[id] = &sub{callback: req.CallbackURL, status: "pending_verification"}
	d.mu.Unlock()
	go func() {
		ping := map[string]any{"schema": "cis/change/v1", "msg_id": "01K6P0A1B2C3D4E5F6G7H8J9KM", "dataset": "zones", "version": 0, "reason": "subscription_test"}
		if d.deliver(id, req.CallbackURL, ping, 0) {
			d.mu.Lock()
			if s, ok := d.subs[id]; ok {
				s.status = "active"
			}
			d.mu.Unlock()
		}
	}()
	writeJSON(w, 201, "application/json", map[string]string{"id": id, "status": "pending_verification"})
}

func (d *cispDouble) deliver(subID, callback string, change map[string]any, delay time.Duration) bool {
	if delay > 0 {
		time.Sleep(delay)
	}
	key := d.key
	if d.f.wrongWebhookKey {
		key = d.other
	}
	body, _ := json.Marshal(change)
	u, _ := url.Parse(callback)
	tok, err := auth.SignCompact(auth.SigningKey{KID: "cisp-test", Key: key},
		auth.CompactClaims{Issuer: d.issuer, Audience: u.Hostname(), Subject: subID, JTI: newID("D")}, body, time.Now())
	if err != nil {
		d.t.Errorf("sign: %v", err)
		return false
	}
	resp, err := http.Post(callback, "application/jose", bytes.NewReader([]byte(tok)))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

func (d *cispDouble) status(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	pubs := []map[string]any{{"client_id": "authority-01", "stale_after_s": 60, "stale": true}}
	for id, at := range d.lastBeat {
		stale := now.Sub(at) > 60*time.Second || d.f.staleAfterBeat
		pubs = append(pubs, map[string]any{"client_id": id, "last_heartbeat_at": at.Format(time.RFC3339Nano), "stale_after_s": 60, "stale": stale})
	}
	writeJSON(w, 200, "application/json", map[string]any{"now": now.Format(time.RFC3339Nano), "publishers": pubs})
}

// fakeJWT is a JWT-shaped token naming sub: the double does not verify.
func fakeJWT(sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(`{"sub":"`+sub+`"}`)) + ".x"
}

func suiteFor(t *testing.T, d *cispDouble, notify time.Duration) *Suite {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cisp-fixture.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := national.LoadContract("cisp", raw, national.Overrides{System: "cisp", Unsecured: "token", Unstated: []string{
		"getDataset", "listDatasetVersions", "putPublication", "listChanges", "getStatus"}}, national.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prob, err := national.CompileProblem(filepath.Join("..", "..", "schemas", "common"))
	if err != nil {
		t.Fatal(err)
	}
	authKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	s, err := New(Config{
		Contract: c, BaseURL: d.srv.URL, Problem: prob,
		ReaderToken: fakeJWT("lab-01"), AuthorityToken: fakeJWT("authority-01"),
		AuthorityKey: &auth.SigningKey{KID: "authority-test", Key: authKey}, Publish: true,
		ANSPToken: fakeJWT("ansp-01"), JWKSURL: d.srv.URL + "/.well-known/jwks.json", IssuerURL: d.issuer,
		WebhookListen: addr, WebhookURL: "http://" + addr + "/v1/cis/notifications",
		Area: area, ChangeNotification: notify, StaleTolerance: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fold(out []result.Outcome) map[string]result.Status {
	by := map[string][]result.Outcome{}
	for _, o := range out {
		by[o.Requirement] = append(by[o.Requirement], o)
	}
	st := map[string]result.Status{}
	for r, os := range by {
		st[r], _ = result.Fold(os)
	}
	return st
}

func dump(t *testing.T, out []result.Outcome) {
	t.Helper()
	for _, o := range out {
		t.Logf("%-20s %-18s %-14s %3d %-40s %s | %s", o.Requirement, o.Check, o.Status, o.HTTPStatus, o.Subject, o.Reason, o.Detail)
	}
}

var allReqs = []string{ReqSchema, ReqVertical, ReqApplicability, ReqVersioning, ReqChanges, ReqWebhook, ReqHeartbeat}

// TestSuitePassesCorrectDouble is the branch that says nothing is
// wrong (E-02): every requirement passes against a correct CISP, the
// first read finding no dataset and the fixture publication making one.
func TestSuitePassesCorrectDouble(t *testing.T) {
	d := newCISPDouble(t, cispFaults{})
	s := suiteFor(t, d, time.Second)
	out := s.Run(context.Background())
	st := fold(out)
	for _, r := range allReqs {
		if st[r] != result.Pass {
			dump(t, out)
			t.Errorf("%s: %s, want pass", r, st[r])
		}
	}
	for _, o := range out {
		if err := o.Validate(); err != nil {
			t.Error(err)
		}
	}
}

// TestSuiteFailsEachDefect: each defect fails its requirement (E-01).
func TestSuiteFailsEachDefect(t *testing.T) {
	cases := []struct {
		name   string
		f      cispFaults
		notify time.Duration
		want   string
	}{
		{"applies_at not annotated", cispFaults{noMark: true}, time.Second, ReqApplicability},
		{"at= keeps an expired zone", cispFaults{keepAll: true}, time.Second, ReqApplicability},
		{"If-None-Match ignored", cispFaults{ignoreNoneMatch: true}, time.Second, ReqVersioning},
		{"a stale If-Match applied", cispFaults{acceptStale: true}, time.Second, ReqVersioning},
		{"an invalid body stored", cispFaults{acceptInvalid: true}, time.Second, ReqVersioning},
		{"the webhook later than the target", cispFaults{webhookDelay: 600 * time.Millisecond}, 300 * time.Millisecond, ReqWebhook},
		{"the webhook signed by an unknown key", cispFaults{wrongWebhookKey: true}, time.Second, ReqWebhook},
		{"stale right after a heartbeat", cispFaults{staleAfterBeat: true}, time.Second, ReqHeartbeat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newCISPDouble(t, tc.f)
			out := suiteFor(t, d, tc.notify).Run(context.Background())
			if st := fold(out); st[tc.want] != result.Fail {
				dump(t, out)
				t.Errorf("%s: %s, want fail", tc.want, st[tc.want])
			}
		})
	}
}

// TestPublicationNeedsConsent: without ed318.publish nothing is
// written to the target, neither a publication nor a heartbeat (a
// heartbeat moves a real publisher's last_heartbeat_at), and the checks
// that would write say why.
func TestPublicationNeedsConsent(t *testing.T) {
	d := newCISPDouble(t, cispFaults{})
	s := suiteFor(t, d, time.Second)
	s.cfg.Publish = false
	out := s.Run(context.Background())
	d.mu.Lock()
	v, beats := d.version, len(d.lastBeat)
	d.mu.Unlock()
	if v != 0 {
		t.Fatalf("published %d versions without consent", v)
	}
	if beats != 0 {
		t.Fatalf("sent a heartbeat without consent (%d publishers recorded)", beats)
	}
	for _, o := range out {
		if o.Requirement == ReqHeartbeat && (o.Status != result.NotApplicable || !strings.Contains(o.Reason, "ed318.publish")) {
			t.Errorf("heartbeat outcome %+v: want not applicable naming ed318.publish", o)
		}
	}
	st := fold(out)
	for _, r := range []string{ReqChanges, ReqWebhook, ReqHeartbeat} {
		if st[r] != result.NotApplicable {
			t.Errorf("%s: %s, want not_applicable", r, st[r])
		}
	}
}

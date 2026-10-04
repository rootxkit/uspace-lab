// Package ed318 is the CISP's ED-318 publication test
// (docs/WORKPACKAGES/WP-L7.md, spec 00 §7 "our own tests ... for ED-318
// publication"): the published datasets against the contract and
// uspace-core's strict ED-318 reader, their vertical references, the
// applicability filters ?at= and ?applies_at=, versioning and ETag, the
// /v1/changes feed, the signed webhook within the policy's latency, and
// the publisher heartbeat and stale rules.
//
// Publishing replaces the target's whole zones dataset and a heartbeat
// moves a publisher's last_heartbeat_at, so the publication half and the
// heartbeat run only when the target file enables them for a disposable
// stack (ed318.publish); otherwise those checks are not applicable, with
// that reason. The read half runs against any CISP.
package ed318

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
	coreed318 "github.com/rootxkit/uspace-core/ed318"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rootxkit/uspace-lab/conformance/national"
	"github.com/rootxkit/uspace-lab/conformance/result"
)

// Requirement ids (conformance/requirements.yaml).
const (
	ReqSchema        = "ED318-SCHEMA"
	ReqVertical      = "ED318-VERTICAL"
	ReqApplicability = "ED318-APPLICABILITY"
	ReqVersioning    = "ED318-VERSIONING"
	ReqChanges       = "ED318-CHANGES"
	ReqWebhook       = "ED318-WEBHOOK"
	ReqHeartbeat     = "ED318-HEARTBEAT"
)

// Bounds (E-10).
const (
	MaxBodyBytes    = 16 << 20
	MaxWebhookBytes = 64 << 10
	RequestTimeout  = 15 * time.Second
	// ObserveFor is how long the suite watches the change feed and the
	// webhook receiver for a publication before it reports what it saw.
	ObserveFor = 10 * time.Second
	// MaxChangePages bounds the walk to the end of the change feed.
	MaxChangePages = 200
)

// Config is what the suite is handed.
type Config struct {
	Contract *national.Contract
	BaseURL  string
	HTTP     *http.Client
	Problem  *jsonschema.Schema
	// ReaderToken reads (cis.read).
	ReaderToken string
	// AuthorityToken and AuthorityKey publish zones; Publish allows it.
	AuthorityToken string
	AuthorityKey   *auth.SigningKey
	Publish        bool
	// ANSPToken sends the heartbeat (any cis.publish:* scope).
	ANSPToken string
	// JWKSURL and IssuerURL verify the CISP's webhook JWS.
	JWKSURL   string
	IssuerURL string
	// WebhookListen and WebhookURL: the suite's receiver.
	WebhookListen string
	WebhookURL    string
	Area          FixtureArea
	// ChangeNotification is the webhook latency target (policy).
	ChangeNotification time.Duration
	// StaleTolerance absorbs clock skew in the stale judgement.
	StaleTolerance time.Duration
	Now            func() time.Time
}

// Suite runs the tests.
type Suite struct {
	cfg Config
	out []result.Outcome
}

// New checks the configuration.
func New(cfg Config) (*Suite, error) {
	if cfg.Contract == nil || cfg.BaseURL == "" {
		return nil, errors.New("ed318: the CISP contract and base URL are required")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = national.NewHTTPClient(nil)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ChangeNotification <= 0 {
		return nil, errors.New("ed318: the change notification target must be positive")
	}
	return &Suite{cfg: cfg}, nil
}

func (s *Suite) add(o result.Outcome) { s.out = append(s.out, o) }

// Run runs every check and returns the outcomes.
func (s *Suite) Run(ctx context.Context) []result.Outcome {
	s.out = nil
	if s.cfg.ReaderToken == "" {
		for _, r := range []string{ReqSchema, ReqVertical, ReqApplicability, ReqVersioning, ReqChanges, ReqWebhook} {
			s.add(result.Skipped(r, "reader", "cis.read", "the target file supplies no cis.read token (ed318.reader_token)"))
		}
	} else {
		for _, ds := range []string{"zones", "uspace_airspace"} {
			s.readDataset(ctx, ds)
		}
		s.applicability(ctx, nil)
		s.publication(ctx)
	}
	s.heartbeat(ctx)
	return s.out
}

// --- reads -------------------------------------------------------------

// dataset is one read of a dataset.
type dataset struct {
	status int
	etag   string
	header http.Header
	body   []byte
	fc     *coreed318.FeatureCollection
}

func (s *Suite) get(ctx context.Context, path string, q url.Values, hdr http.Header, token string) (int, http.Header, []byte, error) {
	target := strings.TrimRight(s.cfg.BaseURL, "/") + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return s.do(ctx, http.MethodGet, target, hdr, nil, token)
}

func (s *Suite) do(ctx context.Context, method, target string, hdr http.Header, body []byte, token string) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(b) > MaxBodyBytes {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("the body is above the suite's %d-byte cap", MaxBodyBytes)
	}
	return resp.StatusCode, resp.Header, b, nil
}

func (s *Suite) read(ctx context.Context, ds string, q url.Values, hdr http.Header) (*dataset, error) {
	st, h, b, err := s.get(ctx, "/v1/"+url.PathEscape(ds), q, hdr, s.cfg.ReaderToken)
	if err != nil {
		return nil, err
	}
	d := &dataset{status: st, header: h, body: b, etag: h.Get("ETag")}
	return d, nil
}

func (s *Suite) readDataset(ctx context.Context, ds string) {
	subj := "GET /v1/" + ds
	d, err := s.read(ctx, ds, nil, nil)
	if err != nil {
		s.add(result.Failed(ReqSchema, "dataset", subj, 0, "no answer: "+err.Error(), ""))
		return
	}
	if d.status == http.StatusNotFound {
		s.add(result.Skipped(ReqSchema, "dataset", subj, "the dataset has never been published (404 "+problemType(d.body)+")"))
		return
	}
	if d.status != http.StatusOK {
		s.add(result.Failed(ReqSchema, "dataset", subj, d.status, fmt.Sprintf("answered %d, not 200", d.status), problemType(d.body)))
		return
	}
	s.judgeRead(ctx, ds, subj, d)
}

// judgeRead holds a 200 read to the contract and ED-318, then checks its
// ETag, If-None-Match, signature header and the version list.
func (s *Suite) judgeRead(ctx context.Context, ds, subj string, d *dataset) {
	s.judgeDataset(subj, d)
	if d.etag == "" {
		s.add(result.Failed(ReqVersioning, "etag", subj, d.status, "no ETag on an unfiltered read", ""))
	} else {
		s.add(result.Passed(ReqVersioning, "etag", subj, d.status, "ETag "+d.etag))
		nm, err := s.read(ctx, ds, nil, http.Header{"If-None-Match": {d.etag}})
		switch {
		case err != nil:
			s.add(result.Failed(ReqVersioning, "if_none_match", subj, 0, "no answer: "+err.Error(), ""))
		case nm.status != http.StatusNotModified:
			s.add(result.Failed(ReqVersioning, "if_none_match", subj, nm.status, fmt.Sprintf("If-None-Match with the current ETag answered %d, not 304", nm.status), ""))
		case len(bytes.TrimSpace(nm.body)) != 0:
			s.add(result.Failed(ReqVersioning, "if_none_match", subj, nm.status, "a 304 with a body", ""))
		default:
			s.add(result.Passed(ReqVersioning, "if_none_match", subj, nm.status, "304 on the current ETag"))
		}
	}
	if d.header.Get("X-CIS-Signature") == "" {
		s.add(result.Failed(ReqVersioning, "signed_snapshot", subj, d.status, "an unfiltered read without X-CIS-Signature", ""))
	} else {
		s.add(result.Passed(ReqVersioning, "signed_snapshot", subj, d.status, "X-CIS-Signature present"))
	}
	s.versions(ctx, ds, d)
}

// judgeDataset holds a 200 dataset read to the contract, to
// uspace-core's strict ED-318 reader and to the vertical references.
func (s *Suite) judgeDataset(subj string, d *dataset) {
	op := s.cfg.Contract.Op("getDataset")
	if op == nil {
		s.add(result.Failed(ReqSchema, "contract", subj, d.status, "the CISP contract has no getDataset operation", ""))
		return
	}
	if err := s.cfg.Contract.ValidateResponse(op, d.status, d.header.Get("Content-Type"), d.body); err != nil {
		s.add(result.Failed(ReqSchema, "contract", subj, d.status, "the dataset does not match the contract", short(err.Error())))
	} else {
		s.add(result.Passed(ReqSchema, "contract", subj, d.status, "schema-valid "+d.header.Get("Content-Type")))
	}
	fc, probs := coreed318.Parse(d.body, coreed318.Limits{MaxBytes: MaxBodyBytes})
	if probs != nil && len(probs.List) > 0 {
		s.add(result.Failed(ReqSchema, "ed318_parse", subj, d.status, "uspace-core's ED-318 reader refuses the dataset", short(probs.Error())))
		return
	}
	d.fc = fc
	s.add(result.Passed(ReqSchema, "ed318_parse", subj, d.status, fmt.Sprintf("%d features read by uspace-core ed318.Parse", len(fc.Features))))
	if len(fc.Features) == 0 {
		s.add(result.Skipped(ReqVertical, "vertical", subj, "the dataset has no features"))
		return
	}
	var bad []string
	refs := map[string]bool{}
	for i := range fc.Features {
		for _, l := range layers(fc.Features[i].Geometry) {
			for _, r := range []core.VerticalRef{l.UpperReference, l.LowerReference} {
				if r == "" {
					continue
				}
				refs[string(r)] = true
				if !r.Valid() {
					bad = append(bad, fmt.Sprintf("features[%d]: reference %q", i, r))
				}
			}
			if l.Uom != nil && *l.Uom != coreed318.UomMetres && *l.Uom != coreed318.UomFeet {
				bad = append(bad, fmt.Sprintf("features[%d]: uom %q", i, *l.Uom))
			}
		}
	}
	if len(bad) > 0 {
		s.add(result.Failed(ReqVertical, "vertical", subj, d.status, "a vertical reference or unit outside ED-318's", strings.Join(bad, "; ")))
		return
	}
	s.add(result.Passed(ReqVertical, "vertical", subj, d.status, "references "+strings.Join(sortedSet(refs), ", ")+" in m or ft"))
}

func layers(g coreed318.Geometry) []coreed318.Layer {
	var out []coreed318.Layer
	if g.Layer != nil {
		out = append(out, *g.Layer)
	}
	for i := range g.Geometries {
		out = append(out, layers(g.Geometries[i])...)
	}
	return out
}

func (s *Suite) versions(ctx context.Context, ds string, d *dataset) {
	subj := "GET /v1/" + ds + "/versions"
	st, h, b, err := s.get(ctx, "/v1/"+url.PathEscape(ds)+"/versions", nil, nil, s.cfg.ReaderToken)
	if err != nil {
		s.add(result.Failed(ReqVersioning, "versions", subj, 0, "no answer: "+err.Error(), ""))
		return
	}
	if st != http.StatusOK {
		s.add(result.Failed(ReqVersioning, "versions", subj, st, fmt.Sprintf("answered %d, not 200", st), problemType(b)))
		return
	}
	if op := s.cfg.Contract.Op("listDatasetVersions"); op != nil {
		if err := s.cfg.Contract.ValidateResponse(op, st, h.Get("Content-Type"), b); err != nil {
			s.add(result.Failed(ReqVersioning, "versions", subj, st, "the version list does not match the contract", short(err.Error())))
			return
		}
	}
	v, ok := versionOf(d.etag)
	if !ok {
		s.add(result.Failed(ReqVersioning, "versions", subj, st, "the dataset's ETag "+d.etag+" does not name a version", ""))
		return
	}
	if !bytes.Contains(b, []byte(`"version":`+strconv.FormatInt(v, 10))) && !bytes.Contains(b, []byte(`"version": `+strconv.FormatInt(v, 10))) {
		s.add(result.Failed(ReqVersioning, "versions", subj, st, fmt.Sprintf("the version list does not hold the current version %d", v), ""))
		return
	}
	s.add(result.Passed(ReqVersioning, "versions", subj, st, fmt.Sprintf("lists version %d", v)))
}

// versionOf reads "<dataset>:<version>" out of an ETag.
func versionOf(etag string) (int64, bool) {
	e := strings.Trim(strings.TrimPrefix(etag, "W/"), `"`)
	i := strings.LastIndex(e, ":")
	if i < 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(e[i+1:], 10, 64)
	return v, err == nil
}

// --- applicability -----------------------------------------------------

// applicability checks ?at= and ?applies_at= on zones. With fixture
// (two identifiers just published) it also checks that the active zone
// is kept and annotated applies and the expired one dropped and
// annotated not_applicable.
func (s *Suite) applicability(ctx context.Context, fixture []string) {
	at := s.cfg.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	// at=: what applies, plus what cannot be evaluated (marked unknown).
	d, err := s.read(ctx, "zones", url.Values{"at": {at}}, nil)
	subj := "GET /v1/zones?at=" + at
	switch {
	case err != nil:
		s.add(result.Failed(ReqApplicability, "at", subj, 0, "no answer: "+err.Error(), ""))
	case d.status == http.StatusNotFound:
		s.add(result.Skipped(ReqApplicability, "at", subj, "zones has never been published"))
	case d.status != http.StatusOK:
		s.add(result.Failed(ReqApplicability, "at", subj, d.status, fmt.Sprintf("answered %d, not 200", d.status), problemType(d.body)))
	default:
		s.judgeFiltered(subj, d, at, false, fixture)
	}
	d, err = s.read(ctx, "zones", url.Values{"applies_at": {at}}, nil)
	subj = "GET /v1/zones?applies_at=" + at
	switch {
	case err != nil:
		s.add(result.Failed(ReqApplicability, "applies_at", subj, 0, "no answer: "+err.Error(), ""))
	case d.status == http.StatusNotFound:
		s.add(result.Skipped(ReqApplicability, "applies_at", subj, "zones has never been published"))
	case d.status != http.StatusOK:
		s.add(result.Failed(ReqApplicability, "applies_at", subj, d.status, fmt.Sprintf("answered %d, not 200", d.status), problemType(d.body)))
	default:
		s.judgeFiltered(subj, d, at, true, fixture)
	}
	if fixture != nil {
		return
	}
	// The refusals the contract names: a time without an offset, and at
	// with applies_at.
	for _, q := range []url.Values{
		{"at": {strings.TrimSuffix(at, "Z")}},
		{"at": {at}, "applies_at": {at}},
	} {
		subj := "GET /v1/zones?" + q.Encode()
		d, err := s.read(ctx, "zones", q, nil)
		switch {
		case err != nil:
			s.add(result.Failed(ReqApplicability, "filter_refused", subj, 0, "no answer: "+err.Error(), ""))
		case d.status == http.StatusNotFound:
			s.add(result.Skipped(ReqApplicability, "filter_refused", subj, "zones has never been published"))
		case d.status != http.StatusBadRequest:
			s.add(result.Failed(ReqApplicability, "filter_refused", subj, d.status, fmt.Sprintf("answered %d, not 400", d.status), problemType(d.body)))
		default:
			if why := s.problemFault(d.header, d.body); why != "" {
				s.add(result.Failed(ReqApplicability, "filter_refused", subj, d.status, why, short(string(d.body))))
			} else {
				s.add(result.Passed(ReqApplicability, "filter_refused", subj, d.status, problemType(d.body)))
			}
		}
	}
}

// judgeFiltered compares a filtered read with uspace-core's own
// judgement of each feature at the same instant (ed318.Applies at the
// feature's centroid, NOAA daylight): the same package the CISP calls,
// so a difference is the CISP's handling, not a second opinion.
func (s *Suite) judgeFiltered(subj string, d *dataset, at string, annotate bool, fixture []string) {
	check := "at"
	if annotate {
		check = "applies_at"
	}
	if !strings.Contains(d.header.Get("X-CIS-Filtered"), check) {
		s.add(result.Failed(ReqApplicability, check, subj, d.status, "X-CIS-Filtered does not name "+check, d.header.Get("X-CIS-Filtered")))
		return
	}
	fc, probs := coreed318.Parse(d.body, coreed318.Limits{MaxBytes: MaxBodyBytes})
	if probs != nil && len(probs.List) > 0 {
		s.add(result.Failed(ReqApplicability, check, subj, d.status, "uspace-core's ED-318 reader refuses the filtered collection", short(probs.Error())))
		return
	}
	when, _ := time.Parse(time.RFC3339, at)
	var bad []string
	got := map[string]string{}
	for i := range fc.Features {
		f := &fc.Features[i]
		id := f.Properties.Identifier
		mark := applicabilityMark(f.Properties.ExtendedProperties)
		got[id] = mark
		applies, err := coreed318.Applies(f.Properties.LimitedApplicability, when, centroid(f.Geometry), coreed318.NOAADaylight{})
		want := "not_applicable"
		switch {
		case err != nil:
			want = "unknown"
		case applies:
			want = "applies"
		}
		if annotate {
			if mark != want {
				bad = append(bad, fmt.Sprintf("%s marked %q, uspace-core judges %q", id, mark, want))
			}
			continue
		}
		if want == "not_applicable" {
			bad = append(bad, fmt.Sprintf("%s kept although it does not apply", id))
		}
		if want == "unknown" && mark != "unknown" {
			bad = append(bad, fmt.Sprintf("%s kept unevaluated without cis_applicability unknown", id))
		}
	}
	if fixture != nil {
		active, expired := fixture[0], fixture[1]
		if annotate {
			if got[active] != "applies" || got[expired] != "not_applicable" {
				bad = append(bad, fmt.Sprintf("fixture zones marked %s=%q %s=%q, want applies and not_applicable", active, got[active], expired, got[expired]))
			}
		} else {
			if _, ok := got[active]; !ok {
				bad = append(bad, "the fixture zone "+active+" that applies now was dropped")
			}
			if _, ok := got[expired]; ok {
				bad = append(bad, "the fixture zone "+expired+" that ended yesterday was kept")
			}
		}
	}
	if len(bad) > 0 {
		s.add(result.Failed(ReqApplicability, check, subj, d.status, "the filter disagrees with uspace-core's judgement", strings.Join(bad, "; ")))
		return
	}
	detail := fmt.Sprintf("features: %d, as uspace-core judges them", len(fc.Features))
	if fixture != nil {
		detail += " (with the published fixture)"
	}
	s.add(result.Passed(ReqApplicability, check, subj, d.status, detail))
}

func applicabilityMark(ext map[string]json.RawMessage) string {
	raw, ok := ext["cis_applicability"]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "(not a string)"
	}
	return v
}

// centroid is the mean of a geometry's first ring, its centre, or its
// first part's: where daylight events resolve, nothing more.
func centroid(g coreed318.Geometry) core.LatLon {
	if g.Center != nil {
		return *g.Center
	}
	if len(g.Rings) > 0 && len(g.Rings[0]) > 0 {
		var lat, lon float64
		ring := g.Rings[0]
		n := len(ring)
		if n > 1 {
			n-- // closed ring: the last position repeats the first
		}
		for _, p := range ring[:n] {
			lat += p.LatDeg
			lon += p.LonDeg
		}
		return core.LatLon{LatDeg: lat / float64(n), LonDeg: lon / float64(n)}
	}
	if len(g.Geometries) > 0 {
		return centroid(g.Geometries[0])
	}
	return core.LatLon{}
}

// --- publication -------------------------------------------------------

func (s *Suite) publication(ctx context.Context) {
	subj := "PUT /v1/publications/zones"
	why := ""
	switch {
	case !s.cfg.Publish:
		why = "publishing replaces the target's zones dataset: enable ed318.publish only on a disposable stack"
	case s.cfg.AuthorityToken == "" || s.cfg.AuthorityKey == nil:
		why = "the target file supplies no authority token and signing key (ed318.authority_token, authority_key_file)"
	}
	if why != "" {
		for _, r := range []string{ReqVersioning, ReqChanges, ReqWebhook} {
			s.add(result.Skipped(r, "publication", subj, why))
		}
		s.add(result.Skipped(ReqApplicability, "fixture", subj, why))
		return
	}
	cursor, err := s.endOfChanges(ctx)
	if err != nil {
		s.add(result.Failed(ReqChanges, "cursor", "GET /v1/changes", 0, "cannot reach the end of the change feed", short(err.Error())))
		return
	}
	hook, cleanup := s.webhook(ctx)
	defer cleanup()

	cur, err := s.read(ctx, "zones", nil, nil)
	if err != nil {
		s.add(result.Failed(ReqVersioning, "publication", subj, 0, "no answer: "+err.Error(), ""))
		return
	}
	current := `"zones:0"`
	if cur.status == http.StatusOK && cur.etag != "" {
		current = cur.etag
	}
	active, expired := newID("A"), newID("E")
	body, err := Fixture(s.cfg.Area, s.cfg.Now(), active, expired)
	if err != nil {
		s.add(result.Failed(ReqVersioning, "publication", subj, 0, "the fixture does not build: "+err.Error(), ""))
		return
	}
	// 428 without If-Match, 412 on a stale one with the current ETag.
	if st, h, b, err := s.put(ctx, body, ""); err != nil {
		s.add(result.Failed(ReqVersioning, "if_match_required", subj, 0, "no answer: "+err.Error(), ""))
	} else {
		s.refusal(ReqVersioning, "if_match_required", subj, st, h, b, http.StatusPreconditionRequired, false)
	}
	st, h, b, err := s.put(ctx, body, `"zones:999999"`)
	switch {
	case err != nil:
		s.add(result.Failed(ReqVersioning, "stale_if_match", subj, 0, "no answer: "+err.Error(), ""))
	case st != http.StatusPreconditionFailed:
		s.add(result.Failed(ReqVersioning, "stale_if_match", subj, st, fmt.Sprintf("a stale If-Match answered %d, not 412", st), problemType(b)))
	case h.Get("ETag") != current:
		s.add(result.Failed(ReqVersioning, "stale_if_match", subj, st, fmt.Sprintf("412 names ETag %q, the current version is %s", h.Get("ETag"), current), ""))
	default:
		s.add(result.Passed(ReqVersioning, "stale_if_match", subj, st, "412 with the current ETag "+current))
	}
	// A signed body the CISP must refuse whole, naming the field, and
	// leave the version where it was (validate before any side effect).
	bad, err := InvalidFixture(s.cfg.Area, s.cfg.Now(), active, expired)
	if err == nil {
		st, h, b, perr := s.put(ctx, bad, current)
		if perr != nil {
			s.add(result.Failed(ReqVersioning, "invalid_refused", subj, 0, "no answer: "+perr.Error(), ""))
		} else {
			s.refusal(ReqVersioning, "invalid_refused", subj, st, h, b, http.StatusBadRequest, true)
			if after, rerr := s.read(ctx, "zones", nil, nil); rerr == nil && after.status == cur.status && after.etag != cur.etag {
				s.add(result.Failed(ReqVersioning, "invalid_refused", subj, st, "a refused publication moved the version", cur.etag+" -> "+after.etag))
			}
		}
	}
	st, h, b, err = s.put(ctx, body, current)
	accepted := s.cfg.Now()
	if err != nil {
		s.add(result.Failed(ReqVersioning, "accepted", subj, 0, "no answer: "+err.Error(), ""))
		return
	}
	if st != http.StatusCreated {
		s.add(result.Failed(ReqVersioning, "accepted", subj, st, fmt.Sprintf("the fixture answered %d, not 201", st), short(string(b))))
		return
	}
	if op := s.cfg.Contract.Op("putPublication"); op != nil {
		if err := s.cfg.Contract.ValidateResponse(op, st, h.Get("Content-Type"), b); err != nil {
			s.add(result.Failed(ReqVersioning, "accepted", subj, st, "the publication result does not match the contract", short(err.Error())))
			return
		}
	}
	newTag := h.Get("ETag")
	oldV, _ := versionOf(current)
	newV, ok := versionOf(newTag)
	if !ok || newV != oldV+1 {
		s.add(result.Failed(ReqVersioning, "accepted", subj, st, fmt.Sprintf("201 with ETag %q after %s: want version %d", newTag, current, oldV+1), ""))
		return
	}
	after, err := s.read(ctx, "zones", nil, nil)
	if err != nil || after.etag != newTag {
		got := ""
		if after != nil {
			got = after.etag
		}
		s.add(result.Failed(ReqVersioning, "accepted", subj, st, "the read after the publication does not carry its ETag", newTag+" vs "+got))
		return
	}
	s.add(result.Passed(ReqVersioning, "accepted", subj, st, current+" -> "+newTag))
	s.judgeRead(ctx, "zones", "GET /v1/zones (the fixture)", after)
	s.applicability(ctx, []string{active, expired})
	s.changes(ctx, cursor, newV)
	s.observeWebhook(ctx, hook, newV, accepted)
}

func (s *Suite) put(ctx context.Context, body []byte, ifMatch string) (int, http.Header, []byte, error) {
	sig, err := auth.SignDetached(*s.cfg.AuthorityKey, body, s.cfg.Now())
	if err != nil {
		return 0, nil, nil, fmt.Errorf("signing: %w", err)
	}
	hdr := http.Header{"Content-Type": {"application/geo+json"}, "X-JWS-Signature": {sig}}
	if ifMatch != "" {
		hdr.Set("If-Match", ifMatch)
	}
	return s.do(ctx, http.MethodPut, strings.TrimRight(s.cfg.BaseURL, "/")+"/v1/publications/zones", hdr, body, s.cfg.AuthorityToken)
}

func (s *Suite) refusal(req, check, subj string, st int, h http.Header, b []byte, want int, withErrors bool) {
	if st != want {
		s.add(result.Failed(req, check, subj, st, fmt.Sprintf("answered %d, not %d", st, want), problemType(b)))
		return
	}
	if why := s.problemFault(h, b); why != "" {
		s.add(result.Failed(req, check, subj, st, why, short(string(b))))
		return
	}
	if withErrors {
		var p struct {
			Errors []json.RawMessage `json:"errors"`
		}
		if json.Unmarshal(b, &p) != nil || len(p.Errors) == 0 {
			s.add(result.Failed(req, check, subj, st, "errors[] is empty: the refusal names no field", short(string(b))))
			return
		}
	}
	s.add(result.Passed(req, check, subj, st, problemType(b)))
}

func (s *Suite) problemFault(h http.Header, b []byte) string {
	ct := h.Get("Content-Type")
	if mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])); mt != national.ProblemMediaType {
		return fmt.Sprintf("Content-Type %q, want %s", ct, national.ProblemMediaType)
	}
	if s.cfg.Problem == nil {
		return ""
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return "the problem body is not JSON"
	}
	if err := s.cfg.Problem.Validate(doc); err != nil {
		return "not a problem/v1 body: " + short(err.Error())
	}
	return ""
}

// --- change feed -------------------------------------------------------

type changeList struct {
	Changes []struct {
		MsgID   string `json:"msg_id"`
		Dataset string `json:"dataset"`
		Version int64  `json:"version"`
		Reason  string `json:"reason"`
	} `json:"changes"`
	Next int64 `json:"next"`
}

func (s *Suite) changesAfter(ctx context.Context, since int64) (*changeList, []byte, int, http.Header, error) {
	st, h, b, err := s.get(ctx, "/v1/changes", url.Values{"since": {strconv.FormatInt(since, 10)}, "limit": {"500"}}, nil, s.cfg.ReaderToken)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	if st != http.StatusOK {
		return nil, b, st, h, fmt.Errorf("answered %d", st)
	}
	var cl changeList
	if err := json.Unmarshal(b, &cl); err != nil {
		return nil, b, st, h, fmt.Errorf("not a change list: %w", err)
	}
	return &cl, b, st, h, nil
}

// endOfChanges walks the feed to its end (bounded) and returns the
// cursor after the last change.
func (s *Suite) endOfChanges(ctx context.Context) (int64, error) {
	var since int64
	for range MaxChangePages {
		cl, _, _, _, err := s.changesAfter(ctx, since)
		if err != nil {
			return 0, err
		}
		if cl.Next == since || len(cl.Changes) == 0 {
			return cl.Next, nil
		}
		since = cl.Next
	}
	return 0, fmt.Errorf("more than %d pages of changes", MaxChangePages)
}

func (s *Suite) changes(ctx context.Context, since, version int64) {
	subj := "GET /v1/changes?since=" + strconv.FormatInt(since, 10)
	deadline := s.cfg.Now().Add(ObserveFor)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		cl, b, st, h, err := s.changesAfter(ctx, since)
		if err != nil {
			s.add(result.Failed(ReqChanges, "change_feed", subj, st, "the change feed did not answer a change list", short(err.Error())))
			return
		}
		if op := s.cfg.Contract.Op("listChanges"); op != nil {
			if err := s.cfg.Contract.ValidateResponse(op, st, h.Get("Content-Type"), b); err != nil {
				s.add(result.Failed(ReqChanges, "change_feed", subj, st, "the change list does not match the contract", short(err.Error())))
				return
			}
		}
		for _, c := range cl.Changes {
			if c.Dataset == "zones" && c.Version == version {
				s.add(result.Passed(ReqChanges, "change_feed", subj, st, fmt.Sprintf("change %s (%s) for zones:%d", c.MsgID, c.Reason, version)))
				return
			}
		}
		if !s.cfg.Now().Before(deadline) {
			s.add(result.Failed(ReqChanges, "change_feed", subj, st, fmt.Sprintf("no change for zones:%d within %s", version, ObserveFor), ""))
			return
		}
		select {
		case <-ctx.Done():
			s.add(result.Failed(ReqChanges, "change_feed", subj, 0, "run ended: "+ctx.Err().Error(), ""))
			return
		case <-tick.C:
		}
	}
}

// --- webhook -----------------------------------------------------------

// delivery is one POST the receiver took.
type delivery struct {
	at    time.Time
	token string
}

type receiver struct {
	mu   sync.Mutex
	got  []delivery
	wake chan struct{}
	sub  string // subscription id, empty when none was made
	err  string // why the webhook check cannot run
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, err := io.ReadAll(io.LimitReader(req.Body, MaxWebhookBytes+1))
	if err != nil || len(b) > MaxWebhookBytes {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	r.mu.Lock()
	r.got = append(r.got, delivery{at: time.Now(), token: strings.TrimSpace(string(b))})
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *receiver) deliveries() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// webhook starts the receiver and registers a subscription for zones,
// waiting until the CISP has verified it. The returned cleanup deletes
// the subscription and stops the receiver.
func (s *Suite) webhook(ctx context.Context) (*receiver, func()) {
	r := &receiver{wake: make(chan struct{}, 1)}
	if s.cfg.WebhookListen == "" || s.cfg.WebhookURL == "" {
		r.err = "the target file names no webhook receiver the CISP can reach (ed318.webhook_listen, webhook_url)"
		return r, func() {}
	}
	if s.cfg.JWKSURL == "" || s.cfg.IssuerURL == "" {
		r.err = "the target file names no CISP issuer and JWKS to verify the webhook with (ed318.issuer_url, jwks_url)"
		return r, func() {}
	}
	ln, err := net.Listen("tcp", s.cfg.WebhookListen)
	if err != nil {
		r.err = "the webhook receiver cannot listen: " + err.Error()
		return r, func() {}
	}
	srv := &http.Server{Handler: r, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}
	body, _ := json.Marshal(map[string]any{"callback_url": s.cfg.WebhookURL, "datasets": []string{"zones"}})
	st, _, b, err := s.do(ctx, http.MethodPost, strings.TrimRight(s.cfg.BaseURL, "/")+"/v1/subscriptions",
		http.Header{"Content-Type": {"application/json"}}, body, s.cfg.ReaderToken)
	if err != nil || st != http.StatusCreated {
		r.err = fmt.Sprintf("the subscription was not created (%d %s)", st, problemType(b))
		if err != nil {
			r.err = "the subscription was not created: " + err.Error()
		}
		stop()
		return r, func() {}
	}
	var sub struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(b, &sub)
	r.sub = sub.ID
	cleanup := func() {
		dctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
		defer cancel()
		_, _, _, _ = s.do(dctx, http.MethodDelete, strings.TrimRight(s.cfg.BaseURL, "/")+"/v1/subscriptions/"+url.PathEscape(r.sub), nil, nil, s.cfg.ReaderToken)
		stop()
	}
	// Wait (bounded) until the CISP calls it active after its ping.
	deadline := time.Now().Add(ObserveFor)
	for sub.Status != "active" {
		if time.Now().After(deadline) {
			r.err = "the subscription did not become active within " + ObserveFor.String() + " (status " + sub.Status + ")"
			break
		}
		select {
		case <-ctx.Done():
			r.err = "run ended"
			return r, cleanup
		case <-r.wake:
		case <-time.After(250 * time.Millisecond):
		}
		st, _, b, err := s.get(ctx, "/v1/subscriptions/"+url.PathEscape(r.sub), nil, nil, s.cfg.ReaderToken)
		if err == nil && st == http.StatusOK {
			_ = json.Unmarshal(b, &sub)
		}
	}
	return r, cleanup
}

func (s *Suite) observeWebhook(ctx context.Context, r *receiver, version int64, accepted time.Time) {
	subj := "POST " + s.cfg.WebhookURL
	if r.err != "" {
		s.add(result.Skipped(ReqWebhook, "webhook", subj, r.err))
		return
	}
	host := ""
	if u, err := url.Parse(s.cfg.WebhookURL); err == nil {
		host = u.Hostname()
	}
	v, err := auth.NewCompactVerifier(ctx, auth.CompactConfig{
		Issuers:    map[string]auth.IssuerConfig{s.cfg.IssuerURL: {JWKSURL: s.cfg.JWKSURL}},
		Audiences:  []string{host},
		HTTPClient: s.cfg.HTTP,
	})
	if err != nil {
		s.add(result.Failed(ReqWebhook, "webhook", subj, 0, "cannot build the JWS verifier", short(err.Error())))
		return
	}
	deadline := time.Now().Add(ObserveFor)
	seen := 0
	for {
		ds := r.deliveries()
		for _, d := range ds[seen:] {
			cl, body, err := v.Verify(ctx, d.token)
			if err != nil {
				s.add(result.Failed(ReqWebhook, "webhook", subj, 0, "a delivery whose JWS does not verify", short(err.Error())))
				return
			}
			var c struct {
				Dataset string `json:"dataset"`
				Version int64  `json:"version"`
				Reason  string `json:"reason"`
			}
			if json.Unmarshal(body, &c) != nil || c.Dataset != "zones" || c.Version != version {
				continue
			}
			if err := s.cfg.Contract.ValidateJSON("#/components/schemas/Change", body); err != nil {
				s.add(result.Failed(ReqWebhook, "webhook", subj, 0, "the delivered change does not match cis/change/v1", short(err.Error())))
				return
			}
			lat := d.at.Sub(accepted)
			detail := fmt.Sprintf("zones:%d delivered %.3f s after the 201, sub %s, jti %s", version, lat.Seconds(), cl.Subject, cl.JTI)
			if lat > s.cfg.ChangeNotification {
				s.add(result.Failed(ReqWebhook, "webhook", subj, 0, fmt.Sprintf("delivered after %.3f s, the target is %s (pending GCAA)", lat.Seconds(), s.cfg.ChangeNotification), detail))
				return
			}
			s.add(result.Passed(ReqWebhook, "webhook", subj, 0, detail))
			return
		}
		seen = len(ds)
		if time.Now().After(deadline) {
			s.add(result.Failed(ReqWebhook, "webhook", subj, 0, fmt.Sprintf("no signed delivery of zones:%d within %s", version, ObserveFor), fmt.Sprintf("%d deliveries received", seen)))
			return
		}
		select {
		case <-ctx.Done():
			s.add(result.Failed(ReqWebhook, "webhook", subj, 0, "run ended: "+ctx.Err().Error(), ""))
			return
		case <-r.wake:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// --- heartbeat ---------------------------------------------------------

func (s *Suite) heartbeat(ctx context.Context) {
	subj := "POST /v1/publishers/heartbeat"
	// A heartbeat is a write: it moves the publisher's
	// last_heartbeat_at on the target, which a real deployment's stale
	// judgement reads. Like a publication, only on a disposable stack.
	if !s.cfg.Publish {
		s.add(result.Skipped(ReqHeartbeat, "heartbeat", subj, "a heartbeat is recorded against the target's publisher: enable ed318.publish only on a disposable stack"))
		return
	}
	if s.cfg.ANSPToken == "" || s.cfg.ReaderToken == "" {
		s.add(result.Skipped(ReqHeartbeat, "heartbeat", subj, "the target file supplies no publisher token (ed318.ansp_token) or no cis.read token"))
		return
	}
	sub := tokenSubject(s.cfg.ANSPToken)
	sent := s.cfg.Now().UTC()
	body, _ := json.Marshal(map[string]any{"sent_at": sent.Format("2006-01-02T15:04:05.000Z")})
	st, _, b, err := s.do(ctx, http.MethodPost, strings.TrimRight(s.cfg.BaseURL, "/")+"/v1/publishers/heartbeat",
		http.Header{"Content-Type": {"application/json"}}, body, s.cfg.ANSPToken)
	if err != nil {
		s.add(result.Failed(ReqHeartbeat, "heartbeat", subj, 0, "no answer: "+err.Error(), ""))
		return
	}
	if st != http.StatusNoContent {
		s.add(result.Failed(ReqHeartbeat, "heartbeat", subj, st, fmt.Sprintf("answered %d, not 204", st), problemType(b)))
		return
	}
	s.add(result.Passed(ReqHeartbeat, "heartbeat", subj, st, "recorded for "+sub))
	st, h, b, err := s.get(ctx, "/v1/status", nil, nil, s.cfg.ReaderToken)
	subj = "GET /v1/status"
	if err != nil || st != http.StatusOK {
		s.add(result.Failed(ReqHeartbeat, "status", subj, st, "the status document did not answer 200", problemType(b)))
		return
	}
	if op := s.cfg.Contract.Op("getStatus"); op != nil {
		if err := s.cfg.Contract.ValidateResponse(op, st, h.Get("Content-Type"), b); err != nil {
			s.add(result.Failed(ReqHeartbeat, "status", subj, st, "the status document does not match the contract", short(err.Error())))
			return
		}
	}
	var doc struct {
		Now        time.Time `json:"now"`
		Publishers []struct {
			ClientID        string     `json:"client_id"`
			LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
			StaleAfterS     *float64   `json:"stale_after_s"`
			Stale           bool       `json:"stale"`
		} `json:"publishers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		s.add(result.Failed(ReqHeartbeat, "status", subj, st, "the status document is not JSON", ""))
		return
	}
	tol := s.cfg.StaleTolerance
	var bad []string
	mine := false
	for _, p := range doc.Publishers {
		if p.ClientID == sub {
			mine = true
			switch {
			case p.LastHeartbeatAt == nil:
				bad = append(bad, sub+" shows no heartbeat after one was recorded")
			case p.LastHeartbeatAt.Before(sent.Add(-tol)):
				bad = append(bad, fmt.Sprintf("%s last heartbeat %s is before the one sent at %s", sub, p.LastHeartbeatAt.Format(time.RFC3339Nano), sent.Format(time.RFC3339Nano)))
			case p.Stale:
				bad = append(bad, sub+" is stale right after a heartbeat")
			}
		}
		// The stale rule, for every publisher: stale exactly when the
		// last heartbeat is older than stale_after_s (or there is none).
		if p.StaleAfterS == nil {
			continue
		}
		limit := time.Duration(*p.StaleAfterS * float64(time.Second))
		if p.LastHeartbeatAt == nil {
			if !p.Stale {
				bad = append(bad, p.ClientID+" was never heard from and is not stale")
			}
		} else {
			age := doc.Now.Sub(*p.LastHeartbeatAt)
			if p.Stale && age < limit-tol {
				bad = append(bad, fmt.Sprintf("%s is stale at age %.1f s, under its %.0f s", p.ClientID, age.Seconds(), *p.StaleAfterS))
			}
			if !p.Stale && age > limit+tol {
				bad = append(bad, fmt.Sprintf("%s is not stale at age %.1f s, over its %.0f s", p.ClientID, age.Seconds(), *p.StaleAfterS))
			}
		}
	}
	if !mine {
		bad = append(bad, "the status lists no publisher "+sub)
	}
	if len(bad) > 0 {
		s.add(result.Failed(ReqHeartbeat, "stale_rule", subj, st, "the status disagrees with the heartbeat and stale rules", strings.Join(bad, "; ")))
		return
	}
	s.add(result.Passed(ReqHeartbeat, "stale_rule", subj, st, fmt.Sprintf("%d publishers consistent with stale_after_s", len(doc.Publishers))))
}

// tokenSubject reads sub from a JWT handed to the suite (unverified:
// only to find the publisher in the status document).
func tokenSubject(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var cl struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(b, &cl)
	return cl.Sub
}

// newID is a fresh ED-318 identifier: prefix and six Crockford
// characters (seven in all, ED-318's limit).
func newID(prefix string) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	out := []byte(prefix)
	for _, c := range b {
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}

func problemType(b []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Type
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

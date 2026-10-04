package national

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rootxkit/uspace-lab/conformance/result"
	"github.com/rootxkit/uspace-lab/internal/ulid"
)

// The checks, each deciding one requirement of
// conformance/requirements.yaml.
const (
	CheckUnauthenticated = "unauthenticated"
	CheckWrongScope      = "wrong_scope"
	CheckNotFound        = "not_found"
	CheckPrecondition    = "stale_precondition"
	CheckInvalidBody     = "invalid_body"
	CheckSuccess         = "success"
)

// CheckKinds lists them in the order they run per operation.
var CheckKinds = []string{CheckUnauthenticated, CheckWrongScope, CheckNotFound, CheckPrecondition, CheckInvalidBody, CheckSuccess}

// Requirement ids (conformance/requirements.yaml).
const (
	ReqUnauthenticated = "NAT-UNAUTH"
	ReqWrongScope      = "NAT-SCOPE"
	ReqNotFound        = "NAT-NOTFOUND"
	ReqPrecondition    = "NAT-PRECONDITION"
	ReqInvalidBody     = "NAT-INVALID"
	ReqSuccess         = "NAT-SUCCESS"
)

var checkRequirement = map[string]string{
	CheckUnauthenticated: ReqUnauthenticated,
	CheckWrongScope:      ReqWrongScope,
	CheckNotFound:        ReqNotFound,
	CheckPrecondition:    ReqPrecondition,
	CheckInvalidBody:     ReqInvalidBody,
	CheckSuccess:         ReqSuccess,
}

// RequirementOf is the requirement a check decides.
func RequirementOf(check string) string { return checkRequirement[check] }

// Bounds of every request the suite sends (E-10: bounded writes,
// payloads and retries).
const (
	MaxResponseBytes = 16 << 20
	RequestTimeout   = 15 * time.Second
	// MaxRetryAfter is the longest Retry-After the suite waits for on a
	// 429; a longer one ends the check as failed with what was observed.
	MaxRetryAfter = 5 * time.Second
	// MaxRetries bounds the attempts after a 429.
	MaxRetries = 2
	// ProblemMediaType is the error body's media type (02 §1, M28).
	ProblemMediaType = "application/problem+json"
)

// Runner runs the national checks of one contract against one target.
type Runner struct {
	Contract *Contract
	// BaseURL is the target's base URL (scheme and host, optional path
	// prefix, no trailing slash needed).
	BaseURL string
	Creds   Credentials
	HTTP    *http.Client
	// Fixtures name existing resources by path parameter name.
	Fixtures map[string]string
	// Skip is the overrides' skip table.
	Skip map[string]map[string]string
	// Problem is the compiled common problem/v1 schema.
	Problem *jsonschema.Schema
	// Only restricts the run to these operation ids (empty: all).
	Only []string
}

// Run executes every applicable check of every operation, in contract
// order, and returns one outcome per check that applies by contract
// (a check the contract makes irrelevant, such as 401 on a public
// operation, produces no outcome).
func (r *Runner) Run(ctx context.Context) []result.Outcome {
	var out []result.Outcome
	for _, op := range r.Contract.Ops {
		if len(r.Only) > 0 && !slices.Contains(r.Only, op.ID) {
			continue
		}
		for _, check := range CheckKinds {
			if err := ctx.Err(); err != nil {
				out = append(out, result.Failed(RequirementOf(check), check, op.Subject(), 0, "run ended before the check: "+err.Error(), ""))
				continue
			}
			if reason, ok := r.Skip[op.ID][check]; ok {
				out = append(out, result.Skipped(RequirementOf(check), check, op.Subject(), "skipped by the overrides: "+reason))
				continue
			}
			if o, ok := r.check(ctx, op, check); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

func (r *Runner) check(ctx context.Context, op *Operation, check string) (result.Outcome, bool) {
	switch check {
	case CheckUnauthenticated:
		return r.unauthenticated(ctx, op)
	case CheckWrongScope:
		return r.wrongScope(ctx, op)
	case CheckNotFound:
		return r.notFound(ctx, op)
	case CheckPrecondition:
		return r.precondition(ctx, op)
	case CheckInvalidBody:
		return r.invalidBody(ctx, op)
	case CheckSuccess:
		return r.success(ctx, op)
	}
	return result.Outcome{}, false
}

// request is what one check sends.
type request struct {
	path    map[string]string
	query   url.Values
	header  http.Header
	body    []byte
	ctype   string
	upgrade bool
}

// observed is what the target answered.
type observed struct {
	status int
	header http.Header
	body   []byte
	// truncated is set when the body was cut at MaxResponseBytes.
	truncated bool
}

func (r *Runner) unauthenticated(ctx context.Context, op *Operation) (result.Outcome, bool) {
	if !op.Auth.NeedsCredential() {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	if !op.DeclaresOrDefault("401") {
		return result.Skipped(ReqUnauthenticated, CheckUnauthenticated, subj, "the contract declares neither 401 nor a default response"), true
	}
	req, why := r.baseRequest(op, false)
	if why != "" {
		return result.Skipped(ReqUnauthenticated, CheckUnauthenticated, subj, why), true
	}
	return r.expectRefusal(ctx, op, CheckUnauthenticated, req, []int{http.StatusUnauthorized}, false), true
}

func (r *Runner) wrongScope(ctx context.Context, op *Operation) (result.Outcome, bool) {
	t := op.Auth.Token
	if t == nil {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	if t.Alternatives == nil {
		return result.Skipped(ReqWrongScope, CheckWrongScope, subj, "the contract does not state the operation's scope"), true
	}
	if !op.DeclaresOrDefault("403") {
		return result.Skipped(ReqWrongScope, CheckWrongScope, subj, "the contract declares neither 403 nor a default response"), true
	}
	tok, granted, err := r.Creds.WrongScope(ctx, t.Issuer, t.Scopes())
	if err != nil {
		return result.Skipped(ReqWrongScope, CheckWrongScope, subj, credentialReason(t.Issuer+" token without "+strings.Join(t.Scopes(), ", "), err)), true
	}
	req, why := r.baseRequest(op, false)
	if why != "" {
		return result.Skipped(ReqWrongScope, CheckWrongScope, subj, why), true
	}
	req.header.Set("Authorization", "Bearer "+tok)
	o := r.expectRefusal(ctx, op, CheckWrongScope, req, []int{http.StatusForbidden}, false)
	o.Detail = strings.TrimSpace("token scopes " + strings.Join(granted, " ") + "; " + o.Detail)
	return o, true
}

func (r *Runner) notFound(ctx context.Context, op *Operation) (result.Outcome, bool) {
	if op.Method != http.MethodGet && op.Method != http.MethodDelete && op.Method != http.MethodHead {
		return result.Outcome{}, false
	}
	if !op.Declares("404") || op.WebSocket {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	var pathParams []Param
	for _, p := range op.Params {
		if p.In == "path" {
			pathParams = append(pathParams, p)
		}
	}
	if len(pathParams) == 0 {
		return result.Outcome{}, false
	}
	req := r.newRequest()
	unknown := ""
	for _, p := range pathParams {
		if unknown == "" {
			if v, ok := r.Contract.UnknownValue(p); ok {
				req.path[p.Name] = v
				unknown = p.Name + "=" + v
				continue
			}
		}
		v, _, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok {
			return result.Skipped(ReqNotFound, CheckNotFound, subj, "no value for path parameter "+p.Name), true
		}
		req.path[p.Name] = v
	}
	if unknown == "" {
		return result.Skipped(ReqNotFound, CheckNotFound, subj, "no path parameter can name an unknown resource (enumerations only)"), true
	}
	if why := r.fillRequired(op, req, false); why != "" {
		return result.Skipped(ReqNotFound, CheckNotFound, subj, why), true
	}
	if why := r.authorize(ctx, op, req); why != "" {
		return result.Skipped(ReqNotFound, CheckNotFound, subj, why), true
	}
	o := r.expectRefusal(ctx, op, CheckNotFound, req, []int{http.StatusNotFound}, false)
	o.Detail = strings.TrimSpace(unknown + "; " + o.Detail)
	return o, true
}

func (r *Runner) precondition(ctx context.Context, op *Operation) (result.Outcome, bool) {
	if _, ok := op.Param("header", "If-Match"); !ok {
		return result.Outcome{}, false
	}
	var want []int
	for _, c := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		if op.Declares(strconv.Itoa(c)) {
			want = append(want, c)
		}
	}
	if len(want) == 0 {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	req := r.newRequest()
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		v, guessed, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok || guessed && !hasEnum(p) {
			return result.Skipped(ReqPrecondition, CheckPrecondition, subj, "the target file names no existing "+p.Name), true
		}
		req.path[p.Name] = v
	}
	if why := r.fillRequired(op, req, true); why != "" {
		return result.Skipped(ReqPrecondition, CheckPrecondition, subj, why), true
	}
	if op.Body != nil {
		ct, md, ok := JSONMedia(op.Body.Content)
		ex, hasEx := r.Contract.ExampleBody(md)
		if !ok || !hasEx {
			return result.Skipped(ReqPrecondition, CheckPrecondition, subj, "the contract gives no valid example body to send with the stale If-Match"), true
		}
		b, err := json.Marshal(ex)
		if err != nil {
			return result.Failed(ReqPrecondition, CheckPrecondition, subj, 0, "the example body does not encode: "+err.Error(), ""), true
		}
		req.body, req.ctype = b, ct
	}
	req.header.Set("If-Match", `"conformance-stale-0"`)
	if why := r.authorize(ctx, op, req); why != "" {
		return result.Skipped(ReqPrecondition, CheckPrecondition, subj, why), true
	}
	return r.expectRefusal(ctx, op, CheckPrecondition, req, want, false), true
}

func (r *Runner) invalidBody(ctx context.Context, op *Operation) (result.Outcome, bool) {
	if op.Body == nil {
		return result.Outcome{}, false
	}
	var want []int
	for _, c := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		if op.Declares(strconv.Itoa(c)) {
			want = append(want, c)
		}
	}
	if len(want) == 0 {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	ct, md, ok := JSONMedia(op.Body.Content)
	if !ok {
		return result.Skipped(ReqInvalidBody, CheckInvalidBody, subj, "the body is not JSON"), true
	}
	bad, what, ok := r.Contract.InvalidBody(md)
	if !ok {
		return result.Skipped(ReqInvalidBody, CheckInvalidBody, subj, "the body schema refuses nothing the suite can construct offline"), true
	}
	req := r.newRequest()
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		v, _, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok {
			return result.Skipped(ReqInvalidBody, CheckInvalidBody, subj, "the target file names no existing "+p.Name), true
		}
		req.path[p.Name] = v
	}
	if why := r.fillRequired(op, req, true); why != "" {
		return result.Skipped(ReqInvalidBody, CheckInvalidBody, subj, why), true
	}
	b, err := json.Marshal(bad)
	if err != nil {
		return result.Failed(ReqInvalidBody, CheckInvalidBody, subj, 0, "the invalid body does not encode: "+err.Error(), ""), true
	}
	req.body, req.ctype = b, ct
	if why := r.authorize(ctx, op, req); why != "" {
		return result.Skipped(ReqInvalidBody, CheckInvalidBody, subj, why), true
	}
	o := r.expectRefusal(ctx, op, CheckInvalidBody, req, want, true)
	o.Detail = strings.TrimSpace("sent " + what + "; " + o.Detail)
	return o, true
}

func (r *Runner) success(ctx context.Context, op *Operation) (result.Outcome, bool) {
	if op.Method != http.MethodGet {
		return result.Outcome{}, false
	}
	subj := op.Subject()
	req := r.newRequest()
	guessed := false
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		v, g, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok {
			return result.Skipped(ReqSuccess, CheckSuccess, subj, "the target file names no existing "+p.Name), true
		}
		guessed = guessed || g
		req.path[p.Name] = v
	}
	if why := r.fillRequired(op, req, true); why != "" {
		return result.Skipped(ReqSuccess, CheckSuccess, subj, why), true
	}
	if op.Auth.NeedsCredential() {
		if why := r.authorize(ctx, op, req); why != "" {
			return result.Skipped(ReqSuccess, CheckSuccess, subj, why), true
		}
	}
	if op.WebSocket {
		req.upgrade = true
	}
	obs, err := r.send(ctx, op, req, true)
	if err != nil {
		return result.Failed(ReqSuccess, CheckSuccess, subj, 0, "no answer: "+err.Error(), ""), true
	}
	if op.WebSocket {
		if obs.status == http.StatusSwitchingProtocols {
			return result.Passed(ReqSuccess, CheckSuccess, subj, obs.status, "WebSocket handshake accepted"), true
		}
		return result.Failed(ReqSuccess, CheckSuccess, subj, obs.status, fmt.Sprintf("the WebSocket handshake was answered %d, not 101", obs.status), problemType(obs.body)), true
	}
	if obs.status == http.StatusNotFound && guessed {
		return result.Skipped(ReqSuccess, CheckSuccess, subj, "nothing at the path parameter values the contract enumerates (404 "+problemType(obs.body)+")"), true
	}
	if obs.status < 200 || obs.status > 299 {
		return result.Failed(ReqSuccess, CheckSuccess, subj, obs.status, fmt.Sprintf("answered %d, not a 2xx", obs.status), problemType(obs.body)), true
	}
	if obs.truncated {
		return result.Failed(ReqSuccess, CheckSuccess, subj, obs.status, fmt.Sprintf("the body is above the suite's %d-byte cap", MaxResponseBytes), ""), true
	}
	if !op.Declares(strconv.Itoa(obs.status)) && !op.Declares("2XX") {
		return result.Failed(ReqSuccess, CheckSuccess, subj, obs.status, fmt.Sprintf("%d is not a status the contract declares", obs.status), ""), true
	}
	if err := r.Contract.ValidateResponse(op, obs.status, obs.header.Get("Content-Type"), obs.body); err != nil {
		return result.Failed(ReqSuccess, CheckSuccess, subj, obs.status, "the response does not match the contract", short(err.Error())), true
	}
	return result.Passed(ReqSuccess, CheckSuccess, subj, obs.status, "schema-valid "+obs.header.Get("Content-Type")), true
}

// expectRefusal sends req and checks that the answer is one of want
// with a problem/v1 body that also matches what the operation declares
// for that status; withErrors also requires a non-empty errors[].
func (r *Runner) expectRefusal(ctx context.Context, op *Operation, check string, req *request, want []int, withErrors bool) result.Outcome {
	subj := op.Subject()
	reqID := RequirementOf(check)
	obs, err := r.send(ctx, op, req, isRead(op) || check == CheckUnauthenticated || check == CheckWrongScope)
	if err != nil {
		return result.Failed(reqID, check, subj, 0, "no answer: "+err.Error(), "")
	}
	if obs.status == http.StatusServiceUnavailable && !slices.Contains(want, obs.status) && op.DeclaresOrDefault("503") {
		// The operation is switched off on this target (a console not
		// configured): the refusal under test was not observed, which is
		// neither a pass nor a failure of it. The 503 itself must still
		// be the declared error body.
		if why := r.problemFault(op, obs, false); why != "" {
			return result.Failed(reqID, check, subj, obs.status, "answered 503 with a body that is not the declared problem: "+why, short(string(obs.body)))
		}
		return result.Skipped(reqID, check, subj, "the operation is unavailable on this target (503 "+problemType(obs.body)+"): its "+strings.ReplaceAll(check, "_", " ")+" refusal was not observed")
	}
	if !slices.Contains(want, obs.status) {
		return result.Failed(reqID, check, subj, obs.status, fmt.Sprintf("answered %d, want %s", obs.status, joinInts(want)), problemType(obs.body))
	}
	if why := r.problemFault(op, obs, withErrors); why != "" {
		return result.Failed(reqID, check, subj, obs.status, why, short(string(obs.body)))
	}
	return result.Passed(reqID, check, subj, obs.status, problemType(obs.body))
}

// problemFault says what is wrong with a refusal body, empty when it is
// a problem/v1 document of the right media type that matches the
// operation's declaration.
func (r *Runner) problemFault(op *Operation, obs *observed, withErrors bool) string {
	ct := obs.header.Get("Content-Type")
	if op.Method == http.MethodHead {
		return ""
	}
	if !declaresProblem(op, obs.status) {
		// A standard endpoint (F3411, F3548) answers its standard's
		// error body, not problem/v1 (02 §1 covers national APIs).
		if err := r.Contract.ValidateResponse(op, obs.status, ct, obs.body); err != nil && !errors.Is(err, ErrNotDeclared) {
			return "the body does not match the contract: " + short(err.Error())
		}
		return ""
	}
	if mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])); mt != ProblemMediaType {
		return fmt.Sprintf("Content-Type %q, want %s", ct, ProblemMediaType)
	}
	if obs.truncated {
		return "the problem body is above the suite's cap"
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(obs.body))
	if err != nil {
		return "the problem body is not JSON: " + err.Error()
	}
	if r.Problem != nil {
		if err := r.Problem.Validate(doc); err != nil {
			return "not a problem/v1 body: " + short(err.Error())
		}
	}
	if err := r.Contract.ValidateResponse(op, obs.status, ct, obs.body); err != nil && !errors.Is(err, ErrNotDeclared) {
		return "the body does not match the contract: " + short(err.Error())
	}
	m, _ := doc.(map[string]any)
	if st, ok := m["status"].(json.Number); ok && st.String() != strconv.Itoa(obs.status) {
		return fmt.Sprintf("the body's status %s is not the response's %d", st, obs.status)
	}
	if withErrors {
		if l, _ := m["errors"].([]any); len(l) == 0 {
			return "errors[] is empty: the refusal names no field"
		}
	}
	return ""
}

// declaresProblem reports whether the response the operation declares
// for status (exactly, else default) carries a problem/v1 body.
func declaresProblem(op *Operation, status int) bool {
	resp, ok := op.Responses[strconv.Itoa(status)]
	if !ok {
		resp, ok = op.Responses["default"]
	}
	if !ok {
		return true
	}
	_, has := resp.Content[ProblemMediaType]
	return has
}

func (r *Runner) newRequest() *request {
	return &request{path: map[string]string{}, query: url.Values{}, header: http.Header{}}
}

// baseRequest is a request that would reach the operation but for its
// credential: path parameters (known or unknown values), required
// headers and query parameters, a body when one is required.
func (r *Runner) baseRequest(op *Operation, strict bool) (*request, string) {
	req := r.newRequest()
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		v, _, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok {
			v, ok = r.Contract.UnknownValue(p)
		}
		if !ok {
			return nil, "no value for path parameter " + p.Name
		}
		req.path[p.Name] = v
	}
	if why := r.fillRequired(op, req, strict); why != "" {
		return nil, why
	}
	if op.Body != nil {
		if ct, md, ok := JSONMedia(op.Body.Content); ok {
			v, has := r.Contract.ExampleBody(md)
			if !has {
				v = map[string]any{}
			}
			b, err := json.Marshal(v)
			if err != nil {
				return nil, "the example body does not encode"
			}
			req.body, req.ctype = b, ct
		} else {
			for _, ct := range sortedKeys(op.Body.Content) {
				req.ctype = ct
				break
			}
		}
	}
	if op.WebSocket {
		req.upgrade = true
	}
	return req, ""
}

// fillRequired sets the required query and header parameters. strict
// makes a parameter without a usable value an error (the request must
// be well-formed); otherwise it is left out.
func (r *Runner) fillRequired(op *Operation, req *request, strict bool) string {
	for _, p := range op.Params {
		if !p.Required || p.In == "path" {
			continue
		}
		var v string
		switch {
		case p.In == "header" && strings.EqualFold(p.Name, "Idempotency-Key"):
			v = "conformance-" + ulid.Make(time.Now())
		case p.In == "header" && strings.EqualFold(p.Name, "If-Match"):
			continue
		default:
			val, _, ok := r.Contract.KnownValue(p, r.Fixtures)
			if !ok {
				if strict {
					return "no value for required " + p.In + " parameter " + p.Name
				}
				continue
			}
			v = val
		}
		switch p.In {
		case "query":
			req.query.Set(p.Name, v)
		case "header":
			req.header.Set(p.Name, v)
		case "cookie":
			req.header.Add("Cookie", p.Name+"="+v)
		}
	}
	return ""
}

// authorize adds an accepted credential, or says why there is none.
func (r *Runner) authorize(ctx context.Context, op *Operation, req *request) string {
	a := op.Auth
	if a.Public {
		return ""
	}
	if t := a.Token; t != nil {
		if t.MTLS && !r.Creds.ClientCertificate() {
			if !a.Session {
				return "the operation needs a client certificate bound to the token and the target file supplies none"
			}
		} else {
			var scopes []string
			if len(t.Alternatives) > 0 {
				scopes = t.Alternatives[0]
			}
			tok, err := r.Creds.Token(ctx, t.Issuer, scopes)
			if err == nil {
				req.header.Set("Authorization", "Bearer "+tok)
				return ""
			}
			if !a.Session {
				return credentialReason(t.Issuer+" token "+scopeWords(scopes), err)
			}
		}
	}
	if a.Session {
		s, err := r.Creds.Session(ctx)
		if err != nil {
			return credentialReason("console or portal session", err)
		}
		req.header.Set("Authorization", "Bearer "+s)
		return ""
	}
	return "the operation takes " + a.Special + ", which the suite does not mint"
}

func scopeWords(scopes []string) string {
	if len(scopes) == 0 {
		return "(the contract states no scope)"
	}
	return "with " + strings.Join(scopes, " ")
}

func credentialReason(what string, err error) string {
	if errors.Is(err, ErrNoCredential) {
		return "the target file supplies no " + what
	}
	return "could not obtain " + what + ": " + short(err.Error())
}

// send issues one request (more only after a 429 with a short
// Retry-After when retry is set) and reads a bounded answer.
func (r *Runner) send(ctx context.Context, op *Operation, req *request, retry bool) (*observed, error) {
	target := strings.TrimRight(r.BaseURL, "/") + expandPath(op.Path, req.path)
	if len(req.query) > 0 {
		target += "?" + req.query.Encode()
	}
	for attempt := 0; ; attempt++ {
		obs, err := r.once(ctx, op.Method, target, req)
		if err != nil {
			return nil, err
		}
		if obs.status != http.StatusTooManyRequests || !retry || attempt >= MaxRetries {
			return obs, nil
		}
		wait, ok := retryAfter(obs.header.Get("Retry-After"))
		if !ok || wait > MaxRetryAfter {
			return obs, nil
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func (r *Runner) once(ctx context.Context, method, target string, req *request) (*observed, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	var body io.Reader
	if req.body != nil {
		body = bytes.NewReader(req.body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range req.header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	// The declared media type goes with the request even when the suite
	// has no body to send in it (a compact JWS it cannot forge): a
	// system answers a request without its Content-Type 415 before it
	// looks at the credential, which would leave the check unobserved.
	if req.ctype != "" {
		hr.Header.Set("Content-Type", req.ctype)
	}
	hr.Header.Set("Accept", "application/json, application/geo+json, application/problem+json;q=0.9, */*;q=0.5")
	if req.upgrade {
		key := make([]byte, 16)
		_, _ = rand.Read(key)
		hr.Header.Set("Connection", "Upgrade")
		hr.Header.Set("Upgrade", "websocket")
		hr.Header.Set("Sec-WebSocket-Version", "13")
		hr.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(key))
		// No Origin: the suite is a machine client, not a browser (M22
		// holds browsers to an Origin allow-list).
	}
	hc := r.HTTP
	if hc == nil {
		hc = NewHTTPClient(nil)
	}
	resp, err := hc.Do(hr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	obs := &observed{status: resp.StatusCode, header: resp.Header}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return obs, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	if len(b) > MaxResponseBytes {
		b, obs.truncated = b[:MaxResponseBytes], true
	}
	obs.body = b
	return obs, nil
}

// NewHTTPClient is the suite's client: no redirects followed (a
// redirect is an answer the suite reports), the request timeout on
// every call. tr may carry a client certificate; nil is the default
// transport.
func NewHTTPClient(tr http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: tr,
		Timeout:   RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func expandPath(p string, values map[string]string) string {
	for k, v := range values {
		p = strings.ReplaceAll(p, "{"+k+"}", url.PathEscape(v))
	}
	return p
}

func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}
	return 0, false
}

func isRead(op *Operation) bool {
	return op.Method == http.MethodGet || op.Method == http.MethodHead
}

func hasEnum(p Param) bool {
	if p.Schema == nil {
		return false
	}
	_, ok := p.Schema["enum"]
	return ok
}

// problemType is the type member of a problem body, for the detail.
func problemType(b []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(b, &m) != nil || m.Type == "" {
		return ""
	}
	return m.Type
}

func joinInts(l []int) string {
	s := make([]string, 0, len(l))
	for _, v := range l {
		s = append(s, strconv.Itoa(v))
	}
	return strings.Join(s, " or ")
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

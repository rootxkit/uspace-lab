package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

// The domain "disable": 05 §6 "One source type or instance (disabled or
// dead)", the disabled case, which SC-08 says is an audited act by a
// person. The harness switches the source type off and on again through
// the authority's console API, with the admin session the targets file
// names (requests.authority, as SC-08's scenario does), and judges
// three things beyond the row's expectations: the switch is seen from
// the authority's side (GET /v1/sources, never the PUT's word alone),
// and each switch is in the audit log as an act of that person.

// kindDisabled is a source type the authority holds switched off.
const kindDisabled = "disabled"

// domainDisable is the domain the harness runs itself, with no script.
const domainDisable = "disable"

// adminAPI is the authority's console API as the admin session reaches
// it.
type adminAPI struct {
	base, session, csrf string
	hc                  *http.Client
	// run names the chaos run in each switch's reason.
	run string
}

// newAdminAPI reads requests.authority of the targets file.
func newAdminAPI(targets string, hc *http.Client, run string) (*adminAPI, error) {
	tg, err := runner.LoadTargets(targets)
	if err != nil {
		return nil, err
	}
	ra, ok := tg.Requests["authority"]
	if !ok || ra.BaseURL == "" || ra.SessionFile == "" {
		return nil, fmt.Errorf("chaos: the disable row needs requests.authority (base URL and the admin session) in %s", targets)
	}
	a := &adminAPI{base: strings.TrimRight(ra.BaseURL, "/"), hc: hc, run: run}
	if a.session, err = tg.Secret(ra.SessionFile); err != nil {
		return nil, err
	}
	if ra.CSRFFile != "" {
		if a.csrf, err = tg.Secret(ra.CSRFFile); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// do sends one request as the admin (the session as bearer and cookie,
// the CSRF pair, as the console's BFF forwards it).
func (a *adminAPI) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+a.session)
	cookie := "uspace_session=" + a.session
	if a.csrf != "" {
		cookie += "; uspace_csrf=" + a.csrf
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	req.Header.Set("Cookie", cookie)
	resp, err := a.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	return resp.StatusCode, b, err
}

// reason is what a switch of this run says.
func (a *adminAPI) reason(source, action string) string {
	return fmt.Sprintf("uspace-lab chaos %s: %s %s", a.run, source, action)
}

// switchRun switches source off (inject) or on (restore), as a script
// run: exit 0 only when the authority answered 200 and says it changed
// the switch.
// The hold is counted from End, so it is set on every return (a named
// result: a deferred write to a local is lost once it is returned).
func (a *adminAPI) switchRun(ctx context.Context, action string, args []string) (sr ScriptRun) {
	sr = ScriptRun{Start: time.Now().UTC(), ExitCode: 1}
	defer func() { sr.End = time.Now().UTC() }()
	if len(args) != 1 || (action != "inject" && action != "restore") {
		sr.Output = []string{"disable takes inject|restore SOURCE_TYPE"}
		return sr
	}
	on := action == "restore"
	sr.Command = fmt.Sprintf("PUT /v1/sources/%s {enabled: %t}", args[0], on)
	code, b, err := a.do(ctx, http.MethodPut, "/v1/sources/"+url.PathEscape(args[0]), map[string]any{"enabled": on, "reason": a.reason(args[0], action)})
	if err != nil {
		sr.Output = []string{"could not switch: " + shortErr(err)}
		return sr
	}
	var res struct {
		Changed bool `json:"changed"`
		Control struct {
			Enabled bool   `json:"enabled"`
			Actor   string `json:"actor"`
		} `json:"control"`
	}
	_ = json.Unmarshal(b, &res)
	sr.Output = []string{fmt.Sprintf("chaos: %s %s answered %d: changed %t, enabled %t, by %s", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), sr.Command, code, res.Changed, res.Control.Enabled, res.Control.Actor)}
	if code == http.StatusOK && res.Changed && res.Control.Enabled == on {
		sr.ExitCode = 0
	} else if code != http.StatusOK {
		sr.Output = append(sr.Output, bounded(string(b)))
	}
	return sr
}

// enabled is whether the authority holds source switched on, from its
// own list of switches (GET /v1/sources): the type's own switch, else
// its default.
func (a *adminAPI) enabled(ctx context.Context, source string) (bool, error) {
	code, b, err := a.do(ctx, http.MethodGet, "/v1/sources", nil)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, fmt.Errorf("GET /v1/sources answered %d: %s", code, bounded(string(b)))
	}
	var ov struct {
		DefaultDeny bool `json:"default_deny"`
		Controls    []struct {
			SourceType string `json:"source_type"`
			InstanceID string `json:"instance_id"`
			Enabled    bool   `json:"enabled"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(b, &ov); err != nil {
		return false, fmt.Errorf("GET /v1/sources: %w", err)
	}
	for _, c := range ov.Controls {
		if c.SourceType == source && c.InstanceID == "" {
			return c.Enabled, nil
		}
	}
	return !ov.DefaultDeny, nil
}

// AuditResult is what the audit log says of a disable row's switches.
type AuditResult struct {
	// Events are the source switch events of this row's reason.
	Events int      `json:"events"`
	Actors []string `json:"actors,omitempty"`
	Pass   bool     `json:"pass"`
	Note   string   `json:"note"`
}

// audited reads the audit log for the row's two switches (off, then on)
// as acts of a person: event source_disabled, entity the source type,
// actor a user, the payload naming this run's reason.
func (a *adminAPI) audited(ctx context.Context, source string, from, to time.Time) AuditResult {
	q := url.Values{"event_type": {"source_disabled"}, "from": {from.Add(-5 * time.Second).UTC().Format(time.RFC3339)},
		"to": {to.Add(5 * time.Second).UTC().Format(time.RFC3339)}, "limit": {"100"}, "purpose": {"uspace-lab chaos: the disable row's audit check"}}
	code, b, err := a.do(ctx, http.MethodGet, "/v1/audit/events?"+q.Encode(), nil)
	if err != nil {
		return AuditResult{Note: "the audit log could not be read: " + shortErr(err)}
	}
	if code != http.StatusOK {
		return AuditResult{Note: fmt.Sprintf("GET /v1/audit/events answered %d: %s", code, bounded(string(b)))}
	}
	var page struct {
		Events []struct {
			ActorType string          `json:"actor_type"`
			ActorID   string          `json:"actor_id"`
			EntityID  string          `json:"entity_id"`
			Payload   json.RawMessage `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return AuditResult{Note: "the audit log answered no page: " + err.Error()}
	}
	res := AuditResult{}
	offOn := map[string]bool{}
	for _, e := range page.Events {
		p := string(e.Payload)
		for _, action := range []string{"inject", "restore"} {
			if !strings.Contains(p, a.reason(source, action)) {
				continue
			}
			res.Events++
			if e.ActorType == "user" && e.ActorID != "" {
				offOn[action] = true
				res.Actors = append(res.Actors, e.ActorID)
			}
		}
	}
	switch {
	case !offOn["inject"]:
		res.Note = fmt.Sprintf("no audit event of a person switching %s off with this run's reason (%d source_disabled events in the window)", source, len(page.Events))
	case !offOn["restore"]:
		res.Note = fmt.Sprintf("no audit event of a person switching %s on again (%d source_disabled events in the window)", source, len(page.Events))
	default:
		res.Pass = true
		res.Note = fmt.Sprintf("both switches audited as acts of %s", strings.Join(res.Actors, ", "))
	}
	return res
}

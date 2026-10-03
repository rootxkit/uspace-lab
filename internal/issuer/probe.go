package issuer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The F3548 request the probe sends, with the field names and constants
// read from the InterUSS DSS at the commit deploy/dss/SOURCE pins
// (pkg/api/scdv1/types.gen.go: QueryOperationalIntentReferenceParameters,
// Volume4D, Volume3D, Circle, LatLngPoint, Radius, Altitude, Time;
// pkg/scd/models/conversions.go: TimeFormatRFC3339 = "RFC3339",
// UnitsM = "M", ReferenceW84 = "W84"; route
// POST /dss/v1/operational_intent_references/query in
// pkg/api/scdv1/server.gen.go, scopes utm.strategic_coordination or
// utm.conformance_monitoring_sa in interface.gen.go). Never from memory
// (LESSONS E-03, E-04); TestProbeBodyFieldNames pins the JSON.
const (
	// QueryPath is the operational intent reference search.
	QueryPath = "/dss/v1/operational_intent_references/query"
	// ProbeScope is the scope the probe's token carries.
	ProbeScope = "utm.strategic_coordination"
)

type dssQuery struct {
	AreaOfInterest dssVolume4D `json:"area_of_interest"`
}

type dssVolume4D struct {
	Volume    dssVolume3D `json:"volume"`
	TimeStart dssTime     `json:"time_start"`
	TimeEnd   dssTime     `json:"time_end"`
}

type dssVolume3D struct {
	OutlineCircle dssCircle   `json:"outline_circle"`
	AltitudeLower dssAltitude `json:"altitude_lower"`
	AltitudeUpper dssAltitude `json:"altitude_upper"`
}

type dssCircle struct {
	Center dssLatLng `json:"center"`
	Radius dssRadius `json:"radius"`
}

type dssLatLng struct {
	Lng float64 `json:"lng"`
	Lat float64 `json:"lat"`
}

type dssRadius struct {
	Value float64 `json:"value"`
	Units string  `json:"units"`
}

type dssAltitude struct {
	Value     float64 `json:"value"`
	Reference string  `json:"reference"`
	Units     string  `json:"units"`
}

type dssTime struct {
	Value  string `json:"value"`
	Format string `json:"format"`
}

// ProbeArea is the area the probe searches: configuration, never a
// constant in code (INV-03).
type ProbeArea struct {
	LatDeg, LngDeg float64
	RadiusM        float64
	AltLowerWGS84M float64
	AltUpperWGS84M float64
	Window         time.Duration
}

// Validate refuses an area the DSS would refuse or that is meaningless.
func (a ProbeArea) Validate() error {
	switch {
	case a.LatDeg < -90 || a.LatDeg > 90:
		return fmt.Errorf("probe latitude %v is outside -90..90", a.LatDeg)
	case a.LngDeg < -180 || a.LngDeg > 180:
		return fmt.Errorf("probe longitude %v is outside -180..180", a.LngDeg)
	case a.RadiusM <= 0:
		return fmt.Errorf("probe radius %v m is not positive", a.RadiusM)
	case a.AltLowerWGS84M >= a.AltUpperWGS84M:
		return fmt.Errorf("probe altitude band %v..%v m is empty", a.AltLowerWGS84M, a.AltUpperWGS84M)
	case a.Window <= 0:
		return fmt.Errorf("probe window %s is not positive", a.Window)
	}
	return nil
}

func (a ProbeArea) body(now time.Time) ([]byte, error) {
	now = now.UTC()
	q := dssQuery{AreaOfInterest: dssVolume4D{
		Volume: dssVolume3D{
			OutlineCircle: dssCircle{
				Center: dssLatLng{Lng: a.LngDeg, Lat: a.LatDeg},
				Radius: dssRadius{Value: a.RadiusM, Units: "M"},
			},
			AltitudeLower: dssAltitude{Value: a.AltLowerWGS84M, Reference: "W84", Units: "M"},
			AltitudeUpper: dssAltitude{Value: a.AltUpperWGS84M, Reference: "W84", Units: "M"},
		},
		TimeStart: dssTime{Value: now.Format(time.RFC3339), Format: "RFC3339"},
		TimeEnd:   dssTime{Value: now.Add(a.Window).Format(time.RFC3339), Format: "RFC3339"},
	}}
	return json.Marshal(q)
}

// ProbeConfig configures Probe.
type ProbeConfig struct {
	IssuerURL string
	DSSURL    string
	ClientID  string
	Secret    string
	Area      ProbeArea
	// WrongAudience is a host the DSS must not accept.
	WrongAudience string
	HTTP          *http.Client
	Now           func() time.Time
	Out           io.Writer
}

// maxProbeBody bounds every response the probe reads.
const maxProbeBody = 1 << 20

// Probe proves the DSS and the issuer together (WP-L2, LESSONS E-02):
//
//  1. the search without a token is refused with 401, so a 200 later is
//     known to come from the token;
//  2. a token for the DSS host with ProbeScope is issued and the search
//     with it answers 200 with an operational_intent_references list;
//  3. a token for WrongAudience is refused by the DSS with 401, so the
//     accepted audience list is in force.
//
// It writes one line per step and returns an error naming every step
// whose answer was not the expected one.
func Probe(ctx context.Context, c ProbeConfig) error {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if err := c.Area.Validate(); err != nil {
		return err
	}
	du, err := url.Parse(c.DSSURL)
	if err != nil || du.Hostname() == "" {
		return fmt.Errorf("DSS URL %q has no host", c.DSSURL)
	}
	dssHost := strings.ToLower(du.Hostname())
	if c.WrongAudience == "" || c.WrongAudience == dssHost {
		return fmt.Errorf("the wrong audience %q must be set and differ from the DSS host %q", c.WrongAudience, dssHost)
	}
	body, err := c.Area.body(c.Now())
	if err != nil {
		return err
	}
	queryURL := strings.TrimRight(c.DSSURL, "/") + QueryPath
	var failed []string
	report := func(step string, got, want int, extra string) {
		verdict := "ok"
		if got != want {
			verdict = "FAILED"
			failed = append(failed, step)
		}
		_, _ = fmt.Fprintf(c.Out, "%-6s %s -> HTTP %d (want %d)%s\n", verdict, step, got, want, extra)
	}

	code, _, err := c.post(ctx, queryURL, "", body)
	if err != nil {
		return fmt.Errorf("refusal step: %w", err)
	}
	report("POST "+QueryPath+" without a token", code, http.StatusUnauthorized, "")

	tok, err := c.token(ctx, dssHost)
	if err != nil {
		return err
	}
	code, resp, err := c.post(ctx, queryURL, tok, body)
	if err != nil {
		return fmt.Errorf("success step: %w", err)
	}
	extra := ""
	if code == http.StatusOK {
		var r struct {
			OIRs *[]json.RawMessage `json:"operational_intent_references"`
		}
		if json.Unmarshal(resp, &r) != nil || r.OIRs == nil {
			code = -1
			extra = ", body has no operational_intent_references list"
		} else {
			extra = fmt.Sprintf(", %d operational intent references", len(*r.OIRs))
		}
	}
	report(fmt.Sprintf("POST %s as %s, aud %s, scope %s", QueryPath, c.ClientID, dssHost, ProbeScope), code, http.StatusOK, extra)

	wrong, err := c.token(ctx, c.WrongAudience)
	if err != nil {
		return err
	}
	code, _, err = c.post(ctx, queryURL, wrong, body)
	if err != nil {
		return fmt.Errorf("audience step: %w", err)
	}
	report(fmt.Sprintf("POST %s with aud %s", QueryPath, c.WrongAudience), code, http.StatusUnauthorized, "")

	if len(failed) > 0 {
		return fmt.Errorf("probe failed: %s", strings.Join(failed, "; "))
	}
	return nil
}

func (c ProbeConfig) token(ctx context.Context, aud string) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {ProbeScope}, "audience": {aud}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.IssuerURL, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(c.ClientID), url.QueryEscape(c.Secret))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return "", fmt.Errorf("token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request for aud %s: HTTP %d: %s", aud, resp.StatusCode, bytes.TrimSpace(b))
	}
	var tr tokenResponse
	if err := json.Unmarshal(b, &tr); err != nil || tr.AccessToken == "" {
		return "", errors.New("token response has no access_token")
	}
	return tr.AccessToken, nil
}

func (c ProbeConfig) post(ctx context.Context, u, token string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, b, nil
}

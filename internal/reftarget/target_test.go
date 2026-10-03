package reftarget

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/alerting"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

func newTarget(t *testing.T) (*Target, *httptest.Server) {
	t.Helper()
	pol, err := scenario.LoadPolicy(wiretest.Root() + "/scenarios/policy/demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tg, err := New(ctx, Config{Policy: pol, Audience: "ref.test", Geoid: geoidx.Constant(15.9), ConsoleToken: "console", MaxLiveHz: 1000,
		Clients: []Client{
			{ID: "op-a", Secret: "sa", Serials: []string{"A1"}, Scopes: []string{ScopeTelemetry, ScopeIntents, ScopeTraffic}},
			{ID: "op-x", Secret: "sx", Serials: []string{"X1"}, Scopes: []string{ScopeIntents}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(tg.Handler())
	t.Cleanup(srv.Close)
	return tg, srv
}

func token(t *testing.T, srv *httptest.Server, id, secret string) string {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/oauth/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&tr) != nil {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	return tr.AccessToken
}

func TestRoutesFailClosed(t *testing.T) {
	_, srv := newTarget(t)
	ws := strings.Replace(srv.URL, "http", "ws", 1)
	dial := func(path, tok string) int {
		h := http.Header{}
		if tok != "" {
			h.Set("Authorization", "Bearer "+tok)
		}
		c, resp, err := websocket.Dial(context.Background(), ws+path, &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			_ = c.CloseNow()
			return http.StatusSwitchingProtocols
		}
		return resp.StatusCode
	}
	if got := dial("/v1/telemetry", ""); got != http.StatusUnauthorized {
		t.Errorf("no token: %d", got)
	}
	if got := dial("/v1/telemetry", "forged"); got != http.StatusUnauthorized {
		t.Errorf("forged token: %d", got)
	}
	if got := dial("/v1/telemetry", token(t, srv, "op-x", "sx")); got != http.StatusForbidden {
		t.Errorf("token without ussp.telemetry: %d", got)
	}
	// Presence: the right token upgrades.
	if got := dial("/v1/telemetry", token(t, srv, "op-a", "sa")); got != http.StatusSwitchingProtocols {
		t.Errorf("the right token: %d", got)
	}
	resp, _ := http.PostForm(srv.URL+"/oauth/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {"op-a"}, "client_secret": {"wrong"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/nowhere")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown route: %d", resp.StatusCode)
	}
	// The picture upgrades and closes 4401 without a session (M22).
	c, _, err := websocket.Dial(context.Background(), ws+"/v1/picture/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(context.Background())
	if websocket.CloseStatus(err) != 4401 {
		t.Fatalf("picture without a session: %v", err)
	}
}

// Two flights 20 m apart stream; core's monitor raises proximity for both,
// one alert per flight naming the other, valid alert/v1; a flight landing
// clears it.
func TestProximityFromTelemetry(t *testing.T) {
	tg, srv := newTarget(t)
	tg.mu.Lock()
	tg.clients["op-a"] = Client{ID: "op-a", Secret: "sa", Serials: []string{"A1", "A2"}, Scopes: []string{ScopeTelemetry, ScopeIntents, ScopeTraffic}}
	tg.serials["A2"] = "op-a"
	tg.mu.Unlock()
	tok := token(t, srv, "op-a", "sa")
	// File and activate an intent for A1 to open its alert stream.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/intents", strings.NewReader(`{"client_ref":"r1","uas_serial":"A1","volumes":[{}]}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("intent: %v %v", resp, err)
	}
	var d map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&d)
	ws := strings.Replace(srv.URL, "http", "ws", 1)
	h := http.Header{"Authorization": {"Bearer " + tok}}
	alerts, _, err := websocket.Dial(context.Background(), ws+"/v1/alerts?intent_id="+d["intent_id"].(string), &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer alerts.CloseNow()
	tel, _, err := websocket.Dial(context.Background(), ws+"/v1/telemetry", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.CloseNow()
	send := func(serial string, seq int, lat float64, status string) {
		body := map[string]any{"ts": wire.Format(time.Now()), "serial": serial, "seq": seq, "epoch": "e", "position": map[string]any{"lat": lat, "lng": 44.8271},
			"alt_wgs84_m": 650.9, "height_m": 30, "height_ref": "TakeoffLocation", "speed_ms": 0, "track_deg": nil, "vspeed_ms": 0,
			"status": status, "emergency": false, "accuracy_h": "HA10m", "accuracy_v": "VA10m", "timestamp_accuracy_s": nil}
		b, _ := json.Marshal(map[string]any{"schema": "telemetry/v1", "body": body})
		if err := tel.Write(context.Background(), websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		send("A1", i, 41.7151, "Airborne")
		send("A2", i, 41.71528, "Airborne") // 20 m north
	}
	raised := readAlert(t, alerts, func(b map[string]any) bool { return b["state"] == "raised" })
	wiretest.Validate(t, "ussp/alert-v1.json", raised)
	body := raised["body"].(map[string]any)
	if body["kind"] != "proximity" || body["detail"].(map[string]any)["peer"].(map[string]any)["track_id"] == "" {
		t.Fatalf("%v", body)
	}
	// Landing clears it (alerting.ClearLanded).
	send("A1", 3, 41.7151, "Ground")
	cleared := readAlert(t, alerts, func(b map[string]any) bool { return b["state"] == "cleared" })
	wiretest.Validate(t, "ussp/alert-v1.json", cleared)
	if cleared["body"].(map[string]any)["clear_reason"] != "landed" {
		t.Fatalf("%v", cleared["body"])
	}
}

func readAlert(t *testing.T, c *websocket.Conn, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["schema"] != "alert/v1" {
			continue
		}
		if ok(m["body"].(map[string]any)) {
			return m
		}
	}
}

// The violation frames the reference authority sends are uspace-
// authority's violation/v1 (pinned), raised and cleared.
func TestViolationFrameMatchesTheAuthoritySchema(t *testing.T) {
	tg, _ := newTarget(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	aa := &activeAlert{system: "authority", key: "authority|zone:GEO:Z1:A1", kind: "zone_incursion", id: "01J00000000000000000000000",
		flight: &flight{ID: "A1"}, raisedAt: now, captured: now,
		alert: alerting.Alert{Key: "zone:GEO:Z1:A1", Kind: "zone", Severity: "critical", Aircraft: []string{"A1"},
			Detail: map[string]any{"identifier": "Z1", "restriction": "PROHIBITED", "vertical_known": true}}}
	tg.mu.Lock()
	tg.trackPos["A1"] = core.LatLon{LatDeg: 41.7151, LonDeg: 44.8271}
	raised := tg.violationFrameLocked(aa, "raised", "", nil, now)
	cleared := tg.violationFrameLocked(aa, "cleared", "resolved", map[string]any{"x": 1}, now.Add(time.Second))
	tg.mu.Unlock()
	for _, b := range [][]byte{raised, cleared} {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		wiretest.Validate(t, "authority/violation-v1.json", m)
	}
}

package reftarget

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/odid"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

// newRIDTarget is a reference target with one receiver, served.
func newRIDTarget(t *testing.T) (tg *Target, base, bearer string, key []byte) {
	t.Helper()
	pol, err := scenario.LoadPolicy(wiretest.Root() + "/scenarios/policy/demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	key = []byte("0123456789abcdef0123456789abcdef")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tg, err = New(ctx, Config{Policy: pol, Audience: "ref.test", Geoid: geoidx.Constant(15.9), ConsoleToken: "console",
		Receivers: []Receiver{{ID: "rx-1", BearerKey: "bearer-1", HMACKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(tg.Handler())
	t.Cleanup(srv.Close)
	return tg, srv.URL, "bearer-1", key
}

// postLocation reports one Location of transmitter mac at lat (with a
// Basic ID carrying serial when serial is set) as receiver rx-1.
func postLocation(t *testing.T, base, bearer string, key []byte, mac, serial string, lat float64, nonce int) {
	t.Helper()
	now := time.Now().UTC()
	sah := float64(now.Minute()*60+now.Second()) + float64(now.Nanosecond()/1e8)/10
	la, lo, hae, spd, dir, vs := lat, 44.8271, 680.0, 5.0, 90.0, 0.0
	msgs := []odid.Message{odid.Location{Status: odid.StatusAirborne, LatDeg: &la, LonDeg: &lo, AltHAEM: &hae,
		SpeedHorizontalMS: &spd, DirectionDeg: &dir, SpeedVerticalMS: &vs, SecondsAfterHour: &sah}}
	if serial != "" {
		msgs = append(msgs, odid.BasicID{IDType: 1, UAType: 2, UAID: serial})
	}
	pack, err := odid.EncodePack(msgs)
	if err != nil {
		t.Fatal(err)
	}
	rx := now.Format("2006-01-02T15:04:05.000Z")
	body, _ := json.Marshal(map[string]any{"receiver_id": "rx-1", "sent_at_ms": now.UnixMilli(), "nonce": fmt.Sprint("n", nonce), "backlog": false,
		"observations": []any{map[string]any{"transmitter": mac, "payload_hex": hex.EncodeToString(pack), "rx_ts": rx}}})
	m := hmac.New(sha256.New, key)
	m.Write(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/rid/observations", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Report-Signature", hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("observation answered %d", resp.StatusCode)
	}
}

// frames reads c in the background into a channel: a websocket read
// whose context expires closes the connection, so an absence check
// waits on the channel instead.
func frames(t *testing.T, c *websocket.Conn) <-chan map[string]any {
	t.Helper()
	out := make(chan map[string]any, 64)
	go func() {
		defer close(out)
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				return
			}
			out <- m
		}
	}()
	return out
}

// readUntil returns the first frame want accepts; nil when the deadline
// passes first.
func readUntil(t *testing.T, in <-chan map[string]any, d time.Duration, want func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case m, ok := <-in:
			if !ok {
				return nil
			}
			if want(m) {
				return m
			}
		case <-deadline.C:
			return nil
		}
	}
}

// waitFor waits for cond, which the target's own goroutines make true.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	tk := time.NewTicker(5 * time.Millisecond)
	defer tk.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !cond() {
		select {
		case <-tk.C:
		case <-deadline.C:
			t.Fatal("condition not reached")
		}
	}
}

// The picture sends track/telemetry/v1 only to a console that subscribed
// to tracks with a box holding the track: absence before the subscribe
// and outside the box, presence inside it, and the frame is the common
// schema with only the identification the reference can claim.
func TestPictureTracksFollowTheSubscription(t *testing.T) {
	_, base, bearer, key := newRIDTarget(t)
	ws := strings.Replace(base, "http", "ws", 1)
	c, _, err := websocket.Dial(context.Background(), ws+"/v1/picture/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer console"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	in := frames(t, c)
	isTrack := func(m map[string]any) bool { return m["schema"] == "track/telemetry/v1" }
	if readUntil(t, in, 5*time.Second, func(m map[string]any) bool { return m["schema"] == "console/status/v1" }) == nil {
		t.Fatal("no status on connect")
	}
	postLocation(t, base, bearer, key, "02:00:00:00:00:01", "", 41.7151, 1)
	if got := readUntil(t, in, 2500*time.Millisecond, isTrack); got != nil {
		t.Fatalf("a track before any subscribe: %v", got)
	}
	sub := `{"schema":"console/subscribe/v1","body":{"bbox":[44.8,41.70,44.9,41.73],"layers":["tracks","alerts"]}}`
	if err := c.Write(context.Background(), websocket.MessageText, []byte(sub)); err != nil {
		t.Fatal(err)
	}
	// The subscribe is answered with a snapshot once the view is set.
	snap := readUntil(t, in, 5*time.Second, func(m map[string]any) bool { return m["schema"] == "console/snapshot/v1" })
	if snap == nil {
		t.Fatal("no snapshot after the subscribe")
	}
	if err := wiretest.CommonCheck(t, "https://schemas.uspace.ge/console/snapshot/v1.json", snap); err != nil {
		t.Fatalf("console/snapshot/v1 refuses the snapshot: %v", err)
	}
	postLocation(t, base, bearer, key, "02:00:00:00:00:02", "", 41.80, 2) // outside the box
	postLocation(t, base, bearer, key, "02:00:00:00:00:01", "SERIAL-1", 41.7152, 3)
	got := readUntil(t, in, 5*time.Second, isTrack)
	if got == nil {
		t.Fatal("no track after the subscribe")
	}
	if err := wiretest.CommonCheck(t, "https://schemas.uspace.ge/track/telemetry/v1.json", got); err != nil {
		b, _ := json.Marshal(got)
		t.Fatalf("track/telemetry/v1 refuses the frame: %v\n%s", err, b)
	}
	body := got["body"].(map[string]any)
	if body["track_id"] != "SERIAL-1" || body["source_instance"] != "rx-1" {
		t.Fatalf("the track outside the box was sent, or the wrong one: %v", body)
	}
	if id := body["identification"].(map[string]any); id["status"] != "unknown_operator" || id["reason"] != "registry_unavailable" {
		t.Fatalf("identification claims more than the reference judged: %v", id)
	}
}

// parseSubscribe refuses what console/subscribe/v1 refuses.
func TestParseSubscribe(t *testing.T) {
	for _, bad := range []string{
		`{"schema":"console/subscribe/v1","body":{"bbox":[1,2,3],"layers":[]}}`,
		`{"schema":"console/subscribe/v1","body":{"bbox":[1,95,3,4],"layers":[]}}`,
		`{"schema":"console/subscribe/v1","body":{"bbox":[1,5,3,4],"layers":[]}}`,
		`{"schema":"console/subscribe/v1","body":{"bbox":[1,2,3,4],"layers":["nope"]}}`,
		`{"schema":"console/status/v1","body":{"bbox":[1,2,3,4],"layers":[]}}`,
	} {
		if _, ok := parseSubscribe([]byte(bad)); ok {
			t.Errorf("accepted %s", bad)
		}
	}
	v, ok := parseSubscribe([]byte(`{"schema":"console/subscribe/v1","body":{"bbox":[179,-1,-179,1],"layers":["tracks"]}}`))
	if !ok || !v.tracks {
		t.Fatal("refused a valid subscribe")
	}
	for lon, want := range map[float64]bool{179.5: true, -179.5: true, 0: false} {
		if got := v.holds(core.LatLon{LonDeg: lon}); got != want {
			t.Errorf("antimeridian box holds %v: %v", lon, got)
		}
	}
}

// The traffic stream of an intent carries its flight's traffic/product/v1
// (the pinned uspace-ussp schema) after a live sample; the alert stream
// of the same intent does not.
func TestTrafficProductOnTheTrafficStreamOnly(t *testing.T) {
	tg, srv := newTarget(t)
	tok := token(t, srv, "op-a", "sa")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/intents", strings.NewReader(`{"client_ref":"r1","uas_serial":"A1","volumes":[{}]}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("intent: %v %v", resp, err)
	}
	var d map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&d)
	_ = resp.Body.Close()
	id := d["intent_id"].(string)
	ws := strings.Replace(srv.URL, "http", "ws", 1)
	h := http.Header{"Authorization": {"Bearer " + tok}}
	traffic, _, err := websocket.Dial(context.Background(), ws+"/v1/traffic?intent_id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer traffic.CloseNow()
	trafficIn := frames(t, traffic)
	alerts, _, err := websocket.Dial(context.Background(), ws+"/v1/alerts?intent_id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer alerts.CloseNow()
	alertsIn := frames(t, alerts)
	waitFor(t, func() bool { return tg.alerts.wants("traffic:" + uuidFrom("flight:A1")) })
	tel, _, err := websocket.Dial(context.Background(), ws+"/v1/telemetry", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.CloseNow()
	body := map[string]any{"ts": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "serial": "A1", "seq": 0, "epoch": "e",
		"position": map[string]any{"lat": 41.7151, "lng": 44.8271}, "alt_wgs84_m": 650.9, "height_m": 30, "height_ref": "TakeoffLocation",
		"speed_ms": 3, "track_deg": 90, "vspeed_ms": 0, "status": "Airborne", "emergency": false, "accuracy_h": "HA10m", "accuracy_v": "VA10m",
		"timestamp_accuracy_s": nil}
	b, _ := json.Marshal(map[string]any{"schema": "telemetry/v1", "body": body})
	if err := tel.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	isProduct := func(m map[string]any) bool { return m["schema"] == SchemaTrafficProduct }
	got := readUntil(t, trafficIn, 5*time.Second, isProduct)
	if got == nil {
		t.Fatal("no traffic/product/v1 on the traffic stream")
	}
	wiretest.RegisterByID(t, "ussp/alert-v1.json")
	wiretest.Validate(t, "ussp/traffic-product-v1.json", got)
	tracks := got["body"].(map[string]any)["tracks"].([]any)
	if len(tracks) != 1 || tracks[0].(map[string]any)["own"] != true {
		t.Fatalf("tracks: %v", tracks)
	}
	if extra := readUntil(t, alertsIn, 2500*time.Millisecond, isProduct); extra != nil {
		t.Fatalf("a product on the alert stream: %v", extra)
	}
}

// The windows are swept: a receiver nonce, an observation key and a
// telemetry dedupe key are kept inside their windows and gone after
// them, so a long run's state stops growing.
func TestPruneForgetsWhatLeftItsWindow(t *testing.T) {
	clk := &clock{now: time.Now().UTC()}
	tg, _ := newTargetAt(t, clk.Now)
	tg.mu.Lock()
	tg.nonces["rx-1"] = map[string]time.Time{"n1": clk.Now()}
	tg.seenObs["k1"] = clk.Now()
	fl := tg.flightLocked("A1", "op-a")
	fl.dedupe["e|1"] = dedupeEntry{ts: "t", at: clk.Now()}
	tg.mu.Unlock()
	tg.tick() // inside every window: nothing goes
	tg.mu.Lock()
	kept := len(tg.nonces["rx-1"]) == 1 && len(tg.seenObs) == 1 && len(fl.dedupe) == 1
	tg.mu.Unlock()
	if !kept {
		t.Fatal("a key inside its window was forgotten")
	}
	clk.Advance(dedupeWindow + time.Second)
	tg.tick()
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.nonces["rx-1"]) != 0 || len(tg.seenObs) != 0 || len(fl.dedupe) != 0 {
		t.Fatalf("kept after their windows: %d nonces, %d observations, %d dedupe keys", len(tg.nonces["rx-1"]), len(tg.seenObs), len(fl.dedupe))
	}
}

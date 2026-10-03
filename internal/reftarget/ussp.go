package reftarget

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/alerting"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/ulid"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

const (
	producerUSSP = "lab/reftarget-ussp"
	// maxTelemetryFrame is the USSP's per-message bound (ussp openapi:
	// at most 8 KiB).
	maxTelemetryFrame = 8 << 10
	// maxLiveHz is the live rate above which samples are dropped.
	maxLiveHz = 2.0
	// dedupeWindow is telemetry_dedupe_s (600 s, the client's queue).
	dedupeWindow = 600 * time.Second
	statusEvery  = 2 * time.Second
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type intent struct {
	ID       string
	Client   string
	Serial   string
	State    string
	Version  int
	Request  json.RawMessage
	ClientRF string
}

type flight struct {
	ID          string
	Serial      string
	Client      string
	IntentID    string
	lastLive    time.Time
	airborne    bool
	lastRate    time.Time
	dedupe      map[string]dedupeEntry
	lostLink    *activeAlert
	lastCapture time.Time
}

type dedupeEntry struct {
	ts string
	at time.Time
}

// activeAlert is an alert the target is holding open, republished every
// second while it holds (C-08).
type activeAlert struct {
	system   string // ussp or authority
	id       string
	key      string
	kind     string
	flight   *flight
	peer     *flight
	alert    alerting.Alert
	raisedAt time.Time
	captured time.Time
}

// telemetryBody is the part of telemetry/v1 the target reads. Unknown
// members are refused, as trust and source are (06 T11).
type telemetryBody struct {
	TS        string                      `json:"ts"`
	Serial    string                      `json:"serial"`
	Seq       *int64                      `json:"seq"`
	Epoch     string                      `json:"epoch"`
	Backlog   bool                        `json:"backlog"`
	End       bool                        `json:"end"`
	IntentID  *string                     `json:"-"`
	Position  *struct{ Lat, Lng float64 } `json:"position"`
	AltWGS84M *float64                    `json:"alt_wgs84_m"`
	AltPressM *float64                    `json:"alt_pressure_m"`
	HeightM   *float64                    `json:"height_m"`
	HeightRef *string                     `json:"height_ref"`
	SpeedMS   *float64                    `json:"speed_ms"`
	TrackDeg  *float64                    `json:"track_deg"`
	VSpeedMS  *float64                    `json:"vspeed_ms"`
	Status    string                      `json:"status"`
	Emergency bool                        `json:"emergency"`
	OpPos     json.RawMessage             `json:"operator_position"`
	AccH      string                      `json:"accuracy_h"`
	AccV      string                      `json:"accuracy_v"`
	TSAcc     *float64                    `json:"timestamp_accuracy_s"`
}

// sockState is one telemetry socket's counters (its status extras).
type sockState struct {
	id       string
	client   string
	accepted uint64
	refused  uint64
	dropped  uint64
	backlog  uint64
	outcomes map[string]uint64
	acked    map[string]int64
}

func (s *sockState) outcome(name string) {
	s.outcomes[name]++
	switch name {
	case "accepted":
	case "duplicate":
	case "dropped_rate":
		s.dropped++
	default:
		s.refused++
	}
}

func (t *Target) handleTelemetryWS(w http.ResponseWriter, r *http.Request) {
	cl, ok := t.bearerClaims(w, r, ScopeTelemetry)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxTelemetryFrame)
	ctx := r.Context()
	st := &sockState{id: ulid.Make(t.cfg.Now()), client: cl.Subject, outcomes: map[string]uint64{}, acked: map[string]int64{}}
	writes := make(chan []byte, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(statusEvery)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case b, ok := <-writes:
				if !ok {
					return
				}
				if conn.Write(ctx, websocket.MessageText, b) != nil {
					return
				}
			case <-tk.C:
				if conn.Write(ctx, websocket.MessageText, t.sockStatus(st)) != nil {
					return
				}
			}
		}
	}()
	writes <- t.sockStatus(st)
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			break
		}
		refusedBefore := st.refused
		t.ingestMessage(st, b, false, time.Time{})
		status := t.sockStatus(st)
		if st.refused != refusedBefore {
			select {
			case writes <- status:
			default:
			}
		}
	}
	close(writes)
	<-done
	_ = conn.CloseNow()
}

func (t *Target) sockStatus(st *sockState) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	outcomes := map[string]uint64{}
	for k, v := range st.outcomes {
		outcomes[k] = v
	}
	acked := map[string]int64{}
	for k, v := range st.acked {
		acked[k] = v
	}
	return t.statusFrame(producerUSSP, st.id, t.cfg.Now(), map[string]any{
		"accepted": st.accepted, "refused": st.refused, "dropped": st.dropped,
		"outcomes": outcomes, "backlog": st.backlog, "acked_seq": acked, "rate": 0,
	}, 0)
}

// ingestMessage handles one {"schema","body"} message; sentAt, when
// set, is a batch's sent_at (T-02).
func (t *Target) ingestMessage(st *sockState, msg []byte, batch bool, sentAt time.Time) string {
	var m struct {
		Schema string          `json:"schema"`
		Body   json.RawMessage `json:"body"`
	}
	raw := msg
	if !batch {
		if err := json.Unmarshal(msg, &m); err != nil || m.Schema != "telemetry/v1" {
			t.mu.Lock()
			st.outcome("refused_invalid")
			t.countLocked(groupTelemetry, "refused_invalid")
			t.mu.Unlock()
			return "refused_invalid"
		}
		raw = m.Body
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f telemetryBody
	var withIntent struct {
		IntentID *string `json:"intent_id"`
	}
	_ = json.Unmarshal(raw, &withIntent)
	err := decodeTelemetry(raw, &f)
	f.IntentID = withIntent.IntentID
	rx := t.cfg.Now()
	ts, terr := time.Parse(time.RFC3339Nano, f.TS)
	t.mu.Lock()
	defer t.mu.Unlock()
	refuse := func(o string) string {
		st.outcome(o)
		t.countLocked(groupTelemetry, o)
		return o
	}
	switch {
	case err != nil || terr != nil || f.Seq == nil || f.Position == nil || f.Serial == "" ||
		f.Position.Lat < -90 || f.Position.Lat > 90 || f.Position.Lng < -180 || f.Position.Lng > 180:
		return refuse("refused_invalid")
	case t.serials[f.Serial] != st.client:
		return refuse("refused_unbound")
	}
	fl := t.flightLocked(f.Serial, st.client)
	key := fmt.Sprintf("%s|%d", f.Epoch, *f.Seq)
	if d, ok := fl.dedupe[key]; ok && d.ts == f.TS && rx.Sub(d.at) < dedupeWindow {
		st.acked[f.Serial] = max(st.acked[f.Serial], *f.Seq)
		return refuse("duplicate")
	}
	if !f.Backlog && !fl.lastRate.IsZero() && rx.Sub(fl.lastRate).Seconds() < 1/t.cfg.MaxLiveHz*0.9 {
		st.acked[f.Serial] = max(st.acked[f.Serial], *f.Seq)
		return refuse("dropped_rate")
	}
	fl.dedupe[key] = dedupeEntry{ts: f.TS, at: rx}
	if len(fl.dedupe) > 4096 {
		for k, d := range fl.dedupe {
			if rx.Sub(d.at) > dedupeWindow {
				delete(fl.dedupe, k)
			}
		}
	}
	st.accepted++
	st.outcomes["accepted"]++
	st.acked[f.Serial] = max(st.acked[f.Serial], *f.Seq)
	t.countLocked(groupTelemetry, "accepted")
	if f.Backlog {
		st.backlog++
		t.countLocked(groupTelemetry, "backlog")
	}
	// Placement: a live sample at receipt; a batch sample at receipt -
	// (sent_at - ts) (T-02). Backlog is recorded, never judged (T-04).
	captured := rx
	if batch && !sentAt.IsZero() {
		captured = rx.Add(-sentAt.Sub(ts))
		if captured.After(rx) {
			captured = rx
		}
	}
	if !f.Backlog {
		fl.lastRate = rx
		fl.lastLive = rx
		fl.lastCapture = captured
	}
	flying := f.Status == "Airborne" || f.Status == "Emergency"
	fl.airborne = flying
	if f.IntentID != nil {
		fl.IntentID = *f.IntentID
	}
	tr := alerting.Track{
		ID:          fl.ID,
		Pos:         core.LatLon{LatDeg: f.Position.Lat, LonDeg: f.Position.Lng},
		CapturedAtS: unixS(captured),
		RxAtS:       unixS(rx),
		Backlog:     f.Backlog,
		Source:      groupTelemetry,
		Station:     st.client,
		Flying:      &flying,
		AltSource:   core.AltNone,
	}
	tsS := unixS(ts)
	tr.SourceTS = &tsS
	if f.AltWGS84M != nil && t.cfg.Geoid != nil {
		if n, err := t.cfg.Geoid.UndulationM(tr.Pos); err == nil {
			amsl := *f.AltWGS84M - n
			tr.AltAMSLM, tr.AltSource = &amsl, core.AltGeodetic
			tr.Env.UndulationM = &n
		}
	} else if f.AltPressM != nil {
		p := *f.AltPressM
		tr.AltAMSLM, tr.AltSource = &p, core.AltPressure
	}
	if f.SpeedMS != nil && f.TrackDeg != nil {
		rad := *f.TrackDeg * math.Pi / 180
		tr.VNMS, tr.VEMS = *f.SpeedMS*math.Cos(rad), *f.SpeedMS*math.Sin(rad)
	}
	if f.VSpeedMS != nil {
		tr.VDMS = -*f.VSpeedMS
	}
	if fl.lostLink != nil && !f.Backlog {
		t.clearLocked(fl.lostLink, "resolved", rx, nil)
		fl.lostLink = nil
	}
	t.emitUSSP(t.ussp.Observe(tr, unixS(rx)), rx)
	return "accepted"
}

func decodeTelemetry(raw []byte, f *telemetryBody) error {
	// intent_id is read separately (it may be null); the rest strictly.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	for _, k := range []string{"trust", "source"} {
		if _, ok := m[k]; ok {
			return fmt.Errorf("%s is the ingest's, never the client's (06 T11)", k)
		}
	}
	delete(m, "intent_id")
	b, _ := json.Marshal(m)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(f)
}

func (t *Target) flightLocked(serial, client string) *flight {
	id := uuidFrom("flight:" + serial)
	fl := t.flights[id]
	if fl == nil {
		fl = &flight{ID: id, Serial: serial, Client: client, dedupe: map[string]dedupeEntry{}}
		t.flights[id] = fl
	}
	return fl
}

func (t *Target) handleTelemetryBatch(w http.ResponseWriter, r *http.Request) {
	cl, ok := t.bearerClaims(w, r, ScopeTelemetry)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		problem(w, http.StatusRequestEntityTooLarge, "body_too_large", "batch too large")
		return
	}
	var in struct {
		SentAt string            `json:"sent_at"`
		Frames []json.RawMessage `json:"frames"`
	}
	if err := json.Unmarshal(body, &in); err != nil || len(in.Frames) == 0 || len(in.Frames) > 2000 {
		problem(w, http.StatusBadRequest, "validation", "frames: 1..2000 telemetry/v1 samples")
		return
	}
	var sentAt time.Time
	if in.SentAt != "" {
		if sentAt, err = time.Parse(time.RFC3339Nano, in.SentAt); err != nil {
			problem(w, http.StatusBadRequest, "validation", "sent_at is not RFC 3339")
			return
		}
	}
	st := &sockState{id: "batch", client: cl.Subject, outcomes: map[string]uint64{}, acked: map[string]int64{}}
	type out struct {
		Index   int    `json:"index"`
		Outcome string `json:"outcome"`
	}
	var outcomes []out
	for i, f := range in.Frames {
		if o := t.ingestMessage(st, f, true, sentAt); o != "accepted" {
			outcomes = append(outcomes, out{Index: i, Outcome: o})
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": st.accepted, "refused": st.refused, "dropped": st.dropped,
		"duplicate": st.outcomes["duplicate"], "not_acknowledged": 0, "outcomes": nonNil(outcomes),
	})
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// --- intents (a stand-in: every intent of a bound serial is authorised) ---

func (t *Target) handleIntentCreate(w http.ResponseWriter, r *http.Request) {
	cl, ok := t.bearerClaims(w, r, ScopeIntents)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		problem(w, http.StatusRequestEntityTooLarge, "body_too_large", "intent too large")
		return
	}
	var req struct {
		ClientRef string            `json:"client_ref"`
		UASSerial string            `json:"uas_serial"`
		Volumes   []json.RawMessage `json:"volumes"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.ClientRef == "" || req.UASSerial == "" || len(req.Volumes) == 0 {
		problem(w, http.StatusBadRequest, "annex_iv_invalid", "client_ref, uas_serial and volumes are required")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.serials[req.UASSerial] != cl.Subject {
		problem(w, http.StatusForbidden, "forbidden", "the serial is not bound to this client")
		return
	}
	id := uuidFrom("intent:" + cl.Subject + ":" + req.ClientRef)
	in, exists := t.intents[id]
	if !exists {
		in = &intent{ID: id, Client: cl.Subject, Serial: req.UASSerial, State: "accepted", Version: 1, Request: body, ClientRF: req.ClientRef}
		t.intents[id] = in
	}
	status := http.StatusCreated
	if exists {
		status = http.StatusOK
	}
	writeJSON(w, status, decisionOf(in))
}

func decisionOf(in *intent) map[string]any {
	return map[string]any{
		"intent_id": in.ID, "version": in.Version, "client_ref": in.ClientRF, "decision": "authorised",
		"state": in.State, "authorisation_number": nil, "conflicts": []any{},
		"note": "lab reference target: authorised without judgement",
	}
}

func (t *Target) intentFor(w http.ResponseWriter, r *http.Request, scope string) (*intent, bool) {
	cl, ok := t.bearerClaims(w, r, scope)
	if !ok {
		return nil, false
	}
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("intent_id")
	}
	t.mu.Lock()
	in := t.intents[id]
	t.mu.Unlock()
	if !uuidPattern.MatchString(id) || in == nil || in.Client != cl.Subject {
		problem(w, http.StatusNotFound, "not_found", "no such intent")
		return nil, false
	}
	return in, true
}

func (t *Target) handleIntentGet(w http.ResponseWriter, r *http.Request) {
	in, ok := t.intentFor(w, r, ScopeIntents)
	if !ok {
		return
	}
	t.mu.Lock()
	d := decisionOf(in)
	t.mu.Unlock()
	writeJSON(w, http.StatusOK, d)
}

func (t *Target) handleIntentPatch(w http.ResponseWriter, r *http.Request) {
	in, ok := t.intentFor(w, r, ScopeIntents)
	if !ok {
		return
	}
	var p struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&p); err != nil {
		problem(w, http.StatusBadRequest, "validation", "action")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case p.Action == "activate" && in.State == "accepted":
		in.State = "activated"
	case p.Action == "end" && in.State != "ended":
		in.State = "ended"
	default:
		problem(w, http.StatusConflict, "transition", "not allowed from "+in.State)
		return
	}
	in.Version++
	writeJSON(w, http.StatusOK, decisionOf(in))
}

// --- the alert stream ---------------------------------------------------------

func (t *Target) handleAlertsWS(w http.ResponseWriter, r *http.Request) {
	in, ok := t.intentFor(w, r, ScopeTraffic)
	if !ok {
		return
	}
	t.mu.Lock()
	fl := t.flightLocked(in.Serial, in.Client)
	fl.IntentID = in.ID
	flightID := fl.ID
	var initial [][]byte
	for _, a := range t.active {
		if a.system == "ussp" && a.flight == fl {
			initial = append(initial, t.alertFrameLocked(a, "raised", "", nil, t.cfg.Now()))
		}
	}
	t.mu.Unlock()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	s := t.alerts.add(func(topic string) bool { return topic == "flight:"+flightID })
	defer t.alerts.remove(s)
	t.serveFrames(r.Context(), conn, s, producerUSSP, initial)
}

// serveFrames writes the hub's frames and a status every 2 s until the
// client goes away; client messages are read and ignored.
func (t *Target) serveFrames(ctx context.Context, conn *websocket.Conn, s *sub, producer string, initial [][]byte) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	connID := ulid.Make(t.cfg.Now())
	status := func() []byte {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.statusFrame(producer, connID, t.cfg.Now(), nil, s.dropped.Load())
	}
	for _, b := range append([][]byte{status()}, initial...) {
		if conn.Write(ctx, websocket.MessageText, b) != nil {
			return
		}
	}
	tk := time.NewTicker(statusEvery)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.CloseNow()
			return
		case b := <-s.c:
			if conn.Write(ctx, websocket.MessageText, b) != nil {
				return
			}
		case <-tk.C:
			if conn.Write(ctx, websocket.MessageText, status()) != nil {
				return
			}
		}
	}
}

// emitUSSP turns the USSP monitor's events into alert/v1 frames: a
// proximity alert per flight of the pair, naming the other (C-11), and a
// zone incursion per flight.
func (t *Target) emitUSSP(ev alerting.Events, now time.Time) {
	for _, a := range ev.Raised {
		kind, ok := usspKind(a.Kind)
		if !ok {
			continue
		}
		flights := make([]*flight, 0, len(a.Aircraft))
		for _, id := range a.Aircraft {
			if fl := t.flights[id]; fl != nil {
				flights = append(flights, fl)
			}
		}
		for i, fl := range flights {
			var peer *flight
			if kind == "proximity" && len(flights) == 2 {
				peer = flights[1-i]
			}
			key := a.Key + "|" + fl.ID
			if old, ok := t.active[key]; ok {
				old.alert = a
				continue
			}
			aa := &activeAlert{
				system: "ussp", key: key, kind: kind, flight: fl, peer: peer, alert: a,
				raisedAt: now, captured: fromUnixS(a.LastTrueS),
			}
			aa.id = uuidFrom(key + "|" + fmt.Sprint(a.RaisedAtS))
			t.active[key] = aa
			t.alerts.publish("flight:"+fl.ID, t.alertFrameLocked(aa, "raised", "", nil, now))
		}
	}
	for i := range ev.Cleared {
		c := &ev.Cleared[i]
		for _, fl := range c.Aircraft {
			if aa, ok := t.active[c.Key+"|"+fl]; ok {
				aa.alert = c.Alert
				t.clearLocked(aa, string(c.Reason), now, c.ClearingDetail)
			}
		}
	}
}

func usspKind(k string) (string, bool) {
	switch k {
	case alerting.KindConflict:
		return "proximity", true
	case alerting.KindZone:
		return "zone_incursion", true
	}
	return "", false
}

func (t *Target) clearLocked(aa *activeAlert, reason string, now time.Time, clearing map[string]any) {
	delete(t.active, aa.key)
	switch aa.system {
	case "ussp":
		t.alerts.publish("flight:"+aa.flight.ID, t.alertFrameLocked(aa, "cleared", reason, clearing, now))
	case "authority":
		t.picture.publish("picture", t.violationFrameLocked(aa, "cleared", reason, clearing, now))
	}
}

// republishLocked sends every held alert as updated (C-08).
func (t *Target) republishLocked(now time.Time) {
	keys := make([]string, 0, len(t.active))
	for k := range t.active {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		aa := t.active[k]
		switch aa.system {
		case "ussp":
			t.alerts.publish("flight:"+aa.flight.ID, t.alertFrameLocked(aa, "updated", "", nil, now))
		case "authority":
			t.picture.publish("picture", t.violationFrameLocked(aa, "updated", "", nil, now))
		}
	}
}

// lostLinkLocked is the reference target's own lost-link timer (not
// uspace-core's judgement): an airborne flight with no live sample for
// policy lost_link_s raises lost_link; its next live sample clears it.
func (t *Target) lostLinkLocked(now time.Time) {
	limit := time.Duration(t.cfg.Policy.LostLinkS * float64(time.Second))
	for _, fl := range t.flights {
		if fl.lostLink != nil || !fl.airborne || fl.lastLive.IsZero() || now.Sub(fl.lastLive) <= limit {
			continue
		}
		key := "lost_link|" + fl.ID
		aa := &activeAlert{system: "ussp", key: key, kind: "lost_link", flight: fl, raisedAt: now, captured: fl.lastCapture,
			alert: alerting.Alert{Key: key, Kind: "lost_link", Severity: core.SeverityCritical, Aircraft: []string{fl.ID},
				Detail: map[string]any{"silence_s": now.Sub(fl.lastLive).Seconds(), "lost_link_s": t.cfg.Policy.LostLinkS, "judged_by": "lab reference timer"}}}
		aa.id = uuidFrom(key + "|" + now.String())
		fl.lostLink = aa
		t.active[key] = aa
		t.alerts.publish("flight:"+fl.ID, t.alertFrameLocked(aa, "raised", "", nil, now))
	}
}

// alertFrameLocked is one alert/v1 envelope (uspace-ussp schemas/alert/v1).
func (t *Target) alertFrameLocked(aa *activeAlert, state, reason string, clearing map[string]any, now time.Time) []byte {
	a := aa.alert
	detail := map[string]any{}
	switch aa.kind {
	case "proximity":
		get := func(k string) any { return a.Detail[k] }
		detail = map[string]any{
			"t_cpa_s": nonNeg(get("t_cpa_s")), "d_cpa_h_m": nonNeg(get("d_cpa_horizontal_m")), "d_alt_m": get("d_alt_at_cpa_m"),
			"d_horizontal_now_m": nonNeg(get("d_horizontal_now_m")), "vertical_separation_known": get("vertical_separation_known"),
			"los_start_s": nonNeg(get("los_start_s")), "evaluation_period_s": 1,
			"peer": map[string]any{"track_id": peerID(aa), "trust": string(core.TrustSimulated), "source": "operator_ws"},
		}
	case "zone_incursion":
		zt := ""
		for _, z := range t.cfg.Zones {
			if z.Identifier == a.Detail["identifier"] {
				zt = string(z.Type)
			}
		}
		vk, _ := a.Detail["vertical_known"].(bool)
		lnj, _ := a.Detail["limit_not_judged"].(bool)
		detail = map[string]any{"zone_id": a.Detail["identifier"], "zone_type": zt, "vertical_known": vk, "limit_not_judged": lnj, "dataset": "zones"}
		if nj, ok := a.Detail["not_judged"]; ok {
			detail["not_judged"] = nj
		}
	default:
		for k, v := range a.Detail {
			detail[k] = v
		}
	}
	var cr any
	if state == "cleared" {
		cr = reason
	}
	var intentID any
	if aa.flight.IntentID != "" {
		intentID = aa.flight.IntentID
	}
	body := map[string]any{
		"alert_id": aa.id, "kind": aa.kind, "severity": string(a.Severity), "state": state, "clear_reason": cr,
		"flight_id": aa.flight.ID, "intent_id": intentID, "authorisation_number": nil,
		"captured_at": wire.Format(aa.captured), "raised_at": wire.Format(aa.raisedAt), "updated_at": wire.Format(now),
		"policy_version": t.cfg.Policy.PolicyVersion, "detail": detail,
	}
	if clearing != nil {
		body["clearing_detail"] = clearing
	}
	b, _ := wire.New("alert/v1", producerUSSP, aa.captured, now, nil, string(core.TimeSystem), false, body)
	return b
}

func peerID(aa *activeAlert) string {
	if aa.peer == nil {
		return "unknown"
	}
	return aa.peer.ID
}

func nonNeg(v any) any {
	f, ok := v.(float64)
	if !ok || f < 0 || math.IsNaN(f) {
		return 0.0
	}
	return f
}

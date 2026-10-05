package reftarget

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/rootxkit/uspace-core/alerting"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geodesy/cell"
	"github.com/rootxkit/uspace-core/odid"
	"github.com/rootxkit/uspace-core/timeplace"

	"github.com/rootxkit/uspace-lab/internal/ulid"
	"github.com/rootxkit/uspace-lab/internal/wire"
)

const (
	producerAuthority = "lab/reftarget-authority"
	// The rid/observation/v1 bounds (authority schema and openapi).
	maxBatchBytes   = 65536
	maxObservations = 64
	maxSkew         = 30 * time.Second
	nonceWindow     = 60 * time.Second
	maxNonces       = 100_000
)

var (
	receiverIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	macPattern        = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$`)
	sigPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// transmitter is what the ingest knows of one Remote ID transmitter.
type transmitter struct {
	serial     string
	operatorID string
	receiver   string
}

type observation struct {
	Transmitter string   `json:"transmitter"`
	PayloadHex  string   `json:"payload_hex"`
	RSSIDBm     *float64 `json:"rssi_dbm"`
	RxTS        *string  `json:"rx_ts"`
}

type batch struct {
	ReceiverID   string        `json:"receiver_id"`
	SentAtMS     *int64        `json:"sent_at_ms"`
	Nonce        string        `json:"nonce"`
	Backlog      bool          `json:"backlog"`
	Observations []observation `json:"observations"`
}

func (t *Target) handleObservations(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	t.mu.Lock()
	var rec Receiver
	found := false
	for _, rc := range t.receivers {
		if ok && subtle.ConstantTimeCompare([]byte(key), []byte(rc.BearerKey)) == 1 {
			rec, found = rc, true
		}
	}
	t.mu.Unlock()
	if !found {
		t.countRID("refused_unauthenticated")
		problem(w, http.StatusUnauthorized, "unauthenticated", "no or unknown receiver key")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBatchBytes))
	if err != nil {
		t.countRID("refused_body_too_large")
		problem(w, http.StatusRequestEntityTooLarge, "body_too_large", "at most 65536 bytes")
		return
	}
	sig := r.Header.Get("X-Report-Signature")
	m := hmac.New(sha256.New, rec.HMACKey)
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	if !sigPattern.MatchString(sig) || subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		t.countRID("refused_signature")
		problem(w, http.StatusUnauthorized, "signature", "unsigned or wrong signature")
		return
	}
	var b batch
	if err := json.Unmarshal(body, &b); err != nil || b.SentAtMS == nil || b.Nonce == "" || len(b.Nonce) > 256 ||
		!receiverIDPattern.MatchString(b.ReceiverID) || len(b.Observations) > maxObservations {
		t.countRID("refused_validation")
		problem(w, http.StatusBadRequest, "validation", "not rid/observation/v1")
		return
	}
	if b.ReceiverID != rec.ID {
		t.countRID("refused_signature")
		problem(w, http.StatusUnauthorized, "signature", "the batch names another receiver")
		return
	}
	now := t.cfg.Now()
	sentAt := time.UnixMilli(*b.SentAtMS)
	if d := now.Sub(sentAt); d > maxSkew || d < -maxSkew {
		t.countRID("refused_skew")
		problem(w, http.StatusUnauthorized, "skew", fmt.Sprintf("sent_at_ms is %s from the ingest's clock", d.Round(time.Millisecond)))
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rxOff[rec.ID] {
		t.countLocked(groupRID, "refused_source_disabled")
		w.Header().Set("Retry-After", "5")
		problem(w, http.StatusServiceUnavailable, "source_disabled", "this receiver is switched off")
		return
	}
	ns := t.nonces[rec.ID]
	if ns == nil {
		ns = map[string]time.Time{}
		t.nonces[rec.ID] = ns
	}
	if at, seen := ns[b.Nonce]; seen && now.Sub(at) < 2*nonceWindow {
		t.countLocked(groupRID, "refused_replay")
		problem(w, http.StatusConflict, "replay", "nonce seen before")
		return
	}
	if len(ns) >= maxNonces {
		for k, at := range ns {
			if now.Sub(at) >= 2*nonceWindow {
				delete(ns, k)
			}
		}
	}
	ns[b.Nonce] = now
	for i, o := range b.Observations {
		if !macPattern.MatchString(o.Transmitter) || len(o.PayloadHex) < 2 || len(o.PayloadHex) > 1024 {
			t.countLocked(groupRID, "refused_validation")
			problem(w, http.StatusBadRequest, "validation", fmt.Sprintf("observations[%d]", i))
			return
		}
	}
	accepted, dups := 0, 0
	for _, o := range b.Observations {
		rx := ""
		if o.RxTS != nil {
			rx = *o.RxTS
		}
		dk := rec.ID + "|" + strings.ToUpper(o.Transmitter) + "|" + rx + "|" + o.PayloadHex
		if at, seen := t.seenObs[dk]; seen && now.Sub(at) < nonceWindow {
			dups++
			t.countLocked(groupRID, "duplicates")
			continue
		}
		t.seenObs[dk] = now
		accepted++
		t.countLocked(groupRID, "accepted")
		if b.Backlog {
			t.countLocked(groupRID, "backlog")
		}
		t.observeLocked(rec.ID, o, b.Backlog, sentAt, now)
	}
	if len(t.seenObs) > maxNonces {
		for k, at := range t.seenObs {
			if now.Sub(at) >= nonceWindow {
				delete(t.seenObs, k)
			}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"batch_id": rec.ID + ":" + b.Nonce, "accepted": accepted, "duplicates": dups})
}

// observeLocked decodes one observation (uspace-core odid) and feeds the
// authority monitor. The receiver's clock offset cancels: received at
// arrival - (sent_at - rx_ts); without rx_ts, at arrival (T-12).
func (t *Target) observeLocked(receiver string, o observation, backlog bool, sentAt, arrival time.Time) {
	raw, err := hex.DecodeString(o.PayloadHex)
	if err != nil {
		t.countLocked(groupRID, "undecodable")
		return
	}
	msgs, err := odid.Decode(raw, odid.DecodeOptions{})
	if err != nil {
		t.countLocked(groupRID, "undecodable")
		return
	}
	mac := strings.ToUpper(o.Transmitter)
	tx := t.tx[mac]
	if tx == nil {
		tx = &transmitter{}
		t.tx[mac] = tx
	}
	tx.receiver = receiver
	received := arrival
	if o.RxTS != nil {
		if rxT, err := time.Parse(time.RFC3339Nano, *o.RxTS); err == nil {
			received = arrival.Add(-sentAt.Sub(rxT))
		}
	}
	for _, m := range msgs {
		switch v := m.(type) {
		case odid.BasicID:
			if v.UAID != "" {
				tx.serial = v.UAID
			}
		case *odid.BasicID:
			if v.UAID != "" {
				tx.serial = v.UAID
			}
		case odid.OperatorID:
			tx.operatorID = v.OperatorID
		case *odid.OperatorID:
			tx.operatorID = v.OperatorID
		}
	}
	for _, m := range msgs {
		var loc *odid.Location
		switch v := m.(type) {
		case odid.Location:
			loc = &v
		case *odid.Location:
			loc = v
		}
		if loc == nil || loc.LatDeg == nil || loc.LonDeg == nil {
			continue
		}
		t.locationLocked(mac, tx, loc, received, arrival, backlog)
	}
}

func (t *Target) locationLocked(mac string, tx *transmitter, loc *odid.Location, received, arrival time.Time, backlog bool) {
	tenths := uint16(0xFFFF)
	if loc.SecondsAfterHour != nil {
		tenths = uint16(math.Round(*loc.SecondsAfterHour * 10))
	}
	pl := timeplace.PlaceBroadcast(tenths, loc.TSAccuracy, received, timeplace.DefaultBroadcastPolicy())
	id := "rid:" + mac
	identified := tx.serial != ""
	if identified {
		id = tx.serial
	}
	flying := loc.Status.Airborne() && loc.Status != odid.StatusUndeclared
	pos := core.LatLon{LatDeg: *loc.LatDeg, LonDeg: *loc.LonDeg}
	tr := alerting.Track{
		ID: id, Pos: pos, CapturedAtS: unixS(pl.CapturedAt), RxAtS: unixS(arrival), Backlog: backlog,
		Source: groupRID, Station: tx.receiver, Flying: &flying, AltSource: core.AltNone,
		Transmitter: &mac, Identified: &identified,
	}
	if loc.AltHAEM != nil && t.cfg.Geoid != nil {
		if n, err := t.cfg.Geoid.UndulationM(pos); err == nil {
			amsl := *loc.AltHAEM - n
			tr.AltAMSLM, tr.AltSource = &amsl, core.AltGeodetic
			tr.Env.UndulationM = &n
		}
	} else if loc.AltBaroM != nil {
		p := *loc.AltBaroM
		tr.AltAMSLM, tr.AltSource = &p, core.AltPressure
	}
	if loc.SpeedHorizontalMS != nil && loc.DirectionDeg != nil {
		rad := *loc.DirectionDeg * math.Pi / 180
		tr.VNMS, tr.VEMS = *loc.SpeedHorizontalMS*math.Cos(rad), *loc.SpeedHorizontalMS*math.Sin(rad)
	}
	if loc.SpeedVerticalMS != nil {
		tr.VDMS = -*loc.SpeedVerticalMS
	}
	t.trackPos[id] = pos
	t.emitAuthority(t.authority.Observe(tr, unixS(arrival)), arrival)
	t.picture.publishTrack(pos, func() []byte { return t.trackFrameLocked(id, tx, loc, &tr, pl, arrival, backlog) })
}

// trackFrameLocked is the picture's track/telemetry/v1 envelope of one
// Remote ID location (schemas/common/track/telemetry/v1), sent to the
// consoles whose console/subscribe/v1 view holds it. The reference has
// no registry, so a serial is unknown_operator for registry_unavailable
// and a track without one is unidentified (SC-22: nothing claimed that
// was not judged).
func (t *Target) trackFrameLocked(id string, tx *transmitter, loc *odid.Location, tr *alerting.Track, pl timeplace.Placement, arrival time.Time, backlog bool) []byte {
	status := "Ground"
	switch {
	case loc.Status == odid.StatusEmergency:
		status = "Emergency"
	case loc.Status == odid.StatusRemoteIDSystemFailure:
		status = "RemoteIDSystemFailure"
	case loc.Status == odid.StatusUndeclared:
		status = "Undeclared"
	case loc.Status.Airborne():
		status = "Airborne"
	}
	ident := map[string]any{"status": "unidentified", "reason": "no_serial", "serial": nil, "operator_reg": nil,
		"registered_operator_reg": nil, "mismatch": false, "basis": "as_broadcast"}
	if tx.serial != "" {
		ident["status"], ident["reason"], ident["serial"] = "unknown_operator", "registry_unavailable", tx.serial
	}
	if tx.operatorID != "" {
		ident["operator_reg"] = tx.operatorID
	}
	var height, heightRef any
	if loc.HeightM != nil {
		height, heightRef = *loc.HeightM, "TakeoffLocation"
		if loc.HeightReference == odid.HeightOverGround {
			heightRef = "GroundLevel"
		}
	}
	body := map[string]any{
		"track_id": id, "trust": string(core.TrustBroadcast), "source": groupRID, "source_instance": tx.receiver,
		"position":    map[string]any{"lat": tr.Pos.LatDeg, "lng": tr.Pos.LonDeg},
		"alt_wgs84_m": loc.AltHAEM, "alt_amsl_m": tr.AltAMSLM, "alt_source": string(tr.AltSource), "alt_pressure_m": loc.AltBaroM,
		"height_m": height, "height_ref": heightRef,
		"speed_ms": loc.SpeedHorizontalMS, "track_deg": loc.DirectionDeg, "vspeed_ms": loc.SpeedVerticalMS,
		"accuracy_h_m": nil, "accuracy_v_m": nil, "status": status, "emergency": loc.Status == odid.StatusEmergency,
		"identification": ident, "flight_id": nil, "intent_id": nil,
	}
	if c, err := cell.Of(tr.Pos, cell.Level5); err == nil {
		body["cell"] = c.String()
	}
	var ts *time.Time
	if !pl.TS.IsZero() {
		ts = &pl.TS
	}
	b, _ := wire.New(wire.SchemaTrack, producerAuthority, pl.CapturedAt, arrival, ts, string(pl.Source), backlog, body)
	return b
}

// violationKind is the violation/v1 kind of a monitor alert kind, as
// uspace-authority's detect names them (its detectsvc: zone ->
// zone_incursion, height -> height_120m, identification ->
// unregistered); false for a kind that is never a violation (conflicts,
// authority plan D5).
func violationKind(k string) (string, bool) {
	switch k {
	case alerting.KindZone:
		return "zone_incursion", true
	case alerting.KindHeight:
		return "height_120m", true
	case alerting.KindIdentification:
		// uspace-core raises it only for an identification that says
		// unknown_operator or unidentified inside an incident zone; with
		// no identification (no registry) it raises nothing (SC-22).
		return "unregistered", true
	}
	return "", false
}

// emitAuthority turns the authority monitor's events into violation/v1
// frames: zone incursions, the 120 m height limit and unregistered
// aircraft (conflicts are never violations, authority plan D5;
// SkipConflicts is set).
func (t *Target) emitAuthority(ev alerting.Events, now time.Time) {
	for _, a := range ev.Raised {
		kind, ok := violationKind(a.Kind)
		if !ok {
			continue
		}
		key := "authority|" + a.Key
		if old, ok := t.active[key]; ok {
			old.alert = a
			continue
		}
		aa := &activeAlert{system: "authority", key: key, kind: kind, alert: a, raisedAt: now, captured: fromUnixS(a.LastTrueS),
			flight: &flight{ID: strings.Join(a.Aircraft, ",")}}
		aa.id = ulid.Make(now)
		t.active[key] = aa
		t.picture.publish("picture", t.violationFrameLocked(aa, "raised", "", nil, now))
	}
	for i := range ev.Cleared {
		c := &ev.Cleared[i]
		if aa, ok := t.active["authority|"+c.Key]; ok {
			aa.alert = c.Alert
			t.clearLocked(aa, string(c.Reason), now, c.ClearingDetail)
		}
	}
}

// violationFrameLocked is one violation/v1 envelope (uspace-authority
// schemas/violation/v1).
func (t *Target) violationFrameLocked(aa *activeAlert, state, reason string, clearing map[string]any, now time.Time) []byte {
	a := aa.alert
	trackRef := aa.flight.ID
	var serial, opReg, zoneID, zoneType any
	if !strings.HasPrefix(trackRef, "rid:") {
		serial = trackRef
	}
	for mac, tx := range t.tx {
		if tx.serial == trackRef || "rid:"+mac == trackRef {
			if tx.operatorID != "" {
				opReg = tx.operatorID
			}
		}
	}
	if aa.kind == "zone_incursion" {
		zoneID = fmt.Sprint(a.Detail["identifier"])
		for _, z := range t.cfg.Zones {
			if z.Identifier == a.Detail["identifier"] {
				zoneID = z.Country + "/" + z.Identifier
				zoneType = string(z.Type)
			}
		}
	}
	var closedAt, cr, clr any
	if state == "cleared" {
		closedAt, cr = wire.Format(now), reason
		clr = map[string]any{}
		if clearing != nil {
			clr = clearing
		}
	}
	c5 := "c5:0:0"
	if p, ok := t.trackPos[trackRef]; ok {
		if id, err := cell.Of(p, cell.Level5); err == nil {
			c5 = id.String()
		}
	}
	var peak any
	if h, ok := a.Detail["height_agl_m"].(float64); ok {
		peak = map[string]any{"name": "height_agl_m", "value": h}
	}
	body := map[string]any{
		"violation_id": aa.id, "kind": aa.kind, "state": state, "severity": string(a.Severity), "alert_key": a.Key,
		"track_ref": trackRef, "serial": serial, "operator_reg": opReg, "registry_uas_id": nil,
		"zone_id": zoneID, "zone_version": nil, "zone_type": zoneType,
		"captured_at": wire.Format(aa.captured), "opened_at": wire.Format(aa.raisedAt), "closed_at": closedAt,
		"clear_reason": cr, "policy_version": t.cfg.Policy.PolicyVersion, "peak": peak, "detail": a.Detail,
		"clearing_detail": clr, "terrain_source": nil, "in_uspace": false, "evidence_trust": string(core.TrustBroadcast),
		"evidence_refs":    []any{map[string]any{"type": "track", "id": trackRef, "version": nil}},
		"evidence_excerpt": []any{}, "cell5": c5,
	}
	b, _ := wire.New("violation/v1", producerAuthority, aa.captured, now, nil, string(core.TimeSystem), false, body)
	return b
}

func (t *Target) handlePictureWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if !t.consoleOK(r) {
		// The picture's refusal of a session it cannot accept: upgraded,
		// then closed with 4401 (sign in again, M22).
		_ = conn.Close(websocket.StatusCode(4401), "sign in again")
		return
	}
	s := t.picture.add(func(topic string) bool { return topic == "picture" })
	defer t.picture.remove(s)
	// A console/subscribe/v1 sets the view and is answered with a
	// console/snapshot/v1 (Appendix C), after which the view's tracks
	// follow as frames. The reference keeps no picture between frames,
	// so its snapshot holds no tracks and no alerts (the active
	// violations were sent on connect); it is never evidence for a system.
	onMessage := func(b []byte) {
		v, ok := parseSubscribe(b)
		if !ok {
			return
		}
		s.view.Store(v)
		now := t.cfg.Now()
		snap, _ := wire.New(wire.SchemaSnapshot, producerAuthority, now, now, nil, string(core.TimeSystem), false,
			map[string]any{"tracks": []any{}, "alerts": []any{}, "manned": []any{}, "zones_version": nil})
		s.offer(snap)
	}
	t.mu.Lock()
	var initial [][]byte
	now := t.cfg.Now()
	for _, aa := range t.active {
		if aa.system == "authority" {
			initial = append(initial, t.violationFrameLocked(aa, "raised", "", nil, now))
		}
	}
	t.mu.Unlock()
	t.serveFrames(r.Context(), conn, s, producerAuthority, initial, onMessage)
}

// parseSubscribe reads a console/subscribe/v1 frame (schemas/common/
// console/subscribe/v1): a [west, south, east, north] box and the known
// layers. Anything else is ignored, as the systems' pictures do.
func parseSubscribe(b []byte) (*view, bool) {
	var m struct {
		Schema string `json:"schema"`
		Body   struct {
			BBox   []float64 `json:"bbox"`
			Layers []string  `json:"layers"`
		} `json:"body"`
	}
	if json.Unmarshal(b, &m) != nil || m.Schema != wire.SchemaSubscribe || len(m.Body.BBox) != 4 {
		return nil, false
	}
	bb := m.Body.BBox
	for i, lim := range []float64{180, 90, 180, 90} {
		if math.IsNaN(bb[i]) || bb[i] < -lim || bb[i] > lim {
			return nil, false
		}
	}
	if bb[1] > bb[3] {
		return nil, false
	}
	v := &view{west: bb[0], south: bb[1], east: bb[2], north: bb[3]}
	for _, l := range m.Body.Layers {
		switch l {
		case "tracks":
			v.tracks = true
		case "manned", "alerts", "zones":
		default:
			return nil, false
		}
	}
	return v, true
}

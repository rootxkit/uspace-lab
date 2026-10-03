package runner

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/manned"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simadsb"
	"github.com/rootxkit/uspace-lab/internal/simfeed"
	"github.com/rootxkit/uspace-lab/internal/simop"
	"github.com/rootxkit/uspace-lab/internal/simrx"
)

type feedHandle struct {
	stream *simfeed.Feed
	adsb   *simadsb.Server
	url    string
}

func wsURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base
}

// usspEndpoint is the USSP's base and token endpoint for this run.
func (r *run) usspEndpoint() (base, tokenURL, audience string) {
	if r.tg.Mode == ModeReference {
		return r.refURL, r.refURL + "/oauth/token", "reference.lab.invalid"
	}
	return r.tg.USSP.BaseURL, r.tg.USSP.TokenURL, r.tg.USSP.Audience
}

func (r *run) operatorTokens(a *scenario.Aircraft) (*oauth.ClientCredentials, error) {
	_, tokenURL, aud := r.usspEndpoint()
	name := r.clientName(a)
	scopes := []string{"ussp.telemetry", "ussp.intents", "ussp.traffic"}
	if r.tg.Mode == ModeReference {
		return &oauth.ClientCredentials{TokenURL: tokenURL, ClientID: "lab-" + name, ClientSecret: r.refCreds.clientSecrets[name], Scopes: scopes, Audience: aud}, nil
	}
	c, ok := r.tg.USSP.Clients[name]
	if !ok {
		return nil, fmt.Errorf("the targets file has no ussp client %q", name)
	}
	secret, err := r.tg.secret(c.SecretFile)
	if err != nil {
		return nil, err
	}
	return &oauth.ClientCredentials{TokenURL: tokenURL, ClientID: c.ClientID, ClientSecret: secret, Scopes: scopes, Audience: aud}, nil
}

// intentRequest builds the Annex IV request for an aircraft: a circle
// about an offset, the band relative to the aircraft's home ground as
// WGS84 heights (AMSL + N), the window around t0.
func (r *run) intentRequest(a *scenario.Aircraft) (simop.IntentRequest, error) {
	in := a.Operator.Intent
	home := r.lab.Home(a.Sysid)
	c := r.lab.At(in.Center)
	n, err := r.geo.UndulationM(c)
	if err != nil {
		return simop.IntentRequest{}, fmt.Errorf("geoid at the intent: %w", err)
	}
	lower := geoid.HAEFromAMSL(home.AltAMSLM+in.AltLowerRelM, n)
	upper := geoid.HAEFromAMSL(home.AltAMSLM+in.AltUpperRelM, n)
	start := r.t0.Add(-seconds(in.StartsBeforeS)).UTC()
	end := start.Add(seconds(in.LastsS))
	pick := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	return simop.IntentRequest{
		ClientRef: r.opt.Run + "-" + r.sc.ID + "-" + a.Name, UASSerial: a.Serial,
		Mode: pick(in.Mode, "VLOS"), FlightType: "normal", Category: pick(in.Category, "open"),
		Subcategory: in.Subcategory, ClassLabel: in.ClassLabel,
		Volumes: []simop.Volume4D{{
			Volume: simop.Volume3D{
				OutlineCircle: &simop.Circle{Center: simop.Point{Lat: c.LatDeg, Lng: c.LonDeg}, Radius: simop.Radius{Value: in.RadiusM, Units: "M"}},
				AltitudeLower: simop.IntentAltitude{Value: lower, Reference: "W84", Units: "M"},
				AltitudeUpper: simop.IntentAltitude{Value: upper, Reference: "W84", Units: "M"},
			},
			TimeStart: simop.IntentTime{Value: start.Format(time.RFC3339), Format: "RFC3339"},
			TimeEnd:   simop.IntentTime{Value: end.Format(time.RFC3339), Format: "RFC3339"},
		}},
		IdentificationTechnology: pick(in.Identification, "network"), ConnectivityMethods: []string{"lte"},
		EnduranceS: int(in.LastsS) + 600, LossOfC2Procedure: "return to the take-off point and land",
		OperatorReg: a.OperatorReg, Takeoff: &simop.Point{Lat: home.LatDeg, Lng: home.LonDeg},
		Contingency: simop.IntentContingency{Procedure: "land at the take-off point"}, EmergencyContactRef: "lab-" + r.sc.ID,
	}, nil
}

func (r *run) startOperators(ctx, simCtx context.Context, wg *sync.WaitGroup) error {
	base, _, _ := r.usspEndpoint()
	for i := range r.sc.Aircraft {
		a := &r.sc.Aircraft[i]
		if a.Operator == nil {
			continue
		}
		tokens, err := r.operatorTokens(a)
		if err != nil {
			return err
		}
		intentID := ""
		if a.Operator.Intent != nil {
			rec := IntentRecord{Aircraft: a.Name}
			req, err := r.intentRequest(a)
			if err == nil {
				ins := &simop.Intents{BaseURL: base, Tokens: tokens}
				var d simop.Decision
				if d, err = ins.File(ctx, req); err == nil {
					rec.IntentID, rec.Decision, rec.State = d.IntentID, d.Decision, d.State
					if d.State == "accepted" {
						if d, err = ins.Change(ctx, d.IntentID, "activate"); err == nil {
							rec.State = d.State
						}
					}
				}
			}
			if err != nil {
				rec.Error = err.Error()
			}
			r.mu.Lock()
			r.intents = append(r.intents, rec)
			r.mu.Unlock()
			intentID = rec.IntentID
		}
		op := r.lab.Home(a.Sysid)
		opPos := core.LatLon{LatDeg: op.LatDeg, LonDeg: op.LonDeg}
		if a.Operator.OperatorAt != nil {
			opPos = r.lab.At(*a.Operator.OperatorAt)
		}
		c, err := simop.New(simop.Config{BaseURL: base, Tokens: tokens, Serial: a.Serial, Transport: a.Operator.Transport,
			Geoid: r.geo, OperatorPos: &opPos, IntentID: intentID, DropRate: a.Operator.DropRate,
			Latency: seconds(a.Operator.LatencyS), Seed: uint64(a.Sysid)})
		if err != nil {
			return err
		}
		r.operators[a.Name] = c
		sub := r.bus.Subscribe("operator-"+a.Name, 64, a.Sysid)
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Run(simCtx, sub.C) }()
		if intentID != "" && (a.Operator.System == scenario.SystemUSSP) {
			tok, err := tokens.Token(ctx)
			if err != nil {
				return fmt.Errorf("token for the alert stream of %s: %w", a.Name, err)
			}
			r.collect(ctx, observe.Stream{Name: "ussp-alerts-" + a.Name, System: scenario.SystemUSSP, Aircraft: a.Name,
				URL: wsURL(base) + "/v1/alerts?intent_id=" + intentID, Header: http.Header{"Authorization": {"Bearer " + tok}}})
		}
	}
	return nil
}

func (r *run) startReceivers(simCtx context.Context, wg *sync.WaitGroup) error {
	for _, rx := range r.sc.Receivers {
		var txs []simrx.Transmitter
		for i := range r.sc.Aircraft {
			a := &r.sc.Aircraft[i]
			for _, id := range a.Receivers {
				if id == rx.ID {
					txs = append(txs, simrx.Transmitter{Sysid: a.Sysid, MAC: macOf(r.sc.ID, a.Name), Serial: a.Serial, OperatorID: a.OperatorID})
				}
			}
		}
		var base, bearer string
		var key []byte
		if r.tg.Mode == ModeReference {
			base, bearer, key = r.refURL, r.refCreds.rxBearer[rx.ID], r.refCreds.rxHMAC[rx.ID]
		} else {
			t, ok := r.tg.Authority.Receivers[rx.ID]
			if !ok {
				return fmt.Errorf("the targets file has no authority receiver %q", rx.ID)
			}
			var err error
			if bearer, err = r.tg.secret(t.BearerKeyFile); err != nil {
				return err
			}
			hexKey, err := r.tg.secret(t.HMACKeyFile)
			if err != nil {
				return err
			}
			if key, err = decodeHex(hexKey); err != nil {
				return err
			}
			base = r.tg.Authority.BaseURL
		}
		pos := r.lab.At(rx.At)
		rec, err := simrx.New(simrx.Config{BaseURL: base, ReceiverID: rx.ID, BearerKey: bearer, HMACKey: key, Transport: rx.Transport,
			HAE: rx.HAE, Geoid: r.geo, Position: &pos, RSSIDBm: rx.RSSIDBm, DropRate: rx.DropRate, Latency: seconds(rx.LatencyS),
			Seed: rx.Seed, Transmitters: txs})
		if err != nil {
			return err
		}
		r.receivers[rx.ID] = rec
		sub := r.bus.Subscribe("receiver-"+rx.ID, 1024)
		wg.Add(1)
		go func() { defer wg.Done(); _ = rec.Run(simCtx, sub.C) }()
	}
	return nil
}

func (r *run) startFeeds(ctx context.Context, wg *sync.WaitGroup) error {
	for _, fd := range r.sc.Feeds {
		var src manned.Source
		if fd.Recording != "" {
			rec, err := manned.LoadRecording(r.tg.path(fd.Recording))
			if err != nil {
				return err
			}
			rec.T0 = r.t0
			src = rec
		} else {
			legs := &manned.Legs{T0: r.t0}
			for _, t := range fd.Tracks {
				leg := manned.Leg{ICAO24: t.ICAO24, Callsign: t.Callsign, From: r.lab.At(t.From), To: r.lab.At(t.To),
					SpeedMS: t.SpeedMS, AltPressureM: t.AltPressureM, Start: seconds(t.StartS)}
				if t.AltWGS84M != 0 {
					w := t.AltWGS84M
					leg.AltWGS84M = &w
				}
				legs.Legs = append(legs.Legs, leg)
			}
			src = legs
		}
		listen := "127.0.0.1:0"
		ft, configured := r.tg.Feeds[fd.ID]
		if configured && ft.Listen != "" {
			listen = ft.Listen
		}
		var h http.Handler
		handle := feedHandle{}
		switch fd.Kind {
		case "ansp_stream":
			var verify simfeed.Verifier
			if configured && ft.JWKSURL != "" {
				v, err := jwksVerifier(ctx, ft.Issuer, ft.JWKSURL, ft.Audience)
				if err != nil {
					return err
				}
				verify = v
			}
			f, err := simfeed.New(simfeed.Config{Source: src, Instance: "lab-" + fd.ID, Verify: verify,
				StaleAfterS: r.sc.PolicyDoc.Monitor.StaleAfterS, LiveMaxAgeS: r.sc.PolicyDoc.Monitor.LiveMaxAgeS,
				PolicyVersion: fmt.Sprint(r.sc.PolicyDoc.PolicyVersion)})
			if err != nil {
				return err
			}
			handle.stream, h = f, f.Handler()
		case "adsb_file":
			s := &simadsb.Server{Source: src}
			handle.adsb, h = s, s
		}
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
		if err != nil {
			return fmt.Errorf("feed %s: %w", fd.ID, err)
		}
		handle.url = "http://" + ln.Addr().String()
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
		wg.Add(1)
		go func() {
			defer wg.Done()
			go func() {
				<-ctx.Done()
				_ = srv.Close()
			}()
			_ = srv.Serve(ln)
		}()
		r.feeds[fd.ID] = handle
		r.log.Info("feed serving", "feed", fd.ID, "kind", fd.Kind, "url", handle.url)
	}
	return nil
}

// collect starts one stream collector.
func (r *run) collect(ctx context.Context, s observe.Stream) {
	go r.rec.Run(ctx, s)
}

func (r *run) startCollectors(ctx context.Context) {
	if r.wants(scenario.SystemAuthority) {
		r.collectAuthority(ctx)
	}
	if r.wants(scenario.SystemANSP) && r.tg.ANSP != nil {
		r.collectANSP(ctx)
	}
}

func (r *run) collectAuthority(ctx context.Context) {
	if r.tg.Mode == ModeReference {
		r.collect(ctx, observe.Stream{Name: "authority-picture", System: scenario.SystemAuthority, URL: wsURL(r.refURL) + "/v1/picture/ws",
			Header: http.Header{"Authorization": {"Bearer " + r.refCreds.console}}})
		return
	}
	a := r.tg.Authority
	if a == nil || a.PictureURL == "" {
		return
	}
	session, err := r.tg.secret(a.SessionFile)
	if err != nil {
		r.log.Error("authority picture not collected", "err", err)
		return
	}
	r.collect(ctx, observe.Stream{Name: "authority-picture", System: scenario.SystemAuthority, URL: a.PictureURL,
		Header: http.Header{"Cookie": {"uspace_session=" + session}, "Origin": {a.Origin}}})
}

func (r *run) collectANSP(ctx context.Context) {
	tc := r.tg.ANSP.Token
	secret, err := r.tg.secret(tc.SecretFile)
	if err != nil {
		r.log.Error("ansp stream not collected", "err", err)
		return
	}
	cc := &oauth.ClientCredentials{TokenURL: tc.TokenURL, ClientID: tc.ClientID, ClientSecret: secret, Audience: tc.Audience, Scopes: tc.Scopes}
	tok, err := cc.Token(ctx)
	if err != nil {
		r.log.Error("ansp stream not collected", "err", err)
		return
	}
	r.collect(ctx, observe.Stream{Name: "ansp-manned", System: scenario.SystemANSP, URL: r.tg.ANSP.StreamURL,
		Header: http.Header{"Authorization": {"Bearer " + tok}}})
}

func (r *run) wants(sys string) bool {
	for _, s := range r.sc.Systems {
		if s == sys {
			return true
		}
	}
	return false
}

// runTimeline executes the knobs and requests at their times.
func (r *run) runTimeline(ctx context.Context) {
	for _, t := range r.comp.Timeline {
		at := r.t0.Add(seconds(t.AtS))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(at)):
		}
		st := &r.sc.Steps[t.Step]
		rec := TimelineRecord{Step: t.Step, ID: st.ID, Do: st.Do, At: time.Now().UTC()}
		var err error
		if st.Do == scenario.DoKnob {
			rec.Detail, err = r.knob(st)
		} else {
			rec.Detail, err = r.request(ctx, st.Request)
		}
		if err != nil {
			rec.Error = err.Error()
		}
		r.mu.Lock()
		r.timeline = append(r.timeline, rec)
		r.mu.Unlock()
		r.log.Info("timeline", "step", t.Step, "do", st.Do, "detail", rec.Detail, "err", rec.Error)
	}
}

func (r *run) knob(st *scenario.Step) (string, error) {
	k := st.Knob
	var did []string
	for _, name := range st.Aircraft {
		c := r.operators[name]
		if c == nil && (k.OperatorLink != "" || k.OperatorStream != "") {
			return "", fmt.Errorf("aircraft %s has no operator client", name)
		}
		switch k.OperatorLink {
		case "down":
			c.LinkDown()
		case "up":
			c.LinkUp()
		case "":
		default:
			return "", fmt.Errorf("operator_link is down or up")
		}
		switch k.OperatorStream {
		case "stop":
			c.StreamStop()
		case "start":
			c.StreamStart()
		case "":
		default:
			return "", fmt.Errorf("operator_stream is stop or start")
		}
		if k.OperatorLink != "" || k.OperatorStream != "" {
			did = append(did, fmt.Sprintf("operator %s link %s stream %s", name, k.OperatorLink, k.OperatorStream))
		}
		if k.Serial != "" || k.Address != "" {
			a := r.sc.AircraftByName(name)
			for _, rx := range r.receivers {
				if k.Serial != "" {
					rx.SetSerial(a.Sysid, k.Serial)
				}
				if k.Address != "" {
					rx.SetAddress(a.Sysid, k.Address)
				}
			}
			did = append(did, fmt.Sprintf("transmitter of %s serial %q address %q", name, k.Serial, k.Address))
		}
	}
	for _, id := range k.Receivers {
		rx := r.receivers[id]
		if rx == nil {
			return "", fmt.Errorf("no receiver %s", id)
		}
		switch k.Receiver {
		case "down":
			rx.Down()
		case "up":
			rx.Up()
		default:
			return "", fmt.Errorf("receiver is down or up")
		}
		did = append(did, "receiver "+id+" "+k.Receiver)
	}
	for _, id := range k.Feeds {
		f, ok := r.feeds[id]
		if !ok {
			return "", fmt.Errorf("no feed %s", id)
		}
		if f.stream != nil {
			if err := f.stream.SetState(k.Feed); err != nil {
				return "", err
			}
		}
		if f.adsb != nil {
			switch k.Feed {
			case simfeed.StateLive:
				f.adsb.Thaw()
				f.adsb.SetDown(false)
			case simfeed.StateStale:
				f.adsb.Freeze()
			case simfeed.StateOutage:
				f.adsb.SetDown(true)
			}
		}
		did = append(did, "feed "+id+" "+k.Feed)
	}
	if len(did) == 0 {
		return "", fmt.Errorf("the knob changes nothing")
	}
	return strings.Join(did, "; "), nil
}

// request sends one request step with the targets file's credentials for
// its system; ${name} in the path or body is a value an earlier request
// captured.
func (r *run) request(ctx context.Context, q *scenario.Request) (string, error) {
	auth, ok := r.tg.Requests[q.System]
	if !ok || auth.BaseURL == "" {
		return "", fmt.Errorf("the targets file has no requests.%s", q.System)
	}
	sub := func(s string) string {
		r.mu.Lock()
		defer r.mu.Unlock()
		for k, v := range r.captures {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
		}
		// ${extra:key}: a deployment's value from the targets file (an
		// identifier the system issued, never a coordinate).
		for k, v := range r.tg.Extra {
			s = strings.ReplaceAll(s, "${extra:"+k+"}", fmt.Sprint(v))
		}
		s = strings.ReplaceAll(s, "${run}", r.opt.Run)
		return s
	}
	var body io.Reader
	if q.Body != nil {
		b, err := json.Marshal(q.Body)
		if err != nil {
			return "", err
		}
		body = bytes.NewReader([]byte(r.placeholders(sub(string(b)))))
	}
	req, err := http.NewRequestWithContext(ctx, q.Method, strings.TrimRight(auth.BaseURL, "/")+sub(q.Path), body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	switch {
	case auth.Bearer != nil:
		secret, err := r.tg.secret(auth.Bearer.SecretFile)
		if err != nil {
			return "", err
		}
		cc := &oauth.ClientCredentials{TokenURL: auth.Bearer.TokenURL, ClientID: auth.Bearer.ClientID, ClientSecret: secret,
			Audience: auth.Bearer.Audience, Scopes: auth.Bearer.Scopes}
		tok, err := cc.Token(ctx)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	case auth.SessionFile != "":
		session, err := r.tg.secret(auth.SessionFile)
		if err != nil {
			return "", err
		}
		req.Header.Set("Cookie", "uspace_session="+session)
		if auth.CSRFFile != "" {
			csrf, err := r.tg.secret(auth.CSRFFile)
			if err != nil {
				return "", err
			}
			req.Header.Add("Cookie", "uspace_csrf="+csrf)
			req.Header.Set("X-CSRF-Token", csrf)
		}
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	detail := fmt.Sprintf("%s %s -> %d", q.Method, q.Path, resp.StatusCode)
	want := q.Expect
	if (want == 0 && (resp.StatusCode < 200 || resp.StatusCode > 299)) || (want != 0 && resp.StatusCode != want) {
		return detail, fmt.Errorf("%s answered %d: %s", q.System, resp.StatusCode, shortStr(string(rb)))
	}
	if len(q.Capture) > 0 {
		var m map[string]any
		if err := json.Unmarshal(rb, &m); err != nil {
			return detail, fmt.Errorf("capture: the answer is not a JSON object")
		}
		r.mu.Lock()
		for name, member := range q.Capture {
			if v, ok := m[member]; ok {
				r.captures[name] = fmt.Sprint(v)
			}
		}
		r.mu.Unlock()
	}
	return detail, nil
}

var (
	placeLatLng = regexp.MustCompile(`"\$\{(lat|lng):(-?[0-9]+(?:\.[0-9]+)?),(-?[0-9]+(?:\.[0-9]+)?)\}"`)
	placeTime   = regexp.MustCompile(`\$\{time:(-?[0-9]+)\}`)
)

// placeholders fills a request body's lab values: "${lat:N,E}" and
// "${lng:N,E}" become the number of the offset N metres north and E east
// of the origin (no coordinate is written in a scenario, INV-03), and
// ${time:S} the RFC 3339 time t0 + S seconds.
func (r *run) placeholders(s string) string {
	s = placeLatLng.ReplaceAllStringFunc(s, func(m string) string {
		g := placeLatLng.FindStringSubmatch(m)
		n, _ := strconv.ParseFloat(g[2], 64)
		e, _ := strconv.ParseFloat(g[3], 64)
		p := r.lab.At(scenario.Offset{NorthM: n, EastM: e})
		if g[1] == "lat" {
			return strconv.FormatFloat(p.LatDeg, 'f', 7, 64)
		}
		return strconv.FormatFloat(p.LonDeg, 'f', 7, 64)
	})
	return placeTime.ReplaceAllStringFunc(s, func(m string) string {
		g := placeTime.FindStringSubmatch(m)
		sec, _ := strconv.Atoi(g[1])
		return r.t0.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339)
	})
}

func shortStr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func decodeHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("the HMAC key file is not hex: %w", err)
	}
	return b, nil
}

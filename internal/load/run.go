package load

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/conformance/report"
	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/reftarget"
	"github.com/rootxkit/uspace-lab/internal/runner"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simop"
	"github.com/rootxkit/uspace-lab/internal/simrx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// Options configure one run.
type Options struct {
	Files       *Files
	TargetsPath string
	Run         string
	// DurationS overrides the tier's duration (0: the tier's); a run
	// shorter than its tier says so in the report as a scale factor.
	DurationS float64
	RepoRoot  string
	// HostName names the machine (L-Q1: "the droplet, resized or not,
	// or a second host"); recorded as given.
	HostName string
	Log      *slog.Logger
	// ReadyTimeout bounds the wait for every client and stream to come
	// up before the generator starts (0: 60 s).
	ReadyTimeout time.Duration
}

// referenceAudience is the reference target's host (never resolved).
const referenceAudience = "reference.lab.invalid"

// Prepare checks everything a run needs before anything starts: the
// targets file, the lab origin, the geoid and the fleet. A refusal here
// has no side effect.
func Prepare(opt *Options) (*runner.Targets, *scenario.Lab, geoid.Undulator, string, error) {
	if opt.Files == nil {
		return nil, nil, nil, "", errors.New("load: no files")
	}
	if opt.DurationS < 0 || opt.DurationS > maxDurationS {
		return nil, nil, nil, "", fmt.Errorf("load: duration override %v is not in [0, %d]", opt.DurationS, maxDurationS)
	}
	tg, err := runner.LoadTargets(opt.TargetsPath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if tg.Mode != runner.ModeReference {
		// Fail closed rather than load a system with a fleet it has not
		// registered: the systems mode needs the tier's serials bound at
		// the USSP and its receivers registered at the authority first.
		return nil, nil, nil, "", fmt.Errorf("load: targets mode %q: only the reference target is driven yet; a systems run needs the tier's %d serials bound at the USSP and its receivers registered at the authority first (docs/RUNBOOKS/load.md)",
			tg.Mode, opt.Files.Tier.Operators.Aircraft)
	}
	labPath := tg.Lab
	if labPath != "" && !filepath.IsAbs(labPath) {
		labPath = filepath.Join(filepath.Dir(opt.TargetsPath), labPath)
	}
	lab, err := scenario.LoadLab(labPath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	geo, geoDesc, err := geoidx.Load(tg.Geoid)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return tg, lab, geo, geoDesc, nil
}

// Run drives the tier against the target and measures it. It returns
// an error only when nothing could start; anything that goes wrong once
// the run is under way is in the report's errors and fails its verdict.
func Run(ctx context.Context, opt Options) (*Report, error) {
	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}
	if opt.ReadyTimeout <= 0 {
		opt.ReadyTimeout = 60 * time.Second
	}
	tg, lab, geo, geoDesc, err := Prepare(&opt)
	if err != nil {
		return nil, err
	}
	files := opt.Files
	tier := *files.Tier
	tier.Scale = append([]ScaleFactor(nil), files.Tier.Scale...)
	if opt.DurationS > 0 && opt.DurationS != tier.DurationS {
		tier.Scale = append(tier.Scale, ScaleFactor{Quantity: "duration (override)", Spec: fmtNum(tier.DurationS) + " s (this tier)",
			Here: fmtNum(opt.DurationS) + " s", Factor: opt.DurationS / tier.DurationS, Why: "--duration-s given for this run"})
		tier.DurationS = opt.DurationS
	}
	runFiles := *files
	runFiles.Tier = &tier
	fl, err := NewFleet(&runFiles, lab.Origin, geo)
	if err != nil {
		return nil, err
	}
	r := &run{opt: opt, files: &runFiles, tier: &tier, fleet: fl, geo: geo, log: opt.Log,
		picture: NewLedger(), traffic: NewLedger(), rec: observe.NewRecorder(nil)}
	rep := &Report{Format: ReportFormat, Run: opt.Run, StartedAt: time.Now().UTC(), Tier: &tier, Scaled: len(tier.Scale) > 0,
		Target: TargetInfo{Mode: tg.Mode, Name: tg.Name, Images: tg.Images,
			Evidence: "The lab's in-process reference target (internal/reftarget): it proves the harness and is never evidence for a system (docs/PLAN.md L-D4); generator and target share this host's CPUs."},
		Host: hostInfo(opt.HostName), Commits: report.LabCommits(opt.RepoRoot), Files: files.Digests, Policy: files.Policy,
		Lab: LabInfo{File: lab.Path, OriginLat: lab.Origin.LatDeg, OriginLon: lab.Origin.LonDeg, OriginAMSL: lab.Origin.AltAMSLM, Geoid: geoDesc}}
	r.execute(ctx, rep)
	rep.EndedAt = time.Now().UTC()
	rep.Evaluate(files.Criteria, rep.GeneratorS)
	return rep, nil
}

type run struct {
	opt     Options
	files   *Files
	tier    *Tier
	fleet   *Fleet
	geo     geoid.Undulator
	log     *slog.Logger
	picture *Ledger
	traffic *Ledger
	rec     *observe.Recorder

	base      string
	target    *reftarget.Target
	tokens    []*oauth.ClientCredentials
	console   string
	rxCreds   []rxCred
	intents   map[int]string
	operators []*simop.Client
	opIn      []chan vehicle.Sample
	receivers []*simrx.Receiver
	rxIn      []chan vehicle.Sample
	streams   []*stream
	readers   *readers
	errs      []string
	errMu     sync.Mutex
	shedOp    uint64
	shedRID   uint64
	late      uint64
	memory    []uint64
}

type rxCred struct {
	id, bearer string
	key        []byte
}

func (r *run) fail(format string, a ...any) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if len(r.errs) < maxListed {
		r.errs = append(r.errs, fmt.Sprintf(format, a...))
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// execute runs the phases in order; a phase that fails stops the run
// and says why (the report still gets written and fails).
func (r *run) execute(ctx context.Context, rep *Report) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	if err := r.startReference(ctx, &wg); err != nil {
		r.fail("reference target: %v", err)
		r.finish(rep, 0)
		return
	}
	r.fileIntents(ctx)
	simCtx, simCancel := context.WithCancel(ctx)
	defer simCancel()
	if err := r.startClients(simCtx, &wg); err != nil {
		r.fail("clients: %v", err)
		r.finish(rep, 0)
		return
	}
	obsCtx, obsCancel := context.WithCancel(ctx)
	defer obsCancel()
	r.startStreams(obsCtx, &wg)
	if err := r.waitReady(ctx); err != nil {
		r.fail("not ready: %v", err)
		r.finish(rep, 0)
		return
	}
	memCtx, memCancel := context.WithCancel(ctx)
	var memWG sync.WaitGroup
	memWG.Add(1)
	go func() { defer memWG.Done(); r.sampleMemory(memCtx) }()
	t0 := time.Now()
	r.log.Info("generator started", "tier", r.tier.Tier, "aircraft", len(r.fleet.Aircraft), "duration_s", r.tier.DurationS)
	r.generate(ctx, t0)
	genS := time.Since(t0).Seconds()
	r.log.Info("generator stopped; draining", "after_s", math.Round(genS))
	memCancel()
	memWG.Wait()
	r.drain(ctx)
	r.collect(rep, t0, genS)
	simCancel()
	obsCancel()
}

func (r *run) finish(rep *Report, genS float64) {
	rep.GeneratorS = genS
	rep.Errors = append(rep.Errors, r.errs...)
	rep.Metrics = map[string]Observation{}
	for name, d := range Metrics {
		reason := "the run stopped before measuring"
		if d.NotImplemented != "" {
			reason = d.NotImplemented
		}
		rep.Metrics[name] = notMeasured(reason)
	}
}

// startReference starts the reference target with credentials made for
// this run, never written anywhere and never logged.
func (r *run) startReference(ctx context.Context, wg *sync.WaitGroup) error {
	t := r.tier
	clients := make([]reftarget.Client, t.Operators.Clients)
	secrets := make([]string, t.Operators.Clients)
	for i := range clients {
		secrets[i] = randomHex(16)
		clients[i] = reftarget.Client{ID: fmt.Sprintf("load-op-%03d", i), Secret: secrets[i],
			Scopes: []string{reftarget.ScopeTelemetry, reftarget.ScopeIntents, reftarget.ScopeTraffic}}
	}
	for _, a := range r.fleet.Aircraft {
		clients[a.Client].Serials = append(clients[a.Client].Serials, a.Serial)
	}
	receivers := make([]reftarget.Receiver, len(r.fleet.Receivers))
	for i := range receivers {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		c := rxCred{id: fmt.Sprintf("load-rx-%04d", i), bearer: randomHex(16), key: key}
		r.rxCreds = append(r.rxCreds, c)
		receivers[i] = reftarget.Receiver{ID: c.id, BearerKey: c.bearer, HMACKey: c.key}
	}
	r.console = randomHex(16)
	tgt, err := reftarget.New(ctx, reftarget.Config{Policy: r.files.Policy, Geoid: r.geo, Audience: referenceAudience,
		Clients: clients, Receivers: receivers, ConsoleToken: r.console})
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: tgt.Handler(), ReadHeaderTimeout: 5 * time.Second}
	r.target, r.base = tgt, "http://"+ln.Addr().String()
	wg.Add(3)
	go func() { defer wg.Done(); tgt.Run(ctx) }()
	go func() { defer wg.Done(); _ = srv.Serve(ln) }()
	go func() { defer wg.Done(); <-ctx.Done(); _ = srv.Close() }()
	for i := range clients {
		r.tokens = append(r.tokens, &oauth.ClientCredentials{TokenURL: r.base + "/oauth/token", ClientID: clients[i].ID,
			ClientSecret: secrets[i], Audience: referenceAudience, Scopes: clients[i].Scopes})
	}
	return nil
}

// intentRequest is one aircraft's intent: a circle over its path, its
// altitude +-30 m as WGS84 heights, for the run's span.
func (r *run) intentRequest(a *Aircraft, start, end time.Time) (simop.IntentRequest, error) {
	fl := r.fleet
	radius := r.files.Paths.Walkers.RadiusM + 100
	if a.Role == RolePair {
		radius = r.files.Paths.Pairs.AmplitudeM + 100
	}
	c := scenario.Move(fl.originLatLon(), a.centre)
	n, err := r.geo.UndulationM(c)
	if err != nil {
		return simop.IntentRequest{}, fmt.Errorf("geoid at %s: %w", a.Name, err)
	}
	amsl := fl.origin.AltAMSLM + a.AltRelM
	vol := simop.Volume4D{
		Volume: simop.Volume3D{OutlineCircle: &simop.Circle{Center: simop.Point{Lat: c.LatDeg, Lng: c.LonDeg}, Radius: simop.Radius{Value: radius, Units: "M"}},
			AltitudeLower: simop.IntentAltitude{Value: geoid.HAEFromAMSL(amsl-30, n), Reference: "W84", Units: "M"},
			AltitudeUpper: simop.IntentAltitude{Value: geoid.HAEFromAMSL(amsl+30, n), Reference: "W84", Units: "M"}},
		TimeStart: simop.IntentTime{Value: start.Format(time.RFC3339), Format: "RFC3339"},
		TimeEnd:   simop.IntentTime{Value: end.Format(time.RFC3339), Format: "RFC3339"},
	}
	ref := fmt.Sprintf("%s-%s", r.opt.Run, a.Name)
	if len(ref) > 64 {
		sum := sha256.Sum256([]byte(ref))
		ref = "load-" + hex.EncodeToString(sum[:12])
	}
	return simop.IntentRequest{
		ClientRef: ref, UASSerial: a.Serial, Mode: "VLOS", FlightType: "normal", Category: "open", Volumes: []simop.Volume4D{vol},
		IdentificationTechnology: "network", ConnectivityMethods: []string{"lte"}, EnduranceS: int(end.Sub(start).Seconds()) + 600,
		LossOfC2Procedure: "return to the take-off point and land", OperatorReg: "LAB-LOAD-OPERATOR",
		Takeoff:     &simop.Point{Lat: c.LatDeg, Lng: c.LonDeg},
		Contingency: simop.IntentContingency{Procedure: "land at the take-off point"}, EmergencyContactRef: "lab-load",
	}, nil
}

// fileIntents files and activates the intents (05 §1: intent operations
// compressed into the run; "watched" files only those whose traffic is
// read). Eight at a time.
func (r *run) fileIntents(ctx context.Context) {
	r.intents = map[int]string{}
	start := time.Now().Add(-time.Minute).UTC()
	end := start.Add(time.Duration((r.tier.DurationS+r.tier.DrainS+600)*float64(time.Second)) + time.Minute)
	var todo []*Aircraft
	for _, a := range r.fleet.Aircraft {
		if r.tier.Operators.Intents == "all" || a.Watched {
			todo = append(todo, a)
		}
	}
	var mu sync.Mutex
	work := make(chan *Aircraft)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range work {
				req, err := r.intentRequest(a, start, end)
				var id string
				if err == nil {
					in := &simop.Intents{BaseURL: r.base, Tokens: r.tokens[a.Client]}
					var d simop.Decision
					if d, err = in.File(ctx, req); err == nil {
						id = d.IntentID
						if d.State == "accepted" {
							_, err = in.Change(ctx, d.IntentID, "activate")
						}
					}
				}
				mu.Lock()
				if err != nil {
					r.fail("intent of %s: %v", a.Name, err)
				} else {
					r.intents[a.Index] = id
				}
				mu.Unlock()
			}
		}()
	}
	for _, a := range todo {
		select {
		case work <- a:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
}

// startClients starts one operator client per aircraft and one receiver
// per group of heard aircraft.
func (r *run) startClients(ctx context.Context, wg *sync.WaitGroup) error {
	t := r.tier
	poll := time.Duration(t.Operators.PollMS) * time.Millisecond
	for _, a := range r.fleet.Aircraft {
		c, err := simop.New(simop.Config{BaseURL: r.base, Tokens: r.tokens[a.Client], Serial: a.Serial, Transport: simop.TransportWS,
			Period: poll, Geoid: r.geo, IntentID: r.intents[a.Index], Seed: uint64(a.Index) + 1, Epoch: "load-" + r.opt.Run})
		if err != nil {
			return err
		}
		in := make(chan vehicle.Sample, 4)
		r.operators = append(r.operators, c)
		r.opIn = append(r.opIn, in)
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Run(ctx, in) }()
	}
	for i, idx := range r.fleet.Receivers {
		var txs []simrx.Transmitter
		for _, ai := range idx {
			a := r.fleet.Aircraft[ai]
			txs = append(txs, simrx.Transmitter{Sysid: a.Sysid, MAC: a.MAC, Serial: a.Serial, OperatorID: "LAB-LOAD-OPERATOR"})
		}
		c := r.rxCreds[i]
		rx, err := simrx.New(simrx.Config{BaseURL: r.base, ReceiverID: c.id, BearerKey: c.bearer, HMACKey: c.key,
			Transport: t.Receivers.Transports[i%len(t.Receivers.Transports)], Geoid: r.geo, Seed: uint64(i) + 1,
			// Every sample is a Location (the generator's period is the
			// broadcast rate); statics every 3 s as F3411 asks.
			LocationPeriod: time.Millisecond, BatchPeriod: time.Duration(t.Receivers.BatchMS) * time.Millisecond, Transmitters: txs})
		if err != nil {
			return err
		}
		in := make(chan vehicle.Sample, 4*len(idx))
		r.receivers = append(r.receivers, rx)
		r.rxIn = append(r.rxIn, in)
		wg.Add(1)
		go func() { defer wg.Done(); _ = rx.Run(ctx, in) }()
	}
	return nil
}

func wsURL(base string) string {
	if s, ok := strings.CutPrefix(base, "https://"); ok {
		return "wss://" + s
	}
	return "ws://" + strings.TrimPrefix(base, "http://")
}

// startStreams opens the consoles (the first one timed) and the traffic
// stream of every watched aircraft with an intent.
func (r *run) startStreams(ctx context.Context, wg *sync.WaitGroup) {
	rd := &readers{rec: r.rec, picture: r.picture, traffic: r.traffic, bySer: map[string]*Aircraft{}, byMAC: map[string]*Aircraft{}, raised: map[string]time.Time{}, peers: map[string]string{}}
	r.readers = rd
	for _, a := range r.fleet.Aircraft {
		rd.bySer[a.Serial], rd.byMAC[a.MAC] = a, a
		if a.Role == RolePair {
			rd.peers[a.Name] = r.fleet.Aircraft[a.Index^1].Name
		}
	}
	bb := r.fleet.BBox
	sub, _ := json.Marshal(map[string]any{"schema": "console/subscribe/v1", "body": map[string]any{"bbox": bb[:], "layers": []string{"tracks", "alerts"}}})
	for i := range r.tier.Consoles {
		r.streams = append(r.streams, &stream{name: fmt.Sprintf("console-%02d", i), kind: streamPicture, url: wsURL(r.base) + "/v1/picture/ws",
			header: http.Header{"Authorization": {"Bearer " + r.console}}, onOpen: sub, timed: i == 0, needSnap: true, ready: make(chan struct{})})
	}
	for _, a := range r.fleet.Aircraft {
		id, ok := r.intents[a.Index]
		if !a.Watched || !ok {
			continue
		}
		tok, err := r.tokens[a.Client].Token(ctx)
		if err != nil {
			r.fail("token for the traffic of %s: %v", a.Name, err)
			continue
		}
		r.streams = append(r.streams, &stream{name: "traffic-" + a.Name, kind: streamTraffic, url: wsURL(r.base) + "/v1/traffic?intent_id=" + id,
			header: http.Header{"Authorization": {"Bearer " + tok}}, aircraft: a, timed: true, ready: make(chan struct{})})
	}
	for _, s := range r.streams {
		wg.Add(1)
		go func() { defer wg.Done(); rd.run(ctx, s) }()
	}
}

// waitReady waits until every operator client has had a status from its
// socket and every stream its first status (and snapshot), bounded.
func (r *run) waitReady(ctx context.Context) error {
	deadline := time.NewTimer(r.opt.ReadyTimeout)
	defer deadline.Stop()
	for _, s := range r.streams {
		select {
		case <-s.ready:
		case <-deadline.C:
			return fmt.Errorf("stream %s did not open within %s: %s", s.name, r.opt.ReadyTimeout, s.stats().LastError)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	tk := time.NewTicker(20 * time.Millisecond)
	defer tk.Stop()
	for i := 0; i < len(r.operators); {
		if r.operators[i].Ledger().Connections > 0 {
			i++
			continue
		}
		select {
		case <-tk.C:
		case <-deadline.C:
			return fmt.Errorf("operator client %s did not connect within %s: %s", r.fleet.Aircraft[i].Name, r.opt.ReadyTimeout, r.operators[i].LastError())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// generate hands every aircraft a sample each period, the aircraft
// spread over the period, until the tier's duration has passed.
func (r *run) generate(ctx context.Context, t0 time.Time) {
	fl := r.fleet
	period := fl.period
	next := make([]time.Time, len(fl.Aircraft))
	k := make([]int64, len(fl.Aircraft))
	for i := range next {
		next[i] = t0.Add(period * time.Duration(i%50) / 50)
	}
	end := t0.Add(time.Duration(r.tier.DurationS * float64(time.Second)))
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		now := time.Now()
		if !now.Before(end) {
			return
		}
		for i, a := range fl.Aircraft {
			if now.Before(next[i]) {
				continue
			}
			s := fl.Sample(a, k[i], now, t0)
			k[i]++
			next[i] = next[i].Add(period)
			if next[i].Before(now) {
				// The generator fell more than a period behind: it is
				// saturated, and the rate check will say so.
				r.late++
				next[i] = now.Add(period)
			}
			if a.Watched {
				r.traffic.Hand(a.Name, now)
			}
			select {
			case r.opIn[i] <- s:
			default:
				r.shedOp++
			}
			if a.Receiver >= 0 {
				r.picture.Hand(a.Name, now)
				select {
				case r.rxIn[a.Receiver] <- s:
				default:
					r.shedRID++
				}
			}
		}
	}
}

func (r *run) sampleMemory(ctx context.Context) {
	tk := time.NewTicker(time.Duration(r.tier.MemorySampleS * float64(time.Second)))
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if len(r.memory) < 100_000 {
			r.memory = append(r.memory, m.HeapAlloc)
		}
	}
}

// drain waits, bounded by the tier's drain_s, until every client's
// ledger balances and every frame due has arrived (or will not: no
// frame on a timed stream for three seconds), and the timed console has
// sent a status after that (its final dropped_frames).
func (r *run) drain(ctx context.Context) {
	dctx, cancel := context.WithTimeout(ctx, time.Duration(r.tier.DrainS*float64(time.Second)))
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range r.operators {
		wg.Add(1)
		go func() { defer wg.Done(); c.Drain(dctx) }()
	}
	for _, rx := range r.receivers {
		wg.Add(1)
		go func() { defer wg.Done(); rx.Drain(dctx) }()
	}
	wg.Wait()
	settled := time.Now()
	tk := time.NewTicker(100 * time.Millisecond)
	defer tk.Stop()
	for {
		quiet, statusAfter := true, true
		last := time.Time{}
		for _, s := range r.streams {
			if !s.timed {
				continue
			}
			s.mu.Lock()
			if s.lastFrame.After(last) {
				last = s.lastFrame
			}
			if s.kind == streamPicture && !s.lastStatus.After(settled) {
				statusAfter = false
			}
			s.mu.Unlock()
		}
		if time.Since(last) < 3*time.Second && !r.outstandingNone() {
			quiet = false
		}
		if quiet && statusAfter {
			return
		}
		select {
		case <-dctx.Done():
			r.fail("drain: not settled within drain_s %.0f s", r.tier.DrainS)
			return
		case <-tk.C:
		}
	}
}

// outstandingNone is true when every sample handed out is shown or
// already counted unshown.
func (r *run) outstandingNone() bool {
	p, t := r.picture.Counts(), r.traffic.Counts()
	return p.Handed == p.Shown+p.Unshown && t.Handed == t.Shown+t.Unshown
}

// collect turns what the run saw into the report's observations.
func (r *run) collect(rep *Report, t0 time.Time, genS float64) {
	t := r.tier
	fl := r.fleet
	rep.GeneratorS = math.Round(genS*1000) / 1000
	rep.Memory = r.memory
	obs := map[string]Observation{}
	for name, d := range Metrics {
		if d.NotImplemented != "" {
			obs[name] = notMeasured(d.NotImplemented)
		}
	}
	// Ledgers.
	var lo OperatorLoss
	lo.Outcomes = map[string]uint64{}
	watchedSent := 0
	for i, c := range r.operators {
		l := c.Ledger()
		lo.Sent += l.Sent
		lo.Accepted += l.Accepted
		lo.Refused += l.Refused
		lo.Dropped += l.Dropped
		lo.Duplicate += l.Duplicate
		lo.QueueShed += l.QueueShed
		lo.Pending += uint64(l.Pending)
		for k, v := range l.Outcomes {
			lo.Outcomes[k] += v
		}
		if !l.Balanced || l.Pending > 0 || l.AckedSeq < l.LastSeq {
			lo.Unbalanced++
			if len(lo.Unbalance) < maxListed {
				lo.Unbalance = append(lo.Unbalance, fmt.Sprintf("%s: sent %d, accepted %d, refused %d, dropped %d, duplicate %d, pending %d, acked %d of %d",
					fl.Aircraft[i].Name, l.Sent, l.Accepted, l.Refused, l.Dropped, l.Duplicate, l.Pending, l.AckedSeq, l.LastSeq))
			}
		}
		if fl.Aircraft[i].Watched {
			watchedSent++
		}
	}
	var lr ReceiverLoss
	for i, rx := range r.receivers {
		l := rx.Ledger()
		lr.Observed += l.Observed
		lr.Sent += l.Sent
		lr.Accepted += l.Accepted
		lr.Duplicates += l.Duplicates
		lr.Refused += l.Refused
		lr.BacklogShed += l.BacklogShed
		lr.DroppedByKnob += l.DroppedByKnob
		lr.Pending += uint64(l.Pending)
		if !l.Balanced || l.Pending > 0 {
			lr.Unbalanced++
			if len(lr.Unbalance) < maxListed {
				lr.Unbalance = append(lr.Unbalance, fmt.Sprintf("%s: observed %d, sent %d, accepted %d, duplicates %d, refused %d, shed %d, pending %d (last error: %s)",
					r.rxCreds[i].id, l.Observed, l.Sent, l.Accepted, l.Duplicates, l.Refused, l.BacklogShed, l.Pending, rx.LastError()))
			}
		}
	}
	rep.Loss.Operator, rep.Loss.Receivers = lo, lr
	rep.Loss.GeneratorShedOperator, rep.Loss.GeneratorShedRID = r.shedOp, r.shedRID
	rep.Loss.Picture, rep.Loss.Traffic = r.picture.Close(), r.traffic.Close()
	rep.Loss.TargetCounters = r.target.Counters()
	var timed *stream
	consoleFrames := uint64(0)
	for _, s := range r.streams {
		st := s.stats()
		rep.Streams = append(rep.Streams, st)
		if s.kind == streamPicture {
			for _, n := range st.Frames {
				consoleFrames += n
			}
			if s.timed {
				timed = s
			}
		}
		if st.LastError != "" && st.Connects != 1 {
			r.fail("stream %s connected %d times; last error: %s", st.Name, st.Connects, st.LastError)
		}
	}
	// Offered load.
	heard := 0
	for _, idx := range fl.Receivers {
		heard += len(idx)
	}
	v := Volumes{Aircraft: len(fl.Aircraft), Clients: t.Operators.Clients, Heard: heard, Receivers: len(fl.Receivers), Consoles: t.Consoles,
		Watched: watchedSent, IntentsFiled: len(r.intents)}
	v.IntentErrors = r.intentErrors()
	if genS > 0 {
		v.OperatorMsgsPerS = round3(float64(lo.Sent) / genS)
		v.RIDLocationsPerS = round3(float64(rep.Loss.Picture.Handed) / genS)
		v.ConsoleFramesPerS = round3(float64(consoleFrames) / genS)
	}
	v.OperatorTargetPerS = float64(len(fl.Aircraft)) * t.Operators.TelemetryHz
	v.RIDTargetPerS = float64(heard) * t.Operators.TelemetryHz
	rep.Volumes = v
	ratio := func(got, want float64) Observation {
		if want <= 0 {
			return notMeasured("the tier offers none")
		}
		return valueObs(round3(got / want))
	}
	obs["operator_rate_ratio"] = ratio(v.OperatorMsgsPerS, v.OperatorTargetPerS)
	obs["rid_rate_ratio"] = ratio(v.RIDLocationsPerS, v.RIDTargetPerS)
	// Latencies.
	obs["operator_to_ussp_console_s"] = r.traffic.Histogram().Observe(t.MinSamples)
	if timed != nil {
		obs["receiver_to_authority_picture_s"] = r.picture.Histogram().Observe(t.MinSamples)
	} else {
		obs["receiver_to_authority_picture_s"] = notMeasured("the tier opens no console")
	}
	if heard == 0 {
		obs["receiver_to_authority_picture_s"] = notMeasured("the tier hears no aircraft")
	}
	// Loss identities.
	obs["operator_ledgers_unbalanced"] = valueObs(float64(lo.Unbalanced))
	if len(r.receivers) > 0 {
		obs["receiver_ledgers_unbalanced"] = valueObs(float64(lr.Unbalanced))
	} else {
		obs["receiver_ledgers_unbalanced"] = notMeasured("the tier hears no aircraft")
	}
	if timed != nil && heard > 0 {
		pc := rep.Loss.Picture
		dropped := timed.stats().DroppedFrames
		accounted := lr.Refused + dropped + r.shedRID + lr.BacklogShed + lr.DroppedByKnob + lr.Pending
		silent := uint64(0)
		if pc.Unshown > accounted {
			silent = pc.Unshown - accounted
		}
		rep.Loss.PictureDroppedFrames, rep.Loss.PictureAccounted, rep.Loss.PictureSilent = dropped, accounted, silent
		obs["picture_silent_loss"] = valueObs(float64(silent))
		obs["picture_untraceable_frames"] = valueObs(float64(pc.Untraceable + pc.Negative))
	} else {
		obs["picture_silent_loss"] = notMeasured("no timed console or no heard aircraft")
		obs["picture_untraceable_frames"] = notMeasured("no timed console or no heard aircraft")
	}
	// Alerts.
	r.judgeAlerts(rep, obs, t0)
	// Memory.
	if ratio, mono, ok := memoryVerdict(r.memory); ok {
		obs["memory_growth_ratio"] = valueObs(round3(ratio))
		m := 0.0
		if mono {
			m = 1
		}
		obs["memory_monotonic"] = valueObs(m)
	} else {
		obs["memory_growth_ratio"] = notMeasured(fmt.Sprintf("%d memory samples, at least 6 are needed", len(r.memory)))
		obs["memory_monotonic"] = obs["memory_growth_ratio"]
	}
	if r.late > 0 {
		r.fail("the generator fell behind its schedule %d times (saturated host)", r.late)
	}
	rep.Metrics = obs
	rep.Errors = append(rep.Errors, r.errs...)
}

func (r *run) intentErrors() int {
	want := 0
	for _, a := range r.fleet.Aircraft {
		if r.tier.Operators.Intents == "all" || a.Watched {
			want++
		}
	}
	return want - len(r.intents)
}

// judgeAlerts compares the observed proximity alerts with the expected
// set, times each matched raise from the sample it names, and counts
// what was missed, what was unexpected and what is untraceable.
func (r *run) judgeAlerts(rep *Report, obs map[string]Observation, t0 time.Time) {
	byAircraft := map[string][]observe.Event{}
	events := r.rec.Events()
	for i := range events {
		e := &events[i]
		if e.System != scenario.SystemUSSP || e.Kind != "proximity" {
			continue
		}
		byAircraft[e.Aircraft] = append(byAircraft[e.Aircraft], *e)
	}
	used := map[string]bool{}
	hist := NewHistogram()
	sum := AlertSummary{Expected: len(r.fleet.Expected)}
	rel := func(t time.Time) float64 { return t.Sub(t0).Seconds() }
	evalMax := 0.0
	for _, s := range r.streams {
		s.mu.Lock()
		evalMax = math.Max(evalMax, s.evalPeriodS)
		s.mu.Unlock()
	}
	for _, x := range r.fleet.Expected {
		var raise *observe.Event
		for i := range byAircraft[x.Aircraft] {
			e := &byAircraft[x.Aircraft][i]
			if e.Phase == observe.PhaseRaised && !used[e.AlertID] && rel(e.ObservedAt) >= x.RaiseFromS && rel(e.ObservedAt) <= x.RaiseToS {
				raise = e
				break
			}
		}
		if raise == nil {
			if len(sum.Missed) < maxListed {
				sum.Missed = append(sum.Missed, x)
			}
			continue
		}
		used[raise.AlertID] = true
		sum.Raised++
		rec := RaiseRecord{Aircraft: x.Aircraft, AlertID: raise.AlertID, ObservedS: round3(rel(raise.ObservedAt))}
		if raise.CapturedAt == nil {
			sum.Untraceable++
		} else {
			c := round3(rel(*raise.CapturedAt))
			rec.CapturedS = &c
			if handed, ok := r.readers.handedFor(raise.AlertID); ok && !raise.ObservedAt.Before(handed) {
				lat := raise.ObservedAt.Sub(handed)
				hist.Add(lat)
				l := round3(lat.Seconds())
				rec.LatencyS = &l
			} else {
				// No sample of the pair at that captured_at, or one handed
				// out after the raise arrived: either way not a latency.
				sum.Untraceable++
			}
		}
		if len(sum.Raises) < maxListed {
			sum.Raises = append(sum.Raises, rec)
		}
		cleared := false
		for i := range byAircraft[x.Aircraft] {
			e := &byAircraft[x.Aircraft][i]
			if e.Phase == observe.PhaseCleared && e.AlertID == raise.AlertID && rel(e.ObservedAt) <= x.ClearToS {
				cleared = true
				break
			}
		}
		if cleared {
			sum.Cleared++
		} else if len(sum.MissedClear) < maxListed {
			sum.MissedClear = append(sum.MissedClear, x)
		}
	}
	unexpected := 0
	names := make([]string, 0, len(byAircraft))
	for n := range byAircraft {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for i := range byAircraft[n] {
			e := &byAircraft[n][i]
			if e.Phase == observe.PhaseRaised && !used[e.AlertID] {
				unexpected++
				if len(sum.Unexpected) < maxListed {
					sum.Unexpected = append(sum.Unexpected, fmt.Sprintf("%s raised at %+.1f s (peer %s)", n, rel(e.ObservedAt), e.Peer))
				}
			}
		}
	}
	rep.Alerts = sum
	missed := len(r.fleet.Expected) - sum.Raised
	obs["expected_raises"] = valueObs(float64(len(r.fleet.Expected)))
	if len(r.fleet.Expected) == 0 {
		why := notMeasured("the run makes no alert due (no pair crossing inside it): nothing to miss")
		obs["missed_raises"], obs["missed_clears"], obs["alert_raise_s"], obs["alert_untraceable"] = why, why, why, why
	} else {
		obs["missed_raises"] = valueObs(float64(missed))
		obs["missed_clears"] = valueObs(float64(sum.Raised - sum.Cleared))
		obs["alert_raise_s"] = hist.Observe(1)
		obs["alert_untraceable"] = valueObs(float64(sum.Untraceable))
	}
	watched := 0
	for _, a := range r.fleet.Aircraft {
		if a.Watched {
			watched++
		}
	}
	if watched > 0 {
		obs["unexpected_alerts"] = valueObs(float64(unexpected))
	} else {
		obs["unexpected_alerts"] = notMeasured("no aircraft is watched")
	}
	if evalMax > 0 {
		obs["cpa_evaluation_period_max_s"] = valueObs(evalMax)
	} else {
		obs["cpa_evaluation_period_max_s"] = notMeasured("no proximity alert carried evaluation_period_s")
	}
}

// originLatLon is the lab origin as a position.
func (fl *Fleet) originLatLon() core.LatLon {
	return core.LatLon{LatDeg: fl.origin.LatDeg, LonDeg: fl.origin.LonDeg}
}

func hostInfo(name string) Host {
	if name == "" {
		name = "unnamed (pass --host)"
	}
	h := Host{Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Go: runtime.Version(),
		Note: "the host the generator and the reference target ran on together (L-Q1)"}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
				if kb, err := strconv.ParseUint(f[1], 10, 64); err == nil {
					v := kb * 1024
					h.MemTotalBytes = &v
				}
			}
		}
	}
	return h
}

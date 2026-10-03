// Command sim-ussp is the peer USSP (S-M4's second USSP, L-M4's
// onboarding candidate): it writes an Identification Service Area and
// one operational intent reference per --intent to the DSS, then serves
// the F3411 SP and F3548 USS endpoints for the SITL aircraft it is given
// (--aircraft) from the vehicle stream. No judgement: the standard shapes
// of uspace-core's f3411 and f3548 types.
//
//	sim-ussp --listen 127.0.0.1:8093 --base-url https://peer.lab \
//	  --dss-rid-base https://dss.lab/rid/v2 --dss-utm-base https://dss.lab \
//	  --token-url https://issuer.lab/oauth/token --client-id ussp-peer-01 \
//	  --client-secret-file peer.secret --dss-audience dss.lab \
//	  --issuer https://issuer.lab --jwks-url https://issuer.lab/.well-known/jwks.json --audience peer.lab \
//	  --isa 41.7151,44.8271,2000,500,900 --intent 41.7151,44.8271,500,600,760 \
//	  --aircraft 3=LABPEER0003 --vehicles udp:127.0.0.1:15562
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/f3411"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/simussp"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, " ") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func main() {
	os.Exit(run())
}

func run() int {
	var intents, aircraft multi
	var (
		listen      = flag.String("listen", "127.0.0.1:8093", "address to serve on")
		baseURL     = flag.String("base-url", "", "this USS's public base URL (uss_base_url)")
		dssRID      = flag.String("dss-rid-base", "", "the DSS's F3411 v22a base (InterUSS: .../rid/v2)")
		dssUTM      = flag.String("dss-utm-base", "", "the DSS's F3548 base (paths start /dss/v1)")
		tokenURL    = flag.String("token-url", "", "the issuer's /oauth/token")
		clientID    = flag.String("client-id", "", "this USS's client id at the issuer")
		secretFile  = flag.String("client-secret-file", "", "file holding the client secret")
		dssAudience = flag.String("dss-audience", "", "the DSS host (aud of DSS-bound tokens)")
		issuer      = flag.String("issuer", "", "the allow-listed issuer of incoming tokens")
		jwksURL     = flag.String("jwks-url", "", "that issuer's JWKS")
		audience    = flag.String("audience", "", "this USS's host (aud of incoming tokens)")
		isa         = flag.String("isa", "", "lat,lon,radius_m,alt_low_w84_m,alt_high_w84_m")
		vehicles    = flag.String("vehicles", "-", "vehicle stream: - (stdin) or udp:HOST:PORT")
	)
	flag.Var(&intents, "intent", "lat,lon,radius_m,alt_low_w84_m,alt_high_w84_m (repeatable; window now to +1 h)")
	flag.Var(&aircraft, "aircraft", "sysid=serial[:operator_id] (repeatable)")
	flag.Parse()
	log := cli.Logger("sim-ussp")
	secret, err := cli.ReadSecret(*secretFile)
	if err != nil || *baseURL == "" || *dssRID == "" || *dssUTM == "" || *tokenURL == "" || *issuer == "" || *jwksURL == "" || *audience == "" || *isa == "" {
		fmt.Fprintln(os.Stderr, "sim-ussp: --base-url, --dss-rid-base, --dss-utm-base, --token-url, --client-secret-file, --issuer, --jwks-url, --audience and --isa are required")
		return 2
	}
	isaV, err := floats(*isa, 5)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ussp: --isa:", err)
		return 2
	}
	now := time.Now().UTC()
	var ins []simussp.Intent
	for _, s := range intents {
		v, err := floats(s, 5)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sim-ussp: --intent:", err)
			return 2
		}
		ins = append(ins, simussp.Intent{Center: core.LatLon{LatDeg: v[0], LonDeg: v[1]}, RadiusM: v[2], AltLowerW84: v[3], AltUpperW84: v[4], Start: now, End: now.Add(time.Hour)})
	}
	var acs []simussp.Aircraft
	for _, s := range aircraft {
		k, v, ok := strings.Cut(s, "=")
		id, err := strconv.Atoi(k)
		if !ok || err != nil || v == "" {
			fmt.Fprintln(os.Stderr, "sim-ussp: --aircraft", s)
			return 2
		}
		serial, op, _ := strings.Cut(v, ":")
		acs = append(acs, simussp.Aircraft{Sysid: id, Serial: serial, OperatorID: op})
	}
	ctx, stop := cli.SignalContext()
	defer stop()
	ver, err := auth.NewVerifier(ctx, auth.Config{Issuers: map[string]auth.IssuerConfig{*issuer: {JWKSURL: *jwksURL}}, Audience: *audience})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ussp:", err)
		return 2
	}
	tokens := &oauth.ClientCredentials{TokenURL: *tokenURL, ClientID: *clientID, ClientSecret: secret, Audience: *dssAudience,
		Scopes: []string{string(f3411.ScopeServiceProvider), simussp.ScopeStrategicCoordination}}
	peer, err := simussp.New(simussp.Config{
		BaseURL: *baseURL, DSSRIDBase: *dssRID, DSSUTMBase: *dssUTM, Tokens: tokens,
		Verify: func(ctx context.Context, tok string) ([]string, error) {
			cl, err := ver.Verify(ctx, tok)
			return cl.Scopes, err
		},
		ISACenter: core.LatLon{LatDeg: isaV[0], LonDeg: isaV[1]}, ISARadiusM: isaV[2], ISAAltLow: isaV[3], ISAAltHigh: isaV[4],
		Aircraft: acs, Intents: ins,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ussp:", err)
		return 2
	}
	bus, err := cli.Vehicles(ctx, *vehicles, os.Stdin, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ussp:", err)
		return 2
	}
	sub := bus.Subscribe("peer", 256)
	go func() {
		for s := range sub.C {
			peer.Observe(&s)
		}
	}()
	if err := peer.PutISA(ctx); err != nil {
		log.Error("ISA not written", "err", err)
		return 1
	}
	id, v := peer.ISA()
	log.Info("ISA written", "id", id, "version", v)
	if err := peer.PutIntents(ctx); err != nil {
		log.Error("intent reference not written", "err", err)
		return 1
	}
	written := peer.Intents()
	for oid := range written {
		log.Info("operational intent reference written", "id", oid, "ovn", written[oid].Reference.Ovn)
	}
	srv := &http.Server{Addr: *listen, Handler: peer.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}

func floats(s string, n int) ([]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("%q: want %d numbers", s, n)
	}
	out := make([]float64, n)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || !core.IsFinite(v) {
			return nil, fmt.Errorf("%q is not a number", p)
		}
		out[i] = v
	}
	return out, nil
}

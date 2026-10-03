// Command sim-ansp-feed serves manned traffic as the ANSP does (spec 02
// F4): WS /v1/manned-traffic/stream?bbox= and GET
// /v1/manned-traffic/snapshot, track/manned/v1 in the common envelope at
// 1 Hz per aircraft and console/status/v1 every 2 s, from a recording
// (testdata/adsb/*.jsonl). Callers present a bearer token granting
// ansp.traffic, verified with uspace-core against the issuer's JWKS (the
// lab issuer, or the authority's token service); without an issuer it
// refuses to start.
//
//	sim-ansp-feed --listen 127.0.0.1:8091 --recording testdata/adsb/synthetic-approach.jsonl \
//	  --issuer https://issuer.lab --jwks-url https://issuer.lab/.well-known/jwks.json \
//	  --audience ansp.lab --policy scenarios/policy/demo.yaml --admin-token-file feed.admin
//
// POST /lab/state {"state": "live"|"stale"|"outage"} with the admin token
// switches the feed (SC-15).
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/manned"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simfeed"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		listen    = flag.String("listen", "127.0.0.1:8091", "address to serve on")
		recording = flag.String("recording", "", "the .jsonl recording to replay")
		loop      = flag.Bool("loop", true, "replay the recording again when it ends")
		instance  = flag.String("instance", "lab-adsb-1", "the adapter instance id the frames name")
		issuer    = flag.String("issuer", "", "the allow-listed token issuer (iss)")
		jwksURL   = flag.String("jwks-url", "", "the issuer's JWKS (https, or loopback http)")
		audience  = flag.String("audience", "", "this feed's host (aud)")
		policy    = flag.String("policy", "scenarios/policy/demo.yaml", "the policy file (stale_after_s, live_max_age_s, policy_version)")
		adminFile = flag.String("admin-token-file", "", "file holding the token for POST /lab/state")
	)
	flag.Parse()
	log := cli.Logger("sim-ansp-feed")
	if *recording == "" || *issuer == "" || *jwksURL == "" || *audience == "" {
		fmt.Fprintln(os.Stderr, "sim-ansp-feed: --recording, --issuer, --jwks-url and --audience are required (a feed that verifies nobody is not started)")
		return 2
	}
	pol, err := scenario.LoadPolicy(*policy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ansp-feed:", err)
		return 2
	}
	rec, err := manned.LoadRecording(*recording)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ansp-feed:", err)
		return 2
	}
	rec.T0, rec.Loop = time.Now(), *loop
	ctx, stop := cli.SignalContext()
	defer stop()
	ver, err := auth.NewVerifier(ctx, auth.Config{Issuers: map[string]auth.IssuerConfig{*issuer: {JWKSURL: *jwksURL}}, Audience: *audience})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ansp-feed:", err)
		return 2
	}
	feed, err := simfeed.New(simfeed.Config{
		Source: rec, Instance: *instance, StaleAfterS: pol.Monitor.StaleAfterS, LiveMaxAgeS: pol.Monitor.LiveMaxAgeS,
		PolicyVersion: strconv.Itoa(pol.PolicyVersion),
		Verify: func(ctx context.Context, tok string) ([]string, error) {
			cl, err := ver.Verify(ctx, tok)
			return cl.Scopes, err
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-ansp-feed:", err)
		return 2
	}
	admin := ""
	if *adminFile != "" {
		if admin, err = cli.ReadSecret(*adminFile); err != nil {
			fmt.Fprintln(os.Stderr, "sim-ansp-feed:", err)
			return 2
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/", feed.Handler())
	mux.HandleFunc("POST /lab/state", func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if admin == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(admin)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || feed.SetState(in.State) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		log.Info("state", "state", in.State)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("serving", "listen", *listen, "recording", *recording, "policy_version", pol.PolicyVersion)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}

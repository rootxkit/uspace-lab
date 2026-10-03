// Command sim-operator streams SITL aircraft to a USSP as an operator's
// software would (spec 02 F5): one client per aircraft with a machine
// token from the USSP's own issuer, telemetry/v1 at 1 Hz over WS
// /v1/telemetry (or the batch endpoint), the vehicle's own time, a ten
// minute queue replayed as backlog after a disconnect. It reads the
// vehicle stream (sim/vehicle/v1) from sim/mav_reader.py; it never opens
// MAVLink and has no path towards a vehicle (INV-01).
//
//	python sim/mav_reader.py --sysid 1 --out udp:127.0.0.1:14560 \
//	  | sim-operator --ussp https://ussp.lab --token-url https://ussp.lab/oauth/token \
//	      --client-id lab-op-1 --client-secret-file op.secret --audience ussp.lab \
//	      --aircraft 1=LABSER0001 --geoid constant:15.9
//
// On SIGINT or SIGTERM it drains (every frame acknowledged, or 30 s) and
// prints one ledger per aircraft as JSON on stdout; it exits 1 when a
// ledger does not balance (accepted + refused + dropped + duplicate =
// sent).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/simop"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		usspURL    = flag.String("ussp", "", "the USSP's base URL")
		tokenURL   = flag.String("token-url", "", "the USSP's /oauth/token")
		clientID   = flag.String("client-id", "", "operator machine client id")
		secretFile = flag.String("client-secret-file", "", "file holding the client secret")
		audience   = flag.String("audience", "", "the USSP's host (aud)")
		aircraft   = flag.String("aircraft", "", "sysid=serial[,sysid=serial...]")
		vehicles   = flag.String("vehicles", "-", "vehicle stream: - (stdin) or udp:HOST:PORT")
		transport  = flag.String("transport", simop.TransportWS, "ws or batch")
		geoidSpec  = flag.String("geoid", "", "constant:<N metres> or a GeographicLib .pgm; empty sends no HAE")
		dropRate   = flag.Float64("drop-rate", 0, "fraction of samples dropped before sending")
		latency    = flag.Duration("latency", 0, "delay before each frame is written")
		operator   = flag.String("operator-pos", "", "lat,lon of the remote pilot")
		intentID   = flag.String("intent-id", "", "an activated intent the frames fly (optional)")
	)
	flag.Parse()
	log := cli.Logger("sim-operator")
	secret, err := cli.ReadSecret(*secretFile)
	if err != nil || *usspURL == "" || *tokenURL == "" || *clientID == "" || *aircraft == "" {
		fmt.Fprintln(os.Stderr, "sim-operator: --ussp, --token-url, --client-id, --client-secret-file and --aircraft are required", err)
		return 2
	}
	pairs, err := parseAircraft(*aircraft)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-operator:", err)
		return 2
	}
	var g geoid.Undulator
	if *geoidSpec != "" {
		var desc string
		if g, desc, err = geoidx.Load(*geoidSpec); err != nil {
			fmt.Fprintln(os.Stderr, "sim-operator:", err)
			return 2
		}
		log.Info("geoid", "model", desc)
	} else {
		log.Warn("no geoid: alt_wgs84_m is sent as null (R-16)")
	}
	op, err := cli.LatLon(*operator)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-operator:", err)
		return 2
	}
	ctx, stop := cli.SignalContext()
	defer stop()
	bus, err := cli.Vehicles(ctx, *vehicles, os.Stdin, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-operator:", err)
		return 2
	}
	tokens := &oauth.ClientCredentials{TokenURL: *tokenURL, ClientID: *clientID, ClientSecret: secret,
		Scopes: []string{"ussp.telemetry"}, Audience: *audience}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	clients := map[int]*simop.Client{}
	var wg sync.WaitGroup
	for sysid, serial := range pairs {
		c, err := simop.New(simop.Config{BaseURL: *usspURL, Tokens: tokens, Serial: serial, Transport: *transport,
			DropRate: *dropRate, Latency: *latency, Geoid: g, OperatorPos: op, IntentID: *intentID})
		if err != nil {
			fmt.Fprintln(os.Stderr, "sim-operator:", err)
			return 2
		}
		clients[sysid] = c
		sub := bus.Subscribe(serial, 64, sysid)
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Run(runCtx, sub.C) }()
	}
	limiter := cli.NewLimiter(10 * time.Second)
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tk.C:
			for _, c := range clients {
				l := c.Ledger()
				if limiter.Allow(l.Serial) {
					log.Info("ledger", "serial", l.Serial, "sent", l.Sent, "accepted", l.Accepted, "refused", l.Refused,
						"dropped", l.Dropped, "pending", l.Pending, "last_error", c.LastError())
				}
			}
		}
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dcancel()
	ok := true
	out := json.NewEncoder(os.Stdout)
	for _, c := range clients {
		l := c.Drain(dctx)
		ok = ok && l.Balanced
		_ = out.Encode(l)
	}
	cancelRun()
	wg.Wait()
	if !ok {
		return 1
	}
	return 0
}

func parseAircraft(s string) (map[int]string, error) {
	out := map[int]string{}
	for _, p := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		id, err := strconv.Atoi(k)
		if !ok || err != nil || id < 1 || id > 254 || v == "" {
			return nil, fmt.Errorf("--aircraft %q: want sysid=serial", p)
		}
		out[id] = v
	}
	return out, nil
}

// Command sim-receiver broadcasts SITL aircraft as Remote ID and reports
// them as a ground receiver would (spec 02 F9): ODID Basic ID, Location,
// System and Operator ID encoded by uspace-core (HAE = AMSL + N, R-16),
// message packs (Bluetooth 5, Wi-Fi) or single messages (Bluetooth 4),
// batches of at most one second to POST /v1/rid/observations with the
// receiver's bearer key and the body's HMAC in X-Report-Signature. It
// reads the vehicle stream (sim/vehicle/v1); it never opens MAVLink.
//
//	sim-receiver --authority https://authority.lab --receiver-id lab-rx-1 \
//	  --bearer-key-file rx.key --hmac-key-file rx.hmac --geoid constant:15.9 \
//	  --tx 1=02:00:00:00:00:01,LABSER0001,GEOLAB000001 --vehicles udp:127.0.0.1:15560
//
// On SIGINT or SIGTERM it drains and prints its ledger as JSON on stdout;
// exit 1 when it does not balance.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rootxkit/uspace-core/geoid"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/simrx"
)

type txFlags []simrx.Transmitter

func (t *txFlags) String() string { return fmt.Sprint(*t) }

func (t *txFlags) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	id, err := strconv.Atoi(k)
	parts := strings.Split(v, ",")
	if !ok || err != nil || id < 1 || id > 254 || len(parts) < 1 || parts[0] == "" {
		return fmt.Errorf("--tx %q: want sysid=MAC[,serial[,operator_id]]", s)
	}
	tx := simrx.Transmitter{Sysid: id, MAC: parts[0]}
	if len(parts) > 1 {
		tx.Serial = parts[1]
	}
	if len(parts) > 2 {
		tx.OperatorID = parts[2]
	}
	*t = append(*t, tx)
	return nil
}

func main() {
	os.Exit(run())
}

func run() int {
	var txs txFlags
	var (
		authority  = flag.String("authority", "", "the authority's base URL")
		receiverID = flag.String("receiver-id", "", "the receiver's registered id")
		bearerFile = flag.String("bearer-key-file", "", "file holding the receiver's bearer key")
		hmacFile   = flag.String("hmac-key-file", "", "file holding the receiver's HMAC secret, hex")
		vehicles   = flag.String("vehicles", "-", "vehicle stream: - (stdin) or udp:HOST:PORT")
		transport  = flag.String("transport", simrx.TransportPack, "pack or single")
		hae        = flag.String("hae", simrx.HAEGeoid, "geoid (AMSL + N) or gps (the vehicle's alt_hae_m)")
		geoidSpec  = flag.String("geoid", "", "constant:<N metres> or a GeographicLib .pgm")
		dropRate   = flag.Float64("drop-rate", 0, "fraction of broadcasts not heard")
		latency    = flag.Duration("latency", 0, "delay before each batch is sent")
		position   = flag.String("position", "", "lat,lon of the receiver")
		rssi       = flag.Float64("rssi-dbm", -60, "reported signal strength")
	)
	flag.Var(&txs, "tx", "sysid=MAC[,serial[,operator_id]] (repeatable)")
	flag.Parse()
	log := cli.Logger("sim-receiver")
	bearer, err1 := cli.ReadSecret(*bearerFile)
	key, err2 := cli.ReadHexKey(*hmacFile)
	if err1 != nil || err2 != nil || *authority == "" || *receiverID == "" || len(txs) == 0 {
		fmt.Fprintln(os.Stderr, "sim-receiver: --authority, --receiver-id, --bearer-key-file, --hmac-key-file and --tx are required")
		return 2
	}
	var g geoid.Undulator
	if *geoidSpec != "" {
		var desc string
		var err error
		if g, desc, err = geoidx.Load(*geoidSpec); err != nil {
			fmt.Fprintln(os.Stderr, "sim-receiver:", err)
			return 2
		}
		log.Info("geoid", "model", desc)
	} else if *hae == simrx.HAEGeoid {
		log.Warn("no geoid: every Location is broadcast without HAE (R-16)")
	}
	pos, err := cli.LatLon(*position)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-receiver:", err)
		return 2
	}
	r, err := simrx.New(simrx.Config{BaseURL: *authority, ReceiverID: *receiverID, BearerKey: bearer, HMACKey: key,
		Transport: *transport, HAE: *hae, Geoid: g, Position: pos, RSSIDBm: *rssi, DropRate: *dropRate, Latency: *latency, Transmitters: txs})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-receiver:", err)
		return 2
	}
	ctx, stop := cli.SignalContext()
	defer stop()
	bus, err := cli.Vehicles(ctx, *vehicles, os.Stdin, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-receiver:", err)
		return 2
	}
	sub := bus.Subscribe("receiver", 256)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() { _ = r.Run(runCtx, sub.C) }()
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tk.C:
			l := r.Ledger()
			log.Info("ledger", "observed", l.Observed, "sent", l.Sent, "accepted", l.Accepted, "refused", l.Refused,
				"pending", l.Pending, "last_error", r.LastError())
		}
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dcancel()
	l := r.Drain(dctx)
	_ = json.NewEncoder(os.Stdout).Encode(l)
	if !l.Balanced {
		return 1
	}
	return 0
}

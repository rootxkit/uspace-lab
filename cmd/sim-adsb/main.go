// Command sim-adsb serves a recording as a readsb / dump1090
// aircraft.json for the USSP's e-conspicuity reader and the ANSP's
// dump1090_json adapter (docs/PLAN.md L-Q5 default).
//
//	sim-adsb --listen 127.0.0.1:8092 --recording testdata/adsb/synthetic-approach.jsonl
//
// The file is at /data/aircraft.json (--path).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/manned"
	"github.com/rootxkit/uspace-lab/internal/simadsb"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		listen    = flag.String("listen", "127.0.0.1:8092", "address to serve on")
		recording = flag.String("recording", "", "the .jsonl recording to replay")
		path      = flag.String("path", "/data/aircraft.json", "where the file is served")
		loop      = flag.Bool("loop", true, "replay the recording again when it ends")
	)
	flag.Parse()
	log := cli.Logger("sim-adsb")
	rec, err := manned.LoadRecording(*recording)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sim-adsb:", err)
		return 2
	}
	rec.T0, rec.Loop = time.Now(), *loop
	srv := &http.Server{Addr: *listen, Handler: &simadsb.Server{Source: rec, Path: *path}, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := cli.SignalContext()
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("serving", "listen", *listen, "path", *path, "recording", *recording)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}

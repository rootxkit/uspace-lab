// Command results renders the scenario results as static HTML
// (internal/resultsite; docs/WORKPACKAGES/WP-L6.md): one page per run
// and the trend of every scenario over the runs.
//
//	results build --in results --out site      write the pages
//	results serve --in results --listen :8080  serve them, re-read per request
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/cli"
	"github.com/rootxkit/uspace-lab/internal/resultsite"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || (args[0] != "build" && args[0] != "serve") {
		fmt.Fprintln(os.Stderr, "usage: results build --in DIR --out DIR | results serve --in DIR --listen ADDR")
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	in := fs.String("in", "results", "the results directory")
	out := fs.String("out", "site", "where build writes the pages")
	listen := fs.String("listen", "127.0.0.1:8095", "where serve listens")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if args[0] == "build" {
		s, err := resultsite.Load(*in)
		if err == nil {
			err = s.Write(*out)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "results:", err)
			return 1
		}
		fmt.Printf("results: %d runs, %d scenarios, %d unreadable files -> %s\n", len(s.Runs), len(s.Scenarios), len(s.Unreadable), *out)
		return 0
	}
	log := cli.Logger("results")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := resultsite.Load(*in)
		var ps map[string][]byte
		if err == nil {
			ps, err = s.Pages()
		}
		if err != nil {
			http.Error(w, "the results cannot be read", http.StatusInternalServerError)
			log.Error("render", "err", err)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		b, ok := ps[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b) //nolint:gosec // html/template output: every value is escaped
	})
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	log.Info("serving", "listen", *listen, "results", *in)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		return 1
	}
	return 0
}

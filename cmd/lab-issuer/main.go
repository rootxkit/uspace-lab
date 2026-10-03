// Command lab-issuer is the lab's ecosystem token service
// (docs/WORKPACKAGES/WP-L2.md). Subcommands:
//
//	lab-issuer serve        run the token service (the default)
//	lab-issuer probe        prove the DSS and this issuer together (make dss-up)
//	lab-issuer healthcheck  exit 0 when GET /healthz answers 200 (compose)
//
// Every flag defaults to an environment variable, named in its help, so
// the compose file configures it with env alone.
package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rootxkit/uspace-lab/internal/issuer"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "probe":
		err = probe(args)
	case "healthcheck":
		err = healthcheck(args)
	default:
		err = fmt.Errorf("unknown subcommand %q (serve, probe, healthcheck)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lab-issuer:", err)
		os.Exit(1)
	}
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// envDuration returns the duration in name, or def when name is unset.
// An unparsable value is an error, never a silent fallback.
func envDuration(name string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a duration", name, v)
	}
	return d, nil
}

func serve(args []string) error {
	ttlDefault, err := envDuration("LAB_ISSUER_TOKEN_TTL", 50*time.Minute)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", env("LAB_ISSUER_ADDR", ":8080"), "listen address (LAB_ISSUER_ADDR)")
	issuerURL := fs.String("issuer-url", env("LAB_ISSUER_URL", ""), "the iss claim: this issuer's URL as verifiers allow-list it (LAB_ISSUER_URL, required)")
	clientsPath := fs.String("clients", env("LAB_ISSUER_CLIENTS", "deploy/issuer/clients.yaml"), "clients file (LAB_ISSUER_CLIENTS)")
	stateDir := fs.String("state-dir", env("LAB_ISSUER_STATE_DIR", ""), "git-ignored directory for the generated key and secrets and the public key outputs (LAB_ISSUER_STATE_DIR)")
	keyFile := fs.String("key-file", env("LAB_ISSUER_KEY_FILE", ""), "PEM path of a stable signing key (staging); never a committed key (LAB_ISSUER_KEY_FILE)")
	kid := fs.String("kid", env("LAB_ISSUER_KID", ""), "kid of --key-file when its PEM has no Kid header (LAB_ISSUER_KID)")
	ttl := fs.Duration("token-ttl", ttlDefault, "token lifetime, at most 1h (LAB_ISSUER_TOKEN_TTL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stateDir == "" {
		return errors.New("--state-dir is required: secrets are generated into it and read back by the probe")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	raw, err := readBounded(*clientsPath, issuer.MaxClientsFileBytes)
	if err != nil {
		return err
	}
	reg, err := issuer.LoadClients(raw, os.LookupEnv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	var (
		key        *rsa.PrivateKey
		keyID      string
		keyCreated bool
	)
	if *keyFile != "" {
		k, id, err := issuer.LoadKeyFile(*keyFile, *kid)
		if err != nil {
			return err
		}
		key, keyID = k, id
	} else {
		k, id, created, err := issuer.LoadOrCreateStateKey(*stateDir, now)
		if err != nil {
			return err
		}
		key, keyID, keyCreated = k, id, created
	}

	plain, created, err := issuer.LoadOrCreateSecrets(*stateDir, reg.IDs())
	if err != nil {
		return err
	}
	secrets, err := issuer.NewSecrets(plain)
	if err != nil {
		return err
	}
	srv, err := issuer.NewServer(issuer.Config{
		IssuerURL: *issuerURL, Key: key, Kid: keyID, Clients: reg, Secrets: secrets, TTL: *ttl,
	})
	if err != nil {
		return err
	}
	if err := issuer.WritePublic(*stateDir, &key.PublicKey, srv.JWKS()); err != nil {
		return err
	}
	log.Info("lab issuer key", "kid", keyID, "generated", keyCreated, "issuer", *issuerURL, "clients", len(reg.IDs()))
	if len(created) > 0 {
		// Printed once, when generated (WP-L2); afterwards they are only in
		// the state directory's client-secrets.json.
		_, _ = fmt.Fprintf(os.Stdout, "lab-issuer: generated client secrets (shown once; kept in %s/%s):\n", *stateDir, issuer.StateSecretsFile)
		for _, id := range created {
			_, _ = fmt.Fprintf(os.Stdout, "  %s %s\n", id, plain[id])
		}
	}

	hs := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("lab issuer listening", "addr", *addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the clients file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return b, nil
}

func envFloat(name string) (float64, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return 0, fmt.Errorf("%s is unset: the probe area is configuration (deploy/.env)", name)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number", name, v)
	}
	return f, nil
}

func probe(args []string) error {
	windowDefault, err := envDuration("DSS_PROBE_WINDOW", 10*time.Minute)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	issuerURL := fs.String("issuer-url", env("LAB_ISSUER_PROBE_URL", "http://127.0.0.1:8080"), "where the probe reaches this issuer (LAB_ISSUER_PROBE_URL)")
	dssURL := fs.String("dss-url", env("DSS_URL", ""), "the DSS base URL; its host is the token audience (DSS_URL, required)")
	stateDir := fs.String("state-dir", env("LAB_ISSUER_STATE_DIR", ""), "state directory holding client-secrets.json (LAB_ISSUER_STATE_DIR)")
	client := fs.String("client", env("DSS_PROBE_CLIENT", "sim-ussp-01"), "client the probe requests tokens as (DSS_PROBE_CLIENT)")
	wrongAud := fs.String("wrong-audience", env("DSS_PROBE_WRONG_AUDIENCE", "not-the-dss.invalid"), "an audience the DSS must refuse (DSS_PROBE_WRONG_AUDIENCE)")
	window := fs.Duration("window", windowDefault, "time window of the search from now (DSS_PROBE_WINDOW)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dssURL == "" || *stateDir == "" {
		return errors.New("--dss-url and --state-dir are required")
	}
	var area issuer.ProbeArea
	for _, p := range []struct {
		name string
		dst  *float64
	}{
		{"DSS_PROBE_LAT_DEG", &area.LatDeg},
		{"DSS_PROBE_LNG_DEG", &area.LngDeg},
		{"DSS_PROBE_RADIUS_M", &area.RadiusM},
		{"DSS_PROBE_ALT_LOWER_WGS84_M", &area.AltLowerWGS84M},
		{"DSS_PROBE_ALT_UPPER_WGS84_M", &area.AltUpperWGS84M},
	} {
		if *p.dst, err = envFloat(p.name); err != nil {
			return err
		}
	}
	area.Window = *window
	secret, err := issuer.ReadSecret(*stateDir, *client)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return issuer.Probe(ctx, issuer.ProbeConfig{
		IssuerURL: *issuerURL, DSSURL: *dssURL, ClientID: *client, Secret: secret,
		Area: area, WrongAudience: *wrongAud, Out: os.Stdout,
	})
}

func healthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	u := fs.String("url", env("LAB_ISSUER_PROBE_URL", "http://127.0.0.1:8080")+"/healthz", "health URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(*u)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", *u, resp.StatusCode)
	}
	return nil
}

package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rootxkit/uspace-lab/internal/seed"
)

// hostKeys are the demo.env keys of the hosts the lab Caddy serves
// (deploy/demo.env.example): every system, and the DSS with the issuer.
var hostKeys = []string{"AUTHORITY_HOST", "CISP_HOST", "USSP_HOST", "ANSP_HOST", "LAB_HOST"}

// Lab is the stack under test as deploy/demo.env describes it: the compose
// project, the hosts and the published port, and an HTTP client that
// dials every lab host at 127.0.0.1 with the lab CA (internal/seed), so
// nothing on the machine changes (no hosts file, no trust store).
type Lab struct {
	EnvFile  string
	Project  string
	Network  string
	Port     int
	Hosts    map[string]string // demo.env key -> host
	StateDir string
	HTTP     *http.Client
	// Transport is HTTP's transport, installed as http.DefaultTransport
	// for the background run (the runner and the simulators use the
	// default client).
	Transport http.RoundTripper
}

// LoadLab reads the env file. project is the compose project name
// (CHAOS_PROJECT, else COMPOSE_PROJECT_NAME, else uspace-demo, as
// deploy/demo-up.sh and scripts/chaos/lib.sh choose it).
func LoadLab(envFile, project string) (*Lab, error) {
	env, err := seed.Env(envFile)
	if err != nil {
		return nil, fmt.Errorf("chaos: %w", err)
	}
	l := &Lab{EnvFile: envFile, Project: project, Hosts: map[string]string{}}
	if l.Project == "" {
		l.Project = firstNonEmpty(os.Getenv("CHAOS_PROJECT"), os.Getenv("COMPOSE_PROJECT_NAME"), "uspace-demo")
	}
	l.Network = firstNonEmpty(os.Getenv("CHAOS_NETWORK"), l.Project+"_lab")
	l.Port, err = strconv.Atoi(firstNonEmpty(env["DEMO_HTTPS_PORT"], "443"))
	if err != nil || l.Port <= 0 || l.Port > 65535 {
		return nil, fmt.Errorf("chaos: DEMO_HTTPS_PORT %q in %s is not a port", env["DEMO_HTTPS_PORT"], envFile)
	}
	hosts := make([]string, 0, len(hostKeys))
	for _, k := range hostKeys {
		if env[k] == "" {
			return nil, fmt.Errorf("chaos: %s is not set in %s", k, envFile)
		}
		l.Hosts[k] = env[k]
		hosts = append(hosts, env[k])
	}
	state := firstNonEmpty(env["DEMO_STATE_DIR"], "./local-demo")
	if !filepath.IsAbs(state) {
		state = filepath.Join(filepath.Dir(envFile), state)
	}
	l.StateDir = state
	c, err := seed.NewClient(filepath.Join(state, "ca", "ca.pem"), l.Port, hosts)
	if err != nil {
		return nil, fmt.Errorf("chaos: %w", err)
	}
	l.HTTP = c.HTTP
	l.Transport = c.HTTP.Transport
	return l, nil
}

// URL is https://<host of key>[:port]<path>.
func (l *Lab) URL(hostKey, path string) (string, error) {
	h, ok := l.Hosts[hostKey]
	if !ok {
		return "", fmt.Errorf("chaos: no host %s in %s (one of %s)", hostKey, l.EnvFile, strings.Join(hostKeys, ", "))
	}
	if l.Port == 443 {
		return "https://" + h + path, nil
	}
	return fmt.Sprintf("https://%s:%d%s", h, l.Port, path), nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

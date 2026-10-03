// Package cli holds what the cmd/sim-* binaries share: the vehicle
// source flag, secrets read from files, the rate-limited log and the
// signal context.
package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// MaxStateFileBytes bounds a secrets or JWKS file read from the lab
// issuer's state directory (E-10).
const MaxStateFileBytes = 64 << 10

// SignalContext ends on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Vehicles starts the vehicle stream named by spec onto a new bus:
// "-" reads NDJSON on stdin (sim/mav_reader.py piped in),
// "udp:HOST:PORT" listens for mav_reader --emit datagrams.
func Vehicles(ctx context.Context, spec string, stdin io.Reader, log *slog.Logger) (*vehicle.Bus, error) {
	bus := vehicle.NewBus()
	var counters core.Counters
	limiter := NewLimiter(10 * time.Second)
	onInvalid := func(err error) {
		if limiter.Allow("invalid") {
			log.Warn("invalid vehicle line", "err", err, "invalid_lines", counters.Get(vehicle.CounterInvalidLines))
		}
	}
	switch {
	case spec == "-":
		go func() {
			if err := vehicle.ReadLines(ctx, stdin, bus.Publish, &counters, onInvalid); err != nil {
				log.Error("vehicle stream", "err", err)
			}
		}()
	case strings.HasPrefix(spec, "udp:"):
		addr := strings.TrimPrefix(spec, "udp:")
		go func() {
			if err := vehicle.ListenUDP(ctx, addr, bus.Publish, &counters, onInvalid); err != nil {
				log.Error("vehicle stream", "err", err)
			}
		}()
	default:
		return nil, fmt.Errorf("--vehicles %q: want - or udp:HOST:PORT", spec)
	}
	return bus, nil
}

// ReadSecret reads a secret from a file (never a flag value: it would be
// in the process list), trimmed.
func ReadSecret(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("a secret file is required")
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return "", fmt.Errorf("secret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ReadSecretJSON reads client id's secret from a JSON object of client
// id to secret: the lab issuer's client-secrets.json (deploy/README.md),
// so a simulator in the lab stack reads its secret where the issuer
// wrote it. A missing or empty entry is an error.
func ReadSecretJSON(path, id string) (string, error) {
	b, err := readBounded(path)
	if err != nil {
		return "", fmt.Errorf("secret: %w", err)
	}
	var plain map[string]string
	if err := json.Unmarshal(b, &plain); err != nil {
		return "", fmt.Errorf("secret: %s: %w", path, err)
	}
	s := strings.TrimSpace(plain[id])
	if s == "" {
		return "", fmt.Errorf("secret: %s has no secret for client %q", path, id)
	}
	return s, nil
}

// ReadJWKS reads a static JWKS file (the lab issuer's public/jwks.json):
// inside the lab network the issuer is plain HTTP, which uspace-core's
// verifier fetches from localhost only, so the keys are given statically.
func ReadJWKS(path string) (jwk.Set, error) {
	b, err := readBounded(path)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	set, err := jwk.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("jwks: %s: %w", path, err)
	}
	if set.Len() == 0 {
		return nil, fmt.Errorf("jwks: %s holds no key", path)
	}
	return set, nil
}

func readBounded(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("a file is required")
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, err
	}
	if len(b) > MaxStateFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, MaxStateFileBytes)
	}
	return b, nil
}

// ReadHexKey reads a hex key from a file.
func ReadHexKey(path string) ([]byte, error) {
	s, err := ReadSecret(path)
	if err != nil {
		return nil, err
	}
	k, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("key in %s is not hex: %w", path, err)
	}
	return k, nil
}

// LatLon parses "lat,lon".
func LatLon(s string) (*core.LatLon, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // absent is not an error
	}
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return nil, fmt.Errorf("%q is not lat,lon", s)
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	p := core.LatLon{LatDeg: lat, LonDeg: lon}
	if err1 != nil || err2 != nil || !p.Valid() {
		return nil, fmt.Errorf("%q is not a valid lat,lon", s)
	}
	return &p, nil
}

// Limiter allows one event per key per interval (E-09: logged at a
// bounded rate).
type Limiter struct {
	mu    sync.Mutex
	every time.Duration
	last  map[string]time.Time
}

// NewLimiter makes a limiter.
func NewLimiter(every time.Duration) *Limiter {
	return &Limiter{every: every, last: map[string]time.Time{}}
}

// Allow reports whether key may log now.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.last[key]; ok && now.Sub(t) < l.every {
		return false
	}
	l.last[key] = now
	return true
}

// Logger is the binaries' JSON logger on stderr.
func Logger(name string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("process", name)
}

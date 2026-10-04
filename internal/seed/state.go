package seed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is what the seed has created and must not create twice, kept in
// <state dir>/seed-state.json (git-ignored with the rest of the state
// directory). It holds secrets: the console passwords and TOTP secrets
// the seed chose or was given, and the machine credentials the systems
// showed once.
type State struct {
	path string
	mu   sync.Mutex

	// Accounts by "<system>/<username>".
	Accounts map[string]*Account `json:"accounts"`
	// RegistryOperators maps a registration number to the authority's id.
	RegistryOperators map[string]string `json:"registry_operators"`
	// RegistryUAS maps a serial to the authority's id.
	RegistryUAS map[string]string `json:"registry_uas"`
	// Receivers maps a receiver id to its credentials.
	Receivers map[string]*ReceiverCreds `json:"receivers"`
	// USSPOperators maps a registration number to the USSP's operator.
	USSPOperators map[string]*USSPOperator `json:"ussp_operators"`
	// Zones maps a zone identifier to the version the seed published.
	Zones map[string]int `json:"zones"`
	// USpace is the U-space airspace identifier published, and its version.
	USpace        string `json:"uspace"`
	USpaceVersion int    `json:"uspace_version"`
	USpaceDigest  string `json:"uspace_digest"`
}

// Account is a console account: its password, its TOTP secret once
// enrolled, and the last TOTP step used (a code is accepted once).
type Account struct {
	Password string   `json:"password"`
	TOTP     string   `json:"totp,omitempty"`
	LastStep int64    `json:"last_step,omitempty"`
	Recovery []string `json:"recovery,omitempty"`
}

// ReceiverCreds are a receiver's keys as the authority showed them once.
type ReceiverCreds struct {
	BearerKey string `json:"bearer_key"`
	HMACHex   string `json:"hmac_secret_hex"`
}

// USSPOperator is an operator at the USSP and its machine client.
type USSPOperator struct {
	ID            string          `json:"id"`
	AdminUser     string          `json:"admin_user"`
	AdminPassword string          `json:"admin_password"`
	ClientID      string          `json:"client_id,omitempty"`
	ClientSecret  string          `json:"client_secret,omitempty"`
	Serials       map[string]bool `json:"serials,omitempty"`
}

// LoadState reads the state file, or starts an empty one.
func LoadState(path string) (*State, error) {
	s := &State{path: path}
	b, err := os.ReadFile(path) //nolint:gosec // the state directory is the operator's
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, s); err != nil {
			return nil, fmt.Errorf("seed: %s: %w", path, err)
		}
	}
	if s.Accounts == nil {
		s.Accounts = map[string]*Account{}
	}
	if s.RegistryOperators == nil {
		s.RegistryOperators = map[string]string{}
	}
	if s.RegistryUAS == nil {
		s.RegistryUAS = map[string]string{}
	}
	if s.Receivers == nil {
		s.Receivers = map[string]*ReceiverCreds{}
	}
	if s.USSPOperators == nil {
		s.USSPOperators = map[string]*USSPOperator{}
	}
	// An operator saved before any serial was bound has no serials
	// member (omitempty); a seed resumed after a failure binds into it.
	for _, op := range s.USSPOperators {
		if op != nil && op.Serials == nil {
			op.Serials = map[string]bool{}
		}
	}
	if s.Zones == nil {
		s.Zones = map[string]int{}
	}
	return s, nil
}

// Save writes the state (0600), atomically.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Account returns the account, creating it with a new random password.
func (s *State) Account(system, user string) *Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := system + "/" + user
	a := s.Accounts[k]
	if a == nil {
		a = &Account{Password: randomPassword()}
		s.Accounts[k] = a
	}
	return a
}

// Code is the account's TOTP code for a step after the last one used:
// it waits for the next 30 s step when the current one was used (each
// system accepts a code once).
func (a *Account) Code(ctx context.Context, now func() time.Time) (string, error) {
	for {
		t := now()
		step := t.Unix() / 30
		if step > a.LastStep {
			code, err := TOTP(a.TOTP, t)
			if err != nil {
				return "", err
			}
			a.LastStep = step
			return code, nil
		}
		wait := time.Until(time.Unix((step+1)*30, 0)) + 500*time.Millisecond
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
}

// randomPassword is 24 random bytes in hex: above every system's
// minimum length, letters and digits only.
func randomPassword() string {
	var b [24]byte
	_, _ = rand.Read(b[:]) // never an error since Go 1.24
	return hex.EncodeToString(b[:])
}

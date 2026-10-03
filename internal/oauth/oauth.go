// Package oauth gets machine tokens with the RFC 6749 §4.4 client
// credentials grant (client_secret_post, form-encoded, as the USSP's
// /oauth/token and the lab issuer take it), caches them and renews them
// at half their lifetime. Lab-internal: the simulators' clients.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// MaxResponseBytes bounds a token response (E-10).
const MaxResponseBytes = 64 << 10

// ClientCredentials is one client at one token endpoint.
type ClientCredentials struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	Audience     string
	HTTP         *http.Client
	Now          func() time.Time

	mu      sync.Mutex
	token   string
	renewAt time.Time
}

// Error is a refused token request.
type Error struct {
	Status int
	Body   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("token endpoint answered %d: %s", e.Status, e.Body)
}

// Token returns a valid token, fetching one when the cached one is past
// half its lifetime.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && now().Before(c.renewAt) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}}
	if len(c.Scopes) > 0 {
		form.Set("scope", strings.Join(c.Scopes, " "))
	}
	if c.Audience != "" {
		form.Set("audience", c.Audience)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("oauth: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &Error{Status: resp.StatusCode, Body: short(string(body))}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" || !strings.EqualFold(tr.TokenType, "Bearer") || tr.ExpiresIn < 1 {
		return "", &Error{Status: resp.StatusCode, Body: "not a bearer token response"}
	}
	c.token = tr.AccessToken
	c.renewAt = now().Add(time.Duration(tr.ExpiresIn) * time.Second / 2)
	return c.token, nil
}

// Invalidate drops the cached token (after a 401).
func (c *ClientCredentials) Invalidate() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

func short(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

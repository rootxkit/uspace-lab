package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func jsonRaw(b []byte) json.RawMessage { return json.RawMessage(b) }

// USSP drives the USSP's account API (uspace-ussp api/openapi.yaml at
// the image's commit): operator self-registration, the portal session,
// machine clients and serial bindings.
type USSP struct {
	C    *Client
	Host string
	St   *State
	// Wait bounds the wait for an operator to become active (the USSP
	// asks the authority's registry, F8).
	Wait time.Duration
}

func (u *USSP) url(p string) string { return u.C.URL(u.Host, p) }

// OperatorScopes are every operator scope (ClientRequest.scopes).
var OperatorScopes = []string{"ussp.intents", "ussp.telemetry", "ussp.traffic", "ussp.geo"}

// EnsureOperator registers the operator (self-registration), waits until
// the USSP says active, signs its admin in, creates one machine client
// with every operator scope and binds the serials (with their class).
func (u *USSP) EnsureOperator(ctx context.Context, number, name string, serials map[string]string) (*USSPOperator, error) {
	op := u.St.USSPOperators[number]
	if op == nil {
		op = &USSPOperator{AdminUser: "lab-" + strings.ToLower(number), AdminPassword: randomPassword(), Serials: map[string]bool{}}
		u.St.USSPOperators[number] = op
		if err := u.St.Save(); err != nil {
			return nil, err
		}
	}
	if op.ID == "" {
		ans, err := u.C.Do(ctx, http.MethodPost, u.url("/v1/accounts/operators"), Auth{}, map[string]string{
			"registration_number": number, "display_name": name, "contact_email": strings.ToLower(number) + "@lab.uspace.test",
			"admin_username": op.AdminUser, "admin_password": op.AdminPassword,
		})
		if err != nil {
			return nil, err
		}
		if ans.Status != http.StatusCreated {
			return nil, fmt.Errorf("seed: USSP operator %s answered %s", number, ans)
		}
		var o struct {
			ID string `json:"id"`
		}
		if err := ans.JSON(&o); err != nil {
			return nil, err
		}
		op.ID = o.ID
		if err := u.St.Save(); err != nil {
			return nil, err
		}
	}
	sess, err := u.login(ctx, op)
	if err != nil {
		return nil, err
	}
	au := Auth{Bearer: sess}
	if err := u.waitActive(ctx, au, op, number); err != nil {
		return nil, err
	}
	if op.ClientID == "" {
		ans, err := u.C.Expect(ctx, http.MethodPost, u.url("/v1/accounts/operators/"+url.PathEscape(op.ID)+"/clients"), au,
			map[string]any{"scopes": OperatorScopes}, "USSP client of "+number, http.StatusCreated)
		if err != nil {
			return nil, err
		}
		var cs struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := ans.JSON(&cs); err != nil {
			return nil, err
		}
		op.ClientID, op.ClientSecret = cs.ClientID, cs.ClientSecret
		if err := u.St.Save(); err != nil {
			return nil, err
		}
	}
	for serial, class := range serials {
		if op.Serials[serial] {
			continue
		}
		body := map[string]string{"serial": serial}
		if class != "" {
			body["class_label"] = class
		}
		ans, err := u.C.Do(ctx, http.MethodPost, u.url("/v1/accounts/operators/"+url.PathEscape(op.ID)+"/clients/"+url.PathEscape(op.ClientID)+"/serials"), au, body)
		if err != nil {
			return nil, err
		}
		if ans.Status != http.StatusCreated && ans.Status != http.StatusConflict {
			return nil, fmt.Errorf("seed: bind %s to %s answered %s", serial, op.ClientID, ans)
		}
		if ans.Status == http.StatusConflict {
			return nil, fmt.Errorf("seed: %s is bound to another client at the USSP: %s", serial, ans)
		}
		op.Serials[serial] = true
		if err := u.St.Save(); err != nil {
			return nil, err
		}
	}
	return op, nil
}

func (u *USSP) login(ctx context.Context, op *USSPOperator) (string, error) {
	ans, err := u.C.Expect(ctx, http.MethodPost, u.url("/v1/accounts/login"), Auth{}, map[string]string{
		"realm": "portal", "username": op.AdminUser, "password": op.AdminPassword,
	}, "USSP portal login "+op.AdminUser, http.StatusOK)
	if err != nil {
		return "", err
	}
	var s struct {
		Token string `json:"token"`
	}
	return s.Token, ans.JSON(&s)
}

func (u *USSP) waitActive(ctx context.Context, au Auth, op *USSPOperator, number string) error {
	deadline := time.Now().Add(u.Wait)
	for {
		ans, err := u.C.Expect(ctx, http.MethodGet, u.url("/v1/accounts/operators/"+url.PathEscape(op.ID)), au, nil, "USSP operator "+number, http.StatusOK)
		if err != nil {
			return err
		}
		var o struct {
			Status           string `json:"status"`
			ValidationStatus string `json:"validation_status"`
		}
		if err := ans.JSON(&o); err != nil {
			return err
		}
		if o.Status == "active" {
			return nil
		}
		if o.Status != "pending_validation" || time.Now().After(deadline) {
			return fmt.Errorf("seed: USSP operator %s is %s (registry: %s), not active", number, o.Status, o.ValidationStatus)
		}
		// A pending operator is checked with the registry again on a
		// change to its record (OperatorUpdate).
		_, _ = u.C.Do(ctx, http.MethodPatch, u.url("/v1/accounts/operators/"+url.PathEscape(op.ID)), au, map[string]string{"display_name": "uspace-lab " + number})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ANSP drives the ANSP's console API (uspace-ansp api/openapi.yaml at the
// built commit): the bootstrap admin, a watch supervisor, their TOTP
// enrolments and sessions (bearer, as the BFF forwards them).
type ANSP struct {
	C    *Client
	Host string
	St   *State
	Now  func() time.Time
}

func (a *ANSP) url(p string) string { return a.C.URL(a.Host, p) }

// SignIn opens a session for user (password, then TOTP; the enrolment
// on the first sign-in).
func (a *ANSP) SignIn(ctx context.Context, user string) (string, error) {
	acc := a.St.Account("ansp", user)
	ans, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/auth/login"), Auth{},
		map[string]string{"username": user, "password": acc.Password}, "ANSP login "+user, http.StatusOK)
	if err != nil {
		return "", err
	}
	var ch struct {
		MFAToken  string `json:"mfa_token"`
		Enrolment *struct {
			Secret string `json:"secret"`
		} `json:"enrolment"`
	}
	if err := ans.JSON(&ch); err != nil {
		return "", err
	}
	if ch.Enrolment != nil {
		acc.TOTP, acc.LastStep = ch.Enrolment.Secret, 0
	}
	if acc.TOTP == "" {
		return "", fmt.Errorf("seed: ANSP account %s is enrolled with a TOTP secret this state does not hold", user)
	}
	code, err := acc.Code(ctx, a.Now)
	if err != nil {
		return "", err
	}
	ans, err = a.C.Expect(ctx, http.MethodPost, a.url("/v1/auth/mfa"), Auth{},
		map[string]string{"mfa_token": ch.MFAToken, "code": code}, "ANSP mfa "+user, http.StatusOK)
	if err != nil {
		return "", err
	}
	var s struct {
		Token string `json:"token"`
	}
	if err := ans.JSON(&s); err != nil {
		return "", err
	}
	return s.Token, a.St.Save()
}

// EnsureUser creates a console account with a role unless it exists.
func (a *ANSP) EnsureUser(ctx context.Context, admin, user, role string) error {
	acc := a.St.Account("ansp", user)
	if err := a.St.Save(); err != nil {
		return err
	}
	ans, err := a.C.Do(ctx, http.MethodPost, a.url("/v1/users"), Auth{Bearer: admin},
		map[string]string{"username": user, "password": acc.Password, "role": role})
	if err != nil {
		return err
	}
	if ans.Status == http.StatusCreated || ans.Status == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("seed: ANSP user %s answered %s", user, ans)
}

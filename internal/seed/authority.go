package seed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Session is a console session: the JWT and the CSRF value sent with it
// (double-submit, M21).
type Session struct {
	Token string
	CSRF  string
}

// cookie is how a console request carries the session: as a bearer
// (the authority's api reads the session from Authorization, as its BFF
// forwards it) and as the cookie with the CSRF pair (M21).
func (s Session) cookie() Auth { return Auth{Bearer: s.Token, Cookie: s.Token, CSRF: s.CSRF} }

// Authority drives the authority's console API (uspace-authority
// api/openapi.yaml at the image's commit).
type Authority struct {
	C    *Client
	Host string
	St   *State
	Now  func() time.Time
}

func (a *Authority) url(p string) string { return a.C.URL(a.Host, p) }

// SignIn opens a console session for user: the password step, the TOTP
// enrolment when the account has none (its secret kept in the state),
// then the TOTP step.
func (a *Authority) SignIn(ctx context.Context, user string) (Session, error) {
	acc := a.St.Account("authority", user)
	ans, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/auth/login"), Auth{},
		map[string]string{"username": user, "password": acc.Password}, "authority login "+user, http.StatusOK)
	if err != nil {
		return Session{}, err
	}
	var ch struct {
		MFAToken  string `json:"mfa_token"`
		Enrolment *struct {
			Secret string `json:"secret"`
		} `json:"enrolment"`
	}
	if err := ans.JSON(&ch); err != nil {
		return Session{}, err
	}
	if ch.Enrolment != nil {
		acc.TOTP, acc.LastStep = ch.Enrolment.Secret, 0
	}
	if acc.TOTP == "" {
		return Session{}, fmt.Errorf("seed: authority account %s is enrolled with a TOTP secret this state does not hold", user)
	}
	code, err := acc.Code(ctx, a.Now)
	if err != nil {
		return Session{}, err
	}
	ans, err = a.C.Expect(ctx, http.MethodPost, a.url("/v1/auth/mfa"), Auth{},
		map[string]string{"mfa_token": ch.MFAToken, "code": code}, "authority mfa "+user, http.StatusOK)
	if err != nil {
		return Session{}, err
	}
	var si struct {
		Token    string   `json:"token"`
		Recovery []string `json:"recovery_codes"`
	}
	if err := ans.JSON(&si); err != nil {
		return Session{}, err
	}
	if len(si.Recovery) > 0 {
		acc.Recovery = si.Recovery
	}
	if err := a.St.Save(); err != nil {
		return Session{}, err
	}
	return Session{Token: si.Token, CSRF: randomHex(16)}, nil
}

// EnsureUser creates a console account (realm console) unless it exists.
func (a *Authority) EnsureUser(ctx context.Context, admin Session, user string, roles []string) error {
	acc := a.St.Account("authority", user)
	if err := a.St.Save(); err != nil {
		return err
	}
	ans, err := a.C.Do(ctx, http.MethodPost, a.url("/v1/users"), admin.cookie(), map[string]any{
		"username": user, "password": acc.Password, "roles": roles, "realm": "console", "display_name": "uspace-lab " + user,
	})
	if err != nil {
		return err
	}
	if ans.Status == http.StatusCreated || ans.Status == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("seed: authority user %s answered %s", user, ans)
}

// RegistryOperator is one operator to register.
type RegistryOperator struct {
	Number string
	Name   string
	Email  string
	Phone  string
}

// EnsureOperator registers an operator (a natural person with the lab's
// test identity) unless the registry holds the number; returns its id.
func (a *Authority) EnsureOperator(ctx context.Context, registrar Session, op RegistryOperator, validUntil time.Time) (string, error) {
	if id := a.St.RegistryOperators[op.Number]; id != "" {
		return id, nil
	}
	ans, err := a.C.Do(ctx, http.MethodPost, a.url("/v1/registry/operators"), registrar.cookie(), map[string]any{
		"operator_type": "natural", "registration_number": op.Number, "full_name": op.Name, "date_of_birth": "1990-01-01",
		"postal_address": "uspace-lab, test data", "contact_email": op.Email, "contact_phone": op.Phone,
		"valid_until": validUntil.UTC().Format(time.RFC3339), "source": "manual",
	})
	if err != nil {
		return "", err
	}
	var out struct {
		ID        string `json:"id"`
		Operators []struct {
			ID string `json:"id"`
		} `json:"operators"`
	}
	switch ans.Status {
	case http.StatusCreated:
		if err := ans.JSON(&out); err != nil {
			return "", err
		}
	case http.StatusConflict:
		ans, err = a.C.Expect(ctx, http.MethodGet, a.url("/v1/registry/operators?number="+url.QueryEscape(op.Number)), registrar.cookie(), nil,
			"registry operator lookup", http.StatusOK)
		if err != nil {
			return "", err
		}
		if err := ans.JSON(&out); err != nil || len(out.Operators) != 1 {
			return "", fmt.Errorf("seed: registry operator %s exists but the lookup answered %s", op.Number, ans)
		}
		out.ID = out.Operators[0].ID
	default:
		return "", fmt.Errorf("seed: registry operator %s answered %s", op.Number, ans)
	}
	a.St.RegistryOperators[op.Number] = out.ID
	return out.ID, a.St.Save()
}

// RegistryUAS is one aircraft to register.
type RegistryUAS struct {
	Serial     string
	OperatorID string
	ClassLabel string
}

// EnsureUAS registers an aircraft unless the registry holds the serial.
func (a *Authority) EnsureUAS(ctx context.Context, registrar Session, u RegistryUAS) error {
	if a.St.RegistryUAS[u.Serial] != "" {
		return nil
	}
	body := map[string]any{"operator_id": u.OperatorID, "serial": u.Serial, "rid_capability": "both",
		"manufacturer": "uspace-lab", "model": "ArduCopter SITL"}
	if u.ClassLabel != "" {
		body["class_label"] = u.ClassLabel
		body["mtom_g"] = 3900 // a C2 is under 4 kg (2019/945 Part 3)
	}
	ans, err := a.C.Do(ctx, http.MethodPost, a.url("/v1/registry/uas"), registrar.cookie(), body)
	if err != nil {
		return err
	}
	var out struct {
		ID  string `json:"id"`
		UAS []struct {
			ID string `json:"id"`
		} `json:"uas"`
	}
	switch ans.Status {
	case http.StatusCreated:
		if err := ans.JSON(&out); err != nil {
			return err
		}
	case http.StatusConflict:
		out.ID = "exists"
	default:
		return fmt.Errorf("seed: registry UAS %s answered %s", u.Serial, ans)
	}
	a.St.RegistryUAS[u.Serial] = out.ID
	return a.St.Save()
}

// EnsureReceiver registers a Remote ID receiver at its pinned position
// and keeps its keys; a receiver that exists without keys in the state
// has its keys rotated (they are shown once).
func (a *Authority) EnsureReceiver(ctx context.Context, admin Session, id string, latDeg, lonDeg float64) (*ReceiverCreds, error) {
	if c := a.St.Receivers[id]; c != nil {
		return c, nil
	}
	ans, err := a.C.Do(ctx, http.MethodPost, a.url("/v1/rid/receivers"), admin.cookie(), map[string]any{
		"id": id, "label": "uspace-lab " + id, "lat_deg": latDeg, "lon_deg": lonDeg, "owner": "authority",
		"owner_name": "uspace-lab",
	})
	if err != nil {
		return nil, err
	}
	if ans.Status == http.StatusConflict {
		ans, err = a.C.Do(ctx, http.MethodPost, a.url("/v1/rid/receivers/"+url.PathEscape(id)+"/keys/rotate"), admin.cookie(), map[string]any{})
		if err != nil {
			return nil, err
		}
	}
	if ans.Status != http.StatusCreated && ans.Status != http.StatusOK {
		return nil, fmt.Errorf("seed: receiver %s answered %s", id, ans)
	}
	var out struct {
		Credentials *ReceiverCreds `json:"credentials"`
		BearerKey   string         `json:"bearer_key"`
		HMACHex     string         `json:"hmac_secret_hex"`
	}
	if err := ans.JSON(&out); err != nil {
		return nil, err
	}
	c := out.Credentials
	if c == nil {
		c = &ReceiverCreds{BearerKey: out.BearerKey, HMACHex: out.HMACHex}
	}
	if c.BearerKey == "" || len(c.HMACHex) != 64 {
		return nil, fmt.Errorf("seed: receiver %s: no credentials in the answer %s", id, ans)
	}
	a.St.Receivers[id] = c
	return c, a.St.Save()
}

// ImportZones imports an ED-269 document as drafts (inspector), approves
// every draft (admin) and publishes (admin). Returns the identifiers and
// versions published.
func (a *Authority) ImportZones(ctx context.Context, inspector, admin Session, ed269 []byte, from, to time.Time) (map[string]int, error) {
	q := url.Values{"valid_from": {from.UTC().Format(time.RFC3339)}, "valid_to": {to.UTC().Format(time.RFC3339)}}
	ans, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/zones/import?"+q.Encode()), inspector.cookie(), ed269,
		"zones import", http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var imp struct {
		Created []struct {
			Identifier  string `json:"identifier"`
			ZoneVersion int    `json:"zone_version"`
		} `json:"created"`
	}
	if err := ans.JSON(&imp); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, z := range imp.Created {
		if _, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/zones/"+url.PathEscape(z.Identifier)+"/approve"), admin.cookie(),
			map[string]int{"zone_version": z.ZoneVersion}, "approve zone "+z.Identifier, http.StatusOK); err != nil {
			return nil, err
		}
		out[z.Identifier] = z.ZoneVersion
	}
	if _, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/zones/publish"), admin.cookie(), nil, "publish zones", http.StatusOK); err != nil {
		return nil, err
	}
	for k, v := range out {
		a.St.Zones[k] = v
	}
	return out, a.St.Save()
}

// USpace drafts, designates and publishes a U-space airspace (admin)
// unless the state says it is published.
func (a *Authority) USpace(ctx context.Context, admin Session, id string, feature []byte, from, to time.Time, name string) error {
	body := map[string]any{
		"feature": jsonRaw(feature), "designated_from": from.UTC().Format(time.RFC3339), "designated_to": to.UTC().Format(time.RFC3339),
		"designation": map[string]any{
			"airspace_name": name, "services_required": []string{"NID", "GEO", "FA", "TI"},
			"uas_requirements": map[string]any{}, "operational_conditions": map[string]any{},
			"service_performance": map[string]any{"nid_update_hz": 1, "ti_update_hz": 1, "cis_latency_s": 1},
			// No max_height_agl_m: a ceiling above the ground needs
			// terrain at every system that judges it, and the lab stack
			// has none (the USSP refuses an intent whose height against
			// it it cannot judge, airspace_ceiling_not_judged). The
			// airspace's ceiling is the feature's AMSL upper limit
			// (uspaceFeature).
			"airspace_constraints": map[string]any{}, "in_controlled_airspace": false,
		},
	}
	digest := digestOf(body)
	if a.St.USpace == id && a.St.USpaceVersion > 0 && a.St.USpaceDigest == digest {
		return nil
	}
	method, path := http.MethodPost, "/v1/uspace"
	if a.St.USpace == id && a.St.USpaceVersion > 0 {
		// A new version of the published airspace (its draft, then the
		// same designation and publication).
		method, path = http.MethodPut, "/v1/uspace/"+url.PathEscape(id)
	}
	ans, err := a.C.Do(ctx, method, a.url(path), admin.cookie(), body)
	if err != nil {
		return err
	}
	version := 1
	switch ans.Status {
	case http.StatusCreated, http.StatusOK:
		var zv struct {
			ZoneVersion int `json:"zone_version"`
		}
		if err := ans.JSON(&zv); err == nil && zv.ZoneVersion > 0 {
			version = zv.ZoneVersion
		}
	case http.StatusConflict:
	default:
		return fmt.Errorf("seed: U-space airspace %s answered %s", id, ans)
	}
	ans, err = a.C.Do(ctx, http.MethodPost, a.url("/v1/uspace/"+url.PathEscape(id)+"/designate"), admin.cookie(), map[string]int{"zone_version": version})
	if err != nil {
		return err
	}
	if ans.Status != http.StatusOK && ans.Status != http.StatusConflict {
		return fmt.Errorf("seed: designate %s answered %s", id, ans)
	}
	if _, err := a.C.Expect(ctx, http.MethodPost, a.url("/v1/uspace/publish"), admin.cookie(), nil, "publish U-space", http.StatusOK); err != nil {
		return err
	}
	a.St.USpace, a.St.USpaceVersion, a.St.USpaceDigest = id, version, digest
	return a.St.Save()
}

// digestOf is the SHA-256 of v's JSON (what the seed last published).
func digestOf(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// randomHex is n random bytes in hex (crypto/rand.Read never returns an
// error since Go 1.24; it stops the program instead).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

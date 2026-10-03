package issuer

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// The lab clients file: the six clients of the brief, dp.observe on
// lab-01 only, every issuable national scope on lab-01, and ${VAR}
// entries expanded into the hosts of the environment. The USSP's id is
// the one the USSP itself asks with: uspace-ussp internal/auth
// ClientIDFor is "ussp-" + the lower-cased code + "-01" (M24), so for
// USSP_SYSTEM_ID USSP-DEV it is ussp-ussp-dev-01; the issuer's ids are
// case-sensitive, and an upper-case entry refused every token the USSP
// asked for (found by the WP-L6 systems run: 401 on every CIS pull).
func TestLabClientsFile(t *testing.T) {
	r := labRegistry(t)
	want := []string{"lab-01", "authority-01", "cisp-01", "ansp-01", "ussp-ussp-dev-01", "sim-ussp-01"}
	if !slices.Equal(r.IDs(), want) {
		t.Fatalf("clients %v, want %v", r.IDs(), want)
	}
	lab, _ := r.Client(LabClientID)
	for _, s := range IssuableScopes() {
		if !slices.Contains(lab.Scopes, s) {
			t.Errorf("lab-01 lacks %s", s)
		}
	}
	for _, id := range r.IDs() {
		c, _ := r.Client(id)
		if id != LabClientID && slices.Contains(c.Scopes, "dp.observe") {
			t.Errorf("%s holds dp.observe", id)
		}
	}
	auth, _ := r.Client("authority-01")
	if !slices.Contains(auth.Audiences, "cisp.lab.test") || !slices.Contains(auth.Audiences, "cisp") {
		t.Fatalf("authority-01 audiences %v: ${LAB_CISP_AUDIENCES} not expanded", auth.Audiences)
	}
}

const baseClients = `
clients:
  - id: lab-01
    scopes: [cis.read, dp.observe, utm.strategic_coordination]
    audiences: ["${HOSTS}", cisp]
  - id: ussp-A-01
    scopes: [rid.service_provider]
`

// Every LoadClients refusal beside the acceptance of the base file it
// differs from by one edit (LESSONS E-01, B-14).
func TestLoadClientsRefusals(t *testing.T) {
	env := func(name string) (string, bool) {
		if name == "HOSTS" {
			return "authority, ansp", true
		}
		if name == "EMPTY" {
			return " ", true
		}
		return "", false
	}
	r, err := LoadClients([]byte(baseClients), env)
	if err != nil {
		t.Fatalf("base refused: %v", err)
	}
	lab, _ := r.Client("lab-01")
	if !slices.Equal(lab.Audiences, []string{"authority", "ansp", "cisp"}) {
		t.Fatalf("audiences %v", lab.Audiences)
	}
	for _, tc := range []struct{ name, old, new, want string }{
		{"repeated id", "id: ussp-A-01", "id: lab-01", "listed twice"},
		{"empty id", "id: ussp-A-01", `id: ""`, "not a client id"},
		{"scope outside the catalogue", "[rid.service_provider]", "[rid.observe]", "not in the catalogue"},
		{"reserved scope", "[rid.service_provider]", "[cis.publish:ats_data]", "reserved"},
		{"lab-only scope elsewhere", "[rid.service_provider]", "[dp.observe]", "lab-01 only"},
		{"repeated scope", "[rid.service_provider]", "[rid.service_provider, rid.service_provider]", "listed twice"},
		{"unset variable", "${HOSTS}", "${NOPE}", "NOPE is unset"},
		{"empty variable", "${HOSTS}", "${EMPTY}", "EMPTY is unset or empty"},
		{"host with a scheme", ", cisp]", ", https://cisp]", "not a bare"},
		{"host with a port", ", cisp]", `, "cisp:443"]`, "not a bare"},
		{"upper-case host", ", cisp]", ", CISP]", "not a bare"},
		{"national scope without audience", "[rid.service_provider]", "[cis.read]", "no audience"},
		{"unknown field", "    scopes: [rid.service_provider]", "    scopes: [rid.service_provider]\n    secret: x", "secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(baseClients, tc.old) {
				t.Fatalf("edit %q does not apply", tc.old)
			}
			_, err := LoadClients([]byte(strings.Replace(baseClients, tc.old, tc.new, 1)), env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := LoadClients([]byte("clients: []\n"), env); err == nil {
		t.Fatal("an empty client list was accepted")
	}
	if _, err := LoadClients([]byte(strings.Repeat("#", MaxClientsFileBytes+1)), env); err == nil {
		t.Fatal("an oversized file was accepted")
	}
}

// Grant: standard scopes for any audience, national ones only for the
// allowed list (M18), both ways.
func TestGrantAudienceRule(t *testing.T) {
	c := Client{ID: "ussp-A-01", Scopes: []string{"cis.read", "utm.strategic_coordination"}, Audiences: []string{"cisp"}}
	if err := c.Grant([]string{"utm.strategic_coordination"}, "anything.example"); err != nil {
		t.Fatalf("standard scope for a discovered host: %v", err)
	}
	if err := c.Grant([]string{"cis.read"}, "cisp"); err != nil {
		t.Fatalf("national scope for its audience: %v", err)
	}
	var r *RefusalError
	if err := c.Grant([]string{"cis.read"}, "anything.example"); !errors.As(err, &r) || r.Slug != SlugForbiddenAudience {
		t.Fatalf("national scope elsewhere: %v", err)
	}
}

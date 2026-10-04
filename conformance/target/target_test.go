package target

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookup(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// TestCommittedTargetsLoad: every committed target file loads with its
// required variables set, and names each of them when they are not.
func TestCommittedTargetsLoad(t *testing.T) {
	required := map[string][]string{
		"ansp.yaml":      {"ANSP_CONFORMANCE_BASE_URL", "LAB_TOKEN_URL", "ANSP_CONFORMANCE_AUDIENCE"},
		"authority.yaml": {"AUTHORITY_CONFORMANCE_BASE_URL", "LAB_TOKEN_URL"},
		"cisp.yaml":      {"CISP_BASE_URL"},
		"ussp.yaml":      {"USSP_CONFORMANCE_BASE_URL", "LAB_TOKEN_URL"},
		"sim-ussp.yaml":  {"SIM_USSP_CONFORMANCE_BASE_URL"},
	}
	for file, vars := range required {
		path := filepath.Join("..", "targets", file)
		vals := map[string]string{}
		for _, v := range vars {
			vals[v] = "http://target.invalid:8080"
		}
		f, err := Load(path, lookup(vals))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if f.Name == "" || f.Participant == "" {
			t.Errorf("%s: name %q participant %q", file, f.Name, f.Participant)
		}
		if len(f.Fixtures) != 0 || len(f.Interfaces) != 0 {
			t.Errorf("%s: empty optional values kept: %v %v", file, f.Fixtures, f.Interfaces)
		}
		_, err = Load(path, lookup(map[string]string{}))
		if err == nil {
			t.Errorf("%s: loaded with nothing set", file)
			continue
		}
		for _, v := range vars {
			if !strings.Contains(err.Error(), v) {
				t.Errorf("%s: the error does not name %s: %v", file, v, err)
			}
		}
	}
}

func TestEnvFileAndStaticTokens(t *testing.T) {
	dir := t.TempDir()
	// A JWT-shaped value built here, not a token literal in the file.
	enc := base64.RawURLEncoding.EncodeToString
	reader := enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(`{"sub":"lab-01","scope":"cis.read"}`)) + ".x"
	env := filepath.Join(dir, "conformance.env")
	body := "# the stack's identities\nCISP_READER_TOKEN=" + reader + "\n\nCISP_JWKS_URL=http://127.0.0.1:1/jwks\n"
	if err := os.WriteFile(env, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ReadEnvFile(env)
	if err != nil {
		t.Fatal(err)
	}
	m["CISP_BASE_URL"] = "http://127.0.0.1:2"
	f, err := Load(filepath.Join("..", "targets", "cisp.yaml"), lookup(m))
	if err != nil {
		t.Fatal(err)
	}
	if f.ED318 == nil || f.ED318.ReaderToken == "" || f.ED318.Publishes() {
		t.Fatalf("ed318 %+v: want the reader token and no publication by default", f.ED318)
	}
	creds, err := f.Credentials(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.Static) != 1 || creds.Static[0].Scopes[0] != "cis.read" {
		t.Errorf("static tokens %+v", creds.Static)
	}
	if len(creds.Clients) != 0 {
		t.Errorf("an issuer without token_url and client_id was kept: %+v", creds.Clients)
	}
	if err := os.WriteFile(env, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnvFile(env); err == nil {
		t.Error("a line that is not KEY=VALUE was read")
	}
}

func TestRefusesUnknownMembersAndValues(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown-member.yaml": "system: ansp\nbase_url: http://a\nsurprise: 1\n",
		"bad-system.yaml":     "system: tower\nbase_url: http://a\n",
		"bad-url.yaml":        "system: ansp\nbase_url: ftp://a\n",
		"bad-interface.yaml":  "system: ussp\nbase_url: http://a\ninterfaces: {scd_injection: http://x}\n",
		"bad-publish.yaml":    "system: cisp\nbase_url: http://a\ned318: {publish: maybe}\n",
		// The name becomes a directory of the report: never a path.
		"name-parent.yaml":    "system: ansp\nname: ../../escape\nbase_url: http://a\n",
		"name-slash.yaml":     "system: ansp\nname: a/b\nbase_url: http://a\n",
		"name-backslash.yaml": "system: ansp\nname: 'a\\\\b'\nbase_url: http://a\n",
		"name-dots.yaml":      "system: ansp\nname: '..'\nbase_url: http://a\n",
		"name-drive.yaml":     "system: ansp\nname: 'C:x'\nbase_url: http://a\n",
		"name-space.yaml":     "system: ansp\nname: 'a b'\nbase_url: http://a\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p, lookup(nil)); err == nil {
			t.Errorf("%s loaded", name)
		}
	}
}

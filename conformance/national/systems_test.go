package national

import (
	"os"
	"path/filepath"
	"testing"
)

// systemContract finds a system's OpenAPI file: the aggregate's pinned
// mirror, else CONFORMANCE_CONTRACTS_DIR/<system>.yaml (the
// conformance workflow fetches each system's api/openapi.yaml there).
func systemContract(t *testing.T, system string) string {
	t.Helper()
	agg := filepath.Join("..", "..", "api", system, "openapi.yaml")
	if _, err := os.Stat(agg); err == nil {
		return agg
	}
	if dir := os.Getenv("CONFORMANCE_CONTRACTS_DIR"); dir != "" {
		p := filepath.Join(dir, system+".yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if os.Getenv("CONFORMANCE_REQUIRE_CONTRACTS") == "1" {
		t.Fatalf("%s: no contract: api/%s/openapi.yaml is unpinned and CONFORMANCE_CONTRACTS_DIR has no %s.yaml", system, system, system)
	}
	t.Skipf("%s: unverified: api/%s/openapi.yaml is unpinned and CONFORMANCE_CONTRACTS_DIR is unset", system, system)
	return ""
}

// TestSystemContractsClassify loads every system's contract with its
// overrides: every operation must be classified (fail closed), and
// each system must have operations of the kinds the suite exercises.
func TestSystemContractsClassify(t *testing.T) {
	for _, system := range []string{"ansp", "authority", "cisp", "ussp"} {
		t.Run(system, func(t *testing.T) {
			path := systemContract(t, system)
			ov, err := LoadOverrides(filepath.Join("contracts", system+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			c, err := LoadContractFile(system, path, ov, LoadOptions{CommonDir: filepath.Join("..", "..", "schemas", "common")})
			if err != nil {
				t.Fatal(err)
			}
			var public, token, session, special, unstated int
			for _, op := range c.Ops {
				switch {
				case op.Auth.Public:
					public++
				case op.Auth.Special != "":
					special++
				}
				if op.Auth.Token != nil {
					token++
					if op.Auth.Token.Alternatives == nil {
						unstated++
					}
				}
				if op.Auth.Session {
					session++
				}
			}
			t.Logf("%s %s (%s): %d operations: %d public, %d token (%d scope unstated), %d session, %d special",
				system, c.Version, c.SHA256[:19], len(c.Ops), public, token, unstated, session, special)
			if public == 0 || token == 0 {
				t.Errorf("%s: %d public and %d token operations; the suite expects both", system, public, token)
			}
		})
	}
}

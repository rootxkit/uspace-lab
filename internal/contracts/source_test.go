package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pinnedCommit = "0123456789abcdef0123456789abcdef01234567"

func sourceText(commit, fetched string) string {
	return "# comment\nrepo = https://github.com/rootxkit/uspace-cisp.git\ncommit = " + commit +
		"\npath = api/openapi.yaml\nfetched_at = " + fetched + "\n"
}

func TestReadSource(t *testing.T) {
	cases := []struct {
		name, text string
		pinned     bool
		wantErr    string
	}{
		{"unpinned", sourceText("", ""), false, ""},
		{"pinned", sourceText(pinnedCommit, "2026-10-02T10:00:00Z"), true, ""},
		{"short commit", sourceText("0123456", "2026-10-02T10:00:00Z"), false, "full 40-hex"},
		{"commit without fetched_at", sourceText(pinnedCommit, ""), false, "set together"},
		{"unknown key", sourceText("", "") + "comit = x\n", false, "unknown key"},
		{"missing key", "repo = r\ncommit =\npath = p\n", false, "missing key"},
		{"not key value", "repo r\n", false, "not key = value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "SOURCE")
			writeFile(t, p, tc.text)
			s, err := ReadSource(p)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Pinned() != tc.pinned {
				t.Fatalf("Pinned() = %v, want %v", s.Pinned(), tc.pinned)
			}
		})
	}
}

// fixtureRoot writes the mirror layout; cispOpenAPI, when non-empty,
// pins api/cisp at pinnedCommit with that file.
func fixtureRoot(t *testing.T, cispOpenAPI string) string {
	t.Helper()
	root := t.TempDir()
	for _, kind := range []string{"schemas", "api"} {
		for _, sys := range Systems {
			writeFile(t, filepath.Join(root, kind, sys, "SOURCE"), sourceText("", ""))
		}
	}
	if cispOpenAPI != "" {
		writeFile(t, filepath.Join(root, "api", "cisp", "SOURCE"), sourceText(pinnedCommit, "2026-10-02T10:00:00Z"))
		writeFile(t, filepath.Join(root, "api", "cisp", "openapi.yaml"), cispOpenAPI)
	}
	return root
}

func TestMirrorsNeedsASourceInEverySystemDirectory(t *testing.T) {
	root := fixtureRoot(t, "")
	ms, err := Mirrors(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2*len(Systems) {
		t.Fatalf("got %d mirrors, want %d", len(ms), 2*len(Systems))
	}
	if err := os.Remove(filepath.Join(root, "api", "ansp", "SOURCE")); err != nil {
		t.Fatal(err)
	}
	if _, err := Mirrors(root); err == nil || !strings.Contains(err.Error(), "missing SOURCE") {
		t.Fatalf("want missing SOURCE, got %v", err)
	}
}

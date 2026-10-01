package contracts

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specRows reads the first cell of every endpoint-group row of spec
// 02 §3, per system, in spec order.
func specRows(t *testing.T) map[string][]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "docs", "spec", "02-interfaces.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	heading := regexp.MustCompile(`^### (\S+)$`)
	rows := map[string][]string{}
	inSection, system := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "## 3. "):
			inSection = true
			continue
		case strings.HasPrefix(line, "## ") && inSection:
			inSection = false
		}
		if !inSection {
			continue
		}
		if m := heading.FindStringSubmatch(line); m != nil {
			system = m[1]
			continue
		}
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		rows[system] = append(rows[system], strings.TrimSpace(cells[1]))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// The table in groups.go is the spec's text, row for row, in both
// directions: a row added to or changed in 02 §3 fails here until the
// table follows.
func TestEndpointGroupsAreTheSpecRows(t *testing.T) {
	spec := specRows(t)
	for _, sys := range Systems {
		var table []string
		for _, g := range GroupsFor(sys) {
			table = append(table, g.Row)
		}
		if len(spec[sys]) == 0 {
			t.Fatalf("02 §3 has no rows for %s: the spec parser is broken", sys)
		}
		if strings.Join(table, "\n") != strings.Join(spec[sys], "\n") {
			t.Errorf("%s: table differs from 02 §3\ntable:\n  %s\nspec:\n  %s", sys,
				strings.Join(table, "\n  "), strings.Join(spec[sys], "\n  "))
		}
	}
	if len(spec["lab"]) != 0 {
		t.Errorf("02 §3 now lists lab endpoint groups: %v", spec["lab"])
	}
}

func TestEndpointGroupFlagsFollowTheSpecText(t *testing.T) {
	for _, g := range EndpointGroups {
		standard := strings.Contains(g.Row, "/uss/")
		if g.Standard != standard {
			t.Errorf("%s %s: Standard = %v, the row names F3411/F3548 /uss/ endpoints: %v", g.System, g.Row, g.Standard, standard)
		}
	}
}

func TestGroupMatching(t *testing.T) {
	paths := []string{"/v1/zones", "/v1/zones_x", "/v1/{dataset}/versions/{version}", "/v1/registry/operators/{id}", "/.well-known/jwks.json", "/v1/stream"}
	cases := []struct {
		pattern string
		want    bool
	}{
		{"/v1/zones", true},
		{"/v1/registry/*", true},
		{"/v1/registry", false},
		{"/v1/zone", false},
		{"/v1/{dataset}/versions/*", true},
		{"/v1/uspace/*", false},
		{"/.well-known/jwks.json", true},
		{"/v1/stream", true},
		{"/v1/stream/*", true},
		{"/v1/zones_x/*", true},
		{"/v1/changes", false},
	}
	for _, tc := range cases {
		got := false
		for _, p := range paths {
			if matchGroup(tc.pattern, p) {
				got = true
			}
		}
		if got != tc.want {
			t.Errorf("%s: matched %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestMissingNamesTheAbsentPattern(t *testing.T) {
	g := Group{System: "cisp", Row: "`/v1/zones`, `/v1/changes`"}
	if m := g.Missing([]string{"/v1/zones", "/v1/changes"}); len(m) != 0 {
		t.Fatalf("all present, got missing %v", m)
	}
	m := g.Missing([]string{"/v1/zones"})
	if len(m) != 1 || m[0] != "/v1/changes" {
		t.Fatalf("want [/v1/changes], got %v", m)
	}
}

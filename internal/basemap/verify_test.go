package basemap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newBundle is the fixture with glyphs for its fontstack: a bundle
// every check passes.
func newBundle(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	writeGlyphs(t, filepath.Join(f.dir, "fonts"), fixtureStack, nil)
	return f
}

func (f *fixture) verify(t *testing.T, rangeURL string, budget int64) *Report {
	t.Helper()
	return Verify(context.Background(), &VerifyConfig{
		Dir: f.dir, Profile: ProfileBundle, Regions: f.regions, Inputs: f.inputs,
		BudgetBytes: budget, RangeURL: rangeURL,
	})
}

func fileServer(t *testing.T, dir string) string {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv.URL + "/basemap.pmtiles"
}

func reportText(t *testing.T, r *Report) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func failed(r *Report) []string {
	var out []string
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

// The branch that says nothing is wrong, read for what it says (E-02).
func TestVerifyPassesAGoodBundle(t *testing.T) {
	f := newBundle(t)
	r := f.verify(t, fileServer(t, f.dir), 1<<20)
	text := reportText(t, r)
	if !r.OK() {
		t.Fatalf("a good bundle failed:\n%s", text)
	}
	for _, want := range []string{
		"verify: 5 checks, 0 failed",
		"Fixture Regular: Asomtavruli 40/40, Mkhedruli 47/47, Mtavruli 46/46, Nuskhuri 40/40, Basic Latin letters 52/52",
		"9 tiles (z11:1 z12:2 z13:2 z14:4)",
		"harbour: 4 tiles at z14, centre tile 14/9786/6019 present",
		"-> 206 Partial Content",
		"build 20261003, OSM data as of 2026-10-03T04:00:00Z, 2 inputs (1 shipped files hashed)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
}

func TestVerifyFailsWhenAGeorgianRangeIsRemoved(t *testing.T) {
	f := newBundle(t)
	writeGlyphs(t, filepath.Join(f.dir, "fonts"), fixtureStack, map[rune]bool{0x1C90: true, 0x1CBF: true})
	r := f.verify(t, fileServer(t, f.dir), 1<<20)
	text := reportText(t, r)
	if r.OK() || !strings.Contains(text, "Mtavruli (U+1C90-1CBF): 2 of 46 code points do not draw: U+1C90 U+1CBF") {
		t.Errorf("two Mtavruli glyphs removed:\n%s", text)
	}
	if err := os.Remove(filepath.Join(f.dir, "fonts", fixtureStack, "11520-11775.pbf")); err != nil {
		t.Fatal(err)
	}
	text = reportText(t, f.verify(t, fileServer(t, f.dir), 1<<20))
	if !strings.Contains(text, "Nuskhuri") || !strings.Contains(text, "11520-11775.pbf") {
		t.Errorf("Nuskhuri range file removed:\n%s", text)
	}
}

func TestVerifyFailsOverBudget(t *testing.T) {
	f := newBundle(t)
	total, err := BundleBytes(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := f.verify(t, fileServer(t, f.dir), total-1)
	if got := failed(r); len(got) != 1 || got[0] != "size within budget" {
		t.Errorf("one byte over budget: failed %v\n%s", got, reportText(t, r))
	}
	if r := f.verify(t, fileServer(t, f.dir), total); !r.OK() {
		t.Errorf("exactly at budget:\n%s", reportText(t, r))
	}
}

func TestVerifyFailsTilesOutsideThePolicy(t *testing.T) {
	f := newBundle(t)
	// The city moved to the east of its tiles: its z13 and z14 tiles are
	// now outside every box that allows them, and its centre has none.
	f.regions.Cities[0].Bounds = Box{35.045, 43.032, 35.059, 43.039}
	r := f.verify(t, fileServer(t, f.dir), 1<<20)
	text := reportText(t, r)
	for _, want := range []string{"tiles above z12 outside every box that allows their zoom: 13/4892/3009", "harbour: no tile 14/"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	// The country shrunk below the archive: tiles outside the bounds.
	f = newBundle(t)
	f.regions.Country.Bounds = Box{35.0, 43.0, 35.01, 43.005}
	f.regions.Cities[0].Bounds = Box{35.0, 43.0, 35.01, 43.005}
	text = reportText(t, f.verify(t, fileServer(t, f.dir), 1<<20))
	if !strings.Contains(text, "outside the bounds") || !strings.Contains(text, "header bounds") {
		t.Errorf("shrunk country:\n%s", text)
	}
}

func TestVerifyFailsAnIncompleteOrStaleSource(t *testing.T) {
	f := newBundle(t)
	p := filepath.Join(f.dir, "SOURCE.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "osm_data_as_of")
	m["fonts"] = []any{}
	nb, _ := json.Marshal(m)
	if err := os.WriteFile(p, nb, 0o644); err != nil {
		t.Fatal(err)
	}
	text := reportText(t, f.verify(t, fileServer(t, f.dir), 1<<20))
	for _, want := range []string{"osm_data_as_of missing", "fonts missing", `fontstack "Fixture Regular" not recorded`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	// A shipped file changed after the build, and the archive replaced.
	f = newBundle(t)
	if err := os.WriteFile(filepath.Join(f.dir, "sprites", "v4", "light.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/fixture-country.pmtiles", filepath.Join(f.dir, "basemap.pmtiles"))
	text = reportText(t, f.verify(t, fileServer(t, f.dir), 1<<20))
	for _, want := range []string{"sprites/v4/light.json: sha256", "basemap.pmtiles sha256"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
}

func TestVerifyFailsWithoutARangeResponse(t *testing.T) {
	f := newBundle(t)
	// A server that ignores Range and sends the whole file with 200.
	whole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, _ := os.ReadFile(filepath.Join(f.dir, "basemap.pmtiles"))
		w.Write(b)
	}))
	defer whole.Close()
	r := f.verify(t, whole.URL+"/basemap.pmtiles", 1<<20)
	text := reportText(t, r)
	if r.OK() || !strings.Contains(text, "status 200, want 206") {
		t.Errorf("a 200 answer passed:\n%s", text)
	}
	// No server at all: a failure, never a skip.
	r = f.verify(t, "", 1<<20)
	if r.OK() || !strings.Contains(reportText(t, r), "the range request was not made") {
		t.Errorf("no range URL passed:\n%s", reportText(t, r))
	}
}

func TestReadBudget(t *testing.T) {
	n, err := ReadBudget("../../basemap/budget.txt", ProfileBundle)
	if err != nil || n != 1000000000 {
		t.Errorf("bundle budget %d %v, want the L-Q8 default 1 GB", n, err)
	}
	if n, err := ReadBudget("../../basemap/budget.txt", ProfileStorybook); err != nil || n <= 0 || n > 20000000 {
		t.Errorf("storybook budget %d %v, want a few MB", n, err)
	}
	p := filepath.Join(t.TempDir(), "b.txt")
	for _, bad := range []string{"bundle\n", "bundle -5\n", "bundle 1e9\n", "storybook 10\n"} {
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBudget(p, ProfileBundle); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestStorybookProfile(t *testing.T) {
	f := newBundle(t)
	// The fixture's storybook box is the whole archive to z12: its z13
	// and z14 tiles are beyond it.
	r := Verify(context.Background(), &VerifyConfig{
		Dir: f.dir, Profile: ProfileStorybook, Regions: f.regions, Inputs: f.inputs,
		BudgetBytes: 1 << 20, RangeURL: fileServer(t, f.dir),
	})
	text := reportText(t, r)
	if !strings.Contains(text, "6 tiles above z12") || !strings.Contains(text, "sea-centre: 2 tiles at z12") || !strings.Contains(text, "max_zoom 14, the policy says 12") {
		t.Errorf("storybook profile over a bundle:\n%s", text)
	}
}

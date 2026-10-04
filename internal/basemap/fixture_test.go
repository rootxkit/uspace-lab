package basemap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fixtureStack = "Fixture Regular"

// fixture is a bundle directory built from testdata/fixture.pmtiles and
// one shipped sprite file, with the SOURCE.json NewSource writes for it
// and the policy and inputs it was made with. It has no glyphs yet.
type fixture struct {
	dir     string
	regions *Regions
	inputs  *Inputs
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	r, err := LoadRegions("testdata/regions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyFile(t, "testdata/fixture.pmtiles", filepath.Join(dir, "basemap.pmtiles"))
	sprite := []byte(`{"icon":{"x":0,"y":0,"width":1,"height":1,"pixelRatio":1}}`)
	if err := os.MkdirAll(filepath.Join(dir, "sprites", "v4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sprites", "v4", "light.json"), sprite, 0o644); err != nil {
		t.Fatal(err)
	}
	in := &Inputs{
		Tiles:      TileSource{Builds: "https://builds.test/builds.json", Base: "https://tiles.test/", Licence: "ODbL-1.0"},
		Assets:     "https://github.com/protomaps/basemaps-assets@028c18f713baecad011301ff7a69acc39bcc2ae7",
		Licence:    "Map data (c) OpenStreetMap contributors, ODbL.",
		Fontstacks: []Fontstack{{Name: fixtureStack, Fonts: []string{"font"}}},
		Inputs: []Input{
			{ID: "font", URL: "https://files.test/font.ttf", SHA256: sum([]byte("font")), Licence: "OFL-1.1", Version: "Version 1.0"},
			{ID: "sprite", URL: "https://files.test/light.json", SHA256: sum(sprite), Licence: "MIT", Path: "sprites/v4/light.json"},
		},
	}
	if err := in.Check(); err != nil {
		t.Fatal(err)
	}
	fetched := []Fetched{{Input: in.Inputs[0], Bytes: 4}, {Input: in.Inputs[1], Bytes: int64(len(sprite))}}
	src, err := NewSource(&SourceParams{
		Profile: ProfileBundle, Regions: r, Inputs: in, Fetched: fetched,
		Build: BuildInfo{Build: "20261003", Key: "20261003.pmtiles", URL: "https://tiles.test/20261003.pmtiles",
			Size: 138547794382, B3Sum: "927321795f9cfc62a2859b371523e6780ddd5541c0063975231079a84c34acf8", Version: "4.15.2"},
		Archive:   filepath.Join(dir, "basemap.pmtiles"),
		Tools:     map[string]string{"github.com/protomaps/go-pmtiles": "v1.31.2"},
		FetchedAt: time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Write(filepath.Join(dir, "SOURCE.json")); err != nil {
		t.Fatal(err)
	}
	return &fixture{dir: dir, regions: r, inputs: in}
}

package basemap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestRepositoryInputs(t *testing.T) {
	in, err := LoadInputs("../../basemap/inputs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fs := range in.Fontstacks {
		names[fs.Name] = true
		// Every stack carries a Georgian font (WP-L3: every Georgian
		// block has glyphs, in every stack the style uses).
		georgian := false
		for _, id := range fs.Fonts {
			georgian = georgian || strings.Contains(id, "georgian")
		}
		if !georgian {
			t.Errorf("fontstack %s has no Georgian font", fs.Name)
		}
	}
	// uspace-ui src/fonts/faces.ts mapFontstack, and the three stacks of
	// the Protomaps style layers.
	for _, want := range []string{"Noto Sans Regular", "Noto Sans Medium", "Noto Sans Italic"} {
		if !names[want] {
			t.Errorf("fontstack %q missing", want)
		}
	}
	shipped := map[string]bool{}
	for _, x := range in.Inputs {
		shipped[x.Path] = true
	}
	for _, p := range []string{"fonts/OFL.txt", "sprites/v4/LICENSE.md", "sprites/v4/light.json", "sprites/v4/light.png",
		"sprites/v4/light@2x.json", "sprites/v4/light@2x.png", "sprites/v4/dark.json", "sprites/v4/dark.png",
		"sprites/v4/dark@2x.json", "sprites/v4/dark@2x.png"} {
		if !shipped[p] {
			t.Errorf("%s is not shipped", p)
		}
	}
}

func goodInputs(srv string, files map[string][]byte) *Inputs {
	in := &Inputs{
		Tiles:      TileSource{Builds: "https://builds.test/builds.json", Base: "https://tiles.test/", Licence: "ODbL-1.0"},
		Assets:     "https://github.com/protomaps/basemaps-assets@028c18f713baecad011301ff7a69acc39bcc2ae7",
		Licence:    "test",
		Fontstacks: []Fontstack{{Name: "Go Regular", Fonts: []string{"go-regular"}}},
	}
	for _, id := range []string{"go-regular", "licence"} {
		x := Input{ID: id, URL: srv + "/" + id, SHA256: sum(files[id]), Licence: "BSD-3-Clause"}
		if id == "go-regular" {
			x.Version = "Version 2.010; ttfautohint (v1.8.3)"
		} else {
			x.Path = "fonts/LICENSE.txt"
		}
		in.Inputs = append(in.Inputs, x)
	}
	return in
}

func TestFontVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go.ttf")
	if err := os.WriteFile(p, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := FontVersion(p)
	if err != nil || v != "Version 2.010; ttfautohint (v1.8.3)" {
		t.Errorf("FontVersion(goregular) = %q, %v", v, err)
	}
	if err := os.WriteFile(p, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FontVersion(p); err == nil {
		t.Error("a text file has a font version")
	}
}

func TestFetchChecksHashAndVersion(t *testing.T) {
	files := map[string][]byte{"go-regular": goregular.TTF, "licence": []byte("licence text\n")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	ctx := context.Background()

	// Presence: everything matches, the shipped file lands in the bundle.
	in := goodInputs(srv.URL, files)
	dest, bundle := t.TempDir(), t.TempDir()
	got, err := Fetch(ctx, srv.Client(), in, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Bytes != int64(len(goregular.TTF)) {
		t.Fatalf("%+v", got)
	}
	if err := Install(got, bundle); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(bundle, "fonts", "LICENSE.txt")); err != nil || string(b) != "licence text\n" {
		t.Errorf("installed licence: %q %v", b, err)
	}
	if err := WriteFetched(got, filepath.Join(dest, "inputs.json")); err != nil {
		t.Fatal(err)
	}
	back, err := ReadFetched(filepath.Join(dest, "inputs.json"))
	if err != nil || len(back) != 2 || back[1].Path != "fonts/LICENSE.txt" || back[0].Local != filepath.Join(dest, "go-regular") {
		t.Errorf("read back %+v %v", back, err)
	}

	// A changed upstream file: refused and removed.
	in = goodInputs(srv.URL, files)
	in.Inputs[1].SHA256 = sum([]byte("other"))
	dest = t.TempDir()
	if _, err := Fetch(ctx, srv.Client(), in, dest); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Errorf("hash mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "licence")); !os.IsNotExist(err) {
		t.Errorf("the refused file was kept: %v", err)
	}

	// A font of another version: refused.
	in = goodInputs(srv.URL, files)
	in.Inputs[0].Version = "Version 9.999"
	if _, err := Fetch(ctx, srv.Client(), in, t.TempDir()); err == nil || !strings.Contains(err.Error(), "font version") {
		t.Errorf("version mismatch: %v", err)
	}

	// Gone upstream.
	in = goodInputs(srv.URL, files)
	in.Inputs[0].URL = srv.URL + "/gone"
	if _, err := Fetch(ctx, srv.Client(), in, t.TempDir()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
}

func TestInputsCheckRefuses(t *testing.T) {
	files := map[string][]byte{"go-regular": goregular.TTF, "licence": []byte("x")}
	if err := goodInputs("https://files.test", files).Check(); err != nil {
		t.Fatalf("the good manifest is refused: %v", err)
	}
	cases := map[string]struct {
		f    func(in *Inputs)
		want string
	}{
		"short hash":     {func(in *Inputs) { in.Inputs[0].SHA256 = "abc" }, "sha256"},
		"plain http":     {func(in *Inputs) { in.Inputs[0].URL = "http://files.test/x" }, "https"},
		"no licence":     {func(in *Inputs) { in.Inputs[1].Licence = "" }, "licence missing"},
		"path escape":    {func(in *Inputs) { in.Inputs[1].Path = "fonts/../../etc/x" }, "clean path"},
		"path elsewhere": {func(in *Inputs) { in.Inputs[1].Path = "SOURCE.json" }, "under fonts/ or sprites/"},
		"unknown font":   {func(in *Inputs) { in.Fontstacks[0].Fonts = []string{"nope"} }, "not a declared font"},
		"shipped font":   {func(in *Inputs) { in.Fontstacks[0].Fonts = []string{"licence"} }, "not a declared font"},
		"assets unpinned": {func(in *Inputs) {
			in.Assets = "https://github.com/protomaps/basemaps-assets@main"
		}, "assets"},
		"other assets commit": {func(in *Inputs) {
			in.Inputs[1].URL = "https://raw.githubusercontent.com/protomaps/basemaps-assets/main/sprites/v4/light.json"
		}, "assets commit"},
		"no fontstacks": {func(in *Inputs) { in.Fontstacks = nil }, "fontstacks: none"},
		"tile base":     {func(in *Inputs) { in.Tiles.Base = "https://tiles.test" }, "base ending in /"},
	}
	for name, c := range cases {
		in := goodInputs("https://files.test", files)
		c.f(in)
		err := in.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

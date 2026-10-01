package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cispFixture covers every 02 §3 cisp group.
const cispFixture = `openapi: 3.1.0
info:
  title: uspace-cisp
  version: 0.1.0
paths:
  /v1/publications/{dataset}: {}
  /v1/restrictions: {}
  /v1/zones: {}
  /v1/uspace_airspace: {}
  /v1/ussp_list: {}
  /v1/{dataset}/versions/{version}: {}
  /v1/changes: {}
  /v1/subscriptions/{id}: {}
  /v1/stream: {}
  /public/zones: {}
`

func TestParseOpenAPI(t *testing.T) {
	o, err := ParseOpenAPI([]byte(cispFixture))
	if err != nil {
		t.Fatal(err)
	}
	if o.InfoVersion != "0.1.0" || len(o.Paths) != 10 || o.Paths[0] != "/public/zones" {
		t.Fatalf("unexpected parse: %+v", o)
	}
	refusals := map[string]string{
		"3.0":           strings.Replace(cispFixture, "3.1.0", "3.0.3", 1),
		"no version":    strings.Replace(cispFixture, "  version: 0.1.0\n", "", 1),
		"no paths":      strings.SplitN(cispFixture, "paths:", 2)[0],
		"relative path": cispFixture + "  v1/oops: {}\n",
		"not yaml":      "openapi: [3.1.0",
	}
	for name, doc := range refusals {
		if _, err := ParseOpenAPI([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEveryCISPGroupFoundInTheFixtureAndOneMissingWhenRemoved(t *testing.T) {
	o, err := ParseOpenAPI([]byte(cispFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range GroupsFor("cisp") {
		if m := g.Missing(o.Paths); len(m) != 0 {
			t.Errorf("%s: missing %v", g.Row, m)
		}
	}
	o, err = ParseOpenAPI([]byte(strings.Replace(cispFixture, "  /v1/changes: {}\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range GroupsFor("cisp") {
		for _, m := range g.Missing(o.Paths) {
			found = found || m == "/v1/changes"
		}
	}
	if !found {
		t.Fatal("removing /v1/changes was not reported")
	}
}

func TestRenderIndexUnpinnedAndPinned(t *testing.T) {
	got, err := RenderIndex(fixtureRoot(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "0 of 4 systems pinned") || !strings.Contains(got, "| cisp | — | unpinned") {
		t.Fatalf("unpinned index:\n%s", got)
	}
	// The pinned branch: the row carries the commit and info.version.
	got, err = RenderIndex(fixtureRoot(t, cispFixture))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "| cisp | `0.1.0` | `"+pinnedCommit[:12]+"` |") || !strings.Contains(got, "1 of 4 systems pinned") {
		t.Fatalf("pinned index:\n%s", got)
	}
	// A pinned mirror whose file is not OpenAPI 3.1 fails the index.
	root := fixtureRoot(t, cispFixture)
	writeFile(t, filepath.Join(root, "api", "cisp", "openapi.yaml"), strings.Replace(cispFixture, "3.1.0", "3.0.3", 1))
	if _, err := RenderIndex(root); err == nil {
		t.Fatal("a 3.0 mirror rendered")
	}
}

// The committed index is what the generator writes for this repository.
func TestCommittedIndexIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	want, err := RenderIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, IndexPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date: run go run ./scripts/contracts index", IndexPath)
	}
}

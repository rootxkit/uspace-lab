package basemap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shape of build-metadata.protomaps.dev/builds.json as read on
// 2026-10-04 (three entries of it, verbatim).
const buildsJSON = `[
{"key":"20261001.pmtiles","size":138507309367,"md5sum":"iCH6tIUXmKS50bxDMUs/uA==","b3sum":"2b7e76698654cd00de83cd1f9a58d526a5eb9d4419f949966e6e759e39b35ac0","uploaded":"2026-10-01T08:53:09.033Z","version":"4.15.2"},
{"key":"20261002.pmtiles","size":138538266061,"md5sum":"FiouJXpG/gZZMxTfOnCs6A==","b3sum":"0c6171ea16a56aa34cf45678ad17629fca00457cf87ccd5dbfd5c9a22cbd2802","uploaded":"2026-10-02T09:03:21.092Z","version":"4.15.2"},
{"key":"20261003.pmtiles","size":138547794382,"md5sum":"9pqA5fS2f2UYyxqhJYjq3w==","b3sum":"927321795f9cfc62a2859b371523e6780ddd5541c0063975231079a84c34acf8","uploaded":"2026-10-03T09:01:02.614Z","version":"4.15.2"}
]`

func TestFetchBuildInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(buildsJSON))
	}))
	defer srv.Close()
	src := TileSource{Builds: srv.URL + "/builds.json", Base: "https://build.protomaps.com/"}
	ctx := context.Background()
	newest, err := FetchBuildInfo(ctx, srv.Client(), src, "")
	if err != nil {
		t.Fatal(err)
	}
	if newest.Build != "20261003" || newest.URL != "https://build.protomaps.com/20261003.pmtiles" ||
		newest.B3Sum != "927321795f9cfc62a2859b371523e6780ddd5541c0063975231079a84c34acf8" || newest.Size != 138547794382 {
		t.Errorf("newest %+v", newest)
	}
	named, err := FetchBuildInfo(ctx, srv.Client(), src, "20261001")
	if err != nil || named.Build != "20261001" || named.Version != "4.15.2" {
		t.Errorf("named %+v %v", named, err)
	}
	if _, err := FetchBuildInfo(ctx, srv.Client(), src, "20250101"); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Errorf("a pruned build: %v", err)
	}
	if _, err := FetchBuildInfo(ctx, srv.Client(), src, "latest"); err == nil {
		t.Error("a build that is not a date was accepted")
	}
}

func TestNewSourceRecordsWhatTheKitReads(t *testing.T) {
	f := newFixture(t)
	b, err := os.ReadFile(filepath.Join(f.dir, "SOURCE.json"))
	if err != nil {
		t.Fatal(err)
	}
	// uspace-ui parseSourceInfo: bounds a 4-number array in TileJSON
	// order, osm_data_as_of a string.
	var kit struct {
		Bounds      []float64 `json:"bounds"`
		OSMDataAsOf string    `json:"osm_data_as_of"`
	}
	if err := json.Unmarshal(b, &kit); err != nil {
		t.Fatal(err)
	}
	if len(kit.Bounds) != 4 || kit.Bounds[0] != 35 || kit.Bounds[3] != 43.04 || kit.OSMDataAsOf != "2026-10-03T04:00:00Z" {
		t.Errorf("kit view %+v", kit)
	}
	if errs := CheckSource(b, f.regions.Country.Bounds, f.regions.MaxZoom(), f.inputs.Fontstacks); len(errs) != 0 {
		t.Errorf("a fresh SOURCE.json is incomplete: %v", errs)
	}
	var s Source
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Archive.Tiles["14"] != 4 || s.Archive.MaxZoom != 14 || s.FetchedAt != "2026-10-04T06:00:00Z" ||
		len(s.Fonts) != 1 || s.Fonts[0].Fonts[0].Version != "Version 1.0" {
		t.Errorf("source %+v", s)
	}
	if errs := CheckSource(b, Box{1, 2, 3, 4}, 14, nil); len(errs) != 1 || !strings.Contains(errs[0].Error(), "bounds") {
		t.Errorf("other bounds: %v", errs)
	}
}

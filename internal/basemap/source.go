package basemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// BuildInfo is one entry of the Protomaps builds list. The planet file
// is over 100 GB, so its own hashes (BLAKE3, MD5) are what pin the
// upstream; the SHA-256 recorded for the bundle is the extract's.
type BuildInfo struct {
	Build    string `json:"build"`
	URL      string `json:"url"`
	Key      string `json:"key"`
	Size     int64  `json:"size"`
	MD5Sum   string `json:"md5sum"`
	B3Sum    string `json:"b3sum"`
	Uploaded string `json:"uploaded"`
	Version  string `json:"version"`
}

var buildKey = regexp.MustCompile(`^([0-9]{8})\.pmtiles$`)

// BuildKeyPattern is what a build name looks like (a UTC date).
var BuildKeyPattern = regexp.MustCompile(`^[0-9]{8}$`)

// FetchBuildInfo reads the builds list and returns the named build, or
// the newest when build is empty. A build not in the list is an error:
// the builds are pruned upstream, and a missing one cannot be fetched.
func FetchBuildInfo(ctx context.Context, client *http.Client, src TileSource, build string) (BuildInfo, error) {
	if build != "" && !BuildKeyPattern.MatchString(build) {
		return BuildInfo{}, fmt.Errorf("build %q: want YYYYMMDD", build)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Builds, http.NoBody)
	if err != nil {
		return BuildInfo{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return BuildInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return BuildInfo{}, fmt.Errorf("GET %s: %s", src.Builds, resp.Status)
	}
	var list []BuildInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&list); err != nil {
		return BuildInfo{}, fmt.Errorf("%s: %w", src.Builds, err)
	}
	var found *BuildInfo
	for i := range list {
		m := buildKey.FindStringSubmatch(list[i].Key)
		if m == nil {
			continue
		}
		list[i].Build = m[1]
		if build == "" || m[1] == build {
			found = &list[i]
		}
	}
	if found == nil {
		if build == "" {
			return BuildInfo{}, fmt.Errorf("%s: no build listed", src.Builds)
		}
		return BuildInfo{}, fmt.Errorf("%s: build %s not listed (pruned upstream, or not yet built)", src.Builds, build)
	}
	found.URL = src.Base + found.Key
	return *found, nil
}

// ArchiveInfo describes basemap.pmtiles as built.
type ArchiveInfo struct {
	Path    string         `json:"path"`
	Bytes   int64          `json:"bytes"`
	SHA256  string         `json:"sha256"`
	MinZoom int            `json:"min_zoom"`
	MaxZoom int            `json:"max_zoom"`
	Tiles   map[string]int `json:"tiles_per_zoom"`
}

// FontRecord is one font a fontstack is drawn from.
type FontRecord struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Licence string `json:"licence"`
	Source  string `json:"source"`
}

// FontstackRecord is one glyph set of the bundle.
type FontstackRecord struct {
	Name  string       `json:"fontstack"`
	Fonts []FontRecord `json:"fonts"`
}

// Source is SOURCE.json. bounds and osm_data_as_of are what the kit
// reads (uspace-ui PLAN §6.3); the rest says where every byte came from
// and which tool made it (E-05).
type Source struct {
	Source      string            `json:"source"`
	Build       string            `json:"build"`
	Upstream    BuildInfo         `json:"upstream"`
	Profile     string            `json:"profile"`
	Bounds      Box               `json:"bounds"`
	MaxZoom     int               `json:"max_zoom"`
	Regions     any               `json:"regions"`
	OSMDataAsOf string            `json:"osm_data_as_of"`
	Archive     ArchiveInfo       `json:"archive"`
	Fonts       []FontstackRecord `json:"fonts"`
	Assets      string            `json:"assets"`
	Inputs      []Fetched         `json:"inputs"`
	Tools       map[string]string `json:"tools"`
	FetchedAt   string            `json:"fetched_at"`
	Licence     string            `json:"licence"`
}

// Profile names.
const (
	ProfileBundle    = "bundle"
	ProfileStorybook = "storybook"
)

// osmReplicationKey is the metadata key planetiler writes with the time
// of the OSM data it read.
const osmReplicationKey = "planetiler:osm:osmosisreplicationtime"

// SourceParams is what NewSource needs.
type SourceParams struct {
	Profile   string
	Regions   *Regions
	Inputs    *Inputs
	Build     BuildInfo
	Fetched   []Fetched
	Archive   string // path of basemap.pmtiles
	Tools     map[string]string
	FetchedAt time.Time
}

// NewSource assembles SOURCE.json from the archive as built and the
// inputs as fetched.
func NewSource(p *SourceParams) (*Source, error) {
	a, err := OpenArchive(p.Archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = a.Close() }()
	perZoom := map[string]int{}
	if err := a.Tiles(func(e Entry) error {
		z, _, _ := IDToZxy(e.TileID)
		perZoom[fmt.Sprint(z)] += int(e.RunLength)
		return nil
	}); err != nil {
		return nil, err
	}
	sum, n, err := FileSHA256(p.Archive)
	if err != nil {
		return nil, err
	}
	asOf, _ := a.Metadata[osmReplicationKey].(string)
	s := &Source{
		Source:      p.Build.URL,
		Build:       p.Build.Build,
		Upstream:    p.Build,
		Profile:     p.Profile,
		OSMDataAsOf: asOf,
		Archive: ArchiveInfo{
			Path: "basemap.pmtiles", Bytes: n, SHA256: sum,
			MinZoom: int(a.Header.MinZoom), MaxZoom: int(a.Header.MaxZoom), Tiles: perZoom,
		},
		Assets:    p.Inputs.Assets,
		Inputs:    p.Fetched,
		Tools:     p.Tools,
		FetchedAt: p.FetchedAt.UTC().Format(time.RFC3339),
		Licence:   p.Inputs.Licence,
	}
	switch p.Profile {
	case ProfileBundle:
		s.Bounds, s.MaxZoom = p.Regions.Country.Bounds, p.Regions.MaxZoom()
		s.Regions = map[string]any{"country": p.Regions.Country, "cities": p.Regions.Cities}
	case ProfileStorybook:
		s.Bounds, s.MaxZoom = p.Regions.Storybook.Bounds, p.Regions.Storybook.MaxZoom
		s.Regions = map[string]any{"storybook": p.Regions.Storybook}
	default:
		return nil, fmt.Errorf("profile %q: want %s or %s", p.Profile, ProfileBundle, ProfileStorybook)
	}
	byID := map[string]Fetched{}
	for _, f := range p.Fetched {
		byID[f.ID] = f
	}
	for _, fs := range p.Inputs.Fontstacks {
		rec := FontstackRecord{Name: fs.Name}
		for _, id := range fs.Fonts {
			f, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("fontstack %s: font %s was not fetched", fs.Name, id)
			}
			rec.Fonts = append(rec.Fonts, FontRecord{ID: id, Version: f.Version, SHA256: f.SHA256, Licence: f.Licence, Source: f.URL})
		}
		s.Fonts = append(s.Fonts, rec)
	}
	return s, nil
}

// Write writes SOURCE.json.
func (s *Source) Write(p string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(p), append(b, '\n'), 0o644) //nolint:gosec // G306: SOURCE.json is published with the bundle
}

// sameBox compares boxes at the 1e-7 degree resolution of the PMTiles
// header.
func sameBox(a, b Box) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-7 {
			return false
		}
	}
	return true
}

// CheckSource reports what is missing or inconsistent in a SOURCE.json
// body against the policy for the profile.
func CheckSource(body []byte, want Box, wantMaxZoom int, fontstacks []Fontstack) []error {
	var errs []error
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return []error{fmt.Errorf("SOURCE.json: %w", err)}
	}
	for _, k := range []string{"source", "build", "upstream", "bounds", "regions", "osm_data_as_of", "archive", "fonts", "assets", "inputs", "tools", "fetched_at", "licence"} {
		if v, ok := raw[k]; !ok || len(v) == 0 || string(v) == "null" || string(v) == `""` || string(v) == "[]" || string(v) == "{}" {
			errs = append(errs, fmt.Errorf("SOURCE.json: %s missing or empty", k))
		}
	}
	var s Source
	if err := json.Unmarshal(body, &s); err != nil {
		return append(errs, fmt.Errorf("SOURCE.json: %w", err))
	}
	if !sameBox(s.Bounds, want) {
		errs = append(errs, fmt.Errorf("SOURCE.json: bounds %v, the policy says %v", s.Bounds, want))
	}
	if s.MaxZoom != wantMaxZoom {
		errs = append(errs, fmt.Errorf("SOURCE.json: max_zoom %d, the policy says %d", s.MaxZoom, wantMaxZoom))
	}
	for _, k := range []struct{ name, v string }{{"osm_data_as_of", s.OSMDataAsOf}, {"fetched_at", s.FetchedAt}} {
		if _, err := time.Parse(time.RFC3339, k.v); err != nil && k.v != "" {
			errs = append(errs, fmt.Errorf("SOURCE.json: %s %q is not an RFC 3339 time", k.name, k.v))
		}
	}
	if !BuildKeyPattern.MatchString(s.Build) || s.Upstream.B3Sum == "" || s.Upstream.Size <= 0 {
		errs = append(errs, errors.New("SOURCE.json: build or its upstream size and b3sum missing"))
	}
	if !sha256Hex.MatchString(s.Archive.SHA256) {
		errs = append(errs, errors.New("SOURCE.json: archive.sha256 missing"))
	}
	for _, in := range s.Inputs {
		if !sha256Hex.MatchString(in.SHA256) || in.Licence == "" || in.URL == "" {
			errs = append(errs, fmt.Errorf("SOURCE.json: input %q lacks its sha256, licence or source", in.ID))
		}
	}
	have := map[string]FontstackRecord{}
	for _, fs := range s.Fonts {
		have[fs.Name] = fs
	}
	for _, fs := range fontstacks {
		rec, ok := have[fs.Name]
		if !ok || len(rec.Fonts) != len(fs.Fonts) {
			errs = append(errs, fmt.Errorf("SOURCE.json: fontstack %q not recorded with its %d fonts", fs.Name, len(fs.Fonts)))
			continue
		}
		for _, f := range rec.Fonts {
			if f.Version == "" || f.Licence == "" || !sha256Hex.MatchString(f.SHA256) {
				errs = append(errs, fmt.Errorf("SOURCE.json: fontstack %q font %q lacks its version, licence or sha256", fs.Name, f.ID))
			}
		}
	}
	for k, v := range s.Tools {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("SOURCE.json: tool %q has no version", k))
		}
	}
	return errs
}

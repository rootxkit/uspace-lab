package basemap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ReadBudget reads the byte budget of a profile from budget.txt: lines
// "<profile> <bytes>", '#' starts a comment.
func ReadBudget(p, profile string) (int64, error) {
	f, err := os.Open(filepath.Clean(p))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return 0, fmt.Errorf("%s:%d: want \"<profile> <bytes>\"", p, line)
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s:%d: %q is not a positive byte count", p, line, fields[1])
		}
		if fields[0] == profile {
			return n, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("%s: no budget for %q", p, profile)
}

// VerifyConfig is what verify.sh passes.
type VerifyConfig struct {
	Dir         string
	Profile     string
	Regions     *Regions
	Inputs      *Inputs
	BudgetBytes int64
	// RangeURL is basemap.pmtiles as served by a local file_server. It
	// is required: an unchecked range request is a failure, not a skip.
	RangeURL string
	Client   *http.Client
}

// Check is one named result.
type Check struct {
	Name   string
	OK     bool
	Detail []string
}

// Report is the outcome of Verify, check by check.
type Report struct{ Checks []Check }

// OK reports whether every check passed.
func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return len(r.Checks) > 0
}

// Write prints the report, one line per check with its details.
func (r *Report) Write(w io.Writer) error {
	var b bytes.Buffer
	failed := 0
	for _, c := range r.Checks {
		status := "ok  "
		if !c.OK {
			status = "FAIL"
			failed++
		}
		fmt.Fprintf(&b, "%s %s\n", status, c.Name)
		for _, d := range c.Detail {
			fmt.Fprintf(&b, "       %s\n", d)
		}
	}
	fmt.Fprintf(&b, "verify: %d checks, %d failed\n", len(r.Checks), failed)
	_, err := w.Write(b.Bytes())
	return err
}

func result(name string, detail, failures []string) Check {
	return Check{Name: name, OK: len(failures) == 0, Detail: append(detail, failures...)}
}

// Verify runs every check of verify.sh over a bundle directory.
func Verify(ctx context.Context, cfg *VerifyConfig) *Report {
	return &Report{Checks: []Check{
		checkSize(cfg),
		checkGlyphs(cfg),
		checkTiles(cfg),
		checkSourceFile(cfg),
		checkRange(ctx, cfg),
	}}
}

// BundleBytes is the total size of the regular files under dir.
func BundleBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

func checkSize(cfg *VerifyConfig) Check {
	name := "size within budget"
	total, err := BundleBytes(cfg.Dir)
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	detail := []string{fmt.Sprintf("%d bytes (%s), budget %d bytes (%s)", total, mb(total), cfg.BudgetBytes, mb(cfg.BudgetBytes))}
	if total > cfg.BudgetBytes {
		return result(name, detail, []string{fmt.Sprintf("over budget by %d bytes", total-cfg.BudgetBytes)})
	}
	return result(name, detail, nil)
}

func runeList(rs []rune) string {
	parts := make([]string, 0, min(len(rs), 8))
	for i, r := range rs {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("and %d more", len(rs)-8))
			break
		}
		parts = append(parts, fmt.Sprintf("U+%04X", r))
	}
	return strings.Join(parts, " ")
}

func checkGlyphs(cfg *VerifyConfig) Check {
	name := "glyphs: the four Georgian blocks and Basic Latin decoded from every fontstack"
	fontsDir := filepath.Join(cfg.Dir, "fonts")
	var detail, fails []string
	blocks := append(append([]Block{}, GeorgianBlocks...), BasicLatin)
	for _, fsk := range cfg.Inputs.Fontstacks {
		var counts []string
		for _, b := range blocks {
			missing, err := CheckBlock(fontsDir, fsk.Name, b)
			if err != nil {
				fails = append(fails, fmt.Sprintf("%s: %s: %v", fsk.Name, b.Name, err))
				continue
			}
			want := len(b.RequiredRunes())
			if len(missing) > 0 {
				fails = append(fails, fmt.Sprintf("%s: %s (U+%04X-%04X): %d of %d code points do not draw: %s",
					fsk.Name, b.Name, b.First, b.Last, len(missing), want, runeList(missing)))
				continue
			}
			counts = append(counts, fmt.Sprintf("%s %d/%d", b.Name, want, want))
		}
		if len(counts) > 0 {
			detail = append(detail, fmt.Sprintf("%s: %s", fsk.Name, strings.Join(counts, ", ")))
		}
	}
	return result(name, detail, fails)
}

func checkTiles(cfg *VerifyConfig) Check {
	name := "tiles: zoom policy over every tile"
	a, err := OpenArchive(filepath.Join(cfg.Dir, "basemap.pmtiles"))
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	defer func() { _ = a.Close() }()
	type area struct {
		Area
		atMax int
	}
	var bounds Box
	var areas []*area
	var upTo int // zooms up to this one are allowed anywhere inside bounds
	switch cfg.Profile {
	case ProfileBundle:
		bounds, upTo = cfg.Regions.Country.Bounds, cfg.Regions.Country.MaxZoom
		for _, c := range cfg.Regions.Cities {
			areas = append(areas, &area{Area: c})
		}
	case ProfileStorybook:
		bounds, upTo = cfg.Regions.Storybook.Bounds, cfg.Regions.Storybook.MaxZoom
		areas = []*area{{Area: cfg.Regions.Storybook}}
	default:
		return result(name, nil, []string{fmt.Sprintf("profile %q unknown", cfg.Profile)})
	}
	present := map[uint64]bool{}
	perZoom := map[uint8]int{}
	var outside, beyond []string
	const maxRun = 1 << 22
	err = a.Tiles(func(e Entry) error {
		if e.RunLength > maxRun {
			return fmt.Errorf("tile %d: run length %d", e.TileID, e.RunLength)
		}
		for id := e.TileID; id < e.TileID+uint64(e.RunLength); id++ {
			present[id] = true
			z, x, y := IDToZxy(id)
			perZoom[z]++
			tb := TileBox(z, x, y)
			if !tb.Overlaps(bounds) {
				outside = append(outside, fmt.Sprintf("%d/%d/%d", z, x, y))
			}
			allowed := int(z) <= upTo
			for _, ar := range areas {
				if int(z) <= ar.MaxZoom && tb.Overlaps(ar.Bounds) {
					allowed = true
					if int(z) == ar.MaxZoom {
						ar.atMax++
					}
				}
			}
			if !allowed {
				beyond = append(beyond, fmt.Sprintf("%d/%d/%d", z, x, y))
			}
		}
		return nil
	})
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	zs := make([]int, 0, len(perZoom))
	total := 0
	for z, n := range perZoom {
		zs = append(zs, int(z))
		total += n
	}
	sort.Ints(zs)
	counts := make([]string, 0, len(zs))
	for _, z := range zs {
		counts = append(counts, fmt.Sprintf("z%d:%d", z, perZoom[uint8(z)]))
	}
	detail := []string{fmt.Sprintf("%d tiles (%s)", total, strings.Join(counts, " "))}
	var fails []string
	sample := func(s []string) string {
		if len(s) > 5 {
			return strings.Join(s[:5], " ") + fmt.Sprintf(" and %d more", len(s)-5)
		}
		return strings.Join(s, " ")
	}
	if len(outside) > 0 {
		fails = append(fails, fmt.Sprintf("%d tiles outside the bounds %s: %s", len(outside), bounds, sample(outside)))
	}
	if len(beyond) > 0 {
		fails = append(fails, fmt.Sprintf("%d tiles above z%d outside every box that allows their zoom: %s", len(beyond), upTo, sample(beyond)))
	}
	for _, ar := range areas {
		lon, lat := ar.Bounds.Centre()
		z := uint8(ar.MaxZoom)
		x, y := TileAt(z, lon, lat)
		if !present[ZxyToID(z, x, y)] {
			fails = append(fails, fmt.Sprintf("%s: no tile %d/%d/%d at its max zoom over its centre", ar.Name, z, x, y))
			continue
		}
		detail = append(detail, fmt.Sprintf("%s: %d tiles at z%d, centre tile %d/%d/%d present", ar.Name, ar.atMax, z, z, x, y))
	}
	h := a.Header
	if !sameBox(h.Bounds, bounds) {
		fails = append(fails, fmt.Sprintf("header bounds %s, the policy says %s", h.Bounds, bounds))
	}
	return result(name, detail, fails)
}

func checkSourceFile(cfg *VerifyConfig) Check {
	name := "SOURCE.json complete and consistent with the files"
	body, err := os.ReadFile(filepath.Join(cfg.Dir, "SOURCE.json"))
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	want, wantZ := cfg.Regions.Country.Bounds, cfg.Regions.MaxZoom()
	if cfg.Profile == ProfileStorybook {
		want, wantZ = cfg.Regions.Storybook.Bounds, cfg.Regions.Storybook.MaxZoom
	}
	var fails []string
	for _, e := range CheckSource(body, want, wantZ, cfg.Inputs.Fontstacks) {
		fails = append(fails, e.Error())
	}
	var s Source
	if err := json.Unmarshal(body, &s); err != nil {
		return result(name, nil, append(fails, err.Error()))
	}
	sum, _, err := FileSHA256(filepath.Join(cfg.Dir, "basemap.pmtiles"))
	switch {
	case err != nil:
		fails = append(fails, err.Error())
	case sum != s.Archive.SHA256:
		fails = append(fails, fmt.Sprintf("basemap.pmtiles sha256 %s, SOURCE.json says %s", sum, s.Archive.SHA256))
	}
	shipped := 0
	for _, in := range s.Inputs {
		if in.Path == "" {
			continue
		}
		shipped++
		got, _, err := FileSHA256(filepath.Join(cfg.Dir, filepath.FromSlash(in.Path)))
		if err != nil {
			fails = append(fails, err.Error())
			continue
		}
		if got != in.SHA256 {
			fails = append(fails, fmt.Sprintf("%s: sha256 %s, SOURCE.json says %s", in.Path, got, in.SHA256))
		}
	}
	detail := []string{fmt.Sprintf("build %s, OSM data as of %s, %d inputs (%d shipped files hashed), archive sha256 %s",
		s.Build, s.OSMDataAsOf, len(s.Inputs), shipped, s.Archive.SHA256)}
	return result(name, detail, fails)
}

func checkRange(ctx context.Context, cfg *VerifyConfig) Check {
	name := "range request answered with 206 by the file server"
	if cfg.RangeURL == "" {
		return result(name, nil, []string{"no file server URL given: the range request was not made"})
	}
	st, err := os.Stat(filepath.Join(cfg.Dir, "basemap.pmtiles"))
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.RangeURL, http.NoBody)
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", headerLen-1))
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, headerLen+1))
	if err != nil {
		return result(name, nil, []string{err.Error()})
	}
	wantCR := fmt.Sprintf("bytes 0-%d/%d", headerLen-1, st.Size())
	detail := []string{fmt.Sprintf("GET %s Range: bytes=0-%d -> %s, Content-Range %q", cfg.RangeURL, headerLen-1, resp.Status, resp.Header.Get("Content-Range"))}
	var fails []string
	if resp.StatusCode != http.StatusPartialContent {
		fails = append(fails, fmt.Sprintf("status %d, want 206", resp.StatusCode))
	}
	if cr := resp.Header.Get("Content-Range"); cr != wantCR {
		fails = append(fails, fmt.Sprintf("Content-Range %q, want %q", cr, wantCR))
	}
	if len(body) != headerLen || !bytes.HasPrefix(body, []byte("PMTiles")) {
		fails = append(fails, fmt.Sprintf("body: %d bytes, want the %d-byte PMTiles header", len(body), headerLen))
	}
	return result(name, detail, fails)
}

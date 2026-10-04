package basemap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"golang.org/x/image/font/sfnt"
)

// Input is one pinned file of basemap/inputs.yaml.
type Input struct {
	ID      string `yaml:"id" json:"id"`
	URL     string `yaml:"url" json:"source"`
	SHA256  string `yaml:"sha256" json:"sha256"`
	Licence string `yaml:"licence" json:"licence"`
	// Path is where the file lands in the bundle; empty means the build
	// consumes it (a font turned into glyphs).
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	// Version is the name-table version string a font must carry
	// (name ID 5); the fetch reads it from the file and refuses a
	// mismatch. Empty for anything that is not a font.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// Fontstack is one glyph set: its MapLibre fontstack name and the fonts
// font-maker draws it from, first match per code point wins.
type Fontstack struct {
	Name  string   `yaml:"name" json:"name"`
	Fonts []string `yaml:"fonts" json:"fonts"`
}

// TileSource is where the Protomaps daily builds are listed and served.
type TileSource struct {
	Builds  string `yaml:"builds" json:"builds"`
	Base    string `yaml:"base" json:"base"`
	Licence string `yaml:"licence" json:"licence"`
}

// Inputs is basemap/inputs.yaml.
type Inputs struct {
	Tiles      TileSource  `yaml:"tiles"`
	Assets     string      `yaml:"assets"`
	Licence    string      `yaml:"licence"`
	Fontstacks []Fontstack `yaml:"fontstacks"`
	Inputs     []Input     `yaml:"inputs"`
}

var (
	sha256Hex   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	pinnedAsset = regexp.MustCompile(`^https://github\.com/([^/]+/[^/@]+)@([0-9a-f]{40})$`)
)

// maxInputBytes bounds one downloaded input; the largest, a Noto Sans
// TTF, is under half a megabyte.
const maxInputBytes = 32 << 20

// LoadInputs reads and checks an inputs file. Unknown keys are refused.
func LoadInputs(p string) (*Inputs, error) {
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return nil, err
	}
	var in Inputs
	if err := yaml.UnmarshalWithOptions(b, &in, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := in.Check(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &in, nil
}

func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// Check validates the manifest: every input pinned by SHA-256 with a
// licence and an https source, bundle paths under fonts/ or sprites/,
// every basemaps-assets file at the declared commit, every fontstack
// made of declared fonts.
func (in *Inputs) Check() error {
	var errs []error
	if !httpsURL(in.Tiles.Builds) || !httpsURL(in.Tiles.Base) || !strings.HasSuffix(in.Tiles.Base, "/") {
		errs = append(errs, errors.New("tiles: builds and base must be https URLs, base ending in /"))
	}
	if in.Tiles.Licence == "" || in.Licence == "" {
		errs = append(errs, errors.New("tiles.licence and licence: required"))
	}
	m := pinnedAsset.FindStringSubmatch(in.Assets)
	if m == nil {
		errs = append(errs, fmt.Errorf("assets: %q is not https://github.com/OWNER/REPO@COMMIT", in.Assets))
	}
	ids := map[string]*Input{}
	paths := map[string]bool{}
	for i := range in.Inputs {
		x := &in.Inputs[i]
		f := fmt.Sprintf("inputs[%d] (%s)", i, x.ID)
		if x.ID == "" || ids[x.ID] != nil {
			errs = append(errs, fmt.Errorf("%s: id missing or repeated", f))
		}
		ids[x.ID] = x
		if !httpsURL(x.URL) {
			errs = append(errs, fmt.Errorf("%s: url must be https", f))
		}
		if !sha256Hex.MatchString(x.SHA256) {
			errs = append(errs, fmt.Errorf("%s: sha256 must be 64 lower-case hex digits", f))
		}
		if x.Licence == "" {
			errs = append(errs, fmt.Errorf("%s: licence missing", f))
		}
		if x.Path != "" {
			clean := path.Clean(x.Path)
			if clean != x.Path || (!strings.HasPrefix(clean, "fonts/") && !strings.HasPrefix(clean, "sprites/")) {
				errs = append(errs, fmt.Errorf("%s: path %q must be a clean path under fonts/ or sprites/", f, x.Path))
			}
			if paths[clean] {
				errs = append(errs, fmt.Errorf("%s: path %q twice", f, x.Path))
			}
			paths[clean] = true
		}
		if m != nil {
			prefix := "https://raw.githubusercontent.com/" + m[1] + "/"
			if strings.HasPrefix(x.URL, prefix) && !strings.HasPrefix(x.URL, prefix+m[2]+"/") {
				errs = append(errs, fmt.Errorf("%s: %s files must come from the assets commit %s", f, m[1], m[2]))
			}
		}
	}
	if len(in.Fontstacks) == 0 {
		errs = append(errs, errors.New("fontstacks: none"))
	}
	for i, fs := range in.Fontstacks {
		if fs.Name == "" || strings.ContainsAny(fs.Name, "/\\") || len(fs.Fonts) == 0 {
			errs = append(errs, fmt.Errorf("fontstacks[%d]: a name without slashes and at least one font", i))
		}
		for _, id := range fs.Fonts {
			if x := ids[id]; x == nil || x.Version == "" || x.Path != "" {
				errs = append(errs, fmt.Errorf("fontstacks[%d] (%s): %q is not a declared font input (version set, no bundle path)", i, fs.Name, id))
			}
		}
	}
	return errors.Join(errs...)
}

// Fetched is an input on disk with what was measured from it.
type Fetched struct {
	Input
	Local string `json:"-"`
	Bytes int64  `json:"bytes"`
}

// Fetch downloads every input into dest, hashing while it streams, and
// refuses a file whose SHA-256 differs from the pin or a font whose
// name-table version differs from the declared one. A refused file is
// removed.
func Fetch(ctx context.Context, client *http.Client, in *Inputs, dest string) ([]Fetched, error) {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return nil, err
	}
	out := make([]Fetched, 0, len(in.Inputs))
	for _, x := range in.Inputs {
		local := filepath.Join(dest, x.ID)
		n, sum, err := download(ctx, client, x.URL, local)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", x.ID, err)
		}
		if sum != x.SHA256 {
			_ = os.Remove(local)
			return nil, fmt.Errorf("%s: sha256 %s, pinned %s (%s)", x.ID, sum, x.SHA256, x.URL)
		}
		if x.Version != "" {
			v, err := FontVersion(local)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", x.ID, err)
			}
			if v != x.Version {
				_ = os.Remove(local)
				return nil, fmt.Errorf("%s: font version %q, declared %q", x.ID, v, x.Version)
			}
		}
		out = append(out, Fetched{Input: x, Local: local, Bytes: n})
	}
	return out, nil
}

func download(ctx context.Context, client *http.Client, u, local string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	f, err := os.Create(filepath.Clean(local))
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxInputBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxInputBytes {
		err = fmt.Errorf("GET %s: over %d bytes", u, maxInputBytes)
	}
	if err != nil {
		_ = os.Remove(local)
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// FontVersion reads the version string (name ID 5) of a TrueType or
// OpenType font.
func FontVersion(p string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	f, err := sfnt.Parse(b)
	if err != nil {
		return "", fmt.Errorf("font: %w", err)
	}
	v, err := f.Name(nil, sfnt.NameIDVersion)
	if err != nil {
		return "", fmt.Errorf("font version: %w", err)
	}
	return v, nil
}

// FileSHA256 hashes a file.
func FileSHA256(p string) (string, int64, error) {
	f, err := os.Open(filepath.Clean(p))
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Install copies the inputs that have a bundle path into the bundle.
func Install(fetched []Fetched, bundle string) error {
	for _, f := range fetched {
		if f.Path == "" {
			continue
		}
		dst := filepath.Join(bundle, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Clean(f.Local))
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil { //nolint:gosec // G306: the bundle is public data served read-only
			return err
		}
	}
	return nil
}

// WriteFetched records what was fetched, for the source step.
func WriteFetched(fetched []Fetched, p string) error {
	b, err := json.MarshalIndent(fetched, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(p), append(b, '\n'), 0o600)
}

// ReadFetched reads what WriteFetched wrote; Local is the file beside it
// named by the input id.
func ReadFetched(p string) ([]Fetched, error) {
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return nil, err
	}
	var fs []Fetched
	if err := json.Unmarshal(b, &fs); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	for i := range fs {
		fs[i].Local = filepath.Join(filepath.Dir(p), fs[i].ID)
	}
	return fs, nil
}

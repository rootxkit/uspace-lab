package contracts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// OpenAPI is what the aggregate reads from a mirrored openapi.yaml.
type OpenAPI struct {
	// Version is the `openapi` field (3.1.x).
	Version string
	Title   string
	// InfoVersion is `info.version`, the API's own version.
	InfoVersion string
	// Paths are the keys of `paths`, sorted.
	Paths []string
}

type openAPIDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    *struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths map[string]any `yaml:"paths"`
}

// ParseOpenAPI parses an OpenAPI document and refuses anything that is
// not OpenAPI 3.1 with `info.title`, `info.version` and a `paths` object
// (00 §7, 02 §1: every national API is published as OpenAPI 3.1).
func ParseOpenAPI(b []byte) (OpenAPI, error) {
	var d openAPIDoc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return OpenAPI{}, fmt.Errorf("not YAML: %w", err)
	}
	if !strings.HasPrefix(d.OpenAPI, "3.1.") {
		return OpenAPI{}, fmt.Errorf("openapi: %q is not 3.1.x", d.OpenAPI)
	}
	if d.Info == nil || d.Info.Title == "" || d.Info.Version == "" {
		return OpenAPI{}, fmt.Errorf("info: title and version are required")
	}
	if d.Paths == nil {
		return OpenAPI{}, fmt.Errorf("paths: missing")
	}
	o := OpenAPI{Version: d.OpenAPI, Title: d.Info.Title, InfoVersion: d.Info.Version}
	for p := range d.Paths {
		if !strings.HasPrefix(p, "/") {
			return OpenAPI{}, fmt.Errorf("paths: %q does not start with /", p)
		}
		o.Paths = append(o.Paths, p)
	}
	sort.Strings(o.Paths)
	return o, nil
}

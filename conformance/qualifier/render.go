package qualifier

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// CoverageFormat names conformance/uss_qualifier/coverage.yaml.
const CoverageFormat = "conformance-qualifier-coverage/v1"

// Interfaces are the InterUSS automated-testing interfaces a
// configuration may need.
var Interfaces = []string{"rid_injection", "rid_observation", "flight_planning"}

// Cover is one requirement's entry: the configuration that decides it
// and the interface it needs, or why uss_qualifier cannot decide it.
type Cover struct {
	Config string `yaml:"config"`
	Needs  string `yaml:"needs"`
	None   string `yaml:"none"`
}

// Coverage is coverage.yaml.
type Coverage struct {
	Format       string           `yaml:"format"`
	Requirements map[string]Cover `yaml:"requirements"`
}

// LoadCoverage reads and checks coverage.yaml; every configuration it
// names must exist beside it.
func LoadCoverage(path string) (*Coverage, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own configuration
	if err != nil {
		return nil, err
	}
	var c Coverage
	if err := yaml.UnmarshalWithOptions(b, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Format != CoverageFormat {
		return nil, fmt.Errorf("%s: format %q, want %s", path, c.Format, CoverageFormat)
	}
	for id, cv := range c.Requirements {
		switch {
		case cv.None != "" && (cv.Config != "" || cv.Needs != ""):
			return nil, fmt.Errorf("%s: %s: none beside a config", path, id)
		case cv.None != "":
		case cv.Config == "" || !contains(Interfaces, cv.Needs):
			return nil, fmt.Errorf("%s: %s: config and needs (%s) are required", path, id, strings.Join(Interfaces, ", "))
		default:
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), cv.Config)); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, id, err)
			}
		}
	}
	return &c, nil
}

// IDs are the covered requirement ids, sorted.
func (c *Coverage) IDs() []string {
	out := make([]string, 0, len(c.Requirements))
	for id := range c.Requirements {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

var placeholder = regexp.MustCompile(`\$\{(QUALIFIER_[A-Z0-9_]+)\}`)

// Render writes config and the library it references from srcDir into
// outDir with every ${QUALIFIER_*} placeholder replaced from vars. A
// placeholder without a value is an error naming every such variable
// (no half-configured run). It returns the rendered configuration path.
func Render(srcDir, outDir, config string, vars map[string]string) (string, error) {
	files := []string{config, filepath.Join("library", "environment.yaml"), filepath.Join("library", "resources.yaml")}
	missing := map[string]bool{}
	rendered := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(srcDir, f)) //nolint:gosec // the suite's own configurations
		if err != nil {
			return "", err
		}
		rendered[f] = placeholder.ReplaceAllStringFunc(string(b), func(m string) string {
			name := placeholder.FindStringSubmatch(m)[1]
			v, ok := vars[name]
			if !ok || v == "" {
				missing[name] = true
				return m
			}
			return v
		})
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", fmt.Errorf("render %s: unset: %s", config, strings.Join(names, " "))
	}
	for _, f := range files {
		p := filepath.Join(outDir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(rendered[f]), 0o600); err != nil {
			return "", err
		}
	}
	return filepath.Join(outDir, config), nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

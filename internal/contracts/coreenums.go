package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CoreModule is the module whose enumerations the common schemas pin.
const CoreModule = "github.com/rootxkit/uspace-core"

// ModuleDir returns the directory and version of a module in the build
// list of the current module (`go list -m`), so a test reads the source
// at exactly the version go.mod pins.
func ModuleDir(module string) (dir, version string, err error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("go", "list", "-m", "-json", module) //nolint:gosec // G204: fixed go subcommand; module is a constant path from this package
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("go list -m %s: %w: %s", module, err, strings.TrimSpace(errb.String()))
	}
	var m struct{ Dir, Version string }
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		return "", "", err
	}
	if m.Dir == "" {
		return "", "", fmt.Errorf("go list -m %s: module not downloaded", module)
	}
	return m.Dir, m.Version, nil
}

// GoStringEnums reads every non-test .go file in dir and returns, for each
// named type in types, the string constants declared with that type,
// sorted. It reads the source rather than a hand-kept list so that a
// value added in core is seen.
func GoStringEnums(dir string, types ...string) (map[string][]string, error) {
	want := map[string]bool{}
	for _, t := range types {
		want[t] = true
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f) //nolint:gosec // a file of the pinned module
		if err != nil {
			return nil, err
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				id, ok := vs.Type.(*ast.Ident)
				if !ok || !want[id.Name] {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return nil, fmt.Errorf("%s: a %s constant is not a string literal", f, id.Name)
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						return nil, err
					}
					out[id.Name] = append(out[id.Name], s)
				}
			}
		}
	}
	for t := range want {
		if len(out[t]) == 0 {
			return nil, fmt.Errorf("no constants of type %s in %s", t, dir)
		}
		sort.Strings(out[t])
	}
	return out, nil
}

// SchemaEnum returns the enum at a path of keys inside a decoded schema
// document, as strings (null members are dropped: they say "nullable",
// they are not a value of the enumeration), sorted.
func SchemaEnum(doc any, path ...string) ([]string, error) {
	cur := doc
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: not an object at %q", strings.Join(path, "."), k)
		}
		cur, ok = m[k]
		if !ok {
			return nil, fmt.Errorf("%s: no key %q", strings.Join(path, "."), k)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: not an object", strings.Join(path, "."))
	}
	list, ok := m["enum"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s: no enum", strings.Join(path, "."))
	}
	var out []string
	for _, v := range list {
		switch s := v.(type) {
		case string:
			out = append(out, s)
		case nil:
		default:
			return nil, fmt.Errorf("%s: enum member %v is not a string", strings.Join(path, "."), v)
		}
	}
	sort.Strings(out)
	return out, nil
}

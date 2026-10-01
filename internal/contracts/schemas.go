package contracts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// IDBase is the prefix of every schema `$id` (spec 04 §1).
const IDBase = "https://schemas.uspace.ge/"

// MinValidExamples is the number of valid examples every schema needs
// (docs/PLAN.md §3.1: "at least two examples").
const MinValidExamples = 2

// Schema is one schema.json under a schemas root.
type Schema struct {
	// Name is the catalogue name, `family/name/vN`, which is also the
	// directory path below the root.
	Name string
	// Dir is the directory holding schema.json and examples/.
	Dir string
	// ID is the `$id` read from the file.
	ID string
	// Doc is the decoded document (jsonschema.UnmarshalJSON form).
	Doc any
}

// WantID is the `$id` the schema must carry.
func (s Schema) WantID() string { return IDBase + s.Name + ".json" }

// LoadSchemas finds every schema.json under root. A schema's name is its
// directory relative to root, with forward slashes.
func LoadSchemas(root string) ([]Schema, error) {
	var out []Schema
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "schema.json" {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		doc, err := readJSON(path)
		if err != nil {
			return err
		}
		m, ok := doc.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: not a JSON object", path)
		}
		id, _ := m["$id"].(string)
		out = append(out, Schema{Name: filepath.ToSlash(rel), Dir: dir, ID: id, Doc: doc})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func readJSON(path string) (any, error) {
	f, err := os.Open(path) //nolint:gosec // paths come from walking the repository's own schemas tree
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// Compile compiles every schema with all of them registered as
// resources, so `$ref`s between them resolve by `$id` without any
// network access. Formats are asserted (date-time, uri-reference).
func Compile(schemas []Schema) (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(offlineLoader{})
	for _, s := range schemas {
		if s.ID == "" {
			return nil, fmt.Errorf("%s: schema.json has no $id", s.Name)
		}
		if err := c.AddResource(s.ID, s.Doc); err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	out := make(map[string]*jsonschema.Schema, len(schemas))
	for _, s := range schemas {
		sch, err := c.Compile(s.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		out[s.Name] = sch
	}
	return out, nil
}

// offlineLoader refuses every URL that was not added as a resource: a
// `$ref` to anything outside the loaded set is an error, never a fetch.
type offlineLoader struct{}

// Load refuses url: it was not added with AddResource.
func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("offline: %s is not one of the loaded schemas", url)
}

// ExampleResult is the outcome of one example file.
type ExampleResult struct {
	File      string
	WantValid bool
	// Reason is the validator's message for a document that failed, empty
	// for one that passed.
	Reason string
	// Problem is non-empty when the result is wrong: a valid example that
	// failed, an invalid one that passed, or a file that is not JSON.
	Problem string
}

// SchemaReport is the outcome of one schema.
type SchemaReport struct {
	Name     string
	Valid    int
	Invalid  int
	Results  []ExampleResult
	Problems []string
}

// Report is the outcome of CheckExamples.
type Report struct {
	Schemas []SchemaReport
}

// Failed reports whether anything is wrong.
func (r Report) Failed() bool {
	for _, s := range r.Schemas {
		if len(s.Problems) > 0 {
			return true
		}
		for _, e := range s.Results {
			if e.Problem != "" {
				return true
			}
		}
	}
	return false
}

// CheckExamples validates every schema's examples both ways (LESSONS
// E-01): each examples/*.json must validate, each examples/invalid/*.json
// must fail, and every schema needs at least MinValidExamples valid and
// one invalid example. It also checks each schema's `$id`, `title` and,
// for envelope messages, the `schema` constant against its directory.
func CheckExamples(root string) (Report, error) {
	schemas, err := LoadSchemas(root)
	if err != nil {
		return Report{}, err
	}
	if len(schemas) == 0 {
		return Report{}, fmt.Errorf("no schema.json under %s", root)
	}
	compiled, err := Compile(schemas)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	for _, s := range schemas {
		rep.Schemas = append(rep.Schemas, checkSchema(s, compiled[s.Name]))
	}
	return rep, nil
}

func checkSchema(s Schema, sch *jsonschema.Schema) SchemaReport {
	sr := SchemaReport{Name: s.Name}
	m, _ := s.Doc.(map[string]any)
	if s.ID != s.WantID() {
		sr.Problems = append(sr.Problems, fmt.Sprintf("$id is %q, want %q", s.ID, s.WantID()))
	}
	if t, _ := m["title"].(string); t != s.Name {
		sr.Problems = append(sr.Problems, fmt.Sprintf("title is %q, want %q", t, s.Name))
	}
	if c, ok := schemaConst(m); ok && c != s.Name {
		sr.Problems = append(sr.Problems, fmt.Sprintf("properties.schema.const is %q, want %q", c, s.Name))
	}
	for _, want := range []bool{true, false} {
		dir := filepath.Join(s.Dir, "examples")
		if !want {
			dir = filepath.Join(dir, "invalid")
		}
		files, err := exampleFiles(dir)
		if err != nil {
			sr.Problems = append(sr.Problems, err.Error())
			continue
		}
		for _, f := range files {
			res := checkExample(sch, f, want)
			sr.Results = append(sr.Results, res)
			if want {
				sr.Valid++
			} else {
				sr.Invalid++
			}
		}
	}
	if sr.Valid < MinValidExamples {
		sr.Problems = append(sr.Problems, fmt.Sprintf("%d valid examples, need at least %d", sr.Valid, MinValidExamples))
	}
	if sr.Invalid < 1 {
		sr.Problems = append(sr.Problems, "no invalid example: a validator that only sees valid documents proves nothing (E-01)")
	}
	return sr
}

func schemaConst(m map[string]any) (string, bool) {
	props, _ := m["properties"].(map[string]any)
	sp, _ := props["schema"].(map[string]any)
	c, ok := sp["const"].(string)
	return c, ok
}

// exampleFiles lists the *.json files directly in dir. Anything else in
// it (other than the invalid/ directory) is an error, so a misnamed
// example is not skipped in silence.
func exampleFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		switch {
		case e.IsDir() && e.Name() == "invalid" && filepath.Base(dir) == "examples":
		case !e.IsDir() && strings.HasSuffix(e.Name(), ".json"):
			out = append(out, filepath.Join(dir, e.Name()))
		default:
			return nil, fmt.Errorf("%s: unexpected entry %q (examples are *.json files)", dir, e.Name())
		}
	}
	return out, nil
}

func checkExample(sch *jsonschema.Schema, file string, wantValid bool) ExampleResult {
	res := ExampleResult{File: file, WantValid: wantValid}
	doc, err := readJSON(file)
	if err != nil {
		// A document that is not JSON fails for the wrong reason: it is
		// never counted as a correctly refused invalid example.
		res.Problem = "not valid JSON: " + err.Error()
		return res
	}
	err = sch.Validate(doc)
	var ve *jsonschema.ValidationError
	switch {
	case err == nil && wantValid:
	case err == nil:
		res.Problem = "invalid example passed validation"
	case errors.As(err, &ve):
		res.Reason = leafReason(ve)
		if wantValid {
			res.Problem = "valid example failed: " + res.Reason
		}
	default:
		res.Problem = "validator error: " + err.Error()
	}
	return res
}

// leafReason is the deepest validation messages, one line each, which is
// what a reader needs to see that an invalid example fails for the rule
// its name gives.
func leafReason(ve *jsonschema.ValidationError) string {
	type line struct {
		indent int
		text   string
	}
	var lines []line
	for _, l := range strings.Split(ve.Error(), "\n") {
		t := strings.TrimLeft(l, " ")
		if strings.HasPrefix(t, "- ") {
			lines = append(lines, line{indent: len(l) - len(t), text: strings.TrimPrefix(t, "- ")})
		}
	}
	seen := map[string]bool{}
	var leaves []string
	for i, l := range lines {
		if i+1 < len(lines) && lines[i+1].indent > l.indent {
			continue // not a leaf: the lines below it say why
		}
		if !seen[l.text] {
			seen[l.text] = true
			leaves = append(leaves, l.text)
		}
	}
	if len(leaves) == 0 {
		return strings.TrimSpace(ve.Error())
	}
	sort.Strings(leaves)
	return strings.Join(leaves, "; ")
}

package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The checker is tested in both directions (LESSONS E-01): a correct tree
// passes, and each way a tree can be wrong is made to happen and is
// reported. Without the second half, a checker that never fails would
// pass the real schemas just as well.

const thingSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://schemas.uspace.ge/thing/v1.json",
  "title": "thing/v1",
  "allOf": [{ "$ref": "https://schemas.uspace.ge/base/v1.json" }],
  "properties": { "schema": { "const": "thing/v1" }, "a": { "type": "integer" } },
  "required": ["a"]
}`

const baseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://schemas.uspace.ge/base/v1.json",
  "title": "base/v1",
  "type": "object",
  "required": ["schema"],
  "properties": { "at": { "type": "string", "format": "date-time" } }
}`

// fp joins a slash-separated relative path onto root.
func fp(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// goodTree writes two schemas, one referring to the other by $id, each
// with two valid and one invalid example.
func goodTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, fp(root, "thing/v1/schema.json"), thingSchema)
	writeFile(t, fp(root, "thing/v1/examples/one.json"), `{"schema": "thing/v1", "a": 1}`)
	writeFile(t, fp(root, "thing/v1/examples/two.json"), `{"schema": "thing/v1", "a": 2, "at": "2026-10-02T09:15:00.000Z"}`)
	writeFile(t, fp(root, "thing/v1/examples/invalid/missing-a.json"), `{"schema": "thing/v1"}`)
	writeFile(t, fp(root, "base/v1/schema.json"), baseSchema)
	writeFile(t, fp(root, "base/v1/examples/one.json"), `{"schema": "x/v1"}`)
	writeFile(t, fp(root, "base/v1/examples/two.json"), `{"schema": "y/v1"}`)
	writeFile(t, fp(root, "base/v1/examples/invalid/missing-schema.json"), `{}`)
	return root
}

func problems(rep Report) string {
	var out []string
	for _, s := range rep.Schemas {
		for _, p := range s.Problems {
			out = append(out, s.Name+": "+p)
		}
		for _, e := range s.Results {
			if e.Problem != "" {
				out = append(out, s.Name+": "+filepath.Base(e.File)+": "+e.Problem)
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestCheckExamplesAcceptsAGoodTree(t *testing.T) {
	rep, err := CheckExamples(goodTree(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed() {
		t.Fatalf("good tree reported problems:\n%s", problems(rep))
	}
	if len(rep.Schemas) != 2 || rep.Schemas[1].Valid != 2 || rep.Schemas[1].Invalid != 1 {
		t.Fatalf("unexpected counts: %+v", rep.Schemas)
	}
	for _, s := range rep.Schemas {
		for _, e := range s.Results {
			if !e.WantValid && e.Reason == "" {
				t.Errorf("%s: an invalid example carries no reason", e.File)
			}
		}
	}
}

func TestCheckExamplesReportsEachKindOfFault(t *testing.T) {
	cases := []struct {
		name  string
		mess  func(t *testing.T, root string)
		wants string
	}{
		{"invalid example that passes", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/examples/invalid/not-really.json"), `{"schema": "thing/v1", "a": 3}`)
		}, "invalid example passed validation"},
		{"valid example that fails", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/examples/three.json"), `{"schema": "thing/v1", "a": "x"}`)
		}, "valid example failed"},
		{"format is asserted", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/examples/three.json"), `{"schema": "thing/v1", "a": 1, "at": "yesterday"}`)
		}, "valid example failed"},
		{"invalid example that is not JSON", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/examples/invalid/broken.json"), `{"schema": `)
		}, "not valid JSON"},
		{"no invalid example", func(t *testing.T, root string) {
			if err := os.RemoveAll(fp(root, "thing/v1/examples/invalid")); err != nil {
				t.Fatal(err)
			}
		}, "no invalid example"},
		{"one valid example", func(t *testing.T, root string) {
			if err := os.Remove(fp(root, "thing/v1/examples/two.json")); err != nil {
				t.Fatal(err)
			}
		}, "1 valid examples, need at least 2"},
		{"misnamed example file", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/examples/three.yaml"), "a: 1")
		}, "unexpected entry"},
		{"$id that does not match the directory", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/schema.json"), strings.Replace(thingSchema, "thing/v1.json", "thing/v2.json", 1))
		}, "$id is"},
		{"schema constant that does not match the directory", func(t *testing.T, root string) {
			writeFile(t, fp(root, "thing/v1/schema.json"), strings.Replace(thingSchema, `"const": "thing/v1"`, `"const": "thing/v2"`, 1))
		}, "properties.schema.const"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := goodTree(t)
			tc.mess(t, root)
			rep, err := CheckExamples(root)
			if err != nil {
				t.Fatal(err)
			}
			if !rep.Failed() {
				t.Fatal("fault not reported")
			}
			if got := problems(rep); !strings.Contains(got, tc.wants) {
				t.Fatalf("problems do not mention %q:\n%s", tc.wants, got)
			}
		})
	}
}

func TestCompileRefusesARefOutsideTheLoadedSet(t *testing.T) {
	root := goodTree(t)
	writeFile(t, fp(root, "thing/v1/schema.json"), strings.Replace(thingSchema, "https://schemas.uspace.ge/base/v1.json", "https://schemas.uspace.ge/elsewhere/v1.json", 1))
	_, err := CheckExamples(root)
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("want an offline refusal, got %v", err)
	}
}

func TestCheckExamplesRefusesAnEmptyRoot(t *testing.T) {
	if _, err := CheckExamples(t.TempDir()); err == nil {
		t.Fatal("an empty tree must not pass")
	}
}

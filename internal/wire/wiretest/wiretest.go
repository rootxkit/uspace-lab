// Package wiretest validates what the lab writes against the producing
// systems' pinned schemas (internal/wire/testdata, SOURCE) with the lab's
// schemas/common registered, so `$ref`s to the envelope resolve offline.
// Test helper only.
package wiretest

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rootxkit/uspace-lab/internal/contracts"
)

// Root is the repository root (from this file's location).
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// TestdataDir is internal/wire/testdata.
func TestdataDir() string { return filepath.Join(Root(), "internal", "wire", "testdata") }

var (
	once       sync.Once
	compiler   *jsonschema.Compiler
	errCompile error
	cache      = map[string]*jsonschema.Schema{}
	cacheMu    sync.Mutex
)

func setup() {
	common, err := contracts.LoadSchemas(filepath.Join(Root(), "schemas", "common"))
	if err != nil {
		errCompile = err
		return
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	for _, s := range common {
		if err := c.AddResource(s.ID, s.Doc); err != nil {
			errCompile = err
			return
		}
	}
	compiler = c
}

// Schema compiles the pinned copy at rel (for example
// "ussp/telemetry-v1.json"), after checking its SHA-256 against SOURCE.
func Schema(t testing.TB, rel string) *jsonschema.Schema {
	t.Helper()
	once.Do(setup)
	if errCompile != nil {
		t.Fatal(errCompile)
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if s, ok := cache[rel]; ok {
		return s
	}
	if err := CheckPin(rel); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(TestdataDir(), filepath.FromSlash(rel))
	f, err := os.Open(path) //nolint:gosec // a pinned file under testdata
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("%s has no $id", rel)
	}
	// The ussp and ansp copies of envelope-based schemas share $ids with
	// the common ones; a pinned file is added under its own path.
	url := "file:///pinned/" + rel
	if err := compiler.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	s, err := compiler.Compile(url)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	cache[rel] = s
	return s
}

// CheckPin compares the file's SHA-256 with its SOURCE line.
func CheckPin(rel string) error {
	f, err := os.Open(filepath.Join(TestdataDir(), "SOURCE"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || !strings.HasSuffix(line, "-> "+rel) {
			continue
		}
		want := strings.Fields(line)[0]
		b, err := os.ReadFile(filepath.Join(TestdataDir(), filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			return fmt.Errorf("%s: SHA-256 %s, SOURCE pins %s", rel, got, want)
		}
		return nil
	}
	return fmt.Errorf("%s: no SOURCE line", rel)
}

// Validate marshals v and validates it against the pinned schema rel.
func Validate(t testing.TB, rel string, v any) {
	t.Helper()
	if err := Check(t, rel, v); err != nil {
		b, _ := json.Marshal(v)
		t.Fatalf("%s refuses the document: %v\n%s", rel, err, b)
	}
}

// Check returns the validation error (nil when valid).
func Check(t testing.TB, rel string, v any) error {
	t.Helper()
	s := Schema(t, rel)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(doc)
}

// RegisterByID adds the pinned copy rel under its own $id as well, so a
// pinned schema whose $ref names it (traffic/product/v1 refers to
// alert/v1's body) resolves offline. Once per rel.
func RegisterByID(t testing.TB, rel string) {
	t.Helper()
	once.Do(setup)
	if errCompile != nil {
		t.Fatal(errCompile)
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if byID[rel] {
		return
	}
	if err := CheckPin(rel); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(TestdataDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("%s has no $id", rel)
	}
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	byID[rel] = true
}

var byID = map[string]bool{}

// CommonCheck validates v against the schemas/common schema with $id id
// (for example https://schemas.uspace.ge/track/telemetry/v1.json).
func CommonCheck(t testing.TB, id string, v any) error {
	t.Helper()
	once.Do(setup)
	if errCompile != nil {
		t.Fatal(errCompile)
	}
	cacheMu.Lock()
	s, ok := cache[id]
	if !ok {
		var err error
		if s, err = compiler.Compile(id); err != nil {
			cacheMu.Unlock()
			t.Fatalf("%s: %v", id, err)
		}
		cache[id] = s
	}
	cacheMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(doc)
}

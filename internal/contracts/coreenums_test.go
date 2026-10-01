package contracts

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestGoStringEnumsReadsEveryConstantOfTheType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), `package core
type Colour string
const (
	Red Colour = "red"
	Blue Colour = "blue"
	notAColour = "green"
)
const Green Colour = "green"
`)
	writeFile(t, filepath.Join(dir, "a_test.go"), `package core
const Test Colour = "test-only"
`)
	got, err := GoStringEnums(dir, "Colour")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"blue", "green", "red"}; !reflect.DeepEqual(got["Colour"], want) {
		t.Fatalf("got %v, want %v", got["Colour"], want)
	}
	if _, err := GoStringEnums(dir, "Shape"); err == nil {
		t.Fatal("a type with no constants must be an error, not an empty set")
	}
}

func TestSchemaEnumDropsNullAndSorts(t *testing.T) {
	doc := map[string]any{"a": map[string]any{"enum": []any{"z", nil, "b"}}}
	got, err := SchemaEnum(doc, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"b", "z"}) {
		t.Fatalf("got %v", got)
	}
	if _, err := SchemaEnum(doc, "b"); err == nil {
		t.Fatal("missing path accepted")
	}
}

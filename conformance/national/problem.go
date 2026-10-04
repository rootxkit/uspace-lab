package national

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ProblemSchemaID is the $id of the common problem/v1 schema.
const ProblemSchemaID = "https://schemas.uspace.ge/problem/v1.json"

// CompileProblem compiles schemas/common/problem/v1/schema.json, the
// error body every national refusal must carry (02 §1, M28).
func CompileProblem(commonDir string) (*jsonschema.Schema, error) {
	path := filepath.Join(commonDir, "problem", "v1", "schema.json")
	f, err := os.Open(path) //nolint:gosec // the lab's own schema tree
	if err != nil {
		return nil, fmt.Errorf("problem/v1: %w", err)
	}
	defer func() { _ = f.Close() }()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, fmt.Errorf("problem/v1: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource(ProblemSchemaID, doc); err != nil {
		return nil, fmt.Errorf("problem/v1: %w", err)
	}
	return c.Compile(ProblemSchemaID)
}

package national

import (
	"fmt"
	"strings"
)

// unknownCandidates are tried, in order, as an identifier no system
// holds: the first that the parameter's schema accepts is used. Each is
// a well-formed value of a common identifier shape (a ULID, a UUID, a
// slug, a code, a date, an integer), so a 404 is about existence and
// not a 400 about form.
var unknownCandidates = []any{
	"01JZZZZZZZZZZZZZZZZZZZZZZZ",
	"7f3a9c2e-0c0f-4e2d-9b1a-000000000000",
	"conformance-unknown-0",
	"CONFORMANCE-UNKNOWN-0",
	"zz-unknown",
	"ZZZ-UNKNOWN-0",
	"1999-01-01",
	"999999999",
	999999999,
	"x",
}

// UnknownValue is a value of p's schema that names nothing, or false
// when the schema is an enumeration or accepts none of the candidates.
func (c *Contract) UnknownValue(p Param) (string, bool) {
	if p.Schema == nil || p.SchemaPtr == "" {
		return "", false
	}
	if _, ok := p.Schema["enum"]; ok {
		return "", false
	}
	if _, ok := p.Schema["const"]; ok {
		return "", false
	}
	for _, v := range unknownCandidates {
		if c.ValidateValue(p.SchemaPtr, v) == nil {
			return fmt.Sprint(v), true
		}
	}
	return "", false
}

// KnownValue is a value for p that the target accepts as existing: the
// target file's fixture for p's name, else the schema's first enum or
// const value, else the first example the schema accepts. guessed is
// set when the value is not the target's own (an enum value may name a
// resource the target does not hold).
func (c *Contract) KnownValue(p Param, fixtures map[string]string) (value string, guessed, ok bool) {
	if v, found := fixtures[p.Name]; found {
		return v, false, true
	}
	if p.Schema != nil {
		if l, isList := p.Schema["enum"].([]any); isList && len(l) > 0 {
			return fmt.Sprint(l[0]), true, true
		}
		if v, isConst := p.Schema["const"]; isConst {
			return fmt.Sprint(v), true, true
		}
	}
	for _, ex := range p.Examples {
		if p.SchemaPtr == "" || c.ValidateValue(p.SchemaPtr, ex) == nil {
			return fmt.Sprint(ex), true, true
		}
	}
	return "", false, false
}

// InvalidBody is a JSON value the body schema refuses, with what is
// wrong with it: an empty object when the schema requires members, else
// a declared member of the wrong type. False when neither is possible
// (the schema refuses nothing the suite can construct).
func (c *Contract) InvalidBody(md Media) (any, string, bool) {
	if md.SchemaPtr == "" || md.Schema == nil {
		return nil, "", false
	}
	if req, ok := md.Schema["required"].([]any); ok && len(req) > 0 {
		v := map[string]any{}
		if c.ValidateValue(md.SchemaPtr, v) != nil {
			return v, "an empty object (missing " + joinAny(req) + ")", true
		}
	}
	props, _ := md.Schema["properties"].(map[string]any)
	for _, name := range sortedKeys(props) {
		ps := c.Resolve(props[name])
		if ps == nil {
			continue
		}
		var wrong any
		switch t, _ := ps["type"].(string); t {
		case "string":
			wrong = 12345
		case "integer", "number", "boolean":
			wrong = "not-a-" + t
		case "array":
			wrong = "not-an-array"
		case "object":
			wrong = "not-an-object"
		default:
			continue
		}
		v := map[string]any{name: wrong}
		if c.ValidateValue(md.SchemaPtr, v) != nil {
			return v, fmt.Sprintf("member %s of the wrong type", name), true
		}
	}
	return nil, "", false
}

// ExampleBody is the first declared example the schema accepts, else
// false.
func (c *Contract) ExampleBody(md Media) (any, bool) {
	for _, ex := range md.Examples {
		if md.SchemaPtr == "" || c.ValidateValue(md.SchemaPtr, ex) == nil {
			return ex, true
		}
	}
	return nil, false
}

func joinAny(l []any) string {
	s := make([]string, 0, len(l))
	for _, v := range l {
		s = append(s, fmt.Sprint(v))
	}
	return strings.Join(s, ", ")
}

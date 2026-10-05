package national

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Issuers of the machine tokens an operation takes.
const (
	// IssuerEcosystem is the ecosystem token service (the lab issuer in
	// the lab, decision record D4).
	IssuerEcosystem = "ecosystem"
	// IssuerOperator is a system's own issuer of operator client tokens
	// (the USSP's POST /oauth/token).
	IssuerOperator = "operator"
)

// TokenRule is the machine token an operation accepts.
type TokenRule struct {
	Issuer string
	// Alternatives are the scope sets the operation accepts: a token
	// granting every scope of one of them is admitted. Nil when the
	// contract does not state the scope (the overrides file lists the
	// operation as unstated): the checks that need it do not apply.
	Alternatives [][]string
	// MTLS is set when the token must come with a bound client
	// certificate (the ANSP's "+mtls").
	MTLS bool
}

// Scopes is every scope that appears in an alternative.
func (t TokenRule) Scopes() []string {
	seen := map[string]bool{}
	var out []string
	for _, alt := range t.Alternatives {
		for _, s := range alt {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Auth is the credential an operation takes, as the contract states it.
type Auth struct {
	// Public operations take no credential.
	Public bool
	// Token is the machine token the operation accepts, nil for none.
	Token *TokenRule
	// Session is set when a console or portal session is accepted.
	Session bool
	// Special names a credential the suite does not mint (a receiver's
	// bearer key and body HMAC, a compact JWS body).
	Special string
}

// NeedsCredential reports whether a request without any credential
// must be refused.
func (a Auth) NeedsCredential() bool { return !a.Public }

// Param is one path, query or header parameter.
type Param struct {
	Name     string
	In       string
	Required bool
	// SchemaPtr locates the parameter's schema in the document.
	SchemaPtr string
	// Schema is the resolved schema (local $refs followed), nil when it
	// cannot be resolved offline.
	Schema   map[string]any
	Examples []any
}

// Media is one declared content type of a body or a response.
type Media struct {
	SchemaPtr string
	Schema    map[string]any
	Examples  []any
}

// Body is an operation's request body.
type Body struct {
	Required bool
	Content  map[string]Media
}

// Response is one declared response.
type Response struct {
	Content map[string]Media
}

// Operation is one method on one path.
type Operation struct {
	ID        string
	Method    string
	Path      string
	Auth      Auth
	Params    []Param
	Body      *Body
	Responses map[string]Response
	WebSocket bool
}

// Subject is how an outcome names the operation.
func (o *Operation) Subject() string { return o.ID + " " + o.Method + " " + o.Path }

// Declares reports whether the contract declares status code (exactly
// or through "default").
func (o *Operation) Declares(code string) bool {
	_, ok := o.Responses[code]
	return ok
}

// DeclaresOrDefault reports whether code is declared exactly or a
// default response exists.
func (o *Operation) DeclaresOrDefault(code string) bool {
	return o.Declares(code) || o.Declares("default")
}

// Param returns the named parameter.
func (o *Operation) Param(in, name string) (Param, bool) {
	for _, p := range o.Params {
		if p.In == in && strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Param{}, false
}

// Contract is one system's OpenAPI file, classified.
type Contract struct {
	System  string
	Title   string
	Version string
	// SHA256 is the digest of the file as read ("sha256:<hex>").
	SHA256 string
	Ops    []*Operation

	doc      any
	base     string
	compiler *jsonschema.Compiler
	mu       sync.Mutex
	cache    map[string]*jsonschema.Schema
}

// LoadOptions says where the documents an OpenAPI file references live.
type LoadOptions struct {
	// SchemasDir holds the system's schemas/ tree (the aggregate's
	// schemas/<system>/ or the system's own checkout): a relative
	// "../schemas/..." reference resolves below it.
	SchemasDir string
	// CommonDir is the lab's schemas/common/: an https://schemas.uspace.ge/
	// reference resolves below it.
	CommonDir string
}

// MaxContractBytes bounds an OpenAPI file (E-10).
const MaxContractBytes = 8 << 20

// LoadContractFile reads and classifies an OpenAPI file.
func LoadContractFile(system, path string, ov Overrides, opts LoadOptions) (*Contract, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("contract %s: %w", system, err)
	}
	if info.Size() > MaxContractBytes {
		return nil, fmt.Errorf("contract %s: %s is %d bytes, above %d", system, path, info.Size(), MaxContractBytes)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the contract file
	if err != nil {
		return nil, fmt.Errorf("contract %s: %w", system, err)
	}
	return LoadContract(system, b, ov, opts)
}

// LoadContract parses an OpenAPI 3.1 document and classifies every
// operation (fail closed: see the package documentation).
func LoadContract(system string, raw []byte, ov Overrides, opts LoadOptions) (*Contract, error) {
	if ov.System != "" && ov.System != system {
		return nil, fmt.Errorf("contract %s: the overrides are for %s", system, ov.System)
	}
	var y any
	if err := yaml.Unmarshal(raw, &y); err != nil {
		return nil, fmt.Errorf("contract %s: not YAML: %w", system, err)
	}
	j, err := json.Marshal(y)
	if err != nil {
		return nil, fmt.Errorf("contract %s: %w", system, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		return nil, fmt.Errorf("contract %s: %w", system, err)
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("contract %s: not an object", system)
	}
	if v, _ := root["openapi"].(string); !strings.HasPrefix(v, "3.1.") {
		return nil, fmt.Errorf("contract %s: openapi %q is not 3.1.x", system, v)
	}
	sum := sha256.Sum256(raw)
	c := &Contract{System: system, SHA256: "sha256:" + hex.EncodeToString(sum[:]), doc: doc,
		base: "mem://" + system + "/api/openapi.json", cache: map[string]*jsonschema.Schema{}}
	if info, ok := root["info"].(map[string]any); ok {
		c.Title, _ = info["title"].(string)
		c.Version, _ = info["version"].(string)
	}
	c.compiler = jsonschema.NewCompiler()
	c.compiler.DefaultDraft(jsonschema.Draft2020)
	c.compiler.AssertFormat()
	c.compiler.UseLoader(fileLoader{system: system, opts: opts})
	if err := c.compiler.AddResource(c.base, doc); err != nil {
		return nil, fmt.Errorf("contract %s: %w", system, err)
	}
	paths, ok := root["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("contract %s: paths missing", system)
	}
	var problems []string
	for _, p := range sortedKeys(paths) {
		item, _ := paths[p].(map[string]any)
		itemPtr := "#/paths/" + escape(p)
		for _, m := range []string{"get", "put", "post", "patch", "delete", "head", "options"} {
			raw, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			op, err := c.operation(p, m, raw, item, itemPtr+"/"+m, ov)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			c.Ops = append(c.Ops, op)
		}
	}
	for id := range ov.Scopes {
		if c.Op(id) == nil {
			problems = append(problems, fmt.Sprintf("overrides: scopes for %s, which the contract does not have", id))
		}
	}
	for _, id := range ov.Unstated {
		if c.Op(id) == nil {
			problems = append(problems, fmt.Sprintf("overrides: unstated %s, which the contract does not have", id))
		}
	}
	for id := range ov.Skip {
		if c.Op(id) == nil {
			problems = append(problems, fmt.Sprintf("overrides: skip for %s, which the contract does not have", id))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("contract %s: %d operations not classified:\n  %s", system, len(problems), strings.Join(problems, "\n  "))
	}
	if len(c.Ops) == 0 {
		return nil, fmt.Errorf("contract %s: no operations", system)
	}
	return c, nil
}

// Op returns the operation with id, nil when there is none.
func (c *Contract) Op(id string) *Operation {
	for _, o := range c.Ops {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (c *Contract) operation(path, method string, raw, item map[string]any, ptr string, ov Overrides) (*Operation, error) {
	id, _ := raw["operationId"].(string)
	if id == "" {
		return nil, fmt.Errorf("%s %s: no operationId", strings.ToUpper(method), path)
	}
	op := &Operation{ID: id, Method: strings.ToUpper(method), Path: path, Responses: map[string]Response{}}
	op.WebSocket, _ = raw["x-websocket"].(bool)
	// Path-item parameters first; an operation parameter of the same
	// name and location replaces one.
	params := map[string]Param{}
	var order []string
	for _, src := range []struct {
		node any
		ptr  string
	}{{item["parameters"], ptr[:strings.LastIndex(ptr, "/")] + "/parameters"}, {raw["parameters"], ptr + "/parameters"}} {
		list, _ := src.node.([]any)
		for i := range list {
			p, err := c.param(fmt.Sprintf("%s/%d", src.ptr, i))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			k := p.In + ":" + strings.ToLower(p.Name)
			if _, seen := params[k]; !seen {
				order = append(order, k)
			}
			params[k] = p
		}
	}
	for _, k := range order {
		op.Params = append(op.Params, params[k])
	}
	if _, ok := raw["requestBody"]; ok {
		n, bptr := c.deref(ptr + "/requestBody")
		bm, _ := n.(map[string]any)
		req, _ := bm["required"].(bool)
		op.Body = &Body{Required: req, Content: c.content(bm, bptr)}
	}
	resps, _ := raw["responses"].(map[string]any)
	for _, code := range sortedKeys(resps) {
		n, rptr := c.deref(ptr + "/responses/" + escape(code))
		rm, _ := n.(map[string]any)
		op.Responses[code] = Response{Content: c.content(rm, rptr)}
	}
	// A WebSocket is marked x-websocket (the ANSP) or only by its 101
	// Switching Protocols (the authority, the USSP): either way its
	// checks are handshakes, and a plain GET would be refused 426.
	if _, ok := op.Responses["101"]; ok && op.Method == http.MethodGet {
		op.WebSocket = true
	}
	auth, err := c.classify(op, raw, ov)
	if err != nil {
		return nil, fmt.Errorf("%s (%s %s): %w", id, op.Method, path, err)
	}
	op.Auth = auth
	return op, nil
}

func (c *Contract) param(ptr string) (Param, error) {
	n, pptr := c.deref(ptr)
	m, ok := n.(map[string]any)
	if !ok {
		return Param{}, fmt.Errorf("parameter %s is not an object", ptr)
	}
	p := Param{}
	p.Name, _ = m["name"].(string)
	p.In, _ = m["in"].(string)
	p.Required, _ = m["required"].(bool)
	if p.Name == "" || p.In == "" {
		return Param{}, fmt.Errorf("parameter %s has no name or in", ptr)
	}
	if _, ok := m["schema"]; ok {
		p.SchemaPtr = pptr + "/schema"
		p.Schema = c.resolveSchema(p.SchemaPtr)
	}
	p.Examples = examples(m, p.Schema)
	return p, nil
}

func (c *Contract) content(m map[string]any, ptr string) map[string]Media {
	out := map[string]Media{}
	cm, _ := m["content"].(map[string]any)
	for _, ct := range sortedKeys(cm) {
		mm, _ := cm[ct].(map[string]any)
		md := Media{}
		if _, ok := mm["schema"]; ok {
			md.SchemaPtr = ptr + "/content/" + escape(ct) + "/schema"
			md.Schema = c.resolveSchema(md.SchemaPtr)
		}
		md.Examples = examples(mm, md.Schema)
		out[ct] = md
	}
	return out
}

func examples(m, schema map[string]any) []any {
	var out []any
	if v, ok := m["example"]; ok {
		out = append(out, v)
	}
	if ex, ok := m["examples"].(map[string]any); ok {
		for _, k := range sortedKeys(ex) {
			if e, ok := ex[k].(map[string]any); ok {
				if v, ok := e["value"]; ok {
					out = append(out, v)
				}
			}
		}
	}
	if schema != nil {
		if v, ok := schema["example"]; ok {
			out = append(out, v)
		}
		if l, ok := schema["examples"].([]any); ok {
			out = append(out, l...)
		}
	}
	return out
}

// deref follows local $ref chains from ptr and returns the node and its
// pointer.
func (c *Contract) deref(ptr string) (any, string) {
	n := c.at(ptr)
	for range 16 {
		m, ok := n.(map[string]any)
		if !ok {
			return n, ptr
		}
		ref, ok := m["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return n, ptr
		}
		ptr = ref
		n = c.at(ptr)
	}
	return n, ptr
}

// resolveSchema is the schema at ptr with its top-level local $ref
// chain followed; nil for an external or missing one.
func (c *Contract) resolveSchema(ptr string) map[string]any {
	n, _ := c.deref(ptr)
	m, ok := n.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := m["$ref"].(string); ok && !strings.HasPrefix(ref, "#/") {
		return nil
	}
	return m
}

// Resolve follows a local $ref in a schema node (for value generation).
func (c *Contract) Resolve(node any) map[string]any {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	for range 16 {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		if !strings.HasPrefix(ref, "#/") {
			return nil
		}
		next, ok := c.at(ref).(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return nil
}

// at returns the node at a "#/a/b" JSON pointer, nil when absent.
func (c *Contract) at(ptr string) any {
	n := c.doc
	if ptr == "#" || ptr == "#/" {
		return n
	}
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "#/"), "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := n.(type) {
		case map[string]any:
			n = v[tok]
		case []any:
			var i int
			if _, err := fmt.Sscanf(tok, "%d", &i); err != nil || i < 0 || i >= len(v) {
				return nil
			}
			n = v[i]
		default:
			return nil
		}
	}
	return n
}

// Schema compiles the schema at ptr (cached).
func (c *Contract) Schema(ptr string) (*jsonschema.Schema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.cache[ptr]; ok {
		return s, nil
	}
	s, err := c.compiler.Compile(c.base + ptr)
	if err != nil {
		return nil, err
	}
	c.cache[ptr] = s
	return s, nil
}

// ErrNotDeclared is a response status or content type the operation
// does not declare.
var ErrNotDeclared = errors.New("not declared by the contract")

// ValidateResponse checks a response body against what the operation
// declares for code (exactly, else default) and the content type.
func (c *Contract) ValidateResponse(op *Operation, code int, contentType string, body []byte) error {
	r, ok := op.Responses[fmt.Sprint(code)]
	if !ok {
		r, ok = op.Responses[fmt.Sprintf("%dXX", code/100)]
	}
	if !ok {
		r, ok = op.Responses["default"]
	}
	if !ok {
		return fmt.Errorf("status %d: %w", code, ErrNotDeclared)
	}
	if len(r.Content) == 0 {
		if len(bytes.TrimSpace(body)) != 0 {
			return fmt.Errorf("status %d declares no content, got %d bytes", code, len(body))
		}
		return nil
	}
	md, ok := MatchMedia(r.Content, contentType)
	if !ok {
		return fmt.Errorf("status %d content type %q: %w (declared %s)", code, contentType, ErrNotDeclared, strings.Join(sortedKeys(r.Content), ", "))
	}
	if md.SchemaPtr == "" {
		return nil
	}
	if !isJSONMedia(contentType) {
		// A text body (text/plain: Prometheus metrics) is the string the
		// schema describes, not a JSON document.
		return c.ValidateValue(md.SchemaPtr, string(body))
	}
	return c.ValidateJSON(md.SchemaPtr, body)
}

// isJSONMedia reports whether a content type is JSON: application/json
// or a +json structured syntax (application/problem+json).
func isJSONMedia(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.TrimSpace(strings.ToLower(contentType))
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// ValidateJSON validates a JSON document against the schema at ptr.
func (c *Contract) ValidateJSON(ptr string, body []byte) error {
	s, err := c.Schema(ptr)
	if err != nil {
		return fmt.Errorf("schema %s: %w", ptr, err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	return s.Validate(v)
}

// ValidateValue validates a decoded value (jsonschema.UnmarshalJSON
// form, or plain Go values marshalled through JSON) against ptr.
func (c *Contract) ValidateValue(ptr string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.ValidateJSON(ptr, b)
}

// MatchMedia finds the declared media type of contentType: exact, then
// the same type without parameters, then a */* or type/* wildcard.
func MatchMedia(content map[string]Media, contentType string) (Media, bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.TrimSpace(strings.ToLower(contentType))
	}
	if md, ok := content[mt]; ok {
		return md, true
	}
	for k, md := range content {
		if strings.EqualFold(k, mt) {
			return md, true
		}
	}
	if i := strings.Index(mt, "/"); i > 0 {
		if md, ok := content[mt[:i]+"/*"]; ok {
			return md, true
		}
	}
	md, ok := content["*/*"]
	return md, ok
}

// JSONMedia picks the JSON media type of a body: application/json, then
// any +json type, sorted for determinism.
func JSONMedia(content map[string]Media) (string, Media, bool) {
	if md, ok := content["application/json"]; ok {
		return "application/json", md, true
	}
	for _, k := range sortedKeys(content) {
		if strings.HasSuffix(k, "+json") {
			return k, content[k], true
		}
	}
	return "", Media{}, false
}

type fileLoader struct {
	system string
	opts   LoadOptions
}

// Load resolves the two kinds of external reference a system's OpenAPI
// file may hold, offline: its own schemas/ tree and the lab's common
// schemas. Anything else is refused, never fetched.
func (l fileLoader) Load(url string) (any, error) {
	var path string
	switch {
	case strings.HasPrefix(url, "mem://"+l.system+"/schemas/") && l.opts.SchemasDir != "":
		path = filepath.Join(l.opts.SchemasDir, filepath.FromSlash(strings.TrimPrefix(url, "mem://"+l.system+"/schemas/")))
	case strings.HasPrefix(url, "https://schemas.uspace.ge/") && l.opts.CommonDir != "":
		rel := strings.TrimSuffix(strings.TrimPrefix(url, "https://schemas.uspace.ge/"), ".json")
		path = filepath.Join(l.opts.CommonDir, filepath.FromSlash(rel), "schema.json")
	default:
		return nil, fmt.Errorf("offline: %s is outside the system's schemas and the common schemas", url)
	}
	f, err := os.Open(path) //nolint:gosec // below the configured schema directories
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return jsonschema.UnmarshalJSON(f)
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

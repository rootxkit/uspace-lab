package contracts

import (
	"regexp"
	"strings"
)

// Group is one row of spec 02 §3 "API surfaces per system". Row is the
// first cell of the row exactly as the spec writes it; the test in this
// package checks every Row against docs/spec/02-interfaces.md both ways,
// so the table cannot drift from the spec in silence.
type Group struct {
	System string
	Row    string
	// Standard rows are F3411/F3548 endpoints: they are defined by
	// uas_standards, not by the system's national OpenAPI (02 §1
	// "Publication"), so they are listed but not looked for.
	Standard bool
	// Optional rows are marked optional in the spec's Purpose column; a
	// system may omit them, and their absence is reported, not failed.
	Optional bool
}

// EndpointGroups is spec 02 §3, row by row.
var EndpointGroups = []Group{
	{System: "authority", Row: "`/v1/registry/*`"},
	{System: "authority", Row: "`/v1/zones/*`, `/v1/uspace/*`"},
	{System: "authority", Row: "`/v1/certificates/*`"},
	{System: "authority", Row: "`/v1/rid/receivers/*`"},
	{System: "authority", Row: "`/v1/rid/observations`"},
	{System: "authority", Row: "`/v1/rid/frames/*`"},
	{System: "authority", Row: "`/v1/picture/*`"},
	{System: "authority", Row: "`/v1/violations/*`"},
	{System: "authority", Row: "`/v1/incidents/*`"},
	{System: "authority", Row: "`/v1/occurrences/*`"},
	{System: "authority", Row: "`/v1/police/*`"},
	{System: "authority", Row: "`/v1/sources/*`"},
	{System: "authority", Row: "`/v1/audit/*`"},
	{System: "authority", Row: "`/oauth/*`, `/.well-known/jwks.json`"},
	{System: "cisp", Row: "`/v1/publications/*`"},
	{System: "cisp", Row: "`/v1/restrictions/*`"},
	{System: "cisp", Row: "`/v1/zones`, `/v1/uspace_airspace`, `/v1/ussp_list`, `/v1/{dataset}/versions/*`, `/v1/changes`"},
	{System: "cisp", Row: "`/v1/subscriptions/*`"},
	{System: "cisp", Row: "`/v1/stream`"},
	{System: "cisp", Row: "`/public/*`"},
	{System: "ussp", Row: "`/v1/intents/*`"},
	{System: "ussp", Row: "`/v1/telemetry`"},
	{System: "ussp", Row: "`/v1/traffic/*`"},
	{System: "ussp", Row: "`/v1/geo/*`"},
	{System: "ussp", Row: "`/v1/alerts/*`"},
	{System: "ussp", Row: "`/v1/registry/validate`"},
	{System: "ussp", Row: "`GET /uss/flights`, `GET /uss/flights/{id}/details`, `GET`/`POST /uss/identification_service_areas/{id}`", Standard: true},
	{System: "ussp", Row: "`GET /uss/v1/operational_intents/{entityid}`, `GET .../telemetry`, `POST /uss/v1/operational_intents`, `GET /uss/v1/constraints/{entityid}`, `POST /uss/v1/constraints`, `POST /uss/v1/reports`, `GET /uss/v1/log_sets/{log_set_id}`", Standard: true},
	{System: "ussp", Row: "`/v1/records/*`"},
	{System: "ussp", Row: "`/v1/authority/flights`", Optional: true},
	{System: "ussp", Row: "`/v1/weather/*`", Optional: true},
	{System: "ussp", Row: "`/v1/accounts/*`, `/oidc/*`"},
	{System: "ansp", Row: "`/v1/restrictions/*`"},
	{System: "ansp", Row: "`/v1/restriction-requests`"},
	{System: "ansp", Row: "`/v1/manned-traffic/*`"},
	{System: "ansp", Row: "`/v1/coordination/*`"},
	{System: "ansp", Row: "`/v1/adapters/*`"},
}

// GroupsFor returns the rows of one system in spec order.
func GroupsFor(system string) []Group {
	var out []Group
	for _, g := range EndpointGroups {
		if g.System == system {
			out = append(out, g)
		}
	}
	return out
}

var backticked = regexp.MustCompile("`([^`]+)`")

// Patterns are the path patterns of the row: each backticked segment.
func (g Group) Patterns() []string {
	ms := backticked.FindAllStringSubmatch(g.Row, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// Missing returns the row's patterns that match none of paths. A pattern
// ending in `/*` matches its prefix and anything below it; a `{name}`
// segment matches any one segment, templated or literal.
func (g Group) Missing(paths []string) []string {
	var out []string
	for _, pat := range g.Patterns() {
		found := false
		for _, p := range paths {
			if matchGroup(pat, p) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, pat)
		}
	}
	return out
}

func matchGroup(pattern, path string) bool {
	star := strings.HasSuffix(pattern, "/*")
	pattern = strings.TrimSuffix(pattern, "/*")
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(xs) < len(ps) || (!star && len(xs) != len(ps)) {
		return false
	}
	for i, seg := range ps {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != xs[i] {
			return false
		}
	}
	return true
}

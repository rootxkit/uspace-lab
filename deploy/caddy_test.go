package deploy

import (
	"regexp"
	"strings"
	"testing"
)

// caddyRoute is one handle block of a site: the paths of its named
// matcher (none: every path) and what it does.
type caddyRoute struct {
	paths  []string
	action string
}

// caddySites reads the lab Caddyfile into each site's handle blocks in
// order, snippets expanded where they are imported. It reads only what
// that file uses: named path matchers, handle blocks with one
// reverse_proxy or respond each, and imports. Caddy keeps handle blocks
// with named matchers in the order written and runs the first that
// matches; a handle without a matcher sorts last.
func caddySites(t *testing.T) map[string][]caddyRoute {
	t.Helper()
	src := read(t, "systems/Caddyfile")
	blocks := map[string][]string{} // top-level block name -> its lines
	var name string
	depth := 0
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if depth == 0 && strings.HasSuffix(line, "{") {
			name = strings.TrimSpace(strings.TrimSuffix(line, "{"))
		} else if depth > 0 {
			blocks[name] = append(blocks[name], line)
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
	}
	var expand func(lines []string) []string
	expand = func(lines []string) []string {
		var out []string
		for _, l := range lines {
			if s, ok := strings.CutPrefix(l, "import "); ok {
				snip, found := blocks["("+s+")"]
				if !found {
					t.Fatalf("import of unknown snippet %s", s)
				}
				out = append(out, expand(snip)...)
				continue
			}
			out = append(out, l)
		}
		return out
	}
	matcher := regexp.MustCompile(`^(@\w+) path (.+)$`)
	sites := map[string][]caddyRoute{}
	for n, lines := range blocks {
		if !strings.HasPrefix(n, "{$") {
			continue
		}
		lines = expand(lines)
		matchers := map[string][]string{}
		var routes, fallback []caddyRoute
		for i, l := range lines {
			if m := matcher.FindStringSubmatch(l); m != nil {
				matchers[m[1]] = strings.Fields(m[2])
				continue
			}
			if !strings.HasPrefix(l, "handle") || i+1 >= len(lines) {
				continue
			}
			r := caddyRoute{action: lines[i+1]}
			if m := strings.Fields(strings.TrimSuffix(l, "{")); len(m) == 2 {
				p, ok := matchers[m[1]]
				if !ok {
					t.Fatalf("%s: handle with undefined matcher %s", n, m[1])
				}
				r.paths = p
				routes = append(routes, r)
			} else {
				fallback = append(fallback, r)
			}
		}
		sites[n] = append(routes, fallback...)
	}
	return sites
}

// pathMatches is Caddy's path matcher for the forms the file uses: an
// exact path, or a prefix ending in /*.
func pathMatches(pattern, path string) bool {
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(path, p)
	}
	return pattern == path
}

func caddyRouteOf(t *testing.T, sites map[string][]caddyRoute, site, path string) string {
	t.Helper()
	routes, ok := sites[site]
	if !ok {
		t.Fatalf("no site %s in systems/Caddyfile", site)
	}
	for _, r := range routes {
		if r.paths == nil {
			return r.action
		}
		for _, p := range r.paths {
			if pathMatches(p, path) {
				return r.action
			}
		}
	}
	return "no route"
}

// The contracts of the authority and the ANSP declare /healthz, /readyz
// and /metrics as operations, and the conformance suite reaches them
// only through the lab Caddy. The authority serves them on its admin
// listener (ADMIN_ADDR :9090), the ANSP on its api. Before, the
// authority's went to its api (404) and every host's /metrics was
// answered 404 by Caddy itself. Every other host keeps /metrics closed,
// as the droplet does, and the rest of the routing is unchanged.
func TestCaddyRoutesHealthAndMetrics(t *testing.T) {
	sites := caddySites(t)
	for _, c := range []struct{ site, path, want string }{
		{"{$AUTHORITY_HOST}", "/healthz", "reverse_proxy authority-api:9090"},
		{"{$AUTHORITY_HOST}", "/readyz", "reverse_proxy authority-api:9090"},
		{"{$AUTHORITY_HOST}", "/metrics", "reverse_proxy authority-api:9090"},
		{"{$AUTHORITY_HOST}", "/v1/zones", "reverse_proxy authority-api:8080"},
		{"{$AUTHORITY_HOST}", "/v1/rid/observations", "reverse_proxy authority-rid-ingest:8081"},
		{"{$ANSP_HOST}", "/metrics", "reverse_proxy ansp-api:8080"},
		{"{$ANSP_HOST}", "/healthz", "reverse_proxy ansp-api:8080"},
		{"{$ANSP_HOST}", "/readyz", "reverse_proxy ansp-api:8080"},
		{"{$ANSP_HOST}", "/v1/manned-traffic/stream", "reverse_proxy ansp-manned-feed:8080"},
		{"{$CISP_HOST}", "/metrics", "respond 404"},
		{"{$USSP_HOST}", "/metrics", "respond 404"},
		{"{$USSP_HOST}", "/healthz", "reverse_proxy ussp-api:8080"},
		{"{$LAB_HOST}", "/metrics", "respond 404"},
		{"{$LAB_HOST}", "/healthz", "reverse_proxy lab-issuer:8080"},
	} {
		if got := caddyRouteOf(t, sites, c.site, c.path); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.site, c.path, got, c.want)
		}
	}
	// The reader is shown able to see a 404 route ahead of a proxy.
	if !pathMatches("/metrics/*", "/metrics/x") || pathMatches("/metrics", "/metricsx") {
		t.Fatal("pathMatches is wrong")
	}
}

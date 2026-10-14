package deploy

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sourceField returns `name = value` from dss/SOURCE.
func sourceField(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` = (\S+)$`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("dss/SOURCE has no %s", name)
	}
	return m[1]
}

var (
	imageLine = regexp.MustCompile(`(?m)^\s*image:\s*(?:&\w+\s+)?(\S+)\s*$`)
	fromLine  = regexp.MustCompile(`(?m)^FROM\s+(\S+)`)
	digest    = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
)

// Every third-party image is pinned by digest, and the DSS and its
// datastore by exactly the digests dss/SOURCE records. The images
// without a digest are the two built from this repository (the lab
// issuer, the peer USSP), whose base images are pinned. The check is
// shown able to fail on a tag-only line (E-01).
func TestImagesPinnedByDigest(t *testing.T) {
	compose := read(t, "compose.yaml")
	src := read(t, "dss/SOURCE")
	images := imageLine.FindAllStringSubmatch(compose, -1)
	if len(images) < 3 {
		t.Fatalf("found %d image lines in compose.yaml", len(images))
	}
	for _, m := range images {
		img := m[1]
		if strings.HasPrefix(img, "*") || strings.HasPrefix(img, "${LAB_ISSUER_IMAGE") || strings.HasPrefix(img, "${LAB_SIM_USSP_IMAGE") {
			continue
		}
		if !digest.MatchString(img) {
			t.Errorf("compose.yaml: image %s is not pinned by digest", img)
		}
	}
	for _, pair := range [][2]string{{"image", "image_digest"}, {"datastore_image", "datastore_digest"}} {
		ref := strings.TrimPrefix(sourceField(t, src, pair[0]), "docker.io/") + "@" + sourceField(t, src, pair[1])
		if !strings.Contains(compose, "image: "+ref) && !strings.Contains(compose, "image: &dss_image "+ref) {
			t.Errorf("compose.yaml does not use %s from dss/SOURCE", ref)
		}
	}
	if commit := sourceField(t, src, "commit"); !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(commit) {
		t.Errorf("dss/SOURCE commit %q is not a full SHA", commit)
	}
	for _, df := range []string{"issuer/Dockerfile", "sim-ussp/Dockerfile"} {
		froms := fromLine.FindAllStringSubmatch(read(t, df), -1)
		if len(froms) != 2 {
			t.Fatalf("%s: %d FROM lines", df, len(froms))
		}
		for _, m := range froms {
			if !digest.MatchString(m[1]) {
				t.Errorf("%s: %s is not pinned by digest", df, m[1])
			}
		}
	}
	if digest.MatchString("cockroachdb/cockroach:v24.1.3") {
		t.Fatal("the digest check accepts a tag-only image")
	}
}

var varRef = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)`)

// Every variable compose.yaml reads is listed, with a comment, in
// .env.example; LAB_UID and LAB_GID are set by dss-up.sh, not the env
// file. And nothing in .env.example is unused.
func TestEnvExampleListsEveryVariable(t *testing.T) {
	compose := read(t, "compose.yaml")
	env := read(t, ".env.example")
	used := map[string]bool{}
	for _, m := range varRef.FindAllStringSubmatch(compose, -1) {
		used[m[1]] = true
	}
	setByScript := map[string]bool{"LAB_UID": true, "LAB_GID": true}
	listed := map[string]bool{}
	for _, line := range strings.Split(env, "\n") {
		if name, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			listed[name] = true
		}
	}
	var missing, unused []string
	for v := range used {
		if !listed[v] && !setByScript[v] {
			missing = append(missing, v)
		}
	}
	for v := range listed {
		if !used[v] {
			unused = append(unused, v)
		}
	}
	sort.Strings(missing)
	sort.Strings(unused)
	if len(missing) > 0 || len(unused) > 0 {
		t.Fatalf(".env.example: missing %v, unused %v", missing, unused)
	}
	if len(used) < 10 {
		t.Fatalf("only %d variables found in compose.yaml", len(used))
	}
}

// The DSS logs no bearer token: core-service at its default level
// (info) logs each request's headers, Authorization included, even with
// -dump_requests off (found in the WP-L6 systems stack: 387 request
// lines carrying a lab issuer token). It runs at warn, without
// -dump_requests.
func TestDSSLogsNoBearerTokens(t *testing.T) {
	compose := read(t, "compose.yaml")
	if !strings.Contains(compose, "- -log_level=warn") {
		t.Error("the DSS runs at its default log level, which logs every bearer token")
	}
	if regexp.MustCompile(`(?m)^\s*-\s*-dump_requests`).MatchString(compose) {
		t.Error("the DSS dumps its requests")
	}
}

// Nothing is published: no ports: key in compose.yaml (WP-L2: one
// isolated network; on the droplet only Caddy publishes), and no
// absolute host path in a volume (the file is consumed as an include).
func TestNothingPublishedNoAbsolutePaths(t *testing.T) {
	compose := read(t, "compose.yaml")
	if regexp.MustCompile(`(?m)^\s*ports:`).MatchString(compose) {
		t.Error("compose.yaml publishes a port")
	}
	for _, m := range regexp.MustCompile(`(?m)^\s*-\s*(\S+):/`).FindAllStringSubmatch(compose, -1) {
		if strings.HasPrefix(m[1], "/") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(m[1]) {
			t.Errorf("absolute host path %s", m[1])
		}
	}
}

// The systems profile takes every image from demo.env, and demo.env
// pins each by digest except the ANSP's, which is built locally from a
// named commit (it publishes none). A literal image in the compose file
// or a tag-only reference in demo.env fails (shown with a tag below).
func TestSystemsImagesFromTheEnvByDigest(t *testing.T) {
	compose := read(t, "systems/compose.yaml")
	for _, m := range imageLine.FindAllStringSubmatch(compose, -1) {
		if !strings.HasPrefix(m[1], "${") {
			t.Errorf("systems/compose.yaml names image %s literally", m[1])
		}
	}
	env := read(t, "demo.env.example")
	n := 0
	for _, line := range strings.Split(env, "\n") {
		name, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") || !strings.HasSuffix(name, "_IMAGE") {
			continue
		}
		n++
		local := name == "ANSP_GO_IMAGE" || name == "LAB_ISSUER_IMAGE" || name == "LAB_SIM_USSP_IMAGE"
		if !local && !digest.MatchString(v) {
			t.Errorf("demo.env.example %s=%s is not pinned by digest", name, v)
		}
	}
	if n < 8 {
		t.Fatalf("only %d images in demo.env.example", n)
	}
	if digest.MatchString("caddy:2.10.2-alpine") {
		t.Fatal("the digest check accepts a tag")
	}
	if !strings.Contains(env, "ANSP_SOURCE_COMMIT=") {
		t.Error("the locally built ANSP image names no source commit")
	}
}

// Every system takes the address X-Forwarded-For names from the lab
// Caddy only, as on the droplet (uspace-deploy compose/*.yaml trust their
// edge Caddy's address): the lab Caddy has a fixed address on a fixed
// lab subnet from demo.env, and every *_TRUSTED_PROXIES (the CISP's
// _CIDR) is exactly that address. With them empty every client of a
// system was the Caddy, one rate-limit budget and one audit address for
// all: seeding a 100-aircraft load fleet was refused 429 part of the
// way through, and the USSP's registry checks of its intents with it.
// The check is shown able to fail on an empty value (E-01).
func TestSystemsTrustTheLabCaddyOnly(t *testing.T) {
	compose := read(t, "systems/compose.yaml")
	env := read(t, "demo.env.example")
	subnet := regexp.MustCompile(`(?m)^DEMO_LAB_SUBNET=(\d+\.\d+\.\d+)\.0/24$`).FindStringSubmatch(env)
	ipRange := regexp.MustCompile(`(?m)^DEMO_LAB_IP_RANGE=(\d+\.\d+\.\d+)\.0/25$`).FindStringSubmatch(env)
	ip := regexp.MustCompile(`(?m)^DEMO_CADDY_IP=(\d+\.\d+\.\d+)\.(\d+)$`).FindStringSubmatch(env)
	// The Caddy's address is in the subnet and outside the range Docker
	// hands out (the lower half): the first stack up with it at .2
	// failed "Address already in use", the DSS's datastore had it.
	if subnet == nil || ipRange == nil || ip == nil || ip[1] != subnet[1] || ipRange[1] != subnet[1] || !hostAbove(ip[2], 128) {
		t.Fatalf("demo.env.example: DEMO_LAB_SUBNET %v, DEMO_LAB_IP_RANGE %v, DEMO_CADDY_IP %v: not an address in the /24 above the /25", subnet, ipRange, ip)
	}
	if !strings.Contains(compose, "subnet: ${DEMO_LAB_SUBNET:?deploy/demo.env}") ||
		!strings.Contains(compose, "ip_range: ${DEMO_LAB_IP_RANGE:?deploy/demo.env}") ||
		!strings.Contains(compose, "ipv4_address: ${DEMO_CADDY_IP:?deploy/demo.env}") {
		t.Fatal("systems/compose.yaml does not place the lab Caddy at DEMO_CADDY_IP on DEMO_LAB_SUBNET")
	}
	trusted := regexp.MustCompile(`(?m)^\s*(\w+_TRUSTED_PROX\w*):\s*(.*)$`)
	want := `"${DEMO_CADDY_IP}/32"`
	// The authority trusts the host's address on the egress network too:
	// the load harness gives each simulated receiver its own address
	// through it (TestReceiversHaveTheirOwnAddress).
	wantFor := map[string]string{"AUTHORITY_TRUSTED_PROXIES": `"${DEMO_CADDY_IP}/32,${DEMO_HOST_GATEWAY}/32"`}
	seen := map[string]bool{}
	for _, m := range trusted.FindAllStringSubmatch(compose, -1) {
		seen[m[1]] = true
		w := want
		if v, ok := wantFor[m[1]]; ok {
			w = v
		}
		if strings.TrimSpace(m[2]) != w {
			t.Errorf("systems/compose.yaml %s: %s, want %s", m[1], m[2], w)
		}
	}
	for _, k := range []string{"AUTHORITY_TRUSTED_PROXIES", "CISP_TRUSTED_PROXY_CIDR", "USSP_TRUSTED_PROXIES", "ANSP_TRUSTED_PROXIES"} {
		if !seen[k] {
			t.Errorf("systems/compose.yaml sets no %s", k)
		}
	}
	if m := trusted.FindStringSubmatch(`  USSP_TRUSTED_PROXIES: ""`); m == nil || strings.TrimSpace(m[2]) == want {
		t.Fatal("the check accepts an empty value")
	}
}

// hostAbove is whether the last octet n is in [lo, 254].
func hostAbove(n string, lo int) bool {
	v, err := strconv.Atoi(n)
	return err == nil && v >= lo && v <= 254
}

// In the lab every client reaches the stack from one host, so every
// simulated receiver of a load run had one address: the authority's
// per-address request budget (20 a second) refused part of what four
// receivers posting every 100 ms sent (586 observations, 440 requests,
// at the ci tier), which ten real receivers on their own addresses
// would not see. The host's address on the egress network is fixed
// (DEMO_HOST_GATEWAY, the network's gateway, where Docker's published
// port comes from); the lab Caddy trusts X-Forwarded-For from it only,
// and the authority trusts it beside the Caddy, so a receiver that
// names its own address is judged under it. Nothing else changes for a
// client that names none.
func TestReceiversHaveTheirOwnAddress(t *testing.T) {
	compose := read(t, "systems/compose.yaml")
	env := read(t, "demo.env.example")
	caddy := read(t, "systems/Caddyfile")
	subnet := regexp.MustCompile(`(?m)^DEMO_EGRESS_SUBNET=(\d+\.\d+\.\d+)\.0/24$`).FindStringSubmatch(env)
	gw := regexp.MustCompile(`(?m)^DEMO_HOST_GATEWAY=(\d+\.\d+\.\d+)\.1$`).FindStringSubmatch(env)
	lab := regexp.MustCompile(`(?m)^DEMO_LAB_SUBNET=(\d+\.\d+\.\d+)\.0/24$`).FindStringSubmatch(env)
	if subnet == nil || gw == nil || gw[1] != subnet[1] || lab == nil || lab[1] == subnet[1] {
		t.Fatalf("demo.env.example: DEMO_EGRESS_SUBNET %v, DEMO_HOST_GATEWAY %v (the .1 of it, apart from the lab subnet %v)", subnet, gw, lab)
	}
	for _, want := range []string{"subnet: ${DEMO_EGRESS_SUBNET:?deploy/demo.env}", "gateway: ${DEMO_HOST_GATEWAY:?deploy/demo.env}",
		"LAB_HOST_GATEWAY: ${DEMO_HOST_GATEWAY:?deploy/demo.env}"} {
		if !strings.Contains(compose, want) {
			t.Errorf("systems/compose.yaml lacks %q", want)
		}
	}
	global, _, _ := strings.Cut(caddy, "(lab_tls)")
	if !regexp.MustCompile(`(?m)^\s*trusted_proxies static \{\$LAB_HOST_GATEWAY\}/32\s*$`).MatchString(global) {
		t.Error("the lab Caddy's global options do not trust X-Forwarded-For from {$LAB_HOST_GATEWAY}/32 only")
	}
	if strings.Count(caddy, "trusted_proxies") != 1 {
		t.Error("the Caddyfile trusts a proxy somewhere else too")
	}
}

package qualifier

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// The containers that call a system under test over the lab's TLS trust
// the lab CA: uss_qualifier itself queries a Service Provider's
// /uss/flights at the https URL the DSS names, and the mock USS as
// Display Provider does the same. Without the CA every such query failed
// SSLCertVerificationError (uspace-ussp WP-21, second run: "Successful
// flight query" failed 8 times, "Missing flight" 22). The CA file is
// QUALIFIER_CA_FILE, required (fail closed: a run that cannot verify the
// target would only report it unreachable), mounted read-only, and
// python-requests is pointed at it with REQUESTS_CA_BUNDLE.
func TestQualifierContainersTrustTheLabCA(t *testing.T) {
	b, err := os.ReadFile("../uss_qualifier/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]struct {
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Services) != 3 {
		t.Fatalf("%d mock USS services", len(c.Services))
	}
	for name, s := range c.Services {
		if !slices.Contains(s.Volumes, "${QUALIFIER_CA_FILE:?set QUALIFIER_CA_FILE to the lab CA certificate}:/etc/lab-ca/ca.pem:ro") {
			t.Errorf("%s does not mount QUALIFIER_CA_FILE read-only: %v", name, s.Volumes)
		}
		if s.Environment["REQUESTS_CA_BUNDLE"] != "/etc/lab-ca/ca.pem" {
			t.Errorf("%s: REQUESTS_CA_BUNDLE %q", name, s.Environment["REQUESTS_CA_BUNDLE"])
		}
	}
	r, err := os.ReadFile("../uss_qualifier/run-qualifier.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`[ -r "${QUALIFIER_CA_FILE:?`,
		`-v "$(hostpath "$QUALIFIER_CA_FILE"):/etc/lab-ca/ca.pem:ro"`,
		`-e REQUESTS_CA_BUNDLE=/etc/lab-ca/ca.pem`,
	} {
		if !strings.Contains(string(r), want) {
			t.Errorf("run-qualifier.sh has no %s", want)
		}
	}
}

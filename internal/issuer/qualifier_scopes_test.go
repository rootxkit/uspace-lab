package issuer

import (
	"os"
	"slices"
	"testing"

	"github.com/goccy/go-yaml"
)

// qualifierNeeds are the scopes uss_qualifier and the mock USS at the
// commit conformance/uss_qualifier/SOURCE pins (v0.36.0) request from
// the lab issuer, as observed: the automated-testing interfaces
// (uas_standards.interuss.automated_testing.*.constants.Scope) and
// interuss.versioning.read_system_versions, which the F3548 DSS resource
// (resources.astm.f3548.v21.DSSInstanceResource) requires of its auth
// adapter. Without the last, F3548-SCD stops before any scenario with
// "AuthAdapterResource ... is not declared ... as authorized to obtain
// scope interuss.versioning.read_system_versions" (uspace-ussp
// docs/RUNBOOKS/WP-21.md, run of 2026-10-13).
var qualifierNeeds = []string{
	"rid.inject_test_data",
	"dss.read.identification_service_areas",
	"interuss.flight_planning.direct_automated_test",
	"interuss.flight_planning.plan",
	"interuss.versioning.read_system_versions",
}

// The qualifier's auth adapter declares every scope it needs, and every
// scope it declares is one the lab issuer issues to lab-01 (the client
// whose secret run-qualifier.sh hands it) and to no other client when it
// is lab only. A declared scope the issuer refuses would fail the run
// at its first token; a needed scope not declared fails it before any
// scenario.
func TestQualifierScopesIssuedToLab(t *testing.T) {
	b, err := os.ReadFile("../../conformance/uss_qualifier/library/environment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		UTMAuth struct {
			Specification struct {
				ScopesAuthorized []string `yaml:"scopes_authorized"`
			} `yaml:"specification"`
		} `yaml:"utm_auth"`
	}
	if err := yaml.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	declared := env.UTMAuth.Specification.ScopesAuthorized
	if len(declared) == 0 {
		t.Fatal("environment.yaml utm_auth declares no scope")
	}
	for _, s := range qualifierNeeds {
		if !slices.Contains(declared, s) {
			t.Errorf("environment.yaml utm_auth.scopes_authorized lacks %s", s)
		}
	}
	r := labRegistry(t)
	lab, _ := r.Client(LabClientID)
	for _, s := range declared {
		info, ok := catalogue[s]
		switch {
		case !ok:
			t.Errorf("%s is declared to uss_qualifier but not in the issuer's catalogue", s)
		case info.reserved:
			t.Errorf("%s is declared to uss_qualifier but reserved (never issued)", s)
		case !slices.Contains(lab.Scopes, s):
			t.Errorf("%s is declared to uss_qualifier but deploy/issuer/clients.yaml does not grant it to lab-01", s)
		}
	}
	for _, id := range r.IDs() {
		c, _ := r.Client(id)
		for _, s := range qualifierNeeds {
			if id != LabClientID && slices.Contains(c.Scopes, s) {
				t.Errorf("%s holds the lab-only %s", id, s)
			}
		}
	}
}

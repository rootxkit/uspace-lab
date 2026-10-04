// Package policy reads conformance/policy.yaml: the defaults the suite
// applies until GCAA answers L-Q3, L-Q10 and L-Q12 (docs/PLAN.md §7.1),
// each marked "pending GCAA" in the file and printed in every report.
package policy

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// Format names the file shape.
const Format = "conformance-policy/v1"

// Pending is one setting with its status line ("pending GCAA (...)").
type Pending struct {
	Status string `yaml:"status" json:"status"`
}

// Policy is conformance/policy.yaml.
type Policy struct {
	Format        string `yaml:"format" json:"format"`
	PolicyVersion int    `yaml:"policy_version" json:"policy_version"`
	Informative   struct {
		Pending      `yaml:",inline"`
		Requirements []string `yaml:"requirements" json:"requirements"`
	} `yaml:"informative" json:"informative"`
	ReportSignature struct {
		Pending `yaml:",inline"`
		Alg     string `yaml:"alg" json:"alg"`
		Form    string `yaml:"form" json:"form"`
	} `yaml:"report_signature" json:"report_signature"`
	Accessibility struct {
		Pending  `yaml:",inline"`
		Standard string   `yaml:"standard" json:"standard"`
		AxeTags  []string `yaml:"axe_tags" json:"axe_tags"`
	} `yaml:"accessibility" json:"accessibility"`
	CISP struct {
		Pending             `yaml:",inline"`
		ChangeNotificationS float64 `yaml:"change_notification_s" json:"change_notification_s"`
		HeartbeatIntervalS  float64 `yaml:"heartbeat_interval_s" json:"heartbeat_interval_s"`
		StaleToleranceS     float64 `yaml:"stale_tolerance_s" json:"stale_tolerance_s"`
	} `yaml:"cisp" json:"cisp"`
	PersonalDataFields struct {
		Pending `yaml:",inline"`
		Names   []string `yaml:"names" json:"names"`
	} `yaml:"personal_data_fields" json:"personal_data_fields"`
	ED318Fixture struct {
		CentreLatDeg float64 `yaml:"centre_lat_deg" json:"centre_lat_deg"`
		CentreLonDeg float64 `yaml:"centre_lon_deg" json:"centre_lon_deg"`
		HalfSideM    float64 `yaml:"half_side_m" json:"half_side_m"`
		UpperAGLM    float64 `yaml:"upper_agl_m" json:"upper_agl_m"`
		UpperAMSLFt  float64 `yaml:"upper_amsl_ft" json:"upper_amsl_ft"`
	} `yaml:"ed318_fixture" json:"ed318_fixture"`
}

// Load reads and checks the policy file. Every pending setting must say
// it is pending: a value presented as decided would be an inference
// reported as a decision (E-04).
func Load(path string) (*Policy, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the suite's own configuration
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	var p Policy
	if err := yaml.UnmarshalWithOptions(b, &p, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	if p.Format != Format {
		return nil, fmt.Errorf("policy %s: format %q, want %s", path, p.Format, Format)
	}
	if p.PolicyVersion < 1 {
		return nil, fmt.Errorf("policy %s: policy_version must be at least 1", path)
	}
	for name, st := range map[string]string{
		"informative": p.Informative.Status, "report_signature": p.ReportSignature.Status,
		"accessibility": p.Accessibility.Status, "cisp": p.CISP.Status, "personal_data_fields": p.PersonalDataFields.Status,
	} {
		if !strings.HasPrefix(st, "pending GCAA") {
			return nil, fmt.Errorf("policy %s: %s.status %q does not start with \"pending GCAA\"", path, name, st)
		}
	}
	if p.CISP.ChangeNotificationS <= 0 || p.CISP.HeartbeatIntervalS <= 0 || p.CISP.StaleToleranceS < 0 {
		return nil, fmt.Errorf("policy %s: cisp timings must be positive", path)
	}
	if len(p.PersonalDataFields.Names) == 0 {
		return nil, fmt.Errorf("policy %s: personal_data_fields.names is empty", path)
	}
	f := p.ED318Fixture
	if f.CentreLatDeg < -85 || f.CentreLatDeg > 85 || f.CentreLonDeg < -180 || f.CentreLonDeg > 180 || f.HalfSideM <= 0 || f.UpperAGLM <= 0 || f.UpperAMSLFt <= 0 {
		return nil, fmt.Errorf("policy %s: ed318_fixture is out of range", path)
	}
	return &p, nil
}

// IsInformative reports whether a requirement is reported only.
func (p *Policy) IsInformative(id string) bool {
	for _, r := range p.Informative.Requirements {
		if r == id {
			return true
		}
	}
	return false
}

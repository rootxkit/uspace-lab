package scenario

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/alerting"
	"github.com/rootxkit/uspace-core/core"
	"github.com/rootxkit/uspace-core/cpa"
	"github.com/rootxkit/uspace-core/zones"
)

// Policy is the demo policy file a scenario loads and prints
// (scenarios/policy/demo.yaml, docs/PLAN.md §7.1 L-Q4). The systems under
// test run their own policy; the file says what the scenario's
// expectations assume, and the reference target judges with it. Every
// figure is data (INV-03): nothing here has a default.
type Policy struct {
	PolicyVersion int    `yaml:"policy_version" json:"policy_version"`
	Source        string `yaml:"source" json:"source"`
	Deviation     struct {
		HM float64 `yaml:"h_m" json:"h_m"`
		VM float64 `yaml:"v_m" json:"v_m"`
		TS float64 `yaml:"t_s" json:"t_s"`
	} `yaml:"deviation" json:"deviation"`
	TelemetryLostS              float64 `yaml:"telemetry_lost_s" json:"telemetry_lost_s"`
	LostLinkS                   float64 `yaml:"lost_link_s" json:"lost_link_s"`
	NonconformanceNearbyRadiusM float64 `yaml:"nonconformance_nearby_radius_m" json:"nonconformance_nearby_radius_m"`
	CPA                         struct {
		TCPAMaxS         float64 `yaml:"t_cpa_max_s" json:"t_cpa_max_s"`
		DHorizontalMinM  float64 `yaml:"d_horizontal_min_m" json:"d_horizontal_min_m"`
		DVerticalMinM    float64 `yaml:"d_vertical_min_m" json:"d_vertical_min_m"`
		NeighbourRadiusM float64 `yaml:"neighbour_radius_m" json:"neighbour_radius_m"`
		NeighbourMaxAgeS float64 `yaml:"neighbour_max_age_s" json:"neighbour_max_age_s"`
	} `yaml:"cpa" json:"cpa"`
	HeightLimitAGLM float64 `yaml:"height_limit_agl_m" json:"height_limit_agl_m"`
	Monitor         struct {
		ClearAfterS          float64 `yaml:"clear_after_s" json:"clear_after_s"`
		StaleAfterS          float64 `yaml:"stale_after_s" json:"stale_after_s"`
		LiveMaxAgeS          float64 `yaml:"live_max_age_s" json:"live_max_age_s"`
		PressureUncertaintyM float64 `yaml:"pressure_uncertainty_m" json:"pressure_uncertainty_m"`
		ConditionalSeverity  string  `yaml:"conditional_severity" json:"conditional_severity"`
	} `yaml:"monitor" json:"monitor"`
}

// LoadPolicy reads and checks a policy file.
func LoadPolicy(path string) (*Policy, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is the scenario's own reference, read by the operator's runner
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	var p Policy
	if err := yaml.UnmarshalWithOptions(b, &p, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	return &p, nil
}

// Validate refuses a missing version and any figure that is missing,
// zero or negative where the judgement needs it positive (E-15).
func (p *Policy) Validate() error {
	if p.PolicyVersion < 1 {
		return core.Fieldf("policy_version", "missing (a positive integer, as alert/v1 and violation/v1 carry it)")
	}
	positive := map[string]float64{
		"deviation.h_m": p.Deviation.HM, "deviation.v_m": p.Deviation.VM, "deviation.t_s": p.Deviation.TS,
		"telemetry_lost_s": p.TelemetryLostS, "lost_link_s": p.LostLinkS,
		"nonconformance_nearby_radius_m": p.NonconformanceNearbyRadiusM,
		"cpa.t_cpa_max_s":                p.CPA.TCPAMaxS, "cpa.d_horizontal_min_m": p.CPA.DHorizontalMinM,
		"cpa.d_vertical_min_m": p.CPA.DVerticalMinM, "cpa.neighbour_radius_m": p.CPA.NeighbourRadiusM,
		"cpa.neighbour_max_age_s": p.CPA.NeighbourMaxAgeS, "height_limit_agl_m": p.HeightLimitAGLM,
		"monitor.clear_after_s": p.Monitor.ClearAfterS, "monitor.stale_after_s": p.Monitor.StaleAfterS,
		"monitor.live_max_age_s": p.Monitor.LiveMaxAgeS, "monitor.pressure_uncertainty_m": p.Monitor.PressureUncertaintyM,
	}
	for name, v := range positive {
		if !core.IsFinite(v) || v <= 0 {
			return core.Fieldf(name, "must be a positive number, got %v", v)
		}
	}
	switch core.Severity(p.Monitor.ConditionalSeverity) {
	case core.SeverityInfo, core.SeverityWarning:
	case core.SeverityCritical:
		return core.Fieldf("monitor.conditional_severity", "a CONDITIONAL zone never raises critical (Z-10)")
	default:
		return core.Fieldf("monitor.conditional_severity", "%q is not info or warning", p.Monitor.ConditionalSeverity)
	}
	return nil
}

// AlertingConfig is the policy as uspace-core's alerting.Config, for the
// reference target. heightLimit says whether this monitor judges the
// height limit (the authority does, spec 09 §2).
func (p *Policy) AlertingConfig(heightLimit, skipConflicts bool) alerting.Config {
	c := alerting.DefaultConfig()
	c.Policy = cpa.Policy{
		TCPAMaxS:         p.CPA.TCPAMaxS,
		DHorizontalMinM:  p.CPA.DHorizontalMinM,
		DVerticalMinM:    p.CPA.DVerticalMinM,
		NeighbourRadiusM: p.CPA.NeighbourRadiusM,
		NeighbourMaxAgeS: p.CPA.NeighbourMaxAgeS,
	}
	c.ClearAfterS = p.Monitor.ClearAfterS
	c.StaleAfterS = p.Monitor.StaleAfterS
	c.LiveMaxAgeS = p.Monitor.LiveMaxAgeS
	c.ZonePolicy = zones.Policy{
		PressureUncertaintyM: p.Monitor.PressureUncertaintyM,
		ConditionalSeverity:  core.Severity(p.Monitor.ConditionalSeverity),
	}
	if heightLimit {
		h := p.HeightLimitAGLM
		c.ZonePolicy.MaxHeightAGLM = &h
	}
	c.SkipConflicts = skipConflicts
	return c
}

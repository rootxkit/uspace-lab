package issuer

import "sort"

// Kind says which audience rule a scope follows (decision record M18).
type Kind int

const (
	// National scopes may be requested only for an audience on the
	// client's allowed list.
	National Kind = iota
	// Standard scopes (F3548 utm.*, F3411 rid.*) may be requested for any
	// audience: peers are discovered through the DSS, not configured
	// (spec 00 §7).
	Standard
)

// LabClientID is the one client that may hold the lab-only scopes.
const LabClientID = "lab-01"

// scopeInfo is one row of the catalogue.
type scopeInfo struct {
	kind Kind
	// labOnly scopes are issued to LabClientID and to nobody else
	// (decision record M23: dp.observe).
	labOnly bool
	// reserved scopes are in the catalogue but never issued
	// (cis.publish:ats_data until the Annex V SLA names the items).
	reserved bool
}

// catalogue is the ecosystem scope catalogue of decision record
// Appendix B (spec 06 §3 plus M23), as uspace-authority WP-2 table B
// holds it. The USSP-issuer scopes (ussp.intents, ussp.telemetry,
// ussp.traffic, ussp.geo) are not here: they are issued by a USSP's own
// issuer, never by the ecosystem token service. rid.observe is retired
// (receivers use a bearer key and an HMAC).
var catalogue = map[string]scopeInfo{
	"cis.read":                 {kind: National},
	"cis.publish:zones":        {kind: National},
	"cis.publish:uspace":       {kind: National},
	"cis.publish:ussp_list":    {kind: National},
	"cis.publish:restrictions": {kind: National},
	"cis.publish:ats_data":     {kind: National, reserved: true},
	"registry.validate":        {kind: National},
	"ussp.records":             {kind: National},
	"occurrences.write":        {kind: National},
	"certificates.status":      {kind: National},
	"police.query":             {kind: National},
	"ansp.traffic":             {kind: National},
	"ansp.coordination":        {kind: National},
	"ansp.requests":            {kind: National},
	"dp.observe":               {kind: National, labOnly: true},

	"utm.strategic_coordination":    {kind: Standard},
	"utm.constraint_processing":     {kind: Standard},
	"utm.constraint_management":     {kind: Standard},
	"utm.conformance_monitoring_sa": {kind: Standard},
	"utm.availability_arbitration":  {kind: Standard},
	"rid.service_provider":          {kind: Standard},
	"rid.display_provider":          {kind: Standard},

	// The InterUSS automated-testing interfaces uss_qualifier drives:
	// rid v1 injection and observation, flight_planning v1
	// (uas_standards.interuss.automated_testing.*.constants.Scope at the
	// commit conformance/uss_qualifier/SOURCE pins). Lab only: they exist
	// to test a system, never to operate one (WP-L7).
	"rid.inject_test_data":                           {kind: Standard, labOnly: true},
	"dss.read.identification_service_areas":          {kind: Standard, labOnly: true},
	"interuss.flight_planning.direct_automated_test": {kind: Standard, labOnly: true},
	"interuss.flight_planning.plan":                  {kind: Standard, labOnly: true},
}

// ScopeKind returns the audience rule of scope and whether scope is in
// the catalogue.
func ScopeKind(scope string) (Kind, bool) {
	s, ok := catalogue[scope]
	return s.kind, ok
}

// IssuableScopes returns every catalogue scope that may be issued to
// some client (reserved scopes excluded), sorted.
func IssuableScopes() []string {
	out := make([]string, 0, len(catalogue))
	for name, s := range catalogue {
		if !s.reserved {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

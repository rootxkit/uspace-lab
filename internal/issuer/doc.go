// Package issuer is the lab token service (docs/WORKPACKAGES/WP-L2.md):
// uspace-core's auth.Issuer behind POST /oauth/token and
// /.well-known/jwks.json, with the client list of deploy/issuer/
// clients.yaml, the scope catalogue of decision record Appendix B and the
// audience rule of M18 (aud = the host of the target; national scopes
// only for a client's allowed audiences, utm.* and rid.* for any). It
// stands in for the authority's token service until A-M4 and holds no key
// or secret in the repository: both are generated at run time into a
// git-ignored state directory (spec 06 §4).
//
// Probe proves a running DSS and issuer together for make dss-up.
package issuer

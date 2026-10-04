// Package national is the national contract test of the conformance
// suite (docs/WORKPACKAGES/WP-L7.md, spec 00 §7): it reads a system's
// OpenAPI file (api/<system>/openapi.yaml of the aggregate, or the
// system's own copy), classifies every operation by the credential it
// takes, and exercises each operation against the system under test
// both ways (LESSONS E-01): the refusals the contract declares (401
// without a credential, 403 on a wrong scope, 404 on an unknown id,
// 409 or 412 on a stale If-Match, 400 or 422 with a problem/v1 body and
// errors[] on an invalid body) and the success case with a
// schema-validated response.
//
// Classification fails closed: an operation whose credential the
// contract does not state, and that no conformance/national/contracts/
// <system>.yaml entry names, is an error, never a skipped operation.
// What the suite cannot exercise (a console session it was not given,
// an existing resource the target file does not name) is reported as
// not applicable with the reason, never as a pass.
//
// Nothing here writes to a system except the requests a check names,
// and every request is bounded: one attempt (two more on a 429 with a
// short Retry-After for requests that are refused before any effect or
// are reads), a timeout, a capped body.
package national

// Package contracts holds the checks behind the KT-2 contracts aggregate
// (docs/WORKPACKAGES/WP-L1.md): loading and compiling the schemas under
// schemas/common/, validating their examples in both directions (LESSONS
// E-01), reading the mirror SOURCE files, parsing mirrored OpenAPI files,
// the endpoint groups of spec 02 §3, the generated api/index.md, and the
// enumerations of uspace-core/core that the common schemas pin.
//
// Everything here is offline. The online half (fetching a mirror at its
// pinned commit) lives in scripts/pin.sh and scripts/check-mirrors.sh.
package contracts

// Package deploy holds no code. It exists so that `go test ./...` runs
// deploy_test.go, which checks the lab stack's files against each other:
// every image pinned by digest, the DSS digests equal to dss/SOURCE, and
// every variable compose.yaml reads listed in .env.example
// (docs/WORKPACKAGES/WP-L2.md).
package deploy

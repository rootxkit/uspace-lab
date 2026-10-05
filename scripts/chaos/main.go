// Command chaos runs the WP-L9 failure-domain matrix against the lab's
// systems stack (docs/RUNBOOKS/chaos.md, make chaos):
//
//	go run ./scripts/chaos check  [--matrix scripts/chaos/matrix.yaml]
//	go run ./scripts/chaos run    [--matrix ...] [--env deploy/demo.env] [--targets targets/local-demo.yaml]
//	                              [--rows id,id] [--out results/<run>] [--no-background]
//	go run ./scripts/chaos watch  [--env ...] [--targets ...] [--for 60s]
//	go run ./scripts/chaos skew   --skew-s 45 [--env ...] [--targets ...]
//
// run takes every row of the matrix in turn: it waits until every system
// is ready, injects the row's fault with scripts/chaos/<domain>.sh,
// checks independently that the fault is in place (a row whose fault was
// never observed fails, whatever the systems said), samples every
// system's readiness and console status through the hold, restores, and
// samples until the row's recovery bound. Meanwhile the background
// scenario (scripts/chaos/background.yaml) holds one zone alert open in
// the USSP and the authority; a second raise or a clear during any row
// is that row's finding. It writes results/<run>/chaos.json and prints
// what it observed, never what it expected (LESSONS E-04).
//
// Exit status: 0 every row passed; 1 a row failed or a fault was not
// observed; 2 usage or configuration error (nothing was injected); 3 the
// stack was not ready before the first row (nothing was injected).
package main

import (
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

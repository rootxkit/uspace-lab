package geoidx

import (
	"testing"

	"github.com/rootxkit/uspace-core/core"
)

func TestLoad(t *testing.T) {
	u, desc, err := Load("constant:15.9")
	if err != nil || desc == "" {
		t.Fatal(err)
	}
	if n, _ := u.UndulationM(core.LatLon{LatDeg: 41, LonDeg: 44}); n != 15.9 {
		t.Fatalf("N = %v", n)
	}
	for _, bad := range []string{"", "constant:x", "constant:500", "no/such/file.pgm"} {
		if _, _, err := Load(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

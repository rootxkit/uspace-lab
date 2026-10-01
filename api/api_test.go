package api_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rootxkit/uspace-lab/internal/contracts"
)

// Every mirrored openapi.yaml parses as OpenAPI 3.1 and carries a path for
// every national endpoint group spec 02 §3 gives its system. Unpinned
// systems are counted and named, never skipped in silence (LESSONS E-04);
// the checks themselves are exercised on fixtures in internal/contracts,
// so a run with zero mirrors still proves they can fail.
func TestMirroredOpenAPIFiles(t *testing.T) {
	mirrors, err := contracts.Mirrors("..")
	if err != nil {
		t.Fatal(err)
	}
	checked, unpinned := 0, 0
	for _, m := range mirrors {
		if !m.Source.Pinned() {
			unpinned++
			t.Logf("unpinned: %s/%s (uspace-%s has not published it)", m.Kind, m.System, m.System)
			assertOnlyLayoutFiles(t, m)
			continue
		}
		if m.Kind != "api" {
			continue
		}
		checked++
		o, err := contracts.ReadOpenAPI(filepath.Join(m.Dir, "openapi.yaml"))
		if err != nil {
			t.Errorf("api/%s/openapi.yaml: %v", m.System, err)
			continue
		}
		for _, g := range contracts.GroupsFor(m.System) {
			missing := g.Missing(o.Paths)
			switch {
			case g.Standard:
				t.Logf("api/%s: %s is standard (uas_standards), not looked for", m.System, g.Row)
			case len(missing) == 0:
			case g.Optional:
				t.Logf("api/%s: optional group %s absent: %v", m.System, g.Row, missing)
			default:
				t.Errorf("api/%s/openapi.yaml: no path for %v (02 §3 row %s)", m.System, missing, g.Row)
			}
		}
	}
	t.Logf("%d mirrored openapi.yaml files checked, %d mirror directories unpinned", checked, unpinned)
}

// An unpinned mirror directory holds only its SOURCE and README.md: a
// file there would be a copy with no commit to check it against.
func assertOnlyLayoutFiles(t *testing.T, m contracts.Mirror) {
	t.Helper()
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "SOURCE" && e.Name() != "README.md" {
			t.Errorf("%s/%s: unpinned mirror holds %s", m.Kind, m.System, e.Name())
		}
	}
}

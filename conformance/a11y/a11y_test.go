package a11y

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "axe.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportBothWays(t *testing.T) {
	p := write(t, `{"format":"conformance-axe/v1","axe_version":"4.10.3","standard":"WCAG 2.2 AA","pages":[
	  {"url":"http://a/clean","status":200,"passes":30,"violations":[]},
	  {"url":"http://a/dirty","status":200,"passes":28,"violations":[{"id":"image-alt","impact":"critical","nodes":2}]},
	  {"url":"http://a/down","status":0,"error":"net::ERR_CONNECTION_REFUSED","violations":[]},
	  {"url":"http://a/404","status":404,"violations":[]}]}`)
	_, out, err := Import(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]result.Status{"http://a/clean": result.Pass, "http://a/dirty": result.Fail, "http://a/down": result.Fail, "http://a/404": result.Fail}
	for _, o := range out {
		if want[o.Subject] != o.Status {
			t.Errorf("%s: %s, want %s", o.Subject, o.Status, want[o.Subject])
		}
		if err := o.Validate(); err != nil {
			t.Error(err)
		}
	}
	if len(out) != len(want) {
		t.Errorf("%d outcomes, want %d", len(out), len(want))
	}
}

func TestImportNoPage(t *testing.T) {
	_, out, err := Import(write(t, `{"format":"conformance-axe/v1","pages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Status != result.NotApplicable {
		t.Errorf("%+v, want one not_applicable", out)
	}
	if _, _, err := Import(write(t, `{"format":"other"}`)); err == nil {
		t.Error("another format was imported")
	}
}

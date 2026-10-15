package load

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// imageTar is an image's file system: this test binary (a Go binary
// built with uspace-core) at /usr/bin/api, a script and a data file.
func imageTar(t *testing.T) []byte {
	t.Helper()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct {
		name string
		mode int64
		body []byte
	}{{"usr/bin/api", 0o755, self}, {"usr/bin/run.sh", 0o755, []byte("#!/bin/sh\nexit 0\n")}, {"etc/passwd", 0o644, []byte("root:x:0:0\n")}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// coreVersion is the uspace-core this test binary was built with.
func coreVersion(t *testing.T) string {
	t.Helper()
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build information in the test binary")
	}
	for _, d := range bi.Deps {
		if d.Path == CoreModule {
			return d.Version
		}
	}
	t.Skip("the test binary has no uspace-core")
	return ""
}

// fakeVectors is a runner whose docker gives the test image and whose
// core tests answer events, with a module directory vendoring the lab's
// vectors, each edited by edit (nil: as they are; returning nil: left
// out).
func fakeVectors(t *testing.T, events string, edit func(name string, b []byte) []byte) *vectorsRunner {
	t.Helper()
	img := imageTar(t)
	dir := t.TempDir()
	td := filepath.Join(dir, "vectors", "testdata")
	if err := os.MkdirAll(td, 0o700); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(repo("knowledge/vectors"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(repo("knowledge/vectors/" + e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			if b = edit(e.Name(), b); b == nil {
				continue
			}
		}
		if err := os.WriteFile(filepath.Join(td, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	v := newVectorsRunner(repo("knowledge/vectors"))
	v.dockerOut = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "create" {
			return []byte("cid-1\n"), nil
		}
		return nil, nil
	}
	v.export = func(context.Context, string) (io.ReadCloser, func() error, error) {
		return io.NopCloser(bytes.NewReader(img)), func() error { return nil }, nil
	}
	v.coreTests = func(context.Context, string) ([]byte, string, error) { return []byte(events), dir, nil }
	return v
}

const passingEvents = `{"Action":"pass","Package":"github.com/rootxkit/uspace-core/cpa","Test":"TestVectors"}
{"Action":"pass","Package":"github.com/rootxkit/uspace-core/vectors","Test":"TestManifest"}
{"Action":"pass","Package":"github.com/rootxkit/uspace-core/cpa"}
`

// The image's Go binary is found and its uspace-core read, the script
// and the data file are not taken for binaries; with the core's vector
// tests passing and its vectors the lab's, nothing fails. A failing test
// and a vector file that differs each count, a package that does not
// build counts, and an image that cannot be read leaves the row not
// measured rather than passed.
func TestVectorsAgainstTheImages(t *testing.T) {
	want := coreVersion(t)
	ctx := context.Background()
	rep := fakeVectors(t, passingEvents, nil).run(ctx, map[string]string{"ussp": "ghcr.io/x/ussp@sha256:1"})
	if len(rep.Images) != 1 || len(rep.Images[0].Binaries) != 1 || rep.Images[0].Binaries[0].Path != "/usr/bin/api" || rep.Images[0].Cores[0] != want {
		t.Fatalf("images %+v, want /usr/bin/api with uspace-core %s", rep.Images, want)
	}
	obs := map[string]Observation{}
	rep.observe(obs)
	if o := obs["vectors_failed"]; !o.Measured || *o.Value != 0 || rep.Cores[0].Passed != 2 || rep.LabVectors < 10 {
		t.Fatalf("passing: %+v %+v", o, rep.Cores)
	}
	failing := passingEvents + `{"Action":"fail","Package":"github.com/rootxkit/uspace-core/rid","Test":"TestVectors/case_3"}` + "\n"
	// The core vendors an older cpa.json (a case's expected value
	// changed), not geodesy.json, and an alert_lifecycle.json whose
	// owners differ only.
	edit := func(name string, b []byte) []byte {
		switch name {
		case "cpa.json":
			return bytes.Replace(b, []byte(`"expected"`), []byte(`"expected_old"`), 1)
		case "geodesy.json":
			return nil
		case "alert_lifecycle.json":
			return bytes.Replace(b, []byte(`"owners": [`), []byte(`"owners": ["cisp", `), 1)
		}
		return b
	}
	rep = fakeVectors(t, failing, edit).run(ctx, map[string]string{"ussp": "ghcr.io/x/ussp@sha256:1"})
	obs = map[string]Observation{}
	rep.observe(obs)
	// One failing test, cpa.json's behaviour and geodesy.json missing;
	// alert_lifecycle.json's owners are not behaviour.
	if o := obs["vectors_failed"]; !o.Measured || *o.Value != 3 || len(rep.Cores[0].MetadataOnly) != 1 || rep.Cores[0].MetadataOnly[0] != "alert_lifecycle.json" {
		t.Fatalf("failing: %+v %+v", o, rep.Cores)
	}
	broken := `{"Action":"fail","Package":"github.com/rootxkit/uspace-core/zones"}` + "\n"
	rep = fakeVectors(t, broken, nil).run(ctx, map[string]string{"ussp": "ghcr.io/x/ussp@sha256:1"})
	obs = map[string]Observation{}
	rep.observe(obs)
	if o := obs["vectors_failed"]; !o.Measured || *o.Value != 1 {
		t.Fatalf("a package that does not build: %+v %+v", o, rep.Cores)
	}
	unread := fakeVectors(t, passingEvents, nil)
	unread.dockerOut = func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("no such image") }
	rep = unread.run(ctx, map[string]string{"ussp": "ghcr.io/x/ussp@sha256:1"})
	obs = map[string]Observation{}
	rep.observe(obs)
	if o := obs["vectors_failed"]; o.Measured || !strings.Contains(o.Reason, "could not be read") {
		t.Fatalf("an unreadable image: %+v", o)
	}
}

// Against the real images (LOAD_VECTORS_IMAGES=system=ref,...; docker
// and the network): every image has a Go binary built with uspace-core
// and the vectors ran.
func TestVectorsAgainstRealImages(t *testing.T) {
	spec := os.Getenv("LOAD_VECTORS_IMAGES")
	if spec == "" {
		t.Skip("LOAD_VECTORS_IMAGES is not set")
	}
	images := map[string]string{}
	for _, kv := range strings.Split(spec, ",") {
		k, v, _ := strings.Cut(kv, "=")
		images[k] = v
	}
	rep := newVectorsRunner(repo("knowledge/vectors")).run(context.Background(), images)
	obs := map[string]Observation{}
	rep.observe(obs)
	for _, iv := range rep.Images {
		t.Logf("%s %s: %+v %v %s", iv.System, iv.Image, iv.Binaries, iv.Cores, iv.Error)
	}
	for _, c := range rep.Cores {
		t.Logf("core %s: passed %d failed %d skipped %d mismatched %v error %q", c.Version, c.Passed, c.Failed, c.Skipped, c.Mismatched, c.Error)
	}
	t.Logf("vectors_failed %+v", obs["vectors_failed"])
	if !obs["vectors_failed"].Measured {
		t.Fatalf("not measured: %s", obs["vectors_failed"].Reason)
	}
}

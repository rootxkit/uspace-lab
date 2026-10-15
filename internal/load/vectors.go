package load

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 05 §7's vectors row: "all knowledge/vectors/ behaviour vectors pass
// against the packages in every image under test". The packages are
// uspace-core's, compiled into each image's Go binaries at the version
// the image's build recorded. A systems run reads that version from
// every binary of every image (the build information Go embeds), runs
// uspace-core's vector tests at each version found (they run every case
// of every file, vectors/doc.go), and compares the vectors that version
// vendors with this repository's knowledge/vectors (an image judged on
// other vectors than the lab's is not judged on the lab's).

// CoreModule is uspace-core's module path.
const CoreModule = "github.com/rootxkit/uspace-core"

// Bounds of the vectors step (E-10).
const (
	vectorsTimeout     = 15 * time.Minute
	maxImageBinaryB    = 512 << 20
	maxImageBinaries   = 64
	maxVectorsFailures = 50
	vectorsTestPattern = "Vector|Manifest"
)

// ImageVectors is what one image embeds of uspace-core.
type ImageVectors struct {
	System   string          `json:"system"`
	Image    string          `json:"image"`
	Binaries []ImageBinary   `json:"binaries"`
	Cores    []string        `json:"core_versions"`
	Error    string          `json:"error,omitempty"`
	exec     map[string]bool // binaries already read
}

// ImageBinary is one Go binary of an image: its path, its main module
// and the uspace-core version it was built with ("" none).
type ImageBinary struct {
	Path string `json:"path"`
	Main string `json:"main"`
	Core string `json:"core"`
}

// CoreVectors is one uspace-core version's vector tests and vectors.
type CoreVectors struct {
	Version string   `json:"version"`
	Passed  int      `json:"passed"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Tests   []string `json:"failed_tests,omitempty"`
	// Mismatched are the vector files whose behaviour (the file less its
	// owners and words, behaviourOf) differs from this repository's
	// knowledge/vectors, or which only one side holds; MetadataOnly those
	// that differ in their owners or words only (not counted).
	Mismatched   []string `json:"vectors_mismatched,omitempty"`
	MetadataOnly []string `json:"vectors_metadata_only,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// VectorsReport is the vectors row's evidence.
type VectorsReport struct {
	Images []ImageVectors `json:"images"`
	Cores  []CoreVectors  `json:"cores"`
	// LabVectors is how many knowledge/vectors files were compared.
	LabVectors int     `json:"lab_vectors"`
	TookS      float64 `json:"took_s"`
}

// vectorsRunner runs the vectors step; its commands are replaceable for
// tests.
type vectorsRunner struct {
	// docker runs docker with args and returns its standard output as a
	// stream (export) or whole.
	dockerOut func(ctx context.Context, args ...string) ([]byte, error)
	export    func(ctx context.Context, id string) (io.ReadCloser, func() error, error)
	// coreTests runs uspace-core's vector tests at version in a module of
	// its own and returns go test -json's output and the module's
	// directory in the module cache.
	coreTests func(ctx context.Context, version string) (out []byte, dir string, err error)
	labDir    string
}

func newVectorsRunner(labDir string) *vectorsRunner {
	v := &vectorsRunner{labDir: labDir}
	v.dockerOut = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", args...).Output() //nolint:gosec // fixed program, names from the targets file
	}
	v.export = func(ctx context.Context, id string) (io.ReadCloser, func() error, error) {
		cmd := exec.CommandContext(ctx, "docker", "export", id) //nolint:gosec // an id docker gave
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return out, cmd.Wait, nil
	}
	v.coreTests = goCoreTests
	return v
}

// run reads every image and runs every core version found.
func (v *vectorsRunner) run(ctx context.Context, images map[string]string) *VectorsReport {
	ctx, cancel := context.WithTimeout(ctx, vectorsTimeout)
	defer cancel()
	t0 := time.Now()
	rep := &VectorsReport{}
	versions := map[string]bool{}
	for _, sys := range sortedKeysOf(images) {
		iv := v.image(ctx, sys, images[sys])
		for _, c := range iv.Cores {
			versions[c] = true
		}
		rep.Images = append(rep.Images, iv)
	}
	lab, err := labVectors(v.labDir)
	rep.LabVectors = len(lab)
	for _, ver := range sortedKeysOf(versions) {
		cv := CoreVectors{Version: ver}
		if err != nil {
			cv.Error = "knowledge/vectors: " + err.Error()
		} else {
			v.core(ctx, &cv, lab)
		}
		rep.Cores = append(rep.Cores, cv)
	}
	rep.TookS = round3(time.Since(t0).Seconds())
	return rep
}

// image exports ref's file system and reads the build information of
// every executable in it.
func (v *vectorsRunner) image(ctx context.Context, system, ref string) ImageVectors {
	iv := ImageVectors{System: system, Image: ref, exec: map[string]bool{}}
	// A local name may carry a note after the reference (seed.anspImage).
	ref, _, _ = strings.Cut(ref, " ")
	out, err := v.dockerOut(ctx, "create", ref)
	if err != nil {
		iv.Error = "docker create: " + errText(err)
		return iv
	}
	id := strings.TrimSpace(string(out))
	defer func() { _, _ = v.dockerOut(context.WithoutCancel(ctx), "rm", "-f", id) }()
	rc, wait, err := v.export(ctx, id)
	if err != nil {
		iv.Error = "docker export: " + errText(err)
		return iv
	}
	err = iv.read(tar.NewReader(bufio.NewReaderSize(rc, 1<<20)))
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	if werr := wait(); err == nil && werr != nil {
		err = fmt.Errorf("docker export: %w", werr)
	}
	if err != nil {
		iv.Error = err.Error()
		return iv
	}
	sort.Slice(iv.Binaries, func(i, j int) bool { return iv.Binaries[i].Path < iv.Binaries[j].Path })
	seen := map[string]bool{}
	for _, b := range iv.Binaries {
		if b.Core != "" && !seen[b.Core] {
			seen[b.Core] = true
			iv.Cores = append(iv.Cores, b.Core)
		}
	}
	sort.Strings(iv.Cores)
	switch {
	case len(iv.Binaries) == 0:
		iv.Error = "no Go binary in the image"
	case len(iv.Cores) == 0:
		iv.Error = "no binary of the image was built with " + CoreModule
	}
	return iv
}

// read walks an image's file system and keeps the build information of
// each executable that is a Go binary.
func (iv *ImageVectors) read(tr *tar.Reader) error {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the image's file system: %w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Mode&0o111 == 0 || h.Size < 4 || h.Size > maxImageBinaryB {
			continue
		}
		p := "/" + strings.TrimPrefix(path.Clean(h.Name), "/")
		if iv.exec[p] || len(iv.exec) >= maxImageBinaries {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		bi, err := buildinfo.Read(bytes.NewReader(b))
		if err != nil {
			continue // not a Go binary (a shell, a script)
		}
		iv.exec[p] = true
		ib := ImageBinary{Path: p, Main: bi.Main.Path}
		for _, d := range bi.Deps {
			if d.Path != CoreModule {
				continue
			}
			ib.Core = d.Version
			if d.Replace != nil {
				// A replaced core is not a version a test can fetch.
				ib.Core = "replaced by " + d.Replace.Path + " " + d.Replace.Version
			}
		}
		if bi.Main.Path == CoreModule {
			ib.Core = bi.Main.Version
		}
		iv.Binaries = append(iv.Binaries, ib)
	}
}

// vectorSums are a vector file's SHA-256 and its behaviour's.
type vectorSums struct{ sha, behaviour string }

// labVectors is the SHA-256 of every vector JSON file in dir, and of its
// behaviour (behaviourOf).
func labVectors(dir string) (map[string]vectorSums, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]vectorSums{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // the repository's own files
		if err != nil {
			return nil, err
		}
		s := sha256.Sum256(b)
		bh, err := behaviourOf(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out[e.Name()] = vectorSums{sha: hex.EncodeToString(s[:]), behaviour: bh}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no vector file in %s", dir)
	}
	return out, nil
}

// core runs cv's version's vector tests and compares its vectors with
// the lab's.
func (v *vectorsRunner) core(ctx context.Context, cv *CoreVectors, lab map[string]vectorSums) {
	if strings.HasPrefix(cv.Version, "replaced by ") || cv.Version == "" || cv.Version == "(devel)" {
		cv.Error = "not a published version: its vectors cannot be run here"
		return
	}
	out, dir, err := v.coreTests(ctx, cv.Version)
	if err != nil && len(out) == 0 {
		cv.Error = errText(err)
		return
	}
	buildFailed := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Action, Package, Test string
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Test == "" && e.Action == "fail":
			buildFailed = true
			if len(cv.Tests) < maxVectorsFailures {
				cv.Tests = append(cv.Tests, e.Package+" (the package failed)")
			}
		case e.Test == "":
		case e.Action == "pass":
			cv.Passed++
		case e.Action == "skip":
			cv.Skipped++
		case e.Action == "fail":
			cv.Failed++
			if len(cv.Tests) < maxVectorsFailures {
				cv.Tests = append(cv.Tests, e.Package+" "+e.Test)
			}
		}
	}
	if buildFailed && cv.Failed == 0 {
		cv.Failed = 1
	}
	if cv.Passed == 0 && cv.Failed == 0 {
		cv.Error = "go test ran no vector test (" + strings.TrimSpace(errText(err)) + ")"
		return
	}
	theirs, terr := labVectors(filepath.Join(dir, "vectors", "testdata"))
	if terr != nil {
		cv.Error = "the vectors this version vendors: " + terr.Error()
		return
	}
	for _, name := range sortedKeysOf(lab) {
		switch t, ok := theirs[name]; {
		case !ok:
			cv.Mismatched = append(cv.Mismatched, name+" (not vendored)")
		case t.sha == lab[name].sha:
		case t.behaviour == lab[name].behaviour:
			// Who owns a case, and the words about it, are not what the
			// packages are judged on.
			cv.MetadataOnly = append(cv.MetadataOnly, name)
		default:
			cv.Mismatched = append(cv.Mismatched, name)
		}
	}
	for _, name := range sortedKeysOf(theirs) {
		if _, ok := lab[name]; !ok {
			cv.Mismatched = append(cv.Mismatched, name+" (not in knowledge/vectors)")
		}
	}
}

// metadataKeys are the keys of a vector file that route or explain a
// case rather than state its behaviour (knowledge/README.md).
var metadataKeys = map[string]bool{"owners": true, "owner": true, "description": true, "why": true, "source": true, "decision": true, "generated": true}

// behaviourOf is the SHA-256 of a vector file without its metadata, as
// canonical JSON (keys sorted).
func behaviourOf(b []byte) (string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	var strip func(any) any
	strip = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, e := range t {
				if metadataKeys[k] {
					delete(t, k)
					continue
				}
				t[k] = strip(e)
			}
		case []any:
			for i := range t {
				t[i] = strip(t[i])
			}
		}
		return x
	}
	c, err := json.Marshal(strip(v))
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(c)
	return hex.EncodeToString(s[:]), nil
}

// goCoreTests runs uspace-core's vector tests at version from a module
// of its own (go get, then go test -json on every package).
func goCoreTests(ctx context.Context, version string) ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "lab-vectors-")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module labvectors\n\ngo 1.27\n"), 0o600); err != nil {
		return nil, "", err
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed program and arguments
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	if _, err := run("get", CoreModule+"@"+version); err != nil {
		return nil, "", err
	}
	mod, err := run("list", "-m", "-json", CoreModule)
	if err != nil {
		return nil, "", err
	}
	var m struct{ Dir string }
	if err := json.Unmarshal(mod, &m); err != nil || m.Dir == "" {
		return nil, "", fmt.Errorf("go list: no module directory for %s@%s", CoreModule, version)
	}
	out, err := run("test", "-count=1", "-json", "-run", vectorsTestPattern, CoreModule+"/...")
	return out, m.Dir, err
}

// observe turns the step into the vectors row: every failing test, every
// mismatched file and every image without a version that ran count; an
// image that could not be read leaves the row not measured.
func (rep *VectorsReport) observe(obs map[string]Observation) {
	failed := 0
	for _, iv := range rep.Images {
		if iv.Error != "" {
			if strings.HasPrefix(iv.Error, "docker ") || strings.HasPrefix(iv.Error, "the image's") {
				obs["vectors_failed"] = notMeasured(fmt.Sprintf("image %s (%s) could not be read: %s", iv.System, iv.Image, iv.Error))
				return
			}
			failed++ // no Go binary, or none built with uspace-core
		}
	}
	for i := range rep.Cores {
		cv := &rep.Cores[i]
		if cv.Error != "" {
			obs["vectors_failed"] = notMeasured(fmt.Sprintf("uspace-core %s: %s", cv.Version, cv.Error))
			return
		}
		failed += cv.Failed + len(cv.Mismatched)
	}
	if len(rep.Cores) == 0 && failed == 0 {
		obs["vectors_failed"] = notMeasured("no image under test was read")
		return
	}
	obs["vectors_failed"] = valueObs(float64(failed))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimSpace(err.Error() + ": " + string(ee.Stderr))
	}
	return err.Error()
}

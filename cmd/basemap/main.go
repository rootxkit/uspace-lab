// Command basemap is the Go half of basemap/build.sh
// (docs/WORKPACKAGES/WP-L3.md): it reads the region policy and the
// pinned inputs, plans the go-pmtiles extracts, fetches and checks the
// inputs and writes SOURCE.json. The tiles are cut by go-pmtiles and the
// glyphs drawn by font-maker; this command never writes an archive.
//
//	basemap build-info --inputs F [--out FILE] [BUILD]
//	basemap plan       --regions F --profile bundle|storybook --work DIR
//	basemap fetch      --inputs F --dest DIR --bundle DIR
//	basemap fontstacks --inputs F --fetched DIR
//	basemap source     --regions F --inputs F --profile P --build-info FILE
//	                   --fetched DIR --archive FILE --tool NAME=VERSION... --out FILE
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/basemap"
)

const usage = `usage: basemap build-info|plan|fetch|fontstacks|source [flags]
see the comment at the top of cmd/basemap/main.go`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// httpTimeout bounds each download of an input or the builds list.
const httpTimeout = 2 * time.Minute

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	cmds := map[string]func(context.Context, []string, io.Writer) error{
		"build-info": cmdBuildInfo,
		"plan":       cmdPlan,
		"fetch":      cmdFetch,
		"fontstacks": cmdFontstacks,
		"source":     cmdSource,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if err := cmd(ctx, args[1:], stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "basemap %s: %v\n", args[0], err)
		var u usageError
		if errors.As(err, &u) {
			return 2
		}
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func required(vals map[string]string) error {
	var missing []string
	for k, v := range vals {
		if v == "" {
			missing = append(missing, "--"+k)
		}
	}
	if len(missing) > 0 {
		return usageError{"missing " + strings.Join(missing, ", ")}
	}
	return nil
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	return nil
}

func client() *http.Client { return &http.Client{Timeout: httpTimeout} }

func cmdBuildInfo(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("build-info")
	inputs := fs.String("inputs", "", "basemap/inputs.yaml")
	out := fs.String("out", "", "write the build entry as JSON here")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(map[string]string{"inputs": *inputs}); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageError{"at most one BUILD"}
	}
	in, err := basemap.LoadInputs(*inputs)
	if err != nil {
		return err
	}
	bi, err := basemap.FetchBuildInfo(ctx, client(), in.Tiles, fs.Arg(0))
	if err != nil {
		return err
	}
	if *out != "" {
		b, err := json.MarshalIndent(bi, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Clean(*out), append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\n", bi.Build, bi.URL)
	return err
}

func loadPolicy(regions string) (*basemap.Regions, error) {
	return basemap.LoadRegions(regions)
}

// cmdPlan prints one tab-separated line per extract: name, min zoom,
// max zoom, bbox or "-", GeoJSON region file or "-".
func cmdPlan(_ context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("plan")
	regions := fs.String("regions", "", "basemap/regions.yaml")
	profile := fs.String("profile", basemap.ProfileBundle, "bundle or storybook")
	work := fs.String("work", "", "where the GeoJSON region files are written")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(map[string]string{"regions": *regions, "work": *work}); err != nil {
		return err
	}
	r, err := loadPolicy(*regions)
	if err != nil {
		return err
	}
	var plan []basemap.Extract
	switch *profile {
	case basemap.ProfileBundle:
		plan = r.PlanBundle()
	case basemap.ProfileStorybook:
		plan = r.PlanStorybook()
	default:
		return usageError{fmt.Sprintf("profile %q: want bundle or storybook", *profile)}
	}
	var b strings.Builder
	for _, e := range plan {
		bbox, region := "-", "-"
		if e.BBox != nil {
			bbox = e.BBox.String()
		} else {
			gj, err := basemap.RegionGeoJSON(e.Region)
			if err != nil {
				return err
			}
			region = filepath.Join(*work, e.Name+".geojson")
			if err := os.WriteFile(region, gj, 0o600); err != nil {
				return err
			}
		}
		fmt.Fprintf(&b, "%s\t%d\t%d\t%s\t%s\n", e.Name, e.MinZoom, e.MaxZoom, bbox, filepath.ToSlash(region))
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

func cmdFetch(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("fetch")
	inputs := fs.String("inputs", "", "basemap/inputs.yaml")
	dest := fs.String("dest", "", "where the inputs are downloaded")
	bundle := fs.String("bundle", "", "the bundle directory the shipped inputs are copied into")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(map[string]string{"inputs": *inputs, "dest": *dest, "bundle": *bundle}); err != nil {
		return err
	}
	in, err := basemap.LoadInputs(*inputs)
	if err != nil {
		return err
	}
	fetched, err := basemap.Fetch(ctx, client(), in, *dest)
	if err != nil {
		return err
	}
	if err := basemap.WriteFetched(fetched, filepath.Join(*dest, "inputs.json")); err != nil {
		return err
	}
	if err := basemap.Install(fetched, *bundle); err != nil {
		return err
	}
	var b strings.Builder
	for _, f := range fetched {
		where := "consumed"
		if f.Path != "" {
			where = f.Path
		}
		v := ""
		if f.Version != "" {
			v = " (" + f.Version + ")"
		}
		fmt.Fprintf(&b, "fetched %-30s %8d bytes sha256 %s ok -> %s%s\n", f.ID, f.Bytes, f.SHA256, where, v)
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

// cmdFontstacks prints one tab-separated line per fontstack: its name,
// then the fetched font files it is drawn from, in order.
func cmdFontstacks(_ context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("fontstacks")
	inputs := fs.String("inputs", "", "basemap/inputs.yaml")
	fetched := fs.String("fetched", "", "the fetch --dest directory")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(map[string]string{"inputs": *inputs, "fetched": *fetched}); err != nil {
		return err
	}
	in, err := basemap.LoadInputs(*inputs)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, s := range in.Fontstacks {
		parts := []string{s.Name}
		for _, id := range s.Fonts {
			p := filepath.Join(*fetched, id)
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("fontstack %s: %w", s.Name, err)
			}
			parts = append(parts, filepath.ToSlash(p))
		}
		b.WriteString(strings.Join(parts, "\t") + "\n")
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

type toolFlags map[string]string

func (t toolFlags) String() string { return fmt.Sprint(map[string]string(t)) }

func (t toolFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" || strings.TrimSpace(val) == "" {
		return fmt.Errorf("--tool %q: want NAME=VERSION", v)
	}
	t[k] = val
	return nil
}

func cmdSource(_ context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("source")
	regions := fs.String("regions", "", "basemap/regions.yaml")
	inputs := fs.String("inputs", "", "basemap/inputs.yaml")
	profile := fs.String("profile", basemap.ProfileBundle, "bundle or storybook")
	buildInfo := fs.String("build-info", "", "the build-info --out file")
	fetched := fs.String("fetched", "", "the fetch --dest directory")
	archive := fs.String("archive", "", "basemap.pmtiles as built")
	out := fs.String("out", "", "SOURCE.json to write")
	tools := toolFlags{}
	fs.Var(tools, "tool", "NAME=VERSION of a tool that made the bundle (repeatable)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(map[string]string{"regions": *regions, "inputs": *inputs, "build-info": *buildInfo,
		"fetched": *fetched, "archive": *archive, "out": *out}); err != nil {
		return err
	}
	r, err := loadPolicy(*regions)
	if err != nil {
		return err
	}
	in, err := basemap.LoadInputs(*inputs)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Clean(*buildInfo))
	if err != nil {
		return err
	}
	var bi basemap.BuildInfo
	if err := json.Unmarshal(b, &bi); err != nil {
		return fmt.Errorf("%s: %w", *buildInfo, err)
	}
	f, err := basemap.ReadFetched(filepath.Join(*fetched, "inputs.json"))
	if err != nil {
		return err
	}
	src, err := basemap.NewSource(&basemap.SourceParams{
		Profile: *profile, Regions: r, Inputs: in, Build: bi, Fetched: f,
		Archive: *archive, Tools: tools, FetchedAt: time.Now(),
	})
	if err != nil {
		return err
	}
	if err := src.Write(*out); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %s: build %s, OSM data as of %s, archive %d bytes sha256 %s\n",
		*out, src.Build, src.OSMDataAsOf, src.Archive.Bytes, src.Archive.SHA256)
	return err
}

package schemas_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/contracts"
)

// The eight common schemas of docs/PLAN.md §3.1 and WP-L1.
var commonSchemas = []string{
	"console/snapshot/v1",
	"console/status/v1",
	"console/subscribe/v1",
	"envelope/v1",
	"problem/v1",
	"source/status/v1",
	"track/telemetry/v1",
	"zone/applicable/v1",
}

// Every valid example validates, every invalid example is refused, and
// every schema has both (LESSONS E-01). The reason each invalid example
// fails is logged so a reader can see it fails for the rule its name
// gives.
func TestExamplesBothWays(t *testing.T) {
	rep, err := contracts.CheckExamples("common")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	valid, invalid := 0, 0
	for _, s := range rep.Schemas {
		names = append(names, s.Name)
		valid += s.Valid
		invalid += s.Invalid
		for _, p := range s.Problems {
			t.Errorf("%s: %s", s.Name, p)
		}
		for _, e := range s.Results {
			rel, _ := filepath.Rel("common", e.File)
			switch {
			case e.Problem != "":
				t.Errorf("%s: %s", filepath.ToSlash(rel), e.Problem)
			case !e.WantValid:
				t.Logf("refused %s: %s", filepath.ToSlash(rel), e.Reason)
			}
		}
	}
	if !reflect.DeepEqual(names, commonSchemas) {
		t.Errorf("schemas under common/ are %v, want %v", names, commonSchemas)
	}
	t.Logf("%d schemas, %d valid examples validated, %d invalid examples refused", len(names), valid, invalid)
}

// pendingCore lists schema values that core does not have yet at the
// pinned tag, with the release that adds them. The test fails when core
// gains one (the entry must then be removed) and logs each one, so the
// difference is visible on every run rather than hidden in a diff.
var pendingCore = map[string]map[string]string{
	"IdentBasis": {"provider": "uspace-core v1.1.0 core.BasisProvider (core WP-16; decision record Q-A8)"},
}

type enumPin struct {
	schema   string
	path     []string
	coreType string
}

var enumPins = []enumPin{
	{"envelope/v1", []string{"$defs", "time_source"}, "TimeSource"},
	{"envelope/v1", []string{"$defs", "trust"}, "Trust"},
	{"track/telemetry/v1", []string{"$defs", "identification", "properties", "status"}, "IdentStatus"},
	{"track/telemetry/v1", []string{"$defs", "identification", "properties", "reason"}, "IdentReason"},
	{"track/telemetry/v1", []string{"$defs", "identification", "properties", "basis"}, "IdentBasis"},
	{"track/telemetry/v1", []string{"$defs", "body", "properties", "alt_source"}, "AltSource"},
	{"zone/applicable/v1", []string{"$defs", "body", "properties", "type"}, "ZoneType"},
}

// The enumerations the common schemas pin are uspace-core's, value for
// value, read from core's Go source at the version go.mod pins.
func TestEnumerationsEqualCore(t *testing.T) {
	// Compiled reference: keeps uspace-core in go.mod and proves the
	// source being read is the module this package links.
	if core.TrustSimulated != "simulated" {
		t.Fatalf("core.TrustSimulated = %q", core.TrustSimulated)
	}
	dir, version, err := contracts.ModuleDir(contracts.CoreModule)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reading %s@%s from %s", contracts.CoreModule, version, dir)
	types := make([]string, 0, len(enumPins))
	for _, p := range enumPins {
		types = append(types, p.coreType)
	}
	coreEnums, err := contracts.GoStringEnums(filepath.Join(dir, "core"), types...)
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := contracts.LoadSchemas("common")
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]any{}
	for _, s := range schemas {
		docs[s.Name] = s.Doc
	}
	for _, p := range enumPins {
		got, err := contracts.SchemaEnum(docs[p.schema], p.path...)
		if err != nil {
			t.Fatalf("%s: %v", p.schema, err)
		}
		want := map[string]bool{}
		for _, v := range coreEnums[p.coreType] {
			want[v] = true
		}
		for v, why := range pendingCore[p.coreType] {
			if want[v] {
				t.Errorf("core.%s@%s now has %q: remove it from pendingCore", p.coreType, version, v)
				continue
			}
			want[v] = true
			t.Logf("PENDING: %s %s value %q is not in core.%s@%s; expected in %s", p.schema, strings.Join(p.path, "."), v, p.coreType, version, why)
		}
		wantList := make([]string, 0, len(want))
		for v := range want {
			wantList = append(wantList, v)
		}
		sort.Strings(wantList)
		if !reflect.DeepEqual(got, wantList) {
			t.Errorf("%s %s = %v, core.%s@%s (+ pending) = %v", p.schema, strings.Join(p.path, "."), got, p.coreType, version, wantList)
		} else {
			t.Logf("ok %s %s = core.%s (%d values)", p.schema, strings.Join(p.path, "."), p.coreType, len(got))
		}
	}
}

// Each example's `cell` is the c5 cell of its own position (uspace-core
// WP-15 indexing), so the examples teach the rule rather than contradict
// it.
func TestExampleCellsMatchTheirPositions(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("common", "track", "telemetry", "v1", "examples", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := filepath.Glob(filepath.Join("common", "console", "snapshot", "v1", "examples", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range append(files, snap...) {
		for _, tr := range tracksIn(t, f) {
			body, _ := tr["body"].(map[string]any)
			c, ok := body["cell"].(string)
			if !ok {
				continue
			}
			pos, _ := body["position"].(map[string]any)
			lat, _ := pos["lat"].(float64)
			lng, _ := pos["lng"].(float64)
			want := "c5:" + strconv.Itoa(int(math.Floor((lat+90)*10))) + ":" + strconv.Itoa(int(math.Floor((lng+180)*10)))
			if c != want {
				t.Errorf("%s: cell %s, position gives %s", f, c, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no example carries a cell: the check ran on nothing")
	}
	t.Logf("%d example cells checked against their positions", checked)
}

// tracksIn returns the track/telemetry/v1 messages in an example: the
// example itself, or the tracks of a snapshot.
func tracksIn(t *testing.T, file string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	switch doc["schema"] {
	case "track/telemetry/v1":
		return []map[string]any{doc}
	case "console/snapshot/v1":
		body, _ := doc["body"].(map[string]any)
		list, _ := body["tracks"].([]any)
		out := make([]map[string]any, 0, len(list))
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

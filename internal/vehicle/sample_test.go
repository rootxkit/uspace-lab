package vehicle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/core"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func readFixture(t *testing.T) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "sitl-copter-4.5.7.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSpace(b), []byte("\n"))
}

func TestParsesWhatTheReaderWrote(t *testing.T) {
	lines := readFixture(t)
	sysids := map[int]bool{}
	timed, untimed := 0, 0
	for i, l := range lines {
		s, err := ParseLine(l)
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		// The reader's first lines came before the vehicle's SYSTEM_TIME:
		// ts null (R-16), observed, not constructed.
		if _, ok := s.Time(); ok {
			timed++
		} else {
			untimed++
		}
		sysids[s.Sysid] = true
	}
	if len(sysids) != 3 || timed == 0 || untimed == 0 {
		t.Fatalf("sysids %v, %d lines with ts, %d without", sysids, timed, untimed)
	}
}

func TestRoundTrip(t *testing.T) {
	s, err := ParseLine(readFixture(t)[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.MarshalLine()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ParseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSamples(s, s2) {
		t.Fatalf("round trip changed the sample:\n%+v\n%+v", s, s2)
	}
}

func equalSamples(a, b Sample) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func TestRefusals(t *testing.T) {
	good := string(readFixture(t)[1])
	cases := map[string]string{
		"sysid":     strings.Replace(good, `"sysid":1`, `"sysid":0`, 1),
		"status":    strings.Replace(good, `"status":"ground"`, `"status":"flying"`, 1),
		"ts":        strings.Replace(good, `.533Z"`, `Z"`, 1),
		"schema":    strings.Replace(good, `sim/vehicle/v1`, `sim/vehicle/v2`, 1),
		"line":      strings.Replace(good, `{`, `{"extra":1,`, 1),
		"lat_deg":   strings.Replace(good, `"lat_deg":41.7151`, `"lat_deg":91`, 1),
		"alt_hae_m": strings.Replace(good, `"alt_hae_m":605.09`, `"alt_hae_m":1e9`, 1),
		"armed":     strings.Replace(good, `"armed":false,`, ``, 1),
	}
	for field, line := range cases {
		if line == good {
			t.Fatalf("%s: the case did not change the line", field)
		}
		_, err := ParseLine([]byte(line))
		var fe *core.FieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: got %v", field, err)
		}
	}
	long := append([]byte(`{"x":"`), bytes.Repeat([]byte("a"), MaxLineBytes)...)
	if _, err := ParseLine(append(long, `"}`...)); err == nil {
		t.Error("an oversized line was accepted")
	}
}

// What the Go side writes (Synth) is valid against the schema the Python
// reader's tests use, so the two producers of the line agree.
func TestSynthLinesMatchTheSchema(t *testing.T) {
	sch := compileSchema(t)
	home := Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}
	p := Plan{Sysid: 4, Steps: []PlanStep{{Action: ActionArm}, {Action: ActionTakeoff, AltRelM: 10}}}
	v := NewSynth(home, p, SynthOptions{UTCUnknownFor: 1e9})
	now := p.T0()
	for i := range 40 {
		s, _ := v.Step(now.Add(250e6 * time.Duration(i)))
		b, _ := json.Marshal(s)
		var doc any
		_ = json.Unmarshal(b, &doc)
		if err := sch.Validate(doc); err != nil {
			t.Fatalf("sample %d: %v\n%s", i, err, b)
		}
	}
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "..", "sim", "schema", "vehicle-v1.json")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("vehicle-v1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("vehicle-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadLinesCountsInvalid(t *testing.T) {
	fix := readFixture(t)
	in := bytes.Join([][]byte{fix[0], []byte("not json"), fix[1], []byte(""), fix[2]}, []byte("\n"))
	var c core.Counters
	var got []Sample
	var invalid []error
	if err := ReadLines(context.Background(), bytes.NewReader(in), func(s Sample) { got = append(got, s) }, &c, func(err error) { invalid = append(invalid, err) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || c.Get(CounterInvalidLines) != 1 || len(invalid) != 1 || c.Get(CounterLines) != 4 {
		t.Fatalf("got %d samples, counters %v", len(got), c.Snapshot())
	}
}

func FuzzParseLine(f *testing.F) {
	b, err := os.ReadFile(filepath.Join("testdata", "sitl-copter-4.5.7.ndjson"))
	if err != nil {
		f.Fatal(err)
	}
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		f.Add(l)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"sim/vehicle/v1","ts":null}`))
	f.Fuzz(func(t *testing.T, line []byte) {
		s, err := ParseLine(line)
		if err != nil {
			return
		}
		b, err := s.MarshalLine()
		if err != nil {
			t.Fatalf("an accepted sample does not marshal: %v", err)
		}
		if _, err := ParseLine(b); err != nil {
			t.Fatalf("an accepted sample does not parse again: %v", err)
		}
	})
}

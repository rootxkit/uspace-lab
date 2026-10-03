package wire_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootxkit/uspace-lab/internal/wire"
	"github.com/rootxkit/uspace-lab/internal/wire/wiretest"
)

// Every pinned copy still has the SHA-256 its SOURCE line names, and a
// name SOURCE does not list is refused (E-01).
func TestPinnedCopiesMatchSOURCE(t *testing.T) {
	f, err := os.Open(filepath.Join(wiretest.TestdataDir(), "SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		_, rel, ok := strings.Cut(line, "-> ")
		if !ok {
			t.Fatalf("malformed SOURCE line %q", line)
		}
		if err := wiretest.CheckPin(rel); err != nil {
			t.Error(err)
		}
		n++
	}
	if n < 6 {
		t.Fatalf("%d pins", n)
	}
	if wiretest.CheckPin("ussp/not-pinned.json") == nil {
		t.Fatal("an unlisted file passed")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	b, err := wire.New(wire.SchemaStatus, "lab/test", now, now, &now, "system", false, map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := wire.ParseEnvelope(b)
	if err != nil || e.Schema != wire.SchemaStatus || e.CapturedAt != "2026-10-03T12:00:00.000Z" || e.TS == nil {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := wire.ParseEnvelope([]byte(`{"body":{}}`)); err == nil {
		t.Fatal("an envelope without a schema was accepted")
	}
	if _, err := wire.ParseEnvelope(make([]byte, wire.MaxFrameBytes+1)); err == nil {
		t.Fatal("an oversized frame was accepted")
	}
}

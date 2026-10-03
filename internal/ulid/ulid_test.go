package ulid

import (
	"regexp"
	"testing"
	"time"
)

// The envelope/v1 pattern (schemas/common/envelope/v1).
var pattern = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func TestMakeMatchesTheEnvelopePattern(t *testing.T) {
	seen := map[string]bool{}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for range 1000 {
		u := Make(now)
		if !pattern.MatchString(u) {
			t.Fatalf("%q does not match the envelope pattern", u)
		}
		if seen[u] {
			t.Fatalf("duplicate %q", u)
		}
		seen[u] = true
	}
}

func TestEncodeKnownValues(t *testing.T) {
	if got := Encode([16]byte{}); got != "00000000000000000000000000" {
		t.Fatalf("zero = %q", got)
	}
	var top [16]byte
	for i := range top {
		top[i] = 0xff
	}
	if got := Encode(top); got != "7ZZZZZZZZZZZZZZZZZZZZZZZZZ" {
		t.Fatalf("max = %q", got)
	}
	// The time prefix orders lexically with time.
	a := Make(time.UnixMilli(1_000))[:10]
	b := Make(time.UnixMilli(2_000))[:10]
	if a >= b {
		t.Fatalf("time prefix not ordered: %s >= %s", a, b)
	}
}

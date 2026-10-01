# Knowledge carried over from utm

This directory holds what the Python monitoring system (`rootxkit/utm`)
learned, in a form the five Go repositories can use without reading
Python:

| Path | What it is |
|---|---|
| [`LESSONS.md`](LESSONS.md) | 129 lessons, grouped by area, each with its rule, the failure that taught it and the repository that owns it, plus 14 lessons deliberately **not carried over** |
| [`vectors/`](vectors/) | 17 JSON files, 651 test cases a Go implementation must pass |
| [`scenarios.md`](scenarios.md) | 22 end-to-end scenarios that depend on processes, clocks or a flying simulator, so they cannot be vectors |
| [`tools/gen_vectors.py`](tools/gen_vectors.py) | The script that computed every expected value by running the old code |

Nothing here is a specification of the new APIs. It records what the old
system had to get right, and the cases that showed it.

## The vectors

Every file has the same shape:

```json
{
  "description": "what is being tested and the rule",
  "source": ["the old tests and code it came from"],
  "units": {"field": "unit"},
  "tolerance": {"field": 1e-6},
  "owners": ["authority", "ussp"],
  "generated": "computed by knowledge/tools/gen_vectors.py ...",
  "utm_commit": "484cd228035f2f424530e233ce72100d0fc1842a",
  "fixtures": {},
  "cases": [
    {
      "name": "unique-kebab-case",
      "owner": ["authority"],
      "input": {},
      "expected": {},
      "why": "the failure this case prevents",
      "source": "optional: the old test it was taken from",
      "decision": "optional: why expected is not what utm computes"
    }
  ]
}
```

- `fixtures` is present only where cases share data, for example the
  registry in `identification_status.json`.
- Some headers carry extra keys: `policy`, `rule`, `limits`, `defaults`.
- `owner` is always a list. A repository runs the cases that name it and
  skips the rest.
- `decision` is present only on a case whose expected value is
  deliberately not what utm computes: a later decision (uspace-core waves
  1 to 3, PRs #3 to #15) superseded the old behaviour. It names the
  lesson, regulation or review behind it. The generator sets these values
  through one explicit override function, never by hand (see
  [Regenerating](#regenerating)).
- `null` in `expected` means "unknown" or "nothing". In Go that is a nil
  pointer, never a zero value.

| File | Cases | Owners | What |
|---|---|---|---|
| `odid_decode.json` | 187 | authority, lab | ODID messages and packs decoded, from the opendroneid-core-c reference vectors; refusals; unknown sentinels to null |
| `rid_time.json` | 25 | authority, ussp | Remote ID hour reconstruction (rollover, 0xFFFF, clock ahead, too old, accuracy widening); network Remote ID placement |
| `rid_identity.json` | 24 | authority | Basic ID and Location joined by transmitter address: freshness, silence, unidentified tracks, anomalies (frame sequences) |
| `pressure_altitude.json` | 16 | authority, ussp | Geodetic or pressure altitude, the accuracy threshold, the 10 s hold |
| `rid_receiver_auth.json` | 14 | authority | HMAC-SHA256 receiver signatures, skew window, nonce replay |
| `identification_status.json` | 43 | authority, ussp | The U-02 truth table: four statuses, reasons (spec 04 §3.2 codes), mismatch, EU secret suffix, session bindings, look-alike serials, duplicate serials, unrecognised statuses |
| `serials_and_registration.json` | 41 | authority, ussp | CTA-2063-A serials by class; operator registration numbers; public part and compare key; the serial fold key; Unicode look-alikes |
| `fleet_match.json` | 13 | authority, ussp | The spoofing guard: withhold, speak for ours, conflict; history rows ignored; unusable thresholds withhold |
| `cpa.json` | 33 | ussp, authority | Head-on, crossing, overtaking, parallel, hovering inside the minima, diverging, vertical, stale and advanced neighbours, pressure tracks; loss of separation over the window and its start; invalid policy |
| `alert_lifecycle.json` | 33 | authority, ussp, cisp | Raise and clear sequences with hysteresis and reasons (resolved, stale, source_disabled, landed), backlog, lateness, identification alerts; placements behind or ahead refused; eviction, capacity and per-source share |
| `zones_vertical.json` | 47 | authority, ussp | AMSL, AGL and WGS84 limits with and without terrain or geoid, with every missing reason; the unjudged-limit warning for AGL and WGS84 (S-37); pressure margin and `within_band` capped at the zone's severity; U-space at info; height limit |
| `zones_applicability.json` | 32 | cisp, authority, ussp | ED-269 windows, weekly schedules, overnight, midnight UTC, offsets, boundaries |
| `ed269_parse.json` | 54 | cisp | A valid file round-trips unchanged; 40 single-field mutations and 9 document-level refusals, each with its path and reason; the zone type enumeration |
| `geodesy.json` | 17 | cisp, authority, ussp | Vincenty reference distances; points in circles on the ellipsoid; polygons with holes; tangent plane; antimeridian |
| `terrain_geoid.json` | 48 | authority, ussp | DEM cell names, bilinear and edge rules, nodata; geoid grid layout; GeographicLib reference undulations |
| `source_control.json` | 8 | authority, ussp, ansp | Type and instance switches, default deny |
| `jwt_verify.json` | 16 | authority, cisp, ussp, ansp | Ecosystem JWTs (spec 00 §6.2, 06 §3): RS256 only, `kid` from the allow-listed issuer's JWKS, `aud`, `exp`/`nbf` with 30 s skew, `jti`, scopes. Not from utm (see below) |

### What the vectors are not

- **Wire formats of the new system.** The inputs use neutral field names:
  the old system's where they were already clear, and the standard's
  names for ODID and ED-269. The Go side maps its own types to them in
  the test.
- **Exact strings.** Problem and error texts are the old system's
  wording. Each file says which part is binding: usually a `*_contains`
  or `must_include` phrase, and the JSON path for ED-269.
- **Identifiers.** The UUIDs in `rid_identity.json` are utm's. A new
  implementation may derive ids differently, but the rule must hold: one
  serial is always one id, and the unidentified id is per address.
- **Every number the old monitor produced.** Alert details are reduced to
  the fields that carry meaning, and utm rounded them to 0.1.

## Using them from Go

Copy `knowledge/vectors/` into each repository's `testdata/vectors/` at
a pinned commit of uspace-lab. Do not reference the files across
repositories: a vector changing under a repository must be a reviewed
change there. Then load them with a small generic helper, for example:

```go
// Package vectortest loads uspace-lab knowledge vectors.
package vectortest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// File is one vectors/*.json document. I and E are the input and
// expected shapes for that file; use json.RawMessage where a file mixes
// shapes (input.kind or input.function says which).
type File[I, E any] struct {
	Description string            `json:"description"`
	Source      []string          `json:"source"`
	Units       map[string]string `json:"units"`
	Tolerance   map[string]any    `json:"tolerance"`
	Owners      []string          `json:"owners"`
	Generated   string            `json:"generated"`
	UtmCommit   string            `json:"utm_commit"`
	Fixtures    json.RawMessage   `json:"fixtures,omitempty"`
	Cases       []Case[I, E]      `json:"cases"`
	// Some files add header keys (policy, rule, limits, defaults); decode
	// the file without DisallowUnknownFields, or add them here.
}

type Case[I, E any] struct {
	Name     string   `json:"name"`
	Owner    []string `json:"owner"`
	Input    I        `json:"input"`
	Expected E        `json:"expected"`
	Why      string   `json:"why"`
	Source   string   `json:"source,omitempty"`
}

// Load reads testdata/vectors/<name> and fails the test if it cannot.
func Load[I, E any](t testing.TB, name string) File[I, E] {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors", name))
	if err != nil {
		t.Fatalf("vectors %s: %v", name, err)
	}
	var f File[I, E]
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("vectors %s: %v", name, err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("vectors %s: no cases", name) // E-01: an empty table proves nothing
	}
	return f
}

// Run runs every case owned by repo as a subtest, and fails if none is.
func Run[I, E any](t *testing.T, f File[I, E], repo string, fn func(t *testing.T, c Case[I, E])) {
	ran := 0
	for _, c := range f.Cases {
		if !slices.Contains(c.Owner, repo) {
			continue
		}
		ran++
		t.Run(c.Name, func(t *testing.T) {
			t.Logf("why: %s", c.Why) // printed when the case fails
			fn(t, c)
		})
	}
	if ran == 0 {
		t.Fatalf("no case in this file is owned by %q", repo)
	}
}
```

To catch a renamed field, decode each case's input and expected into
structs with `DisallowUnknownFields` inside the test (a renamed field must
fail, not read as zero).

A test then reads like the old one:

```go
func TestRemoteIDTime(t *testing.T) {
	type in struct {
		TimestampTenths uint16    `json:"timestamp_tenths"`
		TsAccuracyCode  int       `json:"ts_accuracy_code"`
		ReceivedAt      time.Time `json:"received_at"`
		ToleranceS      float64   `json:"time_tolerance_s"`
		MaxLatencyS     float64   `json:"max_latency_s"`
	}
	type out struct {
		Ts         time.Time `json:"ts"`
		CapturedAt time.Time `json:"captured_at"`
		TimeSource string    `json:"time_source"`
		Fallback   *string   `json:"fallback"`
	}
	f := vectortest.Load[json.RawMessage, json.RawMessage](t, "rid_time.json")
	vectortest.Run(t, f, "authority", func(t *testing.T, c vectortest.Case[json.RawMessage, json.RawMessage]) {
		var i in
		var e out
		mustUnmarshal(t, c.Input, &i)
		mustUnmarshal(t, c.Expected, &e)
		got := rid.Place(i.TimestampTenths, i.TsAccuracyCode, i.ReceivedAt, i.ToleranceS, i.MaxLatencyS)
		if !got.CapturedAt.Equal(e.CapturedAt) || got.TimeSource != e.TimeSource {
			t.Fatalf("got %+v, want %+v", got, e)
		}
	})
}
```

Practical notes:

- **Times** are ISO 8601 with an offset (`2026-10-01T12:34:56.700000+00:00`).
  `time.Time` unmarshals them as RFC 3339. Compare with `Equal`, not `==`.
- **Floats** use the shortest round-trip representation. Compare within
  the file's `tolerance`. Where the tolerance says "exact", compare
  exactly. Integers stay integers.
- **Bytes** are hex (`hex`) for ODID frames and base64 (`*_base64`) for
  documents that are not valid UTF-8.
- **Stateful files** (`rid_identity.json`, `alert_lifecycle.json`,
  pressure-hold cases) give a sequence of steps. Feed them in order to one
  fresh component and compare `expected.per_step[i]` after each step.
  Also compare the final `counters`: E-09 says refusals are counted, so
  check the counts too.
- **Positions** in `cpa.json` and `alert_lifecycle.json` are given as
  `lat_deg`/`lon_deg`. The `described_as`/`north_m` fields are how the
  case was built, not input.
- **ED-269 refusals**: produce at least the `must_include` problem, with
  the same path suffix and the reason phrase. The vectors list every
  problem the old reader found, so a stricter or looser reader shows up
  in a diff.
- **GeographicLib geoid cases** (`geoid-egm2008-*`, `geoid-egm96-*`) need
  the real grid file and should be skipped when it is absent. Skip them
  visibly, with `t.Skip` and a reason. Their values come from
  GeographicLib, not from the generator.

## Regenerating

The expected values were computed by running utm's own Python code on
these inputs. Do not edit them by hand. Change the generator and rerun
it.

Where a later decision supersedes utm, the generator still runs utm on
the case, then sets the decided value through `decided()`. That function
refuses to run when the decided value equals utm's (a stale override must
be deleted), writes the reason into the case's `decision`, and the run
ends by listing every decided case. When utm has no notion of the input
at all (a policy, a field or a status it cannot hold), the decided value
says so.

`jwt_verify.json` is the exception: utm has no RS256 verifier. It is
written by `rootxkit/uspace-core`'s `auth/internal/genvectors` (WP-11)
from spec 00 §6.2 and 06 §3, with an RSA key generated at run time and
discarded; only the public JWKS is in the file. It is copied here byte
for byte, the generator leaves it alone, and it carries the same
`utm_commit` as the others so that a sync checks one pin. To change it,
regenerate it in uspace-core and copy it again.

```sh
# From the uspace-lab root, with a read-only utm checkout beside it.
PYTHONDONTWRITEBYTECODE=1 ../utm/.venv/Scripts/python.exe \
    knowledge/tools/gen_vectors.py --utm ../utm
```

- `--utm` (or `UTM_REPO`) points at the old checkout. The script only
  imports from it.
- `PYTHONDONTWRITEBYTECODE` keeps it from writing `__pycache__` into utm.
- If the checkout has no `.venv`, make one here instead (it is
  gitignored):

  ```sh
  python -m venv knowledge/tools/.venv
  knowledge/tools/.venv/bin/pip install -e ../utm pytest
  ```

  `pytest` is needed because the generator reads GeographicLib reference
  values from utm's geoid test module.
- The output is deterministic. Running the script twice gives identical
  files, which is checked before every commit of new vectors. Files are
  written with LF line endings on every platform.
- The vectors were generated from utm at
  `484cd228035f2f424530e233ce72100d0fc1842a`. Every file records its
  commit in `utm_commit`.

## What could not be vectorised

The following are described in `scenarios.md` instead:

- Behaviour that needs a broker, a database or several processes:
  switching sources through the KV store, registry projection timing,
  storage outages, drain against intake.
- Anything validated by a flying simulator: hovering, head-on passes,
  sloping terrain, zone passes, the four identification statuses, the
  drain that must not split our aircraft.
- Behaviour utm itself never achieved: the stalled-adapter case (S-24),
  the soak and missed-alert count, slowing to hover beside another
  aircraft. Each is written as the outcome it must show.

## Lessons index

| Area | IDs | Count |
|---|---|---|
| [Invariants](LESSONS.md#0-invariants) | INV-01 to INV-03 | 3 |
| [Engineering rules for every repository](LESSONS.md#1-engineering-rules-for-every-repository) | E-01 to E-15 | 15 |
| [Time and clocks](LESSONS.md#2-time-and-clocks) | T-01 to T-13 | 13 |
| [Remote ID and Open Drone ID](LESSONS.md#3-remote-id-and-open-drone-id) | R-01 to R-17 | 17 |
| [Identity and spoofing](LESSONS.md#4-identity-and-spoofing) | I-01 to I-09 | 9 |
| [Identification and registry](LESSONS.md#5-identification-and-registry) | G-01 to G-12 | 12 |
| [Geodesy and datums](LESSONS.md#6-geodesy-and-datums) | D-01 to D-12 | 12 |
| [Zones and ED-269](LESSONS.md#7-zones-and-ed-269) | Z-01 to Z-13 | 13 |
| [CPA and alerting](LESSONS.md#8-cpa-and-alerting) | C-01 to C-19 | 19 |
| [Ingest reliability and backpressure](LESSONS.md#9-ingest-reliability-and-backpressure) | B-01 to B-16 | 16 |
| [Not carried over](LESSONS.md#10-not-carried-over) | X-01 to X-14 | 14 |

The four engineering rules every repository's own `CLAUDE.md` or
contributing guide should copy are E-01 (test presence, not only
absence), E-02 (run the branch that says nothing is wrong), E-03 (never
write a wire offset from memory) and E-04 (never report an inference as
an observation).

package runner

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/observe"
	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/simop"
	"github.com/rootxkit/uspace-lab/internal/simrx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
	"github.com/rootxkit/uspace-lab/internal/verdict"
)

// Result is results/<run>/<scenario>.json (docs/PLAN.md D8, E-05): the
// commits, the images as configured, the policy, what was flown as the
// vehicles confirmed it, what each system said, the ledgers, the
// latencies and the verdict. Nothing in it is expected rather than
// observed (E-04).
type Result struct {
	Format    string    `json:"format"`
	Run       string    `json:"run"`
	Scenario  string    `json:"scenario"`
	Title     string    `json:"title"`
	Source    string    `json:"source"`
	Owners    []string  `json:"owners"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	T0        time.Time `json:"t0"`
	Mode      struct {
		Vehicles string `json:"vehicles"` // synthetic or sitl
		Targets  string `json:"targets"`  // reference or systems
		Name     string `json:"targets_name,omitempty"`
	} `json:"mode"`
	Evidence       string                       `json:"evidence"`
	Commits        Commits                      `json:"commits"`
	Images         map[string]string            `json:"images,omitempty"`
	PolicyVersion  int                          `json:"policy_version"`
	Policy         *scenario.Policy             `json:"policy"`
	Lab            LabInfo                      `json:"lab"`
	Geoid          string                       `json:"geoid"`
	Marks          map[string]time.Time         `json:"marks"`
	Steps          []vehicle.StepEvent          `json:"steps"`
	StepFailures   []string                     `json:"step_failures,omitempty"`
	Intents        []IntentRecord               `json:"intents,omitempty"`
	Timeline       []TimelineRecord             `json:"timeline,omitempty"`
	Outcome        verdict.Outcome              `json:"outcome"`
	Frames         map[string]map[string]uint64 `json:"frames"`
	StreamErrors   map[string]string            `json:"stream_errors,omitempty"`
	Operators      []simop.Ledger               `json:"operator_ledgers,omitempty"`
	Receivers      []simrx.Ledger               `json:"receiver_ledgers,omitempty"`
	VehicleSamples map[string]uint64            `json:"vehicle_samples"`
	TargetCounters map[string]map[string]uint64 `json:"target_counters,omitempty"`
	Latency        map[string]LatencyStats      `json:"latency_s,omitempty"`
	Verdict        string                       `json:"verdict"`
	Failures       []string                     `json:"failures,omitempty"`
}

// ResultFormat names this shape.
const ResultFormat = "result/v1"

// Commits are the code under test.
type Commits struct {
	Lab      string `json:"lab"`
	LabDirty bool   `json:"lab_dirty"`
	Core     string `json:"core"`
	Go       string `json:"go"`
}

// LabInfo is the origin and numbering the run used.
type LabInfo struct {
	File       string  `json:"file"`
	OriginLat  float64 `json:"origin_lat_deg"`
	OriginLon  float64 `json:"origin_lon_deg"`
	OriginAMSL float64 `json:"origin_alt_amsl_m"`
	SpacingM   float64 `json:"spacing_m"`
}

// IntentRecord is an intent the runner filed, with the system's answer.
type IntentRecord struct {
	Aircraft string `json:"aircraft"`
	IntentID string `json:"intent_id,omitempty"`
	Decision string `json:"decision,omitempty"`
	State    string `json:"state,omitempty"`
	Error    string `json:"error,omitempty"`
	// Ended is the state the USSP answered when the runner ended the
	// intent after the run; EndError why it could not.
	Ended    string `json:"ended,omitempty"`
	EndError string `json:"end_error,omitempty"`
}

// TimelineRecord is a knob or request as it was executed.
type TimelineRecord struct {
	Step   int       `json:"step"`
	ID     string    `json:"id,omitempty"`
	Do     string    `json:"do"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail"`
	Error  string    `json:"error,omitempty"`
}

// LatencyStats summarises captured_at -> observed raise, per system.
type LatencyStats struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

func latency(groups []*verdict.Group) map[string]LatencyStats {
	per := map[string][]float64{}
	for _, g := range groups {
		if g.Raise == nil || g.Raise.CapturedAt == nil || g.Kind == observe.KindDegraded || g.Kind == observe.KindMannedTrack {
			continue
		}
		per[g.System] = append(per[g.System], g.Raise.ObservedAt.Sub(*g.Raise.CapturedAt).Seconds())
	}
	out := map[string]LatencyStats{}
	for sys, xs := range per {
		sort.Float64s(xs)
		q := func(p float64) float64 {
			i := int(math.Ceil(p*float64(len(xs)))) - 1
			return round3(xs[max(0, min(i, len(xs)-1))])
		}
		out[sys] = LatencyStats{N: len(xs), P50: q(0.5), P95: q(0.95), Max: round3(xs[len(xs)-1])}
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// commits reads the lab's commit with git (and whether the tree is
// dirty) and uspace-core's version from the build.
func commits(repo string) Commits {
	c := Commits{Lab: "unknown", Core: "unknown"}
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output(); err == nil { //nolint:gosec // fixed git arguments
		c.Lab = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Output(); err == nil { //nolint:gosec // fixed git arguments
		c.LabDirty = len(strings.TrimSpace(string(out))) > 0
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		c.Go = bi.GoVersion
		for _, d := range bi.Deps {
			if d.Path == "github.com/rootxkit/uspace-core" {
				c.Core = d.Version
			}
		}
	}
	return c
}

// Write writes the result to dir/<scenario>.json.
func (r *Result) Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("results: %w", err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("results: %w", err)
	}
	path := filepath.Join(dir, r.Scenario+".json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("results: %w", err)
	}
	return path, nil
}

// Summary is what the runner prints: what it saw (E-04), not what the
// scenario expected.
func (r *Result) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scenario %s (%s, %s vehicles, %s targets): %s\n", r.Scenario, r.Run, r.Mode.Vehicles, r.Mode.Targets, strings.ToUpper(r.Verdict))
	fmt.Fprintf(&b, "  observed alerts (%d):\n", len(r.Outcome.Groups))
	for _, g := range r.Outcome.Groups {
		line := fmt.Sprintf("    %-9s %-22s %-10s", g.System, g.Kind, g.Aircraft)
		if g.Peer != "" {
			line += " peer " + g.Peer
		}
		if g.Subject != "" {
			line += " on " + g.Subject
		}
		if g.Raise != nil {
			line += fmt.Sprintf(" raised +%.1fs", g.Raise.ObservedAt.Sub(r.T0).Seconds())
		}
		if g.Clear != nil {
			line += fmt.Sprintf(" cleared %s +%.1fs", g.Clear.Reason, g.Clear.ObservedAt.Sub(r.T0).Seconds())
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "  missed %d, false %d\n", r.Outcome.Missed, r.Outcome.False)
	for i := range r.Operators {
		l := &r.Operators[i]
		fmt.Fprintf(&b, "  operator %s: sent %d = accepted %d + refused %d + dropped %d + duplicate %d (balanced %v)\n",
			l.Serial, l.Sent, l.Accepted, l.Refused, l.Dropped, l.Duplicate, l.Balanced)
	}
	for _, l := range r.Receivers {
		fmt.Fprintf(&b, "  receiver %s: observed %d, sent %d = accepted %d + duplicates %d + refused %d (balanced %v)\n",
			l.ReceiverID, l.Observed, l.Sent, l.Accepted, l.Duplicates, l.Refused, l.Balanced)
	}
	for sys, l := range r.Latency {
		fmt.Fprintf(&b, "  latency %s: n %d, p50 %.3f s, p95 %.3f s, max %.3f s\n", sys, l.N, l.P50, l.P95, l.Max)
	}
	for _, f := range r.Failures {
		b.WriteString("  FAIL " + f + "\n")
	}
	for i := range r.Outcome.Expectations {
		e := &r.Outcome.Expectations[i]
		if e.OK || len(e.Seen) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  observed instead of %s:\n", e.Name)
		for _, g := range e.Seen {
			line := fmt.Sprintf("    %s %s %s", g.System, g.Kind, g.Aircraft)
			if g.Peer != "" {
				line += " peer " + g.Peer
			}
			if g.Raise != nil {
				line += fmt.Sprintf(" raised +%.1fs", g.Raise.ObservedAt.Sub(r.T0).Seconds())
			}
			if g.Clear != nil {
				line += fmt.Sprintf(" cleared %s +%.1fs", g.Clear.Reason, g.Clear.ObservedAt.Sub(r.T0).Seconds())
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

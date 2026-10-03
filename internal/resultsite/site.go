// Package resultsite renders the scenario results (results/<run>/
// <scenario>.json, result/v1) as static HTML: an index with every run
// and the trend of each scenario over the runs, and one page per run
// with each scenario's verdict, failures, latencies, ledgers, images and
// commits (docs/WORKPACKAGES/WP-L6.md, cmd/results). Nothing is computed
// that a result file does not say; a file that is not result/v1 is
// listed as unreadable, never skipped silently.
package resultsite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootxkit/uspace-lab/internal/runner"
)

// Run is one results/<run> directory (the layout the runner writes).
// A result whose own run id differs from its directory's name is shown
// with it: the directory is what was committed together.
type Run struct {
	ID      string
	Started time.Time
	Results []*runner.Result
	// Mode is the vehicles and targets of the run's results ("mixed"
	// when they differ).
	Mode       string
	Pass, Fail int
}

// Site is every run under a results directory.
type Site struct {
	Runs       []*Run
	Unreadable []string
	// Scenarios is every scenario id seen, sorted.
	Scenarios []string
}

// Load reads every result/v1 file under dir.
func Load(dir string) (*Site, error) {
	s := &Site{}
	byRun := map[string]*Run{}
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".zones.ed269.json") {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // the operator names the results directory
		if err != nil {
			return err
		}
		r, ok := decode(b)
		if !ok {
			rel, _ := filepath.Rel(dir, p)
			s.Unreadable = append(s.Unreadable, filepath.ToSlash(rel))
			return nil
		}
		rel, _ := filepath.Rel(dir, filepath.Dir(p))
		key := filepath.ToSlash(rel)
		if key == "." {
			key = r.Run
		}
		run := byRun[key]
		if run == nil {
			run = &Run{ID: key, Started: r.StartedAt}
			byRun[key] = run
		}
		if r.StartedAt.Before(run.Started) {
			run.Started = r.StartedAt
		}
		run.Results = append(run.Results, r)
		seen[r.Scenario] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("resultsite: %w", err)
	}
	for _, run := range byRun {
		sort.Slice(run.Results, func(i, j int) bool { return run.Results[i].Scenario < run.Results[j].Scenario })
		modes := map[string]bool{}
		for _, r := range run.Results {
			modes[r.Mode.Vehicles+"/"+r.Mode.Targets] = true
			if r.Verdict == "pass" {
				run.Pass++
			} else {
				run.Fail++
			}
		}
		for m := range modes {
			run.Mode = m
		}
		if len(modes) > 1 {
			run.Mode = "mixed"
		}
		s.Runs = append(s.Runs, run)
	}
	sort.Slice(s.Runs, func(i, j int) bool { return s.Runs[i].Started.Before(s.Runs[j].Started) })
	for sc := range seen {
		s.Scenarios = append(s.Scenarios, sc)
	}
	sort.Strings(s.Scenarios)
	sort.Strings(s.Unreadable)
	return s, nil
}

// Verdict is a scenario's verdict in a run, "" when it was not run.
func (r *Run) Verdict(scenario string) string {
	for _, x := range r.Results {
		if x.Scenario == scenario {
			return x.Verdict
		}
	}
	return ""
}

// Pages renders the site: "index.html" and "runs/<run>.html".
func (s *Site) Pages() (map[string][]byte, error) {
	out := map[string][]byte{}
	var b bytes.Buffer
	if err := pages.ExecuteTemplate(&b, "index", s); err != nil {
		return nil, err
	}
	out["index.html"] = b.Bytes()
	for _, run := range s.Runs {
		var rb bytes.Buffer
		if err := pages.ExecuteTemplate(&rb, "run", run); err != nil {
			return nil, err
		}
		out["runs/"+PageName(run.ID)] = rb.Bytes()
	}
	return out, nil
}

// PageName is a run's page file name.
func PageName(run string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, run) + ".html"
}

// Write renders the site into dir.
func (s *Site) Write(dir string) error {
	ps, err := s.Pages()
	if err != nil {
		return err
	}
	for name, b := range ps {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // a public page
			return err
		}
	}
	return nil
}

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"page": PageName,
	"time": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") },
	"short": func(s string) string {
		if i := strings.Index(s, "@sha256:"); i >= 0 && len(s) > i+20 {
			return s[:i+20] + "..."
		}
		return s
	},
	"f": func(v float64) string { return fmt.Sprintf("%.3f", v) },
}).Parse(templates))

const head = `{{define "head"}}<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.}}</title><style>
:root{--bg:#fbfbfa;--fg:#1d1d1b;--muted:#6b6b66;--line:#e2e1dc;--pass:#1f7a3a;--fail:#b3261e;--none:#9a9a94;--card:#ffffff}
@media (prefers-color-scheme: dark){:root{--bg:#161615;--fg:#ecebe6;--muted:#a3a29b;--line:#34332f;--pass:#5cc27a;--fail:#f2867e;--none:#6b6a64;--card:#1f1f1d}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1100px;margin:0 auto;padding:24px 16px}h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 8px}
p.lead{color:var(--muted);margin:0 0 16px}table{border-collapse:collapse;width:100%;background:var(--card)}
th,td{border-bottom:1px solid var(--line);padding:6px 8px;text-align:left;vertical-align:top}th{color:var(--muted);font-weight:600;font-size:13px}
.wrap{overflow-x:auto}.pass{color:var(--pass);font-weight:600}.fail{color:var(--fail);font-weight:600}.none{color:var(--none)}
code{font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace}ul{margin:4px 0;padding-left:18px}a{color:inherit}
.card{border:1px solid var(--line);background:var(--card);border-radius:8px;padding:12px 14px;margin:12px 0}
</style></head><body><main>{{end}}`

const templates = head + `
{{define "index"}}{{template "head" "Scenario results"}}
<h1>Scenario results</h1><p class="lead">uspace-lab: every run under results/, and each scenario's verdict over the runs.</p>
<h2>Runs</h2><div class="wrap"><table><tr><th>Run</th><th>Started</th><th>Vehicles / targets</th><th>Pass</th><th>Fail</th></tr>
{{range .Runs}}<tr><td><a href="runs/{{page .ID}}">{{.ID}}</a></td><td>{{time .Started}}</td><td>{{.Mode}}</td><td class="pass">{{.Pass}}</td><td class="fail">{{.Fail}}</td></tr>{{end}}
</table></div>
<h2>Trend</h2><div class="wrap"><table><tr><th>Scenario</th>{{range .Runs}}<th><a href="runs/{{page .ID}}">{{.ID}}</a></th>{{end}}</tr>
{{$runs := .Runs}}{{range $sc := .Scenarios}}<tr><td><code>{{$sc}}</code></td>{{range $runs}}{{$v := .Verdict $sc}}<td class="{{if eq $v "pass"}}pass{{else if eq $v ""}}none{{else}}fail{{end}}">{{if eq $v ""}}not run{{else}}{{$v}}{{end}}</td>{{end}}</tr>{{end}}
</table></div>
{{if .Unreadable}}<h2>Not result/v1</h2><ul>{{range .Unreadable}}<li><code>{{.}}</code></li>{{end}}</ul>{{end}}
</main></body></html>{{end}}

{{define "run"}}{{template "head" .ID}}
<p><a href="../index.html">All runs</a></p><h1>{{.ID}}</h1><p class="lead">Started {{time .Started}}; {{.Mode}}; {{.Pass}} pass, {{.Fail}} fail.</p>
{{range .Results}}<div class="card"><h2 id="{{.Scenario}}"><span class="{{if eq .Verdict "pass"}}pass{{else}}fail{{end}}">{{.Verdict}}</span> <code>{{.Scenario}}</code></h2>
<p>{{.Title}}</p>{{if ne .Run $.ID}}<p class="lead">Run id in the file: <code>{{.Run}}</code></p>{{end}}<p class="lead">{{.Mode.Vehicles}} vehicles, {{.Mode.Targets}} targets{{if .Mode.Name}} ({{.Mode.Name}}){{end}}; t0 {{time .T0}}; policy {{.PolicyVersion}}. {{.Evidence}}</p>
{{if .Failures}}<p><strong>Failures</strong></p><ul>{{range .Failures}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Latency}}<p><strong>Latency (s, capture to observed)</strong></p><div class="wrap"><table><tr><th>System</th><th>n</th><th>p50</th><th>p95</th><th>max</th></tr>
{{range $k, $v := .Latency}}<tr><td>{{$k}}</td><td>{{$v.N}}</td><td>{{f $v.P50}}</td><td>{{f $v.P95}}</td><td>{{f $v.Max}}</td></tr>{{end}}</table></div>{{end}}
{{if .Operators}}<p><strong>Operator ledgers</strong></p><div class="wrap"><table><tr><th>Serial</th><th>Sent</th><th>Accepted</th><th>Refused</th><th>Dropped</th><th>Duplicate</th><th>Balanced</th></tr>
{{range .Operators}}<tr><td><code>{{.Serial}}</code></td><td>{{.Sent}}</td><td>{{.Accepted}}</td><td>{{.Refused}}</td><td>{{.Dropped}}</td><td>{{.Duplicate}}</td><td>{{.Balanced}}</td></tr>{{end}}</table></div>{{end}}
{{if .Receivers}}<p><strong>Receiver ledgers</strong></p><div class="wrap"><table><tr><th>Receiver</th><th>Observed</th><th>Sent</th><th>Accepted</th><th>Refused</th><th>Pending</th><th>Balanced</th></tr>
{{range .Receivers}}<tr><td><code>{{.ReceiverID}}</code></td><td>{{.Observed}}</td><td>{{.Sent}}</td><td>{{.Accepted}}</td><td>{{.Refused}}</td><td>{{.Pending}}</td><td>{{.Balanced}}</td></tr>{{end}}</table></div>{{end}}
{{if .Images}}<p><strong>Images</strong></p><ul>{{range $k, $v := .Images}}<li>{{$k}}: <code>{{short $v}}</code></li>{{end}}</ul>{{end}}
<p class="lead">lab {{.Commits.Lab}}{{if .Commits.LabDirty}} (dirty){{end}}, core {{.Commits.Core}}, {{.Commits.Go}}</p></div>{{end}}
</main></body></html>{{end}}`

// decode reads a result/v1 file; false for anything else.
func decode(b []byte) (*runner.Result, bool) {
	var r runner.Result
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, false
	}
	return &r, r.Format == runner.ResultFormat && r.Run != "" && r.Scenario != ""
}

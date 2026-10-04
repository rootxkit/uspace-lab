// Command conformance runs the lab's conformance suite against one
// system's conformance target and reports pass, fail or not applicable
// per requirement (docs/WORKPACKAGES/WP-L7.md, conformance/README.md).
//
//	conformance run --target conformance/targets/ansp.yaml [--env-file F] [--out DIR]
//	    [--qualifier-report F3411-DP=report.json ...] [--axe axe.json]
//	    [--sign-key deploy/local/signing-key.pem] [--baseline conformance/baseline/ansp.json]
//	    [--allow-incomplete]
//	conformance sign --report R --key K [--kid KID]
//	conformance verify --report R --jwks deploy/local/public/jwks.json
//	conformance gate --report R --baseline B
//	conformance baseline --report R --out B [--note ID=where-it-is-tracked ...]
//	conformance qualifier-render --target T --config f3411-dp.yaml --out DIR
//	conformance contract-check --system S --openapi F
//
// run exits 0 when the verdict is pass, 1 when a gate requirement
// failed (with --baseline: only on a regression against the baseline),
// 2 on a usage or configuration error, and 3 when the run is incomplete:
// a gate requirement was not applicable and no reviewed baseline records
// it so, or no gate requirement applied at all. An incomplete run is not
// a pass: an unpinned contract or an unset variable skips checks, and a
// run that checked nothing must not look like one that passed.
// --allow-incomplete accepts it (exit 0) when that is what the operator
// means. Nothing is passed that was not observed (E-04): a check that
// cannot run is reported as not applicable with its reason.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rootxkit/uspace-core/auth"

	"github.com/rootxkit/uspace-lab/conformance/a11y"
	"github.com/rootxkit/uspace-lab/conformance/ed318"
	"github.com/rootxkit/uspace-lab/conformance/national"
	"github.com/rootxkit/uspace-lab/conformance/policy"
	"github.com/rootxkit/uspace-lab/conformance/qualifier"
	"github.com/rootxkit/uspace-lab/conformance/report"
	"github.com/rootxkit/uspace-lab/conformance/result"
	"github.com/rootxkit/uspace-lab/conformance/target"
	"github.com/rootxkit/uspace-lab/internal/issuer"
)

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = "usage: conformance run|sign|verify|gate|baseline|qualifier-render|contract-check [flags] (see the package documentation)"

func dispatch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	var err error
	code := 0
	switch args[0] {
	case "run":
		code, err = cmdRun(args[1:], stdout)
	case "sign":
		err = cmdSign(args[1:], stdout)
	case "verify":
		err = cmdVerify(args[1:], stdout)
	case "gate":
		code, err = cmdGate(args[1:], stdout)
	case "baseline":
		err = cmdBaseline(args[1:], stdout)
	case "qualifier-render":
		err = cmdRender(args[1:], stdout)
	case "contract-check":
		err = cmdContractCheck(args[1:], stdout)
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "conformance:", err)
		var ue usageError
		if errors.As(err, &ue) || code == 0 {
			return 2
		}
	}
	return code
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// multi is a repeatable flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// suite is everything a run loads before it sends a request.
type suite struct {
	root  string
	tf    *target.File
	pol   *policy.Policy
	cat   *report.Catalogue
	inter report.InterUSS
	cov   *qualifier.Coverage
}

func loadSuite(root, targetFile, envFile string) (*suite, error) {
	extra := map[string]string{}
	if envFile != "" {
		m, err := target.ReadEnvFile(envFile)
		if err != nil {
			return nil, err
		}
		extra = m
	}
	tf, err := target.Load(targetFile, target.EnvLookup(extra))
	if err != nil {
		return nil, err
	}
	pol, err := policy.Load(filepath.Join(root, "conformance", "policy.yaml"))
	if err != nil {
		return nil, err
	}
	cat, err := report.LoadCatalogue(filepath.Join(root, "conformance", "requirements.yaml"))
	if err != nil {
		return nil, err
	}
	inter, err := report.ReadInterUSS(filepath.Join(root, "conformance", "uss_qualifier", "SOURCE"))
	if err != nil {
		return nil, err
	}
	cov, err := qualifier.LoadCoverage(filepath.Join(root, "conformance", "uss_qualifier", "coverage.yaml"))
	if err != nil {
		return nil, err
	}
	return &suite{root: root, tf: tf, pol: pol, cat: cat, inter: inter, cov: cov}, nil
}

// applies reports whether the catalogue applies req to the target.
func (s *suite) applies(req string) bool {
	r, ok := s.cat.Get(req)
	if !ok {
		return false
	}
	for _, sys := range r.Systems {
		if sys == s.tf.System {
			return true
		}
	}
	return false
}

func cmdRun(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	targetFile := fs.String("target", "", "the target file (conformance/targets/<name>.yaml)")
	envFile := fs.String("env-file", "", "KEY=VALUE lines resolving ${VAR} in the target file (the CISP's CONFORMANCE_ENV)")
	root := fs.String("lab-root", ".", "this repository's root")
	out := fs.String("out", filepath.Join("conformance", "report", "runs"), "where report/<run>/ is written")
	axe := fs.String("axe", "", "the axe results (conformance/axe) to fold in")
	signKey := fs.String("sign-key", os.Getenv("CONFORMANCE_SIGN_KEY"), "the lab issuer's signing-key.pem; empty: the report is not signed")
	signKID := fs.String("sign-kid", "", "the key's kid when the PEM has no Kid header")
	baseline := fs.String("baseline", "", "the baseline to gate on (exit 1 only on a regression)")
	timeout := fs.Duration("timeout", 30*time.Minute, "bound on the whole run")
	allowIncomplete := fs.Bool("allow-incomplete", false, "exit 0 on an incomplete run (gate requirements not applicable) instead of 3")
	var qreports multi
	fs.Var(&qreports, "qualifier-report", "REQUIREMENT=report.json of a uss_qualifier run (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2, usageError{err.Error()}
	}
	if *targetFile == "" {
		return 2, usageError{"run: --target is required"}
	}
	s, err := loadSuite(*root, *targetFile, *envFile)
	if err != nil {
		return 2, err
	}
	qr := map[string]string{}
	for _, q := range qreports {
		id, path, ok := strings.Cut(q, "=")
		if !ok {
			return 2, usageError{"--qualifier-report " + q + ": want REQUIREMENT=path"}
		}
		qr[id] = path
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelT := context.WithTimeout(ctx, *timeout)
	defer cancelT()

	started := time.Now().UTC()
	var outcomes []result.Outcome
	nat, contractInfo, err := s.national(ctx)
	if err != nil {
		return 2, err
	}
	outcomes = append(outcomes, nat...)
	quals, qo, err := s.qualifier(qr)
	if err != nil {
		return 2, err
	}
	outcomes = append(outcomes, qo...)
	ao, err := s.axe(*axe)
	if err != nil {
		return 2, err
	}
	outcomes = append(outcomes, ao...)

	rep, err := report.Build(s.cat, s.pol, report.Target{System: s.tf.System, Name: s.tf.Name, Role: s.tf.Role, BaseURL: s.tf.BaseURL}, outcomes)
	if err != nil {
		return 2, err
	}
	rep.Run = report.RunID(started, s.tf.Name)
	rep.StartedAt, rep.EndedAt = started, time.Now().UTC()
	rep.Commits = report.LabCommits(s.root)
	rep.Images = s.tf.Images
	rep.InterUSS = s.inter
	rep.Contract = contractInfo
	rep.Qualifier = quals
	path, _, err := rep.Write(*out)
	if err != nil {
		return 2, err
	}
	text := rep.Text()
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "report.txt"), []byte(text), 0o600); err != nil {
		return 2, err
	}
	_, _ = fmt.Fprint(stdout, text)
	_, _ = fmt.Fprintln(stdout, "report:", path)
	if *signKey != "" {
		d, err := report.Sign(path, *signKey, *signKID, time.Now())
		if err != nil {
			return 2, err
		}
		_, _ = fmt.Fprintf(stdout, "signed: %s (%s%s)\n", d, path, report.SignatureSuffix)
	} else {
		_, _ = fmt.Fprintln(stdout, "signed: no (no --sign-key)")
	}
	summary(text)
	var bl *report.Baseline
	if *baseline != "" {
		bl, err = report.ReadBaseline(*baseline)
		if err != nil {
			return 2, err
		}
		fsd, err := report.Gate(rep, bl)
		if err != nil {
			return 2, err
		}
		printFindings(stdout, fsd, *baseline)
		if report.Regressed(fsd) {
			return 1, nil
		}
	} else if rep.Summary.Verdict == report.VerdictFail {
		return 1, nil
	}
	return incomplete(stdout, report.Unreviewed(rep, bl), *baseline, *allowIncomplete), nil
}

// incomplete says which gate requirements were not checked and returns
// the exit status: 3, or 0 when the operator accepted it.
func incomplete(w io.Writer, ids []string, baseline string, allow bool) int {
	if len(ids) == 0 {
		return 0
	}
	where := "no baseline was given"
	if baseline != "" {
		where = baseline + " does not record them not applicable"
	}
	_, _ = fmt.Fprintf(w, "incomplete: %d gate requirements not checked (%s): %s\n", len(ids), where, strings.Join(ids, ", "))
	if allow {
		_, _ = fmt.Fprintln(w, "incomplete: accepted (--allow-incomplete)")
		return 0
	}
	_, _ = fmt.Fprintln(w, "incomplete: not a pass; the reasons are in the report. --allow-incomplete (CONFORMANCE_ALLOW_INCOMPLETE=1) accepts it")
	return 3
}

// national runs the contract tests and, for the CISP, the ED-318 tests.
func (s *suite) national(ctx context.Context) ([]result.Outcome, *report.Contract, error) {
	tf := s.tf
	natReqs := []string{national.ReqUnauthenticated, national.ReqWrongScope, national.ReqNotFound, national.ReqPrecondition, national.ReqInvalidBody, national.ReqSuccess, "REG-NOPII"}
	edReqs := []string{ed318.ReqSchema, ed318.ReqVertical, ed318.ReqApplicability, ed318.ReqVersioning, ed318.ReqChanges, ed318.ReqWebhook, ed318.ReqHeartbeat}
	skipAll := func(ids []string, why string) []result.Outcome {
		var out []result.Outcome
		for _, id := range ids {
			if s.applies(id) {
				out = append(out, result.Skipped(id, "target", tf.Name, why))
			}
		}
		return out
	}
	if !tf.HasNational() {
		return nil, nil, nil
	}
	openapi, schemas, ovPath := tf.ContractPaths(s.root)
	if _, statErr := os.Stat(openapi); statErr != nil {
		why := fmt.Sprintf("no contract to test against: %s is absent (the aggregate's mirror is unpinned and the target names no contract.openapi)", openapi)
		return append(skipAll(natReqs, why), skipAll(edReqs, why)...), nil, nil //nolint:nilerr // an absent contract is reported, not an error
	}
	ov, err := national.LoadOverrides(ovPath)
	if err != nil {
		return nil, nil, err
	}
	opts := national.LoadOptions{CommonDir: filepath.Join(s.root, "schemas", "common")}
	if st, err := os.Stat(schemas); err == nil && st.IsDir() {
		opts.SchemasDir = schemas
	}
	c, err := national.LoadContractFile(tf.System, openapi, ov, opts)
	if err != nil {
		return nil, nil, err
	}
	info := &report.Contract{File: openapi, SHA256: c.SHA256, Title: c.Title, Version: c.Version}
	prob, err := national.CompileProblem(filepath.Join(s.root, "schemas", "common"))
	if err != nil {
		return nil, nil, err
	}
	hc, hasCert, err := tf.HTTPClient()
	if err != nil {
		return nil, nil, err
	}
	creds, err := tf.Credentials(hc, hasCert)
	if err != nil {
		return nil, nil, err
	}
	r := &national.Runner{Contract: c, BaseURL: tf.BaseURL, Creds: creds, HTTP: hc, Fixtures: tf.Fixtures, Skip: ov.Skip, Problem: prob, Only: tf.Only}
	out := r.Run(ctx)
	if s.applies("REG-NOPII") {
		out = append(out, r.RunNoPII(ctx, []national.NoPIIRule{{Requirement: "REG-NOPII", Operation: "validateRegistry", Fields: s.pol.PersonalDataFields.Names}})...)
	}
	if tf.System != "cisp" {
		return out, info, nil
	}
	if tf.ED318 == nil {
		return append(out, skipAll(edReqs, "the target file has no ed318 section")...), info, nil
	}
	e := tf.ED318
	cfg := ed318.Config{
		Contract: c, BaseURL: tf.BaseURL, HTTP: hc, Problem: prob,
		ReaderToken: e.ReaderToken, AuthorityToken: e.AuthorityToken, ANSPToken: e.ANSPToken,
		Publish: e.Publishes(), JWKSURL: e.JWKSURL, IssuerURL: e.IssuerURL,
		WebhookListen: e.WebhookListen, WebhookURL: e.WebhookURL,
		Area: ed318.FixtureArea{
			CentreLatDeg: s.pol.ED318Fixture.CentreLatDeg, CentreLonDeg: s.pol.ED318Fixture.CentreLonDeg,
			HalfSideM: s.pol.ED318Fixture.HalfSideM, UpperAGLM: s.pol.ED318Fixture.UpperAGLM, UpperAMSLFt: s.pol.ED318Fixture.UpperAMSLFt,
		},
		ChangeNotification: time.Duration(s.pol.CISP.ChangeNotificationS * float64(time.Second)),
		StaleTolerance:     time.Duration(s.pol.CISP.StaleToleranceS * float64(time.Second)),
	}
	if e.AuthorityKeyFile != "" {
		key, kid, err := issuer.LoadKeyFile(e.AuthorityKeyFile, e.AuthorityKID)
		if err != nil {
			return nil, nil, fmt.Errorf("ed318.authority_key_file: %w", err)
		}
		cfg.AuthorityKey = &auth.SigningKey{KID: kid, Key: key}
	}
	suite, err := ed318.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	return append(out, suite.Run(ctx)...), info, nil
}

// qualifier imports the uss_qualifier reports given and says, for every
// F3411/F3548 requirement of the target that has none, why.
func (s *suite) qualifier(reports map[string]string) ([]report.Qualifier, []result.Outcome, error) {
	var qs []report.Qualifier
	var out []result.Outcome
	for id := range reports {
		if _, ok := s.cov.Requirements[id]; !ok {
			return nil, nil, usageError{"--qualifier-report " + id + ": not a requirement of coverage.yaml"}
		}
	}
	for _, id := range s.cov.IDs() {
		if !s.applies(id) {
			continue
		}
		cv := s.cov.Requirements[id]
		q := report.Qualifier{Config: cv.Config, Requirement: id}
		switch path, given := reports[id]; {
		case cv.None != "":
			q.Reason = cv.None
			out = append(out, result.Skipped(id, "uss_qualifier", id, cv.None))
		case given:
			if s.tf.Participant == "" {
				return nil, nil, usageError{"the target file names no participant for " + id}
			}
			run, err := qualifier.Import(path, id, s.tf.Participant)
			if err != nil {
				return nil, nil, err
			}
			if run.CommitHash != s.inter.Commit {
				return nil, nil, fmt.Errorf("%s: uss_qualifier ran at %s, the suite pins %s", path, run.CommitHash, s.inter.Commit)
			}
			q.Ran, q.ReportSHA256, q.CommitHash = true, run.Digest, run.CommitHash
			out = append(out, run.Outcomes...)
		case s.tf.Interfaces[cv.Needs] == "":
			q.Reason = "the target exposes no InterUSS " + cv.Needs + " interface (target file interfaces)"
			out = append(out, result.Skipped(id, "uss_qualifier", id, q.Reason))
		default:
			q.Reason = "uss_qualifier was not run in this invocation (conformance/uss_qualifier/run-qualifier.sh)"
			out = append(out, result.Skipped(id, "uss_qualifier", id, q.Reason))
		}
		qs = append(qs, q)
	}
	return qs, out, nil
}

func (s *suite) axe(path string) ([]result.Outcome, error) {
	if !s.applies(a11y.Requirement) {
		return nil, nil
	}
	if path == "" {
		why := "axe was not run in this invocation (conformance/axe)"
		if len(s.tf.Pages) == 0 {
			why = "the target file names no public page"
		}
		return []result.Outcome{result.Skipped(a11y.Requirement, "axe", s.tf.Name, why)}, nil
	}
	_, out, err := a11y.Import(path)
	return out, err
}

func printFindings(w io.Writer, fs []report.Finding, baseline string) {
	if len(fs) == 0 {
		_, _ = fmt.Fprintf(w, "gate: every requirement as in %s\n", baseline)
		return
	}
	for _, f := range fs {
		mark := "      "
		if f.Regression {
			mark = "REGRESSION"
		}
		_, _ = fmt.Fprintf(w, "gate: %s %-20s %s -> %s: %s\n", mark, f.Requirement, orDash(string(f.Was)), orDash(string(f.Now)), f.Why)
	}
	if report.Regressed(fs) {
		_, _ = fmt.Fprintf(w, "gate: regressed against %s\n", baseline)
	} else {
		_, _ = fmt.Fprintf(w, "gate: no regression against %s\n", baseline)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// summary appends the text to the GitHub step summary when there is one.
func summary(text string) {
	p := os.Getenv("GITHUB_STEP_SUMMARY")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // the runner names it
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "```\n%s```\n", text)
}

func cmdSign(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	rep := fs.String("report", "", "report.json")
	key := fs.String("key", "", "the lab issuer's signing-key.pem")
	kid := fs.String("kid", "", "the kid when the PEM has no Kid header")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *rep == "" || *key == "" {
		return usageError{"sign: --report and --key are required"}
	}
	d, err := report.Sign(*rep, *key, *kid, time.Now())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "signed %s: %s\n", *rep, d)
	return nil
}

func cmdVerify(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	rep := fs.String("report", "", "report.json")
	jwks := fs.String("jwks", "", "the lab issuer's public/jwks.json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *rep == "" || *jwks == "" {
		return usageError{"verify: --report and --jwks are required"}
	}
	v, err := report.Verify(*rep, *jwks)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "verified %s: %s, kid %s, signed %s\n", *rep, v.Digest, v.KID, v.IssuedAt.UTC().Format(time.RFC3339))
	return nil
}

func cmdGate(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	rep := fs.String("report", "", "report.json")
	bl := fs.String("baseline", "", "conformance/baseline/<target>.json")
	if err := fs.Parse(args); err != nil {
		return 2, usageError{err.Error()}
	}
	if *rep == "" || *bl == "" {
		return 2, usageError{"gate: --report and --baseline are required"}
	}
	r, _, err := report.Read(*rep)
	if err != nil {
		return 2, err
	}
	b, err := report.ReadBaseline(*bl)
	if err != nil {
		return 2, err
	}
	fsd, err := report.Gate(r, b)
	if err != nil {
		return 2, err
	}
	printFindings(stdout, fsd, *bl)
	if report.Regressed(fsd) {
		return 1, nil
	}
	return 0, nil
}

func cmdBaseline(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	rep := fs.String("report", "", "the reviewed report.json")
	out := fs.String("out", "", "conformance/baseline/<target>.json")
	var notes multi
	fs.Var(&notes, "note", "ID=where the known failure is tracked (repeatable)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *rep == "" || *out == "" {
		return usageError{"baseline: --report and --out are required"}
	}
	r, raw, err := report.Read(*rep)
	if err != nil {
		return err
	}
	nm := map[string]string{}
	for _, n := range notes {
		id, text, ok := strings.Cut(n, "=")
		if !ok || text == "" {
			return usageError{"--note " + n + ": want ID=text"}
		}
		nm[id] = text
	}
	bl, err := report.NewBaseline(r, report.Digest(raw), nm)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, bl); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "baseline %s from %s (%d requirements)\n", *out, r.Run, len(bl.Requirements))
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, 0x0a), 0o600)
}

func cmdRender(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("qualifier-render", flag.ContinueOnError)
	root := fs.String("lab-root", ".", "this repository's root")
	config := fs.String("config", "", "the configuration under conformance/uss_qualifier/")
	out := fs.String("out", "", "where the rendered configuration and library go")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *config == "" || *out == "" {
		return usageError{"qualifier-render: --config and --out are required"}
	}
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "QUALIFIER_") {
			vars[k] = v
		}
	}
	p, err := qualifier.Render(filepath.Join(*root, "conformance", "uss_qualifier"), *out, *config, vars)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, p)
	return nil
}

func cmdContractCheck(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("contract-check", flag.ContinueOnError)
	root := fs.String("lab-root", ".", "this repository's root")
	system := fs.String("system", "", "ansp, authority, cisp or ussp")
	openapi := fs.String("openapi", "", "the system's api/openapi.yaml")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *system == "" || *openapi == "" {
		return usageError{"contract-check: --system and --openapi are required"}
	}
	ov, err := national.LoadOverrides(filepath.Join(*root, "conformance", "national", "contracts", *system+".yaml"))
	if err != nil {
		return err
	}
	c, err := national.LoadContractFile(*system, *openapi, ov, national.LoadOptions{CommonDir: filepath.Join(*root, "schemas", "common")})
	if err != nil {
		return err
	}
	kinds := map[string]int{}
	for _, op := range c.Ops {
		switch {
		case op.Auth.Public:
			kinds["public"]++
		case op.Auth.Special != "":
			kinds["special"]++
		}
		if op.Auth.Token != nil {
			kinds["token"]++
		}
		if op.Auth.Session {
			kinds["session"]++
		}
	}
	keys := make([]string, 0, len(kinds))
	for k := range kinds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, kinds[k]))
	}
	_, _ = fmt.Fprintf(stdout, "%s %s %s (%s): %d operations classified: %s\n", *system, c.Title, c.Version, c.SHA256, len(c.Ops), strings.Join(parts, ", "))
	return nil
}

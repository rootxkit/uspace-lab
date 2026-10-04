package national

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/rootxkit/uspace-lab/conformance/result"
)

// CheckNoPII is the check that an answer carries no personal data.
const CheckNoPII = "no_pii"

// NoPIIRule names an operation whose answer must never carry personal
// data (the registry validation of 02 F8 and 06 §5: status only, never
// a name, address, phone or e-mail) and the member names that are
// personal data (conformance/policy.yaml).
type NoPIIRule struct {
	Requirement string
	Operation   string
	Fields      []string
}

// RunNoPII runs the rules whose operation the contract has. A rule for
// an operation the contract lacks is reported as not applicable.
func (r *Runner) RunNoPII(ctx context.Context, rules []NoPIIRule) []result.Outcome {
	var out []result.Outcome
	for _, rule := range rules {
		op := r.Contract.Op(rule.Operation)
		if op == nil {
			out = append(out, result.Skipped(rule.Requirement, CheckNoPII, rule.Operation, "the contract has no operation "+rule.Operation))
			continue
		}
		out = append(out, r.noPII(ctx, op, rule))
	}
	return out
}

func (r *Runner) noPII(ctx context.Context, op *Operation, rule NoPIIRule) result.Outcome {
	subj := op.Subject()
	req := r.newRequest()
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		v, _, ok := r.Contract.KnownValue(p, r.Fixtures)
		if !ok {
			return result.Skipped(rule.Requirement, CheckNoPII, subj, "the target file names no existing "+p.Name)
		}
		req.path[p.Name] = v
	}
	// The registry is asked about something: every query parameter with
	// a fixture or an example is sent, required or not.
	for _, p := range op.Params {
		if p.In != "query" {
			continue
		}
		if v, _, ok := r.Contract.KnownValue(p, r.Fixtures); ok {
			req.query.Set(p.Name, v)
		} else if p.Required {
			return result.Skipped(rule.Requirement, CheckNoPII, subj, "no value for required query parameter "+p.Name)
		}
	}
	if why := r.fillRequired(op, req, true); why != "" {
		return result.Skipped(rule.Requirement, CheckNoPII, subj, why)
	}
	if why := r.authorize(ctx, op, req); why != "" {
		return result.Skipped(rule.Requirement, CheckNoPII, subj, why)
	}
	obs, err := r.send(ctx, op, req, true)
	if err != nil {
		return result.Failed(rule.Requirement, CheckNoPII, subj, 0, "no answer: "+err.Error(), "")
	}
	if obs.status != http.StatusOK {
		return result.Failed(rule.Requirement, CheckNoPII, subj, obs.status, fmt.Sprintf("answered %d, not 200: nothing to inspect", obs.status), problemType(obs.body))
	}
	var doc any
	if err := json.Unmarshal(obs.body, &doc); err != nil {
		return result.Failed(rule.Requirement, CheckNoPII, subj, obs.status, "the answer is not JSON", "")
	}
	deny := map[string]bool{}
	for _, f := range rule.Fields {
		deny[strings.ToLower(f)] = true
	}
	var found []string
	walkKeys(doc, "$", func(path, key string) {
		if deny[strings.ToLower(key)] {
			found = append(found, path)
		}
	})
	sort.Strings(found)
	if len(found) > 0 {
		return result.Failed(rule.Requirement, CheckNoPII, subj, obs.status, "the answer carries personal data", strings.Join(found, ", "))
	}
	return result.Passed(rule.Requirement, CheckNoPII, subj, obs.status, fmt.Sprintf("no member of %d personal-data names", len(rule.Fields)))
}

func walkKeys(v any, path string, fn func(path, key string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			p := path + "." + k
			fn(p, k)
			walkKeys(x, p, fn)
		}
	case []any:
		for i, x := range t {
			walkKeys(x, fmt.Sprintf("%s[%d]", path, i), fn)
		}
	}
}

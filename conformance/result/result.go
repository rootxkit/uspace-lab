// Package result holds what every part of the conformance suite reports
// (docs/WORKPACKAGES/WP-L7.md): one Outcome per check it ran, each pass,
// fail or not applicable, and nothing it did not observe (LESSONS E-04).
// The national contract tests, the ED-318 publication tests, the
// uss_qualifier import and the axe import all produce Outcomes; the
// report package folds them into one status per requirement.
package result

import (
	"fmt"
	"sort"
)

// Status is the verdict of one check or one requirement.
type Status string

// The three statuses (WP-L7: "pass, fail or not-applicable per
// requirement"). A not-applicable outcome always carries its reason.
const (
	Pass          Status = "pass"
	Fail          Status = "fail"
	NotApplicable Status = "not_applicable"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool {
	switch s {
	case Pass, Fail, NotApplicable:
		return true
	}
	return false
}

// Outcome is one check as it ran against the target.
type Outcome struct {
	// Requirement is the catalogue id the check decides
	// (conformance/requirements.yaml).
	Requirement string `json:"requirement"`
	// Check names the check kind ("unauthenticated", "ed318.etag", ...).
	Check string `json:"check"`
	// Subject is what the check exercised: an operation id with its
	// method and path, a dataset, a qualifier check name, a page URL.
	Subject string `json:"subject"`
	Status  Status `json:"status"`
	// Reason says why a check did not apply or what failed. Required
	// for not_applicable and fail.
	Reason string `json:"reason,omitempty"`
	// HTTPStatus is the status the target answered, 0 when no request
	// was made.
	HTTPStatus int `json:"http_status,omitempty"`
	// Detail is the observed evidence (a validator message, a header).
	Detail string `json:"detail,omitempty"`
}

// Validate refuses an outcome without a requirement, check or valid
// status, and a fail or not_applicable without a reason.
func (o Outcome) Validate() error {
	if o.Requirement == "" || o.Check == "" {
		return fmt.Errorf("outcome %q/%q: requirement and check are required", o.Requirement, o.Check)
	}
	if !o.Status.Valid() {
		return fmt.Errorf("outcome %s/%s: status %q", o.Requirement, o.Check, o.Status)
	}
	if o.Status != Pass && o.Reason == "" {
		return fmt.Errorf("outcome %s/%s %s: a %s needs a reason", o.Requirement, o.Check, o.Subject, o.Status)
	}
	return nil
}

// Passed is an outcome that passed.
func Passed(req, check, subject string, httpStatus int, detail string) Outcome {
	return Outcome{Requirement: req, Check: check, Subject: subject, Status: Pass, HTTPStatus: httpStatus, Detail: detail}
}

// Failed is an outcome that failed for reason.
func Failed(req, check, subject string, httpStatus int, reason, detail string) Outcome {
	return Outcome{Requirement: req, Check: check, Subject: subject, Status: Fail, HTTPStatus: httpStatus, Reason: reason, Detail: detail}
}

// Skipped is an outcome that did not apply, for reason.
func Skipped(req, check, subject, reason string) Outcome {
	return Outcome{Requirement: req, Check: check, Subject: subject, Status: NotApplicable, Reason: reason}
}

// Fold is the status of a requirement from its outcomes: fail when any
// failed, pass when at least one passed and none failed, not applicable
// otherwise (no outcome at all, or only not-applicable ones). The
// reasons are the distinct reasons of the deciding outcomes, sorted.
func Fold(outcomes []Outcome) (Status, []string) {
	var failed, passed bool
	reasons := map[string]bool{}
	for _, o := range outcomes {
		switch o.Status {
		case Fail:
			failed = true
		case Pass:
			passed = true
		case NotApplicable:
		}
	}
	want := NotApplicable
	switch {
	case failed:
		want = Fail
	case passed:
		want = Pass
	}
	for _, o := range outcomes {
		if o.Status == want && o.Reason != "" {
			reasons[o.Reason] = true
		}
	}
	out := make([]string, 0, len(reasons))
	for r := range reasons {
		out = append(out, r)
	}
	sort.Strings(out)
	return want, out
}

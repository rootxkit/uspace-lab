package issuer

import (
	"encoding/json"
	"net/http"
)

// RefusalError slugs: the last path segment of the problem/v1 type and the
// name of the counter the refusal increments (refused_<slug>).
const (
	SlugUnauthenticated   = "unauthenticated"
	SlugForbiddenScope    = "forbidden_scope"
	SlugForbiddenAudience = "forbidden_audience"
	SlugInvalidRequest    = "invalid_request"
	SlugMethodNotAllowed  = "method_not_allowed"
	SlugNotFound          = "not_found"
)

// ProblemTypeBase prefixes every problem type (schemas/common/problem/v1).
const ProblemTypeBase = "https://schemas.uspace.ge/problems/"

// RefusalError is a refused request: the slug and the field at fault.
type RefusalError struct {
	Slug   string
	Field  string
	Reason string
}

func (r *RefusalError) Error() string { return r.Slug + ": " + r.Field + ": " + r.Reason }

func refuse(slug, field, reason string) *RefusalError {
	return &RefusalError{Slug: slug, Field: field, Reason: reason}
}

// status maps a slug to its HTTP status.
func (r *RefusalError) status() int {
	switch r.Slug {
	case SlugUnauthenticated:
		return http.StatusUnauthorized
	case SlugForbiddenScope, SlugForbiddenAudience:
		return http.StatusForbidden
	case SlugMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case SlugNotFound:
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

var titles = map[string]string{
	SlugUnauthenticated:   "The client is not authenticated",
	SlugForbiddenScope:    "A requested scope is not granted to this client",
	SlugForbiddenAudience: "The audience is not allowed for a requested national scope",
	SlugInvalidRequest:    "The token request is malformed",
	SlugMethodNotAllowed:  "Method not allowed",
	SlugNotFound:          "Not found",
}

// problem is the problem/v1 body.
type problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Errors []fieldError `json:"errors"`
}

type fieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func writeProblem(w http.ResponseWriter, r *RefusalError) {
	status := r.status()
	body := problem{
		Type:   ProblemTypeBase + r.Slug,
		Title:  titles[r.Slug],
		Status: status,
		Detail: r.Reason,
		Errors: []fieldError{{Field: r.Field, Reason: r.Reason}},
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="lab-issuer"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

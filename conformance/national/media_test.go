package national

import (
	"testing"
)

const mediaContract = `openapi: 3.1.0
info: {title: media, version: "1"}
paths:
  /metrics:
    get:
      operationId: getMetrics
      security: []
      responses:
        "200":
          description: Prometheus text.
          content:
            text/plain:
              schema: {type: string, pattern: "^#"}
  /thing:
    get:
      operationId: getThing
      security: []
      responses:
        "200":
          description: A thing.
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties: {id: {type: string}}
`

// A text/plain response is checked as the string its schema describes.
// It used to be parsed as JSON, so the Prometheus text the authority's
// and the ANSP's /metrics answer failed NAT-SUCCESS with "not JSON"
// although it is exactly what their contracts declare. A JSON response
// is still parsed and judged as JSON, and a text body the schema
// refuses is still refused.
func TestValidateResponseTextPlain(t *testing.T) {
	c, err := LoadContract("media", []byte(mediaContract), Overrides{System: "media"}, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	metrics, thing := c.Op("getMetrics"), c.Op("getThing")
	if metrics == nil || thing == nil {
		t.Fatal("operations not loaded")
	}
	prom := []byte("# TYPE auth_no_credential counter\nauth_no_credential 0\n")
	for _, ct := range []string{"text/plain", "text/plain; version=0.0.4; charset=utf-8"} {
		if err := c.ValidateResponse(metrics, 200, ct, prom); err != nil {
			t.Errorf("Prometheus text as %s refused: %v", ct, err)
		}
	}
	if err := c.ValidateResponse(metrics, 200, "text/plain", []byte("auth_no_credential 0\n")); err == nil {
		t.Error("a text body the schema's pattern refuses was accepted")
	}
	if err := c.ValidateResponse(thing, 200, "application/json", []byte(`{"id":"a"}`)); err != nil {
		t.Errorf("a valid JSON body refused: %v", err)
	}
	for _, body := range []string{`{}`, `# not json`} {
		if err := c.ValidateResponse(thing, 200, "application/json; charset=utf-8", []byte(body)); err == nil {
			t.Errorf("JSON body %q accepted", body)
		}
	}
}

package simop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rootxkit/uspace-lab/internal/wire"
)

// IntentRequest is intent/request/v1 (uspace-ussp schemas/intent/
// request/v1, the IntentRequest component): the ten Annex IV items.
type IntentRequest struct {
	ClientRef                string            `json:"client_ref"`
	UASSerial                string            `json:"uas_serial"`
	Mode                     string            `json:"mode"`
	FlightType               string            `json:"flight_type"`
	Category                 string            `json:"category"`
	Subcategory              string            `json:"subcategory,omitempty"`
	ClassLabel               string            `json:"class_label,omitempty"`
	Volumes                  []Volume4D        `json:"volumes"`
	IdentificationTechnology string            `json:"identification_technology"`
	ConnectivityMethods      []string          `json:"connectivity_methods"`
	EnduranceS               int               `json:"endurance_s"`
	LossOfC2Procedure        string            `json:"loss_of_c2_procedure"`
	OperatorReg              string            `json:"operator_reg"`
	Takeoff                  *Point            `json:"takeoff,omitempty"`
	Landing                  *Point            `json:"landing,omitempty"`
	Contingency              IntentContingency `json:"contingency"`
	EmergencyContactRef      string            `json:"emergency_contact_ref"`
}

// IntentContingency is the contingency member.
type IntentContingency struct {
	Procedure string `json:"procedure"`
}

// Point is IntentPoint.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Volume4D is IntentVolume4D with a circle outline.
type Volume4D struct {
	Volume    Volume3D   `json:"volume"`
	TimeStart IntentTime `json:"time_start"`
	TimeEnd   IntentTime `json:"time_end"`
}

// Volume3D is the volume member: a circle or a polygon outline.
type Volume3D struct {
	OutlineCircle  *Circle        `json:"outline_circle,omitempty"`
	OutlinePolygon *Polygon       `json:"outline_polygon,omitempty"`
	AltitudeLower  IntentAltitude `json:"altitude_lower"`
	AltitudeUpper  IntentAltitude `json:"altitude_upper"`
}

// Circle is outline_circle.
type Circle struct {
	Center Point  `json:"center"`
	Radius Radius `json:"radius"`
}

// Polygon is outline_polygon: three or more vertices, the last not
// repeating the first (F3548 Polygon).
type Polygon struct {
	Vertices []Point `json:"vertices"`
}

// Radius is the circle radius in metres.
type Radius struct {
	Value float64 `json:"value"`
	Units string  `json:"units"`
}

// IntentTime is IntentTime (format RFC3339).
type IntentTime struct {
	Value  string `json:"value"`
	Format string `json:"format"`
}

// IntentAltitude is IntentAltitude (reference W84, units M).
type IntentAltitude struct {
	Value     float64 `json:"value"`
	Reference string  `json:"reference"`
	Units     string  `json:"units"`
}

// Decision is the part of intent/decision/v1 the lab reads.
type Decision struct {
	IntentID            string  `json:"intent_id"`
	Version             int     `json:"version"`
	Decision            string  `json:"decision"`
	State               string  `json:"state"`
	AuthorisationNumber *string `json:"authorisation_number"`
	Conflicts           []any   `json:"conflicts"`
}

// Intents files and changes intents for one operator client.
type Intents struct {
	BaseURL string
	Tokens  TokenSource
	HTTP    *http.Client
}

// File posts an intent and returns the decision (201 or 200).
func (in *Intents) File(ctx context.Context, r IntentRequest) (Decision, error) {
	var d Decision
	status, body, err := in.do(ctx, http.MethodPost, "/v1/intents", r)
	if err != nil {
		return d, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return d, fmt.Errorf("simop: intent answered %d: %s", status, shortBody(body))
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return d, fmt.Errorf("simop: decision: %w", err)
	}
	return d, nil
}

// Change patches an intent: activate or end.
func (in *Intents) Change(ctx context.Context, id, action string) (Decision, error) {
	var d Decision
	status, body, err := in.do(ctx, http.MethodPatch, "/v1/intents/"+id, map[string]string{"action": action})
	if err != nil {
		return d, err
	}
	if status != http.StatusOK {
		return d, fmt.Errorf("simop: %s answered %d: %s", action, status, shortBody(body))
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return d, fmt.Errorf("simop: decision: %w", err)
	}
	return d, nil
}

func (in *Intents) do(ctx context.Context, method, path string, v any) (int, []byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return 0, nil, fmt.Errorf("simop: %w", err)
	}
	tok, err := in.Tokens.Token(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("simop: token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(in.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, fmt.Errorf("simop: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	hc := in.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("simop: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxFrameBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("simop: %w", err)
	}
	return resp.StatusCode, body, nil
}

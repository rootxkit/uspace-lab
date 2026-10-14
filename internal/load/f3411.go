package load

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// Bounds of the F3411 display-provider polls (E-10).
const (
	maxDPViews       = 1000
	maxViewSideM     = 4949 // F3411 NetMaxDisplayAreaDiagonalKm 7: a square's side
	spRequestTimeout = 10 * time.Second
	spMaxBody        = 4 << 20
)

// DPPolls are the tier's F3411 display-provider polls of the USSP (05
// §1: the authority's DP views, one poll a second per active view).
type DPPolls struct {
	// Views is how many views are polled (05 §1: 10 / 50 / 200 at 100 /
	// 1000 / 5000 drones), the most populated tiles of the fleet first.
	Views int `yaml:"views" json:"views"`
	// ViewSideM is a view's side: F3411 bounds a view's diagonal
	// (NetMaxDisplayAreaDiagonalKm, 7 km), so the side is at most 4.9 km.
	ViewSideM float64 `yaml:"view_side_m" json:"view_side_m"`
	// PollMS is the period of each view's poll (05 §1: 1 Hz).
	PollMS int `yaml:"poll_ms" json:"poll_ms"`
}

func (d *DPPolls) validate() error {
	switch {
	case d.Views < 1 || d.Views > maxDPViews:
		return fmt.Errorf("dp.views %d is not in [1, %d]", d.Views, maxDPViews)
	case !(d.ViewSideM > 0 && d.ViewSideM <= maxViewSideM):
		return fmt.Errorf("dp.view_side_m %v is not in (0, %d] (F3411: a view's diagonal is at most 7 km)", d.ViewSideM, maxViewSideM)
	case d.PollMS < 100 || d.PollMS > 60_000:
		return fmt.Errorf("dp.poll_ms %d is not in [100, 60000]", d.PollMS)
	}
	return nil
}

// view is a DP view: [lat_lo, lng_lo, lat_hi, lng_hi], as F3411's view
// parameter orders its two corners.
type view [4]float64

func (v view) param() string {
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', 7, 64) }
	return f(v[0]) + "," + f(v[1]) + "," + f(v[2]) + "," + f(v[3])
}

// dpViews tiles the fleet's area in squares of side, and keeps the n
// tiles that hold the most aircraft (their walks' centres).
func (fl *Fleet) dpViews(side float64, n int) []view {
	type tile struct {
		i, j  int
		count int
	}
	tiles := map[[2]int]*tile{}
	for _, a := range fl.Aircraft {
		k := [2]int{int(math.Floor(a.centre.NorthM / side)), int(math.Floor(a.centre.EastM / side))}
		if tiles[k] == nil {
			tiles[k] = &tile{i: k[0], j: k[1]}
		}
		tiles[k].count++
	}
	list := make([]*tile, 0, len(tiles))
	for _, t := range tiles {
		list = append(list, t)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].count != list[b].count {
			return list[a].count > list[b].count
		}
		if list[a].i != list[b].i {
			return list[a].i < list[b].i
		}
		return list[a].j < list[b].j
	})
	out := make([]view, 0, min(n, len(list)))
	for _, t := range list[:min(n, len(list))] {
		sw := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: float64(t.i) * side, EastM: float64(t.j) * side})
		ne := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: float64(t.i+1) * side, EastM: float64(t.j+1) * side})
		out = append(out, view{sw.LatDeg, sw.LonDeg, ne.LatDeg, ne.LonDeg})
	}
	return out
}

// spPoller is the lab as an F3411 display provider: it polls the USSP's
// GET /uss/flights for every view at the tier's rate, one request in
// flight per view, and asks the details of one flight of each answer
// (round robin), timing both.
type spPoller struct {
	base   string
	tokens *oauth.ClientCredentials
	views  []view
	period time.Duration
	hc     *http.Client

	mu        sync.Mutex
	flights   *Histogram
	details   *Histogram
	polls     uint64
	statuses  map[int]uint64
	failures  uint64
	lastErr   string
	shown     uint64 // flights returned, summed over the answers
	next      int
	detailsOK uint64
}

func newSPPoller(base string, tokens *oauth.ClientCredentials, views []view, period time.Duration) *spPoller {
	return &spPoller{base: base, tokens: tokens, views: views, period: period, hc: &http.Client{Timeout: spRequestTimeout},
		flights: NewHistogram(), details: NewHistogram(), statuses: map[int]uint64{}}
}

// run polls until ctx ends; each view on its own ticker.
func (p *spPoller) run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, v := range p.views {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Spread the views over one period.
			start := time.NewTimer(p.period * time.Duration(i) / time.Duration(len(p.views)))
			select {
			case <-ctx.Done():
				start.Stop()
				return
			case <-start.C:
			}
			tk := time.NewTicker(p.period)
			defer tk.Stop()
			for {
				p.poll(ctx, v)
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
				}
			}
		}()
	}
	wg.Wait()
}

func (p *spPoller) get(ctx context.Context, path string) (int, []byte, time.Duration, error) {
	tok, err := p.tokens.Token(ctx)
	if err != nil {
		return 0, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	t0 := time.Now()
	resp, err := p.hc.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, spMaxBody))
	took := time.Since(t0)
	if err != nil {
		return resp.StatusCode, nil, took, err
	}
	return resp.StatusCode, b, took, nil
}

func (p *spPoller) poll(ctx context.Context, v view) {
	status, body, took, err := p.get(ctx, "/uss/flights?view="+url.QueryEscape(v.param())+"&recent_positions_duration=0")
	if ctx.Err() != nil {
		return
	}
	p.mu.Lock()
	p.polls++
	p.statuses[status]++
	if err != nil || status != http.StatusOK {
		p.failures++
		if err != nil {
			p.lastErr = err.Error()
		} else {
			p.lastErr = fmt.Sprintf("GET /uss/flights answered %d", status)
		}
		p.mu.Unlock()
		return
	}
	p.flights.Add(took)
	var ans struct {
		Flights []struct {
			ID string `json:"id"`
		} `json:"flights"`
	}
	if json.Unmarshal(body, &ans) != nil {
		p.failures++
		p.lastErr = "GET /uss/flights answered a body that is not a flights response"
		p.mu.Unlock()
		return
	}
	p.shown += uint64(len(ans.Flights))
	id := ""
	if len(ans.Flights) > 0 {
		id = ans.Flights[p.next%len(ans.Flights)].ID
		p.next++
	}
	p.mu.Unlock()
	if id == "" {
		return
	}
	status, _, took, err = p.get(ctx, "/uss/flights/"+url.PathEscape(id)+"/details")
	if ctx.Err() != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses[status]++
	if err != nil || status != http.StatusOK {
		p.failures++
		if err != nil {
			p.lastErr = err.Error()
		} else {
			p.lastErr = fmt.Sprintf("GET /uss/flights/{id}/details answered %d", status)
		}
		return
	}
	p.detailsOK++
	p.details.Add(took)
}

// F3411Polls is what the report says of the DP polls.
type F3411Polls struct {
	Views     int               `json:"views"`
	Polls     uint64            `json:"polls"`
	Flights   uint64            `json:"flights_returned"`
	DetailsOK uint64            `json:"details_answered"`
	Failures  uint64            `json:"failures"`
	Statuses  map[string]uint64 `json:"statuses"`
	LastError string            `json:"last_error,omitempty"`
}

// observe turns the polls into the F3411 SP rows: a latency is measured
// only when the answers carried the fleet's flights (an empty answer
// times nothing the load is about), and a poll the SP did not answer
// with 200 is a run error.
func (p *spPoller) observe(obs map[string]Observation, minSamples int) (F3411Polls, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := F3411Polls{Views: len(p.views), Polls: p.polls, Flights: p.shown, DetailsOK: p.detailsOK, Failures: p.failures, LastError: p.lastErr, Statuses: map[string]uint64{}}
	for k, v := range p.statuses {
		st.Statuses[strconv.Itoa(k)] = v
	}
	switch {
	case p.polls == 0:
		obs["f3411_sp_flights_s"] = notMeasured("no DP poll was answered")
	case p.shown == 0:
		obs["f3411_sp_flights_s"] = notMeasured(fmt.Sprintf("%d polls, none returned a flight: the SP served none of the fleet", p.polls))
	default:
		obs["f3411_sp_flights_s"] = p.flights.Observe(minSamples)
	}
	if p.detailsOK > 0 {
		obs["f3411_details_s"] = p.details.Observe(1)
	} else {
		obs["f3411_details_s"] = notMeasured("no details request was answered (no poll returned a flight)")
	}
	fail := ""
	if p.failures > 0 {
		fail = fmt.Sprintf("F3411: %d of the SP requests failed (statuses %v; last: %s)", p.failures, st.Statuses, p.lastErr)
	}
	return st, fail
}

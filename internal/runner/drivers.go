package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rootxkit/uspace-core/auth"
	"github.com/rootxkit/uspace-core/core"

	"github.com/rootxkit/uspace-lab/internal/simfeed"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// samplePeriod is the synthetic vehicles' rate, MAVProxy's --streamrate
// default of run_sitl.sh (4 Hz).
const samplePeriod = 250 * time.Millisecond

func (r *run) startVehicles(ctx context.Context, wg *sync.WaitGroup) error {
	if r.opt.Vehicles == VehiclesSITL {
		return r.startSITL(ctx, wg)
	}
	for i := range r.sc.Aircraft {
		a := r.sc.Aircraft[i]
		plan := r.comp.Plans[a.Name]
		opt := vehicle.DefaultSynthOptions()
		opt.MarkAltInvalid = a.MarkAltInvalid
		opt.Seed = uint64(a.Sysid) * 7919
		v := vehicle.NewSynth(r.lab.Home(a.Sysid), *plan, opt)
		if len(plan.Steps) == 0 {
			r.mu.Lock()
			r.done[a.Name] = true
			r.mu.Unlock()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(samplePeriod)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-t.C:
					s, evs := v.Step(now.UTC())
					r.bus.Publish(s)
					r.recordSteps(a.Name, evs, v.Done())
				}
			}
		}()
	}
	return nil
}

func (r *run) recordSteps(name string, evs []vehicle.StepEvent, done bool) {
	if len(evs) == 0 && !done {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range evs {
		r.steps = append(r.steps, e)
		if e.State == vehicle.StepFailed {
			r.stepFails = append(r.stepFails, fmt.Sprintf("%s step %d (%s): %s", name, e.Step, e.Action, e.Reason))
		}
	}
	if done {
		r.done[name] = true
	}
}

// startSITL starts, per aircraft, the receive-only reader and the
// harness (sim/fly.py, the only commander) with the plan on stdin. Their
// stderr goes to the run's log directory.
func (r *run) startSITL(ctx context.Context, wg *sync.WaitGroup) error {
	logDir := filepath.Join(os.TempDir(), "uspace-lab-"+r.opt.Run)
	if r.opt.OutDir != "" {
		logDir = filepath.Join(r.opt.OutDir, "logs", r.sc.ID)
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return fmt.Errorf("sitl logs: %w", err)
	}
	maxS := strconv.Itoa(int(r.sc.DurationS + r.opt.Prepare.Seconds() + 60))
	for i := range r.sc.Aircraft {
		a := r.sc.Aircraft[i]
		vars := map[string]string{
			"sysid": strconv.Itoa(a.Sysid), "out_port": strconv.Itoa(r.lab.OutPort(a.Sysid)),
			"fly_port": strconv.Itoa(r.lab.FlyPort(a.Sysid)), "max_s": maxS,
		}
		reader := expand(r.tg.SITL.ReaderCmd, vars)
		rcmd := exec.CommandContext(ctx, reader[0], reader[1:]...)          //nolint:gosec // the operator's own targets file names the command
		rerr, err := os.Create(filepath.Join(logDir, a.Name+"-reader.log")) //nolint:gosec // the run's own log directory
		if err != nil {
			return err
		}
		rcmd.Stderr = rerr
		out, err := rcmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := rcmd.Start(); err != nil {
			return fmt.Errorf("reader for %s: %w", a.Name, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = rerr.Close() }()
			var c core.Counters
			_ = vehicle.ReadLines(ctx, out, r.bus.Publish, &c, func(err error) { r.log.Warn("reader line", "aircraft", a.Name, "err", err) })
			_ = rcmd.Wait()
		}()
		plan := r.comp.Plans[a.Name]
		if len(plan.Steps) == 0 {
			r.mu.Lock()
			r.done[a.Name] = true
			r.mu.Unlock()
			continue
		}
		pj, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		fly := expand(r.tg.SITL.FlyCmd, vars)
		fcmd := exec.CommandContext(ctx, fly[0], fly[1:]...) //nolint:gosec // the operator's own targets file names the command
		fcmd.Stdin = bytes.NewReader(pj)
		ferr, err := os.Create(filepath.Join(logDir, a.Name+"-fly.log")) //nolint:gosec // the run's own log directory
		if err != nil {
			return err
		}
		fcmd.Stderr = ferr
		fout, err := fcmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := fcmd.Start(); err != nil {
			return fmt.Errorf("harness for %s: %w", a.Name, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = ferr.Close() }()
			sc := bufio.NewScanner(fout)
			for sc.Scan() {
				var e vehicle.StepEvent
				if json.Unmarshal(sc.Bytes(), &e) != nil {
					r.log.Warn("harness line", "aircraft", a.Name, "line", string(sc.Bytes()))
					continue
				}
				if e.Sysid != a.Sysid {
					r.log.Warn("harness spoke for another vehicle", "aircraft", a.Name, "sysid", e.Sysid)
					continue
				}
				r.recordSteps(a.Name, []vehicle.StepEvent{e}, false)
			}
			err := fcmd.Wait()
			if err != nil && ctx.Err() == nil {
				r.mu.Lock()
				r.stepFails = append(r.stepFails, fmt.Sprintf("%s: the harness exited with %v (see %s)", a.Name, err, ferr.Name()))
				r.mu.Unlock()
			}
			r.recordSteps(a.Name, nil, true)
		}()
	}
	return nil
}

// jwksVerifier verifies bearer tokens against one issuer's JWKS.
func jwksVerifier(ctx context.Context, issuer, jwksURL, audience string) (simfeed.Verifier, error) {
	v, err := auth.NewVerifier(ctx, auth.Config{Issuers: map[string]auth.IssuerConfig{issuer: {JWKSURL: jwksURL}}, Audience: audience})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, tok string) ([]string, error) {
		cl, err := v.Verify(ctx, tok)
		return cl.Scopes, err
	}, nil
}

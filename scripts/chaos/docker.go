package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Container is what docker says about one container of the project.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Service   string    `json:"service"`
	Status    string    `json:"status"` // running, exited, paused, restarting, created, dead
	Health    string    `json:"health,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Networks  []string  `json:"networks"`
	Image     string    `json:"image,omitempty"`    // the reference compose gave it
	ImageID   string    `json:"image_id,omitempty"` // the local image id it runs
}

// Running is a container whose process runs (not paused).
func (c Container) Running() bool { return c.Status == "running" }

// OnNetwork reports whether the container is attached to the network.
func (c Container) OnNetwork(n string) bool {
	for _, x := range c.Networks {
		if x == n {
			return true
		}
	}
	return false
}

// Docker is the part of the docker CLI the harness reads. Every call is
// bounded by its context; nothing here changes a container (the domain
// scripts do that, and the harness checks what they did through this).
type Docker interface {
	// Containers lists every container of the compose project.
	Containers(ctx context.Context, project string) (map[string]Container, error)
	// Exec runs a command in a running container and returns its
	// combined output and whether it exited 0.
	Exec(ctx context.Context, id string, args ...string) (string, bool, error)
	// Run runs a one-off container (docker run ARGS) and returns its
	// combined output and whether it exited 0.
	Run(ctx context.Context, args ...string) (string, bool, error)
}

// cliDocker runs the docker binary.
type cliDocker struct{ bin string }

// maxCLIOutput bounds what is kept of one docker command's output.
const maxCLIOutput = 4 << 20

func (d cliDocker) run(ctx context.Context, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, d.bin, args...) //nolint:gosec // G204: the docker binary with arguments this harness builds
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{b: &out}, &limited{b: &errb}
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return append(out.Bytes(), errb.Bytes()...), ee.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, fmt.Errorf("docker %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return out.Bytes(), 0, nil
}

// limited keeps at most maxCLIOutput bytes and drops the rest.
type limited struct{ b *bytes.Buffer }

func (l *limited) Write(p []byte) (int, error) {
	if room := maxCLIOutput - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (d cliDocker) Containers(ctx context.Context, project string) (map[string]Container, error) {
	ids, code, err := d.run(ctx, "ps", "-aq", "--no-trunc", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("docker ps exited %d: %s", code, bounded(string(ids)))
	}
	list := strings.Fields(string(ids))
	if len(list) == 0 {
		return map[string]Container{}, nil
	}
	out, code, err := d.run(ctx, append([]string{"inspect"}, list...)...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		// A container removed between ps and inspect: inspect fails as a
		// whole. Say so; the next sample lists again.
		return nil, fmt.Errorf("docker inspect exited %d: %s", code, bounded(string(out)))
	}
	return parseInspect(out)
}

// parseInspect reads `docker inspect` JSON into containers by service.
func parseInspect(b []byte) (map[string]Container, error) {
	var raw []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Image  string `json:"Image"`
		Config struct {
			Labels map[string]string `json:"Labels"`
			Image  string            `json:"Image"`
		} `json:"Config"`
		State struct {
			Status    string `json:"Status"`
			StartedAt string `json:"StartedAt"`
			Health    *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("docker inspect: %w", err)
	}
	out := make(map[string]Container, len(raw))
	for _, r := range raw {
		svc := r.Config.Labels["com.docker.compose.service"]
		if svc == "" {
			continue
		}
		c := Container{ID: r.ID, Name: strings.TrimPrefix(r.Name, "/"), Service: svc, Status: r.State.Status, Image: r.Config.Image, ImageID: r.Image}
		if r.State.Health != nil {
			c.Health = r.State.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, r.State.StartedAt); err == nil {
			c.StartedAt = t.UTC()
		}
		for n := range r.NetworkSettings.Networks {
			c.Networks = append(c.Networks, n)
		}
		sort.Strings(c.Networks)
		if prev, dup := out[svc]; dup {
			return nil, fmt.Errorf("service %s has two containers (%s, %s): scale it to one", svc, prev.Name, c.Name)
		}
		out[svc] = c
	}
	return out, nil
}

func (d cliDocker) Exec(ctx context.Context, id string, args ...string) (string, bool, error) {
	out, code, err := d.run(ctx, append([]string{"exec", id}, args...)...)
	if err != nil {
		return "", false, err
	}
	// Not cut to a note's length: callers match words in it (PostgreSQL's
	// refusal); it is bounded by maxCLIOutput.
	return strings.TrimSpace(string(out)), code == 0, nil
}

func (d cliDocker) Run(ctx context.Context, args ...string) (string, bool, error) {
	out, code, err := d.run(ctx, append([]string{"run"}, args...)...)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), code == 0, nil
}

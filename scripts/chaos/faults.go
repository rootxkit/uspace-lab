package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Fault kinds: what has to be observed for a row's fault to count as
// having happened. A domain script's own "done" is never enough
// (E-04): every kind is checked from docker's or PostgreSQL's side.
const (
	kindDown        = "down"        // every target container not running
	kindPaused      = "paused"      // every target container paused
	kindDBBlocked   = "db_blocked"  // the database refuses a connection, its host running
	kindPartitioned = "partitioned" // every target running with the partition's packet filter, dropping packets
	kindSkew        = "skew"        // a batch left with the configured clock offset
)

// dbRefusal is what PostgreSQL says to a connection to a database with
// ALLOW_CONNECTIONS false.
const dbRefusal = "is not currently accepting connections"

// FaultSpec is what a row's fault acts on and how it is seen.
type FaultSpec struct {
	Kind     string   `json:"kind"`
	Services []string `json:"services"`
	PG       string   `json:"pg,omitempty"`
	Database string   `json:"database,omitempty"`
	Network  string   `json:"network,omitempty"`
	// NetImage is the packet-filter image a partition runs in a
	// target's network namespace (scripts/chaos/net).
	NetImage string `json:"net_image,omitempty"`

	part *partState
}

// partState is what a partition's samples have seen: each target is
// looked at in turn (one docker run per sample), and the fault counts
// only when every target was seen with its filter and packets were
// actually dropped.
type partState struct {
	next     int
	seen     map[string]bool
	dropped  map[string]uint64
	restored bool
}

// faultSpec derives the fault from the row's domain and arguments (the
// same arguments the script gets, so the check cannot look at a
// different container from the one the script touched) and checks,
// before anything is injected, that every target exists and runs.
func faultSpec(r *Row, l *Lab, cs map[string]Container) (FaultSpec, error) {
	f := FaultSpec{Network: l.Network}
	prefixed := func(sys string) []string {
		var out []string
		for svc := range cs {
			if strings.HasPrefix(svc, sys+"-") && cs[svc].Running() {
				out = append(out, svc)
			}
		}
		sort.Strings(out)
		return out
	}
	switch r.Domain {
	case "adapter", "hotpath", "source", "dss", "ansp-feed", "issuer", "dbhost":
		f.Kind, f.Services = kindDown, []string{r.Args[0]}
	case "api":
		f.Kind, f.Services = kindDown, []string{r.Args[0] + "-api"}
	case "nats":
		f.Kind, f.Services = kindDown, []string{r.Args[0] + "-nats"}
	case "stall":
		f.Kind, f.Services = kindPaused, []string{r.Args[0]}
	case "timescale", "postgres":
		f.Kind, f.Services, f.PG, f.Database = kindDBBlocked, []string{r.Args[0]}, r.Args[0], r.Args[1]
	case "system":
		f.Kind, f.Services = kindDown, prefixed(r.Args[0])
	case "partition":
		f.Kind, f.Services = kindPartitioned, prefixed(r.Args[0])
		f.NetImage = firstNonEmpty(os.Getenv("CHAOS_NET_IMAGE"), "uspace-lab/chaos-net:local")
		f.part = &partState{seen: map[string]bool{}, dropped: map[string]uint64{}}
	case "stack":
		f.Kind = kindDown
		for svc := range cs {
			if cs[svc].Running() {
				f.Services = append(f.Services, svc)
			}
		}
		sort.Strings(f.Services)
	case "clock":
		f.Kind = kindSkew
		return f, nil
	default:
		return f, fmt.Errorf("chaos: domain %q has no fault check", r.Domain)
	}
	if len(f.Services) == 0 {
		return f, fmt.Errorf("chaos: row %s: no running container to fault in project %s", r.ID, l.Project)
	}
	for _, s := range f.Services {
		c, ok := cs[s]
		if !ok {
			return f, fmt.Errorf("chaos: row %s: service %s is not in project %s", r.ID, s, l.Project)
		}
		if !c.Running() {
			return f, fmt.Errorf("chaos: row %s: service %s is %s before the fault", r.ID, s, c.Status)
		}
		if f.Kind == kindPartitioned && !c.OnNetwork(l.Network) {
			return f, fmt.Errorf("chaos: row %s: service %s is not on %s before the partition", r.ID, s, l.Network)
		}
	}
	return f, nil
}

// heldNow reports whether the fault is in place in this sample, and why
// not when it is not.
func (f *FaultSpec) heldNow(ctx context.Context, d Docker, cs map[string]Container) (bool, string) {
	switch f.Kind {
	case kindDown:
		for _, s := range f.Services {
			if c, ok := cs[s]; ok && (c.Running() || c.Status == "paused" || c.Status == "restarting") {
				return false, s + " is " + c.Status
			}
		}
		return true, fmt.Sprintf("%d container(s) not running", len(f.Services))
	case kindPaused:
		for _, s := range f.Services {
			if c := cs[s]; c.Status != "paused" {
				return false, s + " is " + c.Status
			}
		}
		return true, "paused"
	case kindDBBlocked:
		c := cs[f.PG]
		if !c.Running() {
			return false, f.PG + " is " + c.Status + " (a blocked database needs its host running)"
		}
		out, ok, err := d.Exec(ctx, c.ID, "psql", "-X", "-U", "postgres", "-d", f.Database, "-tAc", "SELECT 1")
		switch {
		case err != nil:
			return false, "psql: " + err.Error()
		case ok:
			return false, "psql connected to " + f.Database
		case strings.Contains(out, dbRefusal):
			return true, f.Database + " refused the connection"
		}
		return false, "psql failed otherwise: " + bounded(out)
	case kindPartitioned:
		for _, s := range f.Services {
			if c := cs[s]; !c.Running() {
				return false, s + " is " + c.Status + " (a partition is not a crash)"
			}
		}
		// One target per sample, in turn.
		svc := f.Services[f.part.next%len(f.Services)]
		f.part.next++
		dropped, present, err := f.filter(ctx, d, cs[svc].ID)
		switch {
		case err != nil:
			return false, svc + ": " + err.Error()
		case !present:
			return false, svc + " has no partition filter"
		}
		f.part.seen[svc] = true
		f.part.dropped[svc] = dropped
		return true, fmt.Sprintf("%s filtered, %d packet(s) dropped so far", svc, dropped)
	}
	return false, "no check for kind " + f.Kind
}

// restoredNow reports whether the fault is gone: every target running
// again (a new process after a kill or a stop: its StartedAt later than
// before the fault, both read from docker's clock, never compared with
// this host's), the database accepting, the partition healed.
func (f *FaultSpec) restoredNow(ctx context.Context, d Docker, cs, before map[string]Container) (bool, string) {
	switch f.Kind {
	case kindDown:
		for _, s := range f.Services {
			c, ok := cs[s]
			if !ok || !c.Running() {
				return false, s + " is not running"
			}
			if !c.StartedAt.After(before[s].StartedAt) {
				return false, s + " has not been restarted since the fault"
			}
		}
		return true, "every container running, started after the fault"
	case kindPaused:
		for _, s := range f.Services {
			if c := cs[s]; !c.Running() {
				return false, s + " is " + c.Status
			}
		}
		return true, "running"
	case kindDBBlocked:
		c := cs[f.PG]
		if !c.Running() {
			return false, f.PG + " is " + c.Status
		}
		_, ok, err := d.Exec(ctx, c.ID, "psql", "-X", "-U", "postgres", "-d", f.Database, "-tAc", "SELECT 1")
		if err != nil || !ok {
			return false, f.Database + " still refuses"
		}
		return true, f.Database + " accepts connections"
	case kindPartitioned:
		if f.part.restored {
			return true, "every filter removed"
		}
		for _, s := range f.Services {
			c := cs[s]
			if !c.Running() {
				return false, s + " is " + c.Status
			}
			_, present, err := f.filter(ctx, d, c.ID)
			if err != nil || present {
				return false, s + " still has its partition filter"
			}
		}
		f.part.restored = true
		return true, "every filter removed"
	}
	return false, "no check for kind " + f.Kind
}

// filter reads the partition's DROP rule in a container's network
// namespace: whether it is there, and how many packets it dropped.
func (f *FaultSpec) filter(ctx context.Context, d Docker, id string) (uint64, bool, error) {
	out, ok, err := d.Run(ctx, "--rm", "--net", "container:"+id, "--cap-add", "NET_ADMIN", f.NetImage,
		"iptables -L CHAOS_IN -n -v -x 2>/dev/null || echo NO_CHAIN")
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, fmt.Errorf("the packet filter image did not run: %s", bounded(out))
	}
	if strings.Contains(out, "NO_CHAIN") {
		return 0, false, nil
	}
	return dropCount(out)
}

// dropCount reads the DROP line of `iptables -L CHAOS_IN -n -v -x`:
//
//	Chain CHAOS_IN (1 references)
//	    pkts      bytes target     prot opt in     out     source               destination
//	      12      720 ACCEPT     0    --  lo     *       0.0.0.0/0            0.0.0.0/0
//	      40     2400 DROP       0    --  *      *       0.0.0.0/0            0.0.0.0/0
func dropCount(out string) (uint64, bool, error) {
	for _, line := range strings.Split(out, "\n") {
		fs := strings.Fields(line)
		if len(fs) >= 3 && fs[2] == "DROP" {
			n, err := strconv.ParseUint(fs[0], 10, 64)
			if err != nil {
				return 0, false, fmt.Errorf("DROP rule counter %q: %w", fs[0], err)
			}
			return n, true, nil
		}
	}
	return 0, false, fmt.Errorf("CHAOS_IN has no DROP rule: %s", bounded(out))
}

// partitionFailures are a partition's own conditions: every target seen
// with its filter, and at least one packet dropped (a filter that cut
// nothing proves nothing).
func (f *FaultSpec) partitionFailures() []string {
	if f.Kind != kindPartitioned || f.part == nil {
		return nil
	}
	var out, missing []string
	var total uint64
	for _, s := range f.Services {
		if !f.part.seen[s] {
			missing = append(missing, s)
		}
		total += f.part.dropped[s]
	}
	if len(missing) > 0 {
		out = append(out, fmt.Sprintf("fault: the partition filter was never seen in %s", strings.Join(missing, ", ")))
	}
	if total == 0 {
		out = append(out, "fault: the partition filters dropped no packet: nothing was cut")
	}
	return out
}

// PartitionDrops are the packets each target's filter dropped, as last seen.
func (f *FaultSpec) PartitionDrops() map[string]uint64 {
	if f.part == nil {
		return nil
	}
	return f.part.dropped
}

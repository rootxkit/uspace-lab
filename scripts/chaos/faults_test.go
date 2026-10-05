package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDocker is the project as a test says it is; Exec answers psql with
// what the test put in psql.
type fakeDocker struct {
	mu   sync.Mutex
	cs   map[string]Container
	psql map[string]struct {
		out string
		ok  bool
	}
	err error
	// filters maps a container id to its CHAOS_IN DROP count; absent
	// means no partition filter.
	filters map[string]uint64
	runs    int
}

func (f *fakeDocker) Containers(context.Context, string) (map[string]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]Container{}
	for k := range f.cs {
		out[k] = f.cs[k]
	}
	return out, nil
}

func (f *fakeDocker) Exec(_ context.Context, id string, args ...string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	db := ""
	for i, a := range args {
		if a == "-d" && i+1 < len(args) {
			db = args[i+1]
		}
	}
	r := f.psql[id+"/"+db]
	return r.out, r.ok, nil
}

func (f *fakeDocker) Run(_ context.Context, args ...string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	id := ""
	for _, a := range args {
		if strings.HasPrefix(a, "container:") {
			id = strings.TrimPrefix(a, "container:")
		}
	}
	n, ok := f.filters[id]
	if !ok {
		return "NO_CHAIN", true, nil
	}
	return "Chain CHAOS_IN (1 references)\n    pkts      bytes target     prot opt in     out     source               destination\n" +
		"       3      180 ACCEPT     0    --  lo     *       0.0.0.0/0            0.0.0.0/0\n" +
		"      " + strconv.FormatUint(n, 10) + "     2400 DROP       0    --  *      *       0.0.0.0/0            0.0.0.0/0\n", true, nil
}

func (f *fakeDocker) set(svc string, mod func(c *Container)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cs[svc]
	mod(&c)
	f.cs[svc] = c
}

var t0 = time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)

func running(svc string, nets ...string) Container {
	if len(nets) == 0 {
		nets = []string{"p_lab"}
	}
	return Container{ID: "id-" + svc, Service: svc, Status: "running", StartedAt: t0, Networks: nets}
}

func testLab() *Lab { return &Lab{Project: "p", Network: "p_lab"} }

func project() map[string]Container {
	cs := map[string]Container{}
	for _, s := range []string{"ussp-api", "ussp-monitor", "ussp-nats", "ussp-timescaledb", "cisp-api", "cisp-nats", "caddy"} {
		cs[s] = running(s)
	}
	cs["ussp-migrate"] = Container{ID: "id-m", Service: "ussp-migrate", Status: "exited"}
	return cs
}

func TestFaultSpecFromTheScriptsOwnArguments(t *testing.T) {
	cs := project()
	cases := []struct {
		row  Row
		kind string
		svcs string
	}{
		{Row{ID: "a", Domain: "hotpath", Args: []string{"ussp-monitor"}}, kindDown, "ussp-monitor"},
		{Row{ID: "b", Domain: "api", Args: []string{"ussp"}}, kindDown, "ussp-api"},
		{Row{ID: "c", Domain: "nats", Args: []string{"cisp"}}, kindDown, "cisp-nats"},
		{Row{ID: "d", Domain: "stall", Args: []string{"ussp-monitor"}}, kindPaused, "ussp-monitor"},
		{Row{ID: "e", Domain: "postgres", Args: []string{"ussp-timescaledb", "ussp_relational"}}, kindDBBlocked, "ussp-timescaledb"},
		// The running containers only: a one-shot migration that exited
		// is not part of the system's outage.
		{Row{ID: "f", Domain: "system", Args: []string{"ussp"}}, kindDown, "ussp-api,ussp-monitor,ussp-nats,ussp-timescaledb"},
		{Row{ID: "g", Domain: "partition", Args: []string{"cisp"}}, kindPartitioned, "cisp-api,cisp-nats"},
		{Row{ID: "h", Domain: "stack"}, kindDown, "caddy,cisp-api,cisp-nats,ussp-api,ussp-monitor,ussp-nats,ussp-timescaledb"},
	}
	for _, c := range cases {
		f, err := faultSpec(&c.row, testLab(), cs)
		if err != nil {
			t.Fatalf("%s: %v", c.row.ID, err)
		}
		if f.Kind != c.kind || strings.Join(f.Services, ",") != c.svcs {
			t.Fatalf("%s: %s %v, want %s %s", c.row.ID, f.Kind, f.Services, c.kind, c.svcs)
		}
	}
	// Refused before anything is injected: a target that is missing, or
	// not running, cannot be faulted (and could not be seen faulted).
	if _, err := faultSpec(&Row{ID: "x", Domain: "hotpath", Args: []string{"ussp-ghost"}}, testLab(), cs); err == nil {
		t.Fatal("a missing service was accepted")
	}
	if _, err := faultSpec(&Row{ID: "y", Domain: "hotpath", Args: []string{"ussp-migrate"}}, testLab(), cs); err == nil {
		t.Fatal("an exited service was accepted")
	}
	if _, err := faultSpec(&Row{ID: "z", Domain: "system", Args: []string{"ansp"}}, testLab(), cs); err == nil {
		t.Fatal("a system with no container was accepted")
	}
}

func TestDownIsHeldOnlyWhenTheContainerIsNotRunning(t *testing.T) {
	d := &fakeDocker{cs: project()}
	f := FaultSpec{Kind: kindDown, Services: []string{"ussp-monitor"}}
	cs, _ := d.Containers(context.Background(), "p")
	before := cs
	if held, why := f.heldNow(context.Background(), d, cs); held {
		t.Fatalf("a running container counted as down (%s)", why)
	}
	d.set("ussp-monitor", func(c *Container) { c.Status = "exited" })
	cs, _ = d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); !held {
		t.Fatal("an exited container did not count as down")
	}
	// Restored means running again and a new process: a container that
	// runs with its old StartedAt never went down.
	d.set("ussp-monitor", func(c *Container) { c.Status = "running" })
	cs, _ = d.Containers(context.Background(), "p")
	if ok, why := f.restoredNow(context.Background(), d, cs, before); ok {
		t.Fatalf("restored without a restart (%s)", why)
	}
	d.set("ussp-monitor", func(c *Container) { c.StartedAt = t0.Add(time.Minute) })
	cs, _ = d.Containers(context.Background(), "p")
	if ok, why := f.restoredNow(context.Background(), d, cs, before); !ok {
		t.Fatalf("a restarted container is not restored: %s", why)
	}
}

func TestPausedBothWays(t *testing.T) {
	d := &fakeDocker{cs: project()}
	f := FaultSpec{Kind: kindPaused, Services: []string{"ussp-monitor"}}
	cs, _ := d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("running counted as paused")
	}
	d.set("ussp-monitor", func(c *Container) { c.Status = "paused" })
	cs, _ = d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); !held {
		t.Fatal("paused not seen")
	}
	// A paused container is not down: a pause must not pass a kill row.
	if held, _ := (&FaultSpec{Kind: kindDown, Services: []string{"ussp-monitor"}}).heldNow(context.Background(), d, cs); held {
		t.Fatal("paused counted as down")
	}
}

func TestDBBlockedIsPostgresRefusingNotAnyFailure(t *testing.T) {
	d := &fakeDocker{cs: project(), psql: map[string]struct {
		out string
		ok  bool
	}{}}
	f := FaultSpec{Kind: kindDBBlocked, Services: []string{"ussp-timescaledb"}, PG: "ussp-timescaledb", Database: "ussp_relational"}
	set := func(out string, ok bool) {
		d.psql["id-ussp-timescaledb/ussp_relational"] = struct {
			out string
			ok  bool
		}{out, ok}
	}
	cs, _ := d.Containers(context.Background(), "p")
	set("1", true)
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("a connecting database counted as blocked")
	}
	set(`psql: error: FATAL:  password authentication failed`, false)
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("another psql failure counted as the block")
	}
	set(`psql: error: FATAL:  database "ussp_relational" is not currently accepting connections`, false)
	if held, _ := f.heldNow(context.Background(), d, cs); !held {
		t.Fatal("the refusal was not seen")
	}
	// The block is of one database with its host running; the host down
	// is another fault (dbhost) and must not pass this row.
	d.set("ussp-timescaledb", func(c *Container) { c.Status = "exited" })
	cs, _ = d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("a stopped host counted as a blocked database")
	}
	d.set("ussp-timescaledb", func(c *Container) { c.Status = "running" })
	cs, _ = d.Containers(context.Background(), "p")
	set("1", true)
	if ok, _ := f.restoredNow(context.Background(), d, cs, cs); !ok {
		t.Fatal("a connecting database is not restored")
	}
}

func TestPartitionIsAFilterThatDropsInEveryContainer(t *testing.T) {
	d := &fakeDocker{cs: project(), filters: map[string]uint64{}}
	f, err := faultSpec(&Row{ID: "p", Domain: "partition", Args: []string{"cisp"}}, testLab(), d.cs)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("no filter counted as a partition")
	}
	// The filter in both containers, nothing dropped yet: in place, but
	// the row still fails until a packet is dropped.
	d.filters["id-cisp-api"], d.filters["id-cisp-nats"] = 0, 0
	for i := 0; i < 2; i++ {
		if held, why := f.heldNow(context.Background(), d, cs); !held {
			t.Fatalf("the filter was not seen: %s", why)
		}
	}
	if fails := f.partitionFailures(); len(fails) != 1 || !strings.Contains(fails[0], "dropped no packet") {
		t.Fatalf("%v", fails)
	}
	d.filters["id-cisp-api"] = 40
	for i := 0; i < 2; i++ {
		f.heldNow(context.Background(), d, cs)
	}
	if fails := f.partitionFailures(); len(fails) != 0 {
		t.Fatalf("%v", fails)
	}
	// A container seen only before: a filter in one of two is not the
	// system's partition.
	g, _ := faultSpec(&Row{ID: "q", Domain: "partition", Args: []string{"cisp"}}, testLab(), d.cs)
	delete(d.filters, "id-cisp-nats")
	g.heldNow(context.Background(), d, cs)
	g.heldNow(context.Background(), d, cs)
	if fails := g.partitionFailures(); len(fails) == 0 || !strings.Contains(fails[0], "never seen in cisp-nats") {
		t.Fatalf("%v", fails)
	}
	// A crashed container is not a partition.
	d.set("cisp-api", func(c *Container) { c.Status = "exited" })
	cs, _ = d.Containers(context.Background(), "p")
	if held, _ := f.heldNow(context.Background(), d, cs); held {
		t.Fatal("a crash counted as a partition")
	}
	// Restored when no container has the filter, and only then.
	d.set("cisp-api", func(c *Container) { c.Status = "running" })
	cs, _ = d.Containers(context.Background(), "p")
	if ok, _ := f.restoredNow(context.Background(), d, cs, cs); ok {
		t.Fatal("restored with a filter still in cisp-api")
	}
	d.filters = map[string]uint64{}
	if ok, why := f.restoredNow(context.Background(), d, cs, cs); !ok {
		t.Fatalf("healed not seen: %s", why)
	}
}

func TestDropCount(t *testing.T) {
	if _, _, err := dropCount("Chain CHAOS_IN\n 1 2 ACCEPT"); err == nil {
		t.Fatal("a chain without DROP was read")
	}
	if n, ok, err := dropCount("x\n  7 99 DROP 0 -- * * 0.0.0.0/0 0.0.0.0/0"); err != nil || !ok || n != 7 {
		t.Fatalf("%d %v %v", n, ok, err)
	}
}

func TestParseInspect(t *testing.T) {
	b := []byte(`[{"Id":"abc","Name":"/p-ussp-api-1","Image":"sha256:1","Config":{"Image":"ghcr.io/x@sha256:2","Labels":{"com.docker.compose.service":"ussp-api"}},
	  "State":{"Status":"running","StartedAt":"2026-10-05T03:05:09.336560996Z","Health":{"Status":"healthy"}},
	  "NetworkSettings":{"Networks":{"p_lab":{},"p_egress":{}}}},
	 {"Id":"def","Name":"/other","Config":{"Labels":{}},"State":{"Status":"running"}}]`)
	cs, err := parseInspect(b)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := cs["ussp-api"]
	if !ok || len(cs) != 1 {
		t.Fatalf("%+v", cs)
	}
	if !c.Running() || c.Health != "healthy" || !c.OnNetwork("p_lab") || c.OnNetwork("p_x") || c.ImageID != "sha256:1" || c.StartedAt.IsZero() {
		t.Fatalf("%+v", c)
	}
	if _, err := parseInspect([]byte(`[{"Id":"a","Config":{"Labels":{"com.docker.compose.service":"s"}}},{"Id":"b","Config":{"Labels":{"com.docker.compose.service":"s"}}}]`)); err == nil {
		t.Fatal("two containers of one service accepted")
	}
}

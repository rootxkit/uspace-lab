package vehicle

import "testing"

func sample(sysid int) Sample {
	return Sample{Schema: Schema, Sysid: sysid, Status: StatusGround}
}

func TestBusDeliversAndCountsDrops(t *testing.T) {
	b := NewBus()
	all := b.Subscribe("all", 10)
	only2 := b.Subscribe("only2", 10, 2)
	tiny := b.Subscribe("tiny", 1)
	for _, id := range []int{1, 2, 3} {
		b.Publish(sample(id))
	}
	if len(all.C) != 3 || all.Dropped() != 0 {
		t.Fatalf("all: %d queued, %d dropped", len(all.C), all.Dropped())
	}
	if len(only2.C) != 1 || (<-only2.C).Sysid != 2 {
		t.Fatal("the sysid filter let another vehicle through")
	}
	// Presence of the drop: a full queue drops and counts, never blocks.
	if len(tiny.C) != 1 || tiny.Dropped() != 2 {
		t.Fatalf("tiny: %d queued, %d dropped", len(tiny.C), tiny.Dropped())
	}
	if b.Counters().Get(CounterPublished) != 3 {
		t.Fatal("published not counted")
	}
	b.Close()
	if _, ok := <-tiny.C; !ok {
		t.Fatal("a queued sample was lost at close")
	}
	if _, ok := <-tiny.C; ok {
		t.Fatal("the channel is not closed")
	}
}

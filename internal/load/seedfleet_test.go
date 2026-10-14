package load

import (
	"testing"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/scenario"
)

// The seed's view of the fleet is the run's: every client's serials,
// its registration, each receiver's id; the digest is stable for one
// layout and changes with the client count (another binding).
func TestSeedFleetIsTheRunsFleet(t *testing.T) {
	f := loadSmall(t)
	lab, err := scenario.LoadLab(repo("sim/sitl.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	fl, err := NewFleet(f, lab.Origin, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := NewFleet(f, lab.Origin, geoidx.Constant(15.9))
	sf := fl.SeedFleet("test")
	if err := sf.Check(); err != nil {
		t.Fatal(err)
	}
	if sf.Digest != again.Digest() || len(sf.Clients) != f.Tier.Operators.Clients || len(sf.Receivers) != len(fl.Receivers) {
		t.Fatalf("seed fleet %+v", sf)
	}
	for _, a := range fl.Aircraft {
		c := sf.Clients[a.Client]
		if c.Registration != a.OperatorReg || !contains(c.Serials, a.Serial) {
			t.Fatalf("%s is not under client %d: %+v", a.Name, a.Client, c)
		}
	}
	for i, r := range sf.Receivers {
		if r.ID != fl.ReceiverID(i) {
			t.Fatalf("receiver %d is %s, the run says %s", i, r.ID, fl.ReceiverID(i))
		}
	}
	f.Tier.Operators.Clients++
	other, _ := NewFleet(f, lab.Origin, geoidx.Constant(15.9))
	if other.Digest() == fl.Digest() || other.ReceiverID(0) == fl.ReceiverID(0) {
		t.Fatal("another client count has the same layout")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

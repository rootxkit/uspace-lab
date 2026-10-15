package load

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rootxkit/uspace-lab/internal/scenario"
	"github.com/rootxkit/uspace-lab/internal/seed"
)

// Digest names the fleet's layout as the systems are seeded for it:
// which aircraft fly for which client under which registration, and
// which aircraft each receiver hears. A systems run recomputes it and
// refuses a targets file seeded for another layout (another tier, client
// count, heard fraction or paths), because its serials would be bound
// to other clients and its receivers would hear other aircraft.
func (fl *Fleet) Digest() string { return fl.digest }

func (fl *Fleet) layoutDigest() string {
	var b strings.Builder
	for _, a := range fl.Aircraft {
		fmt.Fprintf(&b, "a %d %s %s %d %s\n", a.Index, a.Serial, a.MAC, a.Client, a.OperatorReg)
	}
	for i, rx := range fl.Receivers {
		fmt.Fprintf(&b, "r %d", i)
		for _, ai := range rx {
			fmt.Fprintf(&b, " %d", ai)
		}
		b.WriteByte('\n')
	}
	// The peer's aircraft are registered and its U-space square
	// designated by the seed: another peer is another seed.
	for _, a := range fl.Peer {
		fmt.Fprintf(&b, "p %d %s\n", a.Index, a.Serial)
	}
	if p := fl.peer; p != nil {
		fmt.Fprintf(&b, "u %g %g %g\n", p.USpace.Centre.NorthM, p.USpace.Centre.EastM, p.USpace.HalfSideM)
	}
	// The zone is published by the seed: another zone is another seed.
	if z := fl.zone; z != nil {
		fmt.Fprintf(&b, "z %s %g %g %g\n", z.Type, z.Centre.NorthM, z.Centre.EastM, z.SideM)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ReceiverID is receiver i's id at the authority: the fleet's digest is
// in it, so receivers of two layouts (placed differently, hearing other
// aircraft) are never the same registration.
func (fl *Fleet) ReceiverID(i int) string {
	d := strings.TrimPrefix(fl.Digest(), "sha256:")
	return fmt.Sprintf("load-%s-%04d", d[:10], i)
}

// SeedFleet is the fleet as "loadgen seed" registers it: one operator
// per client with the serials it streams, and every receiver at the
// centre of the walks of the aircraft it hears.
func (fl *Fleet) SeedFleet(tier string) *seed.LoadFleet {
	clients := 0
	for _, a := range fl.Aircraft {
		clients = max(clients, a.Client+1)
	}
	out := &seed.LoadFleet{Tier: tier, Digest: fl.Digest(), ClassLabel: LoadClassLabel, Clients: make([]seed.LoadClient, clients)}
	for _, a := range fl.Aircraft {
		c := &out.Clients[a.Client]
		c.Registration = a.OperatorReg
		c.Serials = append(c.Serials, a.Serial)
	}
	for i, rx := range fl.Receivers {
		var n, e float64
		for _, ai := range rx {
			n += fl.Aircraft[ai].centre.NorthM
			e += fl.Aircraft[ai].centre.EastM
		}
		k := float64(max(1, len(rx)))
		p := scenario.Move(fl.originLatLon(), scenario.Offset{NorthM: n / k, EastM: e / k})
		out.Receivers = append(out.Receivers, seed.LoadReceiver{ID: fl.ReceiverID(i), LatDeg: p.LatDeg, LonDeg: p.LonDeg})
	}
	out.Zone = fl.seedZone()
	if fl.peer != nil {
		lp := &seed.LoadPeer{Area: fl.peerArea(), USpaceCentre: fl.peer.USpace.Centre, USpaceHalfSideM: fl.peer.USpace.HalfSideM}
		for _, a := range fl.Peer {
			lp.Serials = append(lp.Serials, a.Serial)
		}
		out.Peer = lp
	}
	return out
}

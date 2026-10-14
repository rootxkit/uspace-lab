package load

import (
	"context"
	"testing"
	"time"

	"github.com/rootxkit/uspace-core/regnum"
	"github.com/rootxkit/uspace-core/serial"

	"github.com/rootxkit/uspace-lab/internal/geoidx"
	"github.com/rootxkit/uspace-lab/internal/vehicle"
)

// A systems run registers the fleet at the authority and binds it at
// the USSP, which refuse a serial that is not CTA-2063-A and a
// registration number outside the configured format: every aircraft of
// the largest fleet a tier may ask for has a valid, distinct serial and
// its client's registration number, and the two halves of the pair
// (presence: a bad serial is caught by the same validator) agree.
func TestFleetIdentitiesAreRegistrable(t *testing.T) {
	f := loadSmall(t)
	f.Tier.Operators.Aircraft, f.Tier.Operators.Clients = 5000, 50
	fl, err := NewFleet(f, vehicle.Home{LatDeg: 41.7151, LonDeg: 44.8271, AltAMSLM: 605}, geoidx.Constant(15.9))
	if err != nil {
		t.Fatal(err)
	}
	v, err := regnum.NewValidator("")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	regs := map[int]string{}
	for _, a := range fl.Aircraft {
		if err := serial.ValidateCTA2063A(a.Serial); err != nil {
			t.Fatalf("%s: serial %q: %v", a.Name, a.Serial, err)
		}
		if seen[a.Serial] {
			t.Fatalf("serial %q twice", a.Serial)
		}
		seen[a.Serial] = true
		if err := v.Validate(a.OperatorReg); err != nil {
			t.Fatalf("%s: registration %q: %v", a.Name, a.OperatorReg, err)
		}
		if r, ok := regs[a.Client]; ok && r != a.OperatorReg {
			t.Fatalf("client %d has two registrations %q and %q", a.Client, r, a.OperatorReg)
		}
		regs[a.Client] = a.OperatorReg
	}
	if len(regs) != 50 || regs[0] == regs[1] {
		t.Fatalf("registrations %v", regs)
	}
	if serial.ValidateCTA2063A("LAB-LOAD-00001") == nil {
		t.Fatal("the validator takes the old serial shape")
	}
	if got := ClientRegistration(maxClients-1, maxClients); v.Validate(got) != nil {
		t.Fatalf("the last client's registration %q is refused", got)
	}
	if err := serial.ValidateCTA2063A(AircraftSerial(maxAircraft-1, maxClients)); err != nil {
		t.Fatalf("the last aircraft's serial: %v", err)
	}
	// A fleet with another client count binds aircraft 0 elsewhere: it
	// must not reuse the serial or the registration.
	if AircraftSerial(0, 10) == AircraftSerial(0, 50) || ClientRegistration(0, 10) == ClientRegistration(0, 50) {
		t.Fatal("two client layouts share an identity")
	}
}

// Intents are filed at the tier's intents_per_s, not in one burst: the
// USSP asks the authority's registry once per intent, and a fleet's
// intents at once spent the USSP's request budget there (429), so every
// intent was held pending_validation. Filing n intents at rate r takes
// at least (n - 1) / r; the reference target answers at once, so the
// bound is the pacing's.
func TestIntentsArePaced(t *testing.T) {
	f := loadSmall(t)
	f.Tier.Operators.IntentsPerS = 20
	f.Tier.DurationS = 1
	start := time.Now()
	rep, err := Run(context.Background(), Options{Files: f, TargetsPath: repo("targets/reference.yaml"), Run: "paced", RepoRoot: repo(".")})
	if err != nil {
		t.Fatal(err)
	}
	n := rep.Volumes.IntentsFiled
	if n != f.Tier.Operators.Aircraft || rep.Volumes.IntentErrors != 0 {
		t.Fatalf("%d intents filed, %d errors", n, rep.Volumes.IntentErrors)
	}
	if took, least := time.Since(start), time.Duration(float64(n-1)/20*float64(time.Second)); took < least {
		t.Fatalf("%d intents in %s, at 20 a second they take at least %s", n, took, least)
	}
}

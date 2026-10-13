package seed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/rootxkit/uspace-core/regnum"
	"github.com/rootxkit/uspace-core/serial"
)

// TestOperatorsFormat names conformance/fixtures/test-operators.yaml.
const TestOperatorsFormat = "lab-test-operators/v1"

// Bounds of the test operators file: it is the lab's own, but a seed run
// registers every row at the authority, so its size is bounded (E-10).
const (
	MaxTestOperators       = 16
	MaxTestOperatorSerials = 64
	MaxTestOperatorDays    = 3650
	MaxTestOperatorsBytes  = 64 << 10
)

// TestOperator is a test operator the registry step registers, with its
// aircraft (unlabelled: a serial that is not empty, 2019/945, G-06).
type TestOperator struct {
	RegistrationNumber string   `yaml:"registration_number"`
	Name               string   `yaml:"name"`
	Email              string   `yaml:"email"`
	Phone              string   `yaml:"phone"`
	ValidDays          int      `yaml:"valid_days"`
	Serials            []string `yaml:"serials"`
}

// TestOperators is conformance/fixtures/test-operators.yaml.
type TestOperators struct {
	Format    string         `yaml:"format"`
	Operators []TestOperator `yaml:"operators"`
}

// LoadTestOperators reads and checks a test operators file before the
// seed touches any system: the format, the bounds, every registration
// number valid under uspace-core regnum's default pattern (the pattern
// of the lab authority's default policy, pending GCAA, spec 08 Q5; the
// authority refuses a number it does not admit, a hyphen among them),
// name, e-mail and phone present, valid_days in 1..MaxTestOperatorDays,
// and every serial non-empty, at most serial.MaxLen, without white
// space and held once in the file (compared as serial.FoldKey, G-05).
func LoadTestOperators(path string) (*TestOperators, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("seed: test operators: %w", err)
	}
	if fi.Size() > MaxTestOperatorsBytes {
		return nil, fmt.Errorf("seed: %s: %d bytes, more than %d", path, fi.Size(), MaxTestOperatorsBytes)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the lab's own fixture
	if err != nil {
		return nil, fmt.Errorf("seed: test operators: %w", err)
	}
	var f TestOperators
	if err := yaml.UnmarshalWithOptions(b, &f, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("seed: %s: %w", path, err)
	}
	if err := f.check(); err != nil {
		return nil, fmt.Errorf("seed: %s: %w", path, err)
	}
	return &f, nil
}

func (f *TestOperators) check() error {
	switch {
	case f.Format != TestOperatorsFormat:
		return fmt.Errorf("format %q, want %s", f.Format, TestOperatorsFormat)
	case len(f.Operators) == 0:
		return fmt.Errorf("no operator")
	case len(f.Operators) > MaxTestOperators:
		return fmt.Errorf("%d operators, more than %d", len(f.Operators), MaxTestOperators)
	}
	v, err := regnum.NewValidator("")
	if err != nil {
		return err
	}
	numbers := map[string]bool{}
	serials := map[string]string{}
	for i, op := range f.Operators {
		at := fmt.Sprintf("operators[%d]", i)
		if err := v.Validate(op.RegistrationNumber); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		key := v.CompareKey(op.RegistrationNumber)
		if numbers[key] {
			return fmt.Errorf("%s: registration number %s is listed twice", at, op.RegistrationNumber)
		}
		numbers[key] = true
		for name, v := range map[string]string{"name": op.Name, "email": op.Email, "phone": op.Phone} {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%s: %s is empty", at, name)
			}
		}
		if op.ValidDays < 1 || op.ValidDays > MaxTestOperatorDays {
			return fmt.Errorf("%s: valid_days %d is outside 1..%d", at, op.ValidDays, MaxTestOperatorDays)
		}
		if len(op.Serials) > MaxTestOperatorSerials {
			return fmt.Errorf("%s: %d serials, more than %d", at, len(op.Serials), MaxTestOperatorSerials)
		}
		for _, sn := range op.Serials {
			switch {
			case sn == "" || len(sn) > serial.MaxLen:
				return fmt.Errorf("%s: serial %q is empty or longer than %d", at, sn, serial.MaxLen)
			case strings.ContainsAny(sn, " \t\r\n"):
				return fmt.Errorf("%s: serial %q contains white space", at, sn)
			}
			fk := serial.FoldKey(sn)
			if prev, ok := serials[fk]; ok {
				return fmt.Errorf("%s: serial %s is also %s's", at, sn, prev)
			}
			serials[fk] = op.RegistrationNumber
		}
	}
	return nil
}

// EnsureTestOperators registers each test operator, valid from now for
// its valid_days, and its aircraft at the authority's registry, unless
// the registry holds them (EnsureOperator, EnsureUAS). It returns the
// number of aircraft registered or found.
func (a *Authority) EnsureTestOperators(ctx context.Context, registrar Session, f *TestOperators, now time.Time) (int, error) {
	n := 0
	for _, op := range f.Operators {
		id, err := a.EnsureOperator(ctx, registrar, RegistryOperator{Number: op.RegistrationNumber, Name: op.Name,
			Email: op.Email, Phone: op.Phone}, now.AddDate(0, 0, op.ValidDays))
		if err != nil {
			return n, err
		}
		for _, sn := range op.Serials {
			if err := a.EnsureUAS(ctx, registrar, RegistryUAS{Serial: sn, OperatorID: id}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

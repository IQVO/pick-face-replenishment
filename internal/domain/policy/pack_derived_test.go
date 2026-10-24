package policy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

func TestNewIsDeclared(t *testing.T) {
	p, err := policy.New("WH1", "SKU-1", 10, 60, 6)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != policy.SourceDeclared || p.Stale {
		t.Fatalf("New must build a fresh DECLARED policy, got %+v", p)
	}
}

func TestNewPackDerived(t *testing.T) {
	tests := []struct {
		name         string
		min, max     int64
		unitsPerPack int64
		wantErr      error
	}{
		{"case of 12", 10, 60, 12, nil},
		{"case of 1 is a valid multiple", 0, 5, 1, nil},
		{"zero units per pack", 10, 60, 0, policy.ErrInvalidMultiple},
		{"negative units per pack", 10, 60, -12, policy.ErrInvalidMultiple},
		{"min equals max", 60, 60, 12, policy.ErrInvalidBounds},
		{"negative min", -1, 60, 12, policy.ErrInvalidBounds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := policy.NewPackDerived("WH1", "SKU-1", tc.min, tc.max, tc.unitsPerPack)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if p != (policy.Policy{}) {
					t.Fatalf("a failed NewPackDerived must return the zero value, got %+v", p)
				}
				return
			}
			if p.Source != policy.SourcePackDerived || p.Multiple != tc.unitsPerPack || p.Version != 1 || p.Stale {
				t.Fatalf("got %+v", p)
			}
			if p.MinQty != tc.min || p.MaxQty != tc.max {
				t.Fatalf("bounds stay in eaches, got %+v", p)
			}
		})
	}
}

func TestRestoreWithValidatesSource(t *testing.T) {
	if _, err := policy.RestoreWith("WH1", "SKU-1", 10, 60, 12, 2, policy.Source("BOGUS"), false); !errors.Is(err, policy.ErrInvalidSource) {
		t.Fatalf("err = %v, want ErrInvalidSource", err)
	}
	if _, err := policy.RestoreWith("WH1", "SKU-1", 10, 60, 12, 2, policy.SourceDeclared, true); !errors.Is(err, policy.ErrInvalidSource) {
		t.Fatalf("a DECLARED policy can never be stale, err = %v", err)
	}
	p, err := policy.RestoreWith("WH1", "SKU-1", 10, 60, 12, 2, "", false)
	if err != nil || p.Source != policy.SourceDeclared {
		t.Fatalf("an empty source restores as DECLARED, got %+v %v", p, err)
	}
	p, err = policy.RestoreWith("WH1", "SKU-1", 10, 60, 12, 2, policy.SourcePackDerived, true)
	if err != nil || !p.Stale || p.Source != policy.SourcePackDerived {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestRestoreIsDeclared(t *testing.T) {
	p, err := policy.Restore("WH1", "SKU-1", 10, 60, 6, 3)
	if err != nil || p.Source != policy.SourceDeclared || p.Stale {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestWithBoundsTurnsAPackDerivedPolicyIntoDeclared(t *testing.T) {
	d, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 60, 12)
	next, err := d.WithBounds(10, 60, 6)
	if err != nil {
		t.Fatal(err)
	}
	if next.Source != policy.SourceDeclared || next.Stale || next.Multiple != 6 || next.Version != 2 {
		t.Fatalf("an explicit redeclaration is DECLARED, got %+v", next)
	}
}

func TestWithPackDerivedBounds(t *testing.T) {
	d, _ := policy.New("WH1", "SKU-1", 10, 60, 6)
	next, err := d.WithPackDerivedBounds(12, 120, 24)
	if err != nil {
		t.Fatal(err)
	}
	if next.Source != policy.SourcePackDerived || next.Multiple != 24 || next.MinQty != 12 || next.MaxQty != 120 || next.Version != 2 {
		t.Fatalf("got %+v", next)
	}
	if _, err := d.WithPackDerivedBounds(12, 120, 0); !errors.Is(err, policy.ErrInvalidMultiple) {
		t.Fatalf("err = %v", err)
	}
}

func TestRederive(t *testing.T) {
	d, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 60, 12)

	same, changed, err := d.Rederive(12)
	if err != nil || changed || same != d {
		t.Fatalf("an unchanged case pack must change nothing, got %+v %v %v", same, changed, err)
	}

	next, changed, err := d.Rederive(24)
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if next.Multiple != 24 || next.Version != 2 || next.Stale || next.Source != policy.SourcePackDerived || next.MinQty != 10 || next.MaxQty != 60 {
		t.Fatalf("got %+v", next)
	}
	if d.Multiple != 12 || d.Version != 1 {
		t.Fatalf("the receiver must stay untouched, got %+v", d)
	}

	if _, _, err := d.Rederive(0); !errors.Is(err, policy.ErrInvalidMultiple) {
		t.Fatalf("err = %v", err)
	}
}

func TestRederiveClearsStaleEvenWhenTheCaseIsUnchanged(t *testing.T) {
	d, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 60, 12)
	stale, _, _ := d.MarkStale()
	next, changed, err := stale.Rederive(12)
	if err != nil || !changed || next.Stale || next.Multiple != 12 || next.Version != 3 {
		t.Fatalf("got %+v changed=%v err=%v", next, changed, err)
	}
}

func TestRederiveRefusesADeclaredPolicy(t *testing.T) {
	d, _ := policy.New("WH1", "SKU-1", 10, 60, 6)
	if _, _, err := d.Rederive(12); !errors.Is(err, policy.ErrNotPackDerived) {
		t.Fatalf("err = %v, want ErrNotPackDerived", err)
	}
	if _, _, err := d.MarkStale(); !errors.Is(err, policy.ErrNotPackDerived) {
		t.Fatalf("err = %v, want ErrNotPackDerived", err)
	}
}

func TestMarkStale(t *testing.T) {
	d, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 60, 12)
	stale, changed, err := d.MarkStale()
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if !stale.Stale || stale.Multiple != 12 || stale.Version != 2 {
		t.Fatalf("the last derived multiple is kept, got %+v", stale)
	}
	again, changed, err := stale.MarkStale()
	if err != nil || changed || again != stale {
		t.Fatalf("flagging twice must change nothing, got %+v %v %v", again, changed, err)
	}
}

func TestRoundDownIsAWholeNumberOfCases(t *testing.T) {
	p, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 100, 12)
	for _, tc := range []struct{ in, want int64 }{{0, 0}, {11, 0}, {12, 12}, {35, 24}, {36, 36}, {95, 84}} {
		if got := p.RoundDown(tc.in); got != tc.want {
			t.Errorf("RoundDown(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestDeclarationCarriesTheSource(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	d, _ := policy.NewPackDerived("WH1", "SKU-1", 10, 60, 12)
	if got := d.Declaration(at); got.Source != policy.SourcePackDerived || got.Multiple != 12 {
		t.Fatalf("got %+v", got)
	}
	dec, _ := policy.New("WH1", "SKU-1", 10, 60, 6)
	if got := dec.Declaration(at); got.Source != policy.SourceDeclared {
		t.Fatalf("got %+v", got)
	}
}

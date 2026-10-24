package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

func packLevel(t *testing.T, sku, lvl, gtin string, units, version int64) packlevel.PackLevel {
	t.Helper()
	l, err := packlevel.New(sku, lvl, gtin, units, T0, version)
	must(t, err)
	return l
}

// PackLevelView runs the PackLevelView contract. newView must return an EMPTY
// view on every call.
func PackLevelView(t *testing.T, newView func(t *testing.T) ports.PackLevelView) {
	t.Helper()
	t.Run("an unseen level is not found", func(t *testing.T) {
		_, ok, err := newView(t).Get(bg, "SKU-1", packlevel.Case)
		must(t, err)
		if ok {
			t.Fatal("found a level that was never saved")
		}
	})
	t.Run("save then get round trips every field", func(t *testing.T) {
		v := newView(t)
		in := packLevel(t, "SKU-1", "CASE", "24006381333935", 12, 3)
		must(t, v.Save(bg, in))
		got, ok, err := v.Get(bg, "SKU-1", packlevel.Case)
		must(t, err)
		if !ok || got.SKU != in.SKU || got.Level != in.Level || got.GTIN != in.GTIN || got.UnitsPerPack != 12 ||
			got.Version != 3 || !got.EffectiveFrom.Equal(T0) || got.EffectiveTo != nil {
			t.Fatalf("got %+v ok=%v, want %+v", got, ok, in)
		}
	})
	t.Run("a level is keyed by sku and level", func(t *testing.T) {
		v := newView(t)
		must(t, v.Save(bg, packLevel(t, "SKU-1", "CASE", "24006381333935", 12, 3)))
		must(t, v.Save(bg, packLevel(t, "SKU-1", "PALLET", "34006381333936", 480, 4)))
		must(t, v.Save(bg, packLevel(t, "SKU-2", "CASE", "24006381333942", 6, 1)))
		for _, c := range []struct {
			sku string
			lvl packlevel.Level
			ups int64
		}{{"SKU-1", packlevel.Case, 12}, {"SKU-1", packlevel.Pallet, 480}, {"SKU-2", packlevel.Case, 6}} {
			got, ok, err := v.Get(bg, c.sku, c.lvl)
			must(t, err)
			if !ok || got.UnitsPerPack != c.ups {
				t.Fatalf("%s/%s: got %+v ok=%v", c.sku, c.lvl, got, ok)
			}
		}
		if _, ok, _ := v.Get(bg, "SKU-2", packlevel.Pallet); ok {
			t.Fatal("SKU-2 has no pallet level")
		}
	})
	t.Run("saving again replaces the level, an end date included", func(t *testing.T) {
		packLevelReplace(t, newView(t))
	})
}

func packLevelReplace(t *testing.T, v ports.PackLevelView) {
	{
		first := packLevel(t, "SKU-1", "CASE", "24006381333935", 12, 3)
		must(t, v.Save(bg, first))
		end := T0.Add(time.Hour)
		retired, ok := first.Retire("24006381333935", end, 4)
		if !ok {
			t.Fatal("retire refused")
		}
		must(t, v.Save(bg, retired))
		got, _, err := v.Get(bg, "SKU-1", packlevel.Case)
		must(t, err)
		if got.EffectiveTo == nil || !got.EffectiveTo.Equal(end) || got.Version != 4 {
			t.Fatalf("got %+v", got)
		}
		must(t, v.Save(bg, packLevel(t, "SKU-1", "CASE", "24006381333942", 24, 5)))
		got, _, err = v.Get(bg, "SKU-1", packlevel.Case)
		must(t, err)
		if got.EffectiveTo != nil || got.GTIN != "24006381333942" || got.UnitsPerPack != 24 || got.Version != 5 {
			t.Fatalf("the new level must replace the end-dated one, got %+v", got)
		}
	}
}

func policySourceRoundTrip(t *testing.T, r ports.PolicyRepo) {
	derived, err := policy.NewPackDerived("S1", "SKU-1", 10, 60, 12)
	must(t, err)
	must(t, r.Save(bg, repository.PolicyRecord{Policy: derived, DeclaredAt: T0}, 0))
	got, err := r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy != derived {
		t.Fatalf("got %+v, want %+v", got.Policy, derived)
	}

	stale, _, err := derived.MarkStale()
	must(t, err)
	must(t, r.Save(bg, repository.PolicyRecord{Policy: stale, DeclaredAt: T0}, 1))
	got, err = r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy != stale || !got.Policy.Stale || got.Policy.Source != policy.SourcePackDerived {
		t.Fatalf("stale flag lost: %+v", got.Policy)
	}

	declared, err := stale.WithBounds(10, 60, 6)
	must(t, err)
	must(t, r.Save(bg, repository.PolicyRecord{Policy: declared, DeclaredAt: T0}, 2))
	got, err = r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy != declared || got.Policy.Stale || got.Policy.Source != policy.SourceDeclared {
		t.Fatalf("redeclaring must reset source and stale: %+v", got.Policy)
	}

	listed, err := r.List(bg, repository.PolicyFilter{SKU: "SKU-1"}, nil, 10)
	must(t, err)
	if len(listed) != 1 || listed[0].Policy != declared {
		t.Fatalf("list = %+v", listed)
	}
}

package repocontract

import (
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// ReadModel runs the PickFaceReadModel contract. newModel must return an EMPTY
// model on every call.
func ReadModel(t *testing.T, newModel func(t *testing.T) ports.PickFaceReadModel) {
	t.Helper()
	cases := map[string]func(t *testing.T, m ports.PickFaceReadModel){
		"a bin never seen reads as level zero":                    levelUnseen,
		"a level is applied only when its version is greater":     levelVersionGuard,
		"levels are kept per site, bin and sku":                   levelScope,
		"assignments are replaced wholesale per site":             assignmentsReplace,
		"assignments are ordered by bin then sku":                 assignmentsOrder,
		"the forward map stamp follows the last replacement":      assignmentsStamp,
		"an unknown location is not a place":                      placeUnknown,
		"a place joins its zone whenever the zone arrives":        placeJoinsZone,
		"a re-registered zone and location replace the old ones":  placeUpsert,
		"decommissioning is irreversible, even before the sight":  placeDecommission,
		"reserve bins are storage locations of reserve zones":     reserveFilters,
		"reserve bins are per site and sku and ordered by bin":    reserveScope,
		"a reserve bin needs both its location and its zone seen": reserveNeedsLayout,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, newModel(t)) })
	}
}

func level(site, bin, sku string, onHand, reserved, version int64) repository.BinLevel {
	return repository.BinLevel{SiteCode: site, Bin: bin, SKU: sku, OnHand: onHand, Reserved: reserved, Version: version}
}

func apply(t *testing.T, m ports.PickFaceReadModel, l repository.BinLevel) bool {
	t.Helper()
	applied, err := m.ApplyBinLevel(bg, l)
	must(t, err)
	return applied
}

func binLevel(t *testing.T, m ports.PickFaceReadModel, site, bin, sku string) repository.BinLevel {
	t.Helper()
	l, err := m.BinLevel(bg, site, bin, sku)
	must(t, err)
	return l
}

func levelUnseen(t *testing.T, m ports.PickFaceReadModel) {
	if got := binLevel(t, m, "S1", "F-01", "SKU-1"); got != level("S1", "F-01", "SKU-1", 0, 0, 0) {
		t.Fatalf("unseen bin = %+v", got)
	}
}

func levelVersionGuard(t *testing.T, m ports.PickFaceReadModel) {
	if !apply(t, m, level("S1", "F-01", "SKU-1", 8, 1, 3)) {
		t.Fatal("the first level must apply")
	}
	for _, v := range []int64{3, 2, 1} {
		if apply(t, m, level("S1", "F-01", "SKU-1", 99, 99, v)) {
			t.Fatalf("version %d must not replace version 3", v)
		}
	}
	if got := binLevel(t, m, "S1", "F-01", "SKU-1"); got != level("S1", "F-01", "SKU-1", 8, 1, 3) {
		t.Fatalf("level = %+v", got)
	}
	if !apply(t, m, level("S1", "F-01", "SKU-1", 5, 0, 4)) {
		t.Fatal("a greater version must apply")
	}
	if got := binLevel(t, m, "S1", "F-01", "SKU-1"); got != level("S1", "F-01", "SKU-1", 5, 0, 4) {
		t.Fatalf("level = %+v", got)
	}
}

func levelScope(t *testing.T, m ports.PickFaceReadModel) {
	apply(t, m, level("S1", "F-01", "SKU-1", 1, 0, 1))
	apply(t, m, level("S1", "F-01", "SKU-2", 2, 0, 1))
	apply(t, m, level("S1", "F-02", "SKU-1", 3, 0, 1))
	apply(t, m, level("S2", "F-01", "SKU-1", 4, 0, 1))
	for _, want := range []repository.BinLevel{
		level("S1", "F-01", "SKU-1", 1, 0, 1), level("S1", "F-01", "SKU-2", 2, 0, 1),
		level("S1", "F-02", "SKU-1", 3, 0, 1), level("S2", "F-01", "SKU-1", 4, 0, 1),
	} {
		if got := binLevel(t, m, want.SiteCode, want.Bin, want.SKU); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func stamp(plan string, at int) repository.MapStamp {
	return repository.MapStamp{PlanID: plan, ApprovedAt: T0.Add(durationMinutes(at))}
}

func asg(site, sku, bin string) repository.Assignment {
	return repository.Assignment{SiteID: site, SKU: sku, Bin: bin}
}

func assignments(t *testing.T, m ports.PickFaceReadModel, site string) []repository.Assignment {
	t.Helper()
	got, err := m.Assignments(bg, site)
	must(t, err)
	return got
}

func assignmentsReplace(t *testing.T, m ports.PickFaceReadModel) {
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p1", 0), []repository.Assignment{asg("S1", "A", "F2"), asg("S1", "B", "F1")}))
	must(t, m.ReplaceAssignments(bg, "S2", stamp("p2", 0), []repository.Assignment{asg("S2", "A", "G1")}))
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p3", 1), []repository.Assignment{asg("S1", "B", "F1")}))
	if got := assignments(t, m, "S1"); !equalSlice(got, []repository.Assignment{asg("S1", "B", "F1")}) {
		t.Fatalf("S1 = %+v", got)
	}
	bin, ok, err := m.ForwardBin(bg, "S2", "A")
	must(t, err)
	if !ok || bin != "G1" {
		t.Fatalf("S2 must be untouched, got %q %v", bin, ok)
	}
	if _, ok, _ = m.ForwardBin(bg, "S1", "A"); ok {
		t.Fatal("a SKU the new map drops has no forward bin")
	}
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p4", 2), nil))
	if got := assignments(t, m, "S1"); len(got) != 0 {
		t.Fatalf("an empty map clears the site: %+v", got)
	}
}

func assignmentsOrder(t *testing.T, m ports.PickFaceReadModel) {
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p1", 0),
		[]repository.Assignment{asg("S1", "Z", "F1"), asg("S1", "B", "F2"), asg("S1", "A", "F2"), asg("S1", "C", "F0")}))
	want := []repository.Assignment{asg("S1", "C", "F0"), asg("S1", "Z", "F1"), asg("S1", "A", "F2"), asg("S1", "B", "F2")}
	if got := assignments(t, m, "S1"); !equalSlice(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func assignmentsStamp(t *testing.T, m ports.PickFaceReadModel) {
	if _, ok, err := m.ForwardMapStamp(bg, "S1"); err != nil || ok {
		t.Fatalf("a site never mapped has no stamp: %v %v", ok, err)
	}
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p1", 0), []repository.Assignment{asg("S1", "A", "F1")}))
	must(t, m.ReplaceAssignments(bg, "S2", stamp("p9", 9), nil))
	got, ok, err := m.ForwardMapStamp(bg, "S1")
	must(t, err)
	if !ok || got.PlanID != "p1" || !got.ApprovedAt.Equal(stamp("p1", 0).ApprovedAt) {
		t.Fatalf("stamp = %+v %v", got, ok)
	}
	must(t, m.ReplaceAssignments(bg, "S1", stamp("p2", 5), nil))
	if got, _, _ = m.ForwardMapStamp(bg, "S1"); got.PlanID != "p2" || !got.ApprovedAt.Equal(stamp("p2", 5).ApprovedAt) {
		t.Fatalf("stamp after replace = %+v", got)
	}
}

package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

func durationMinutes(n int) time.Duration { return time.Duration(n) * time.Minute }

func zone(id, site, code string) repository.Zone {
	return repository.Zone{ID: id, SiteCode: site, ZoneCode: code}
}

func loc(code, zoneID, role string) repository.Location {
	return repository.Location{Code: code, ZoneID: zoneID, Role: role}
}

func saveZone(t *testing.T, m ports.PickFaceReadModel, z repository.Zone) {
	t.Helper()
	must(t, m.SaveZone(bg, z))
}

func saveLoc(t *testing.T, m ports.PickFaceReadModel, l repository.Location) {
	t.Helper()
	must(t, m.SaveLocation(bg, l))
}

func place(t *testing.T, m ports.PickFaceReadModel, code string) (repository.Place, bool) {
	t.Helper()
	p, ok, err := m.Place(bg, code)
	must(t, err)
	return p, ok
}

func placeUnknown(t *testing.T, m ports.PickFaceReadModel) {
	if p, ok := place(t, m, "L-404"); ok {
		t.Fatalf("an unseen location = %+v", p)
	}
}

func placeJoinsZone(t *testing.T, m ports.PickFaceReadModel) {
	saveLoc(t, m, loc("L-1", "z-rsv", ""))
	p, ok := place(t, m, "L-1")
	if !ok || p.SiteCode != "" || p.ZoneCode != "" || p.Decommissioned || p.Code != "L-1" {
		t.Fatalf("location before its zone = %+v %v", p, ok)
	}
	saveZone(t, m, zone("z-rsv", "S1", "RSV"))
	p, _ = place(t, m, "L-1")
	if p.SiteCode != "S1" || p.ZoneCode != "RSV" || !p.IsStorage() {
		t.Fatalf("convergent once the zone arrives: %+v", p)
	}
	saveLoc(t, m, loc("L-2", "z-rsv", "Staging"))
	if p, _ = place(t, m, "L-2"); p.Role != "Staging" || p.IsStorage() {
		t.Fatalf("role = %+v", p)
	}
}

func placeUpsert(t *testing.T, m ports.PickFaceReadModel) {
	saveZone(t, m, zone("z1", "S1", "RSV"))
	saveZone(t, m, zone("z2", "S1", "FWD"))
	saveLoc(t, m, loc("L-1", "z1", ""))
	saveLoc(t, m, loc("L-1", "z2", "Storage"))
	if p, _ := place(t, m, "L-1"); p.ZoneCode != "FWD" || p.Role != "Storage" {
		t.Fatalf("re-registered location = %+v", p)
	}
	saveZone(t, m, zone("z2", "S2", "PICK"))
	if p, _ := place(t, m, "L-1"); p.SiteCode != "S2" || p.ZoneCode != "PICK" {
		t.Fatalf("re-registered zone = %+v", p)
	}
}

func placeDecommission(t *testing.T, m ports.PickFaceReadModel) {
	saveZone(t, m, zone("z1", "S1", "RSV"))
	saveLoc(t, m, loc("L-1", "z1", ""))
	must(t, m.DecommissionLocation(bg, "L-1"))
	saveLoc(t, m, loc("L-1", "z1", ""))
	if p, _ := place(t, m, "L-1"); !p.Decommissioned {
		t.Fatalf("a decommissioned location must stay retired: %+v", p)
	}
	must(t, m.DecommissionLocation(bg, "L-9"))
	saveLoc(t, m, loc("L-9", "z1", ""))
	if p, ok := place(t, m, "L-9"); !ok || !p.Decommissioned {
		t.Fatalf("decommissioning an unseen location blocks its registration: %+v %v", p, ok)
	}
}

func reserveBins(t *testing.T, m ports.PickFaceReadModel, site, sku string, zones ...string) []string {
	t.Helper()
	got, err := m.ReserveBins(bg, site, sku, zones)
	must(t, err)
	out := make([]string, len(got))
	for i, l := range got {
		out[i] = l.Bin
	}
	return out
}

func stock(t *testing.T, m ports.PickFaceReadModel, site, bin, sku string, onHand int64) {
	t.Helper()
	apply(t, m, level(site, bin, sku, onHand, 0, 1))
}

func reserveFilters(t *testing.T, m ports.PickFaceReadModel) {
	saveZone(t, m, zone("z-rsv", "S1", "RSV"))
	saveZone(t, m, zone("z-bulk", "S1", "BULK"))
	saveZone(t, m, zone("z-fwd", "S1", "FWD"))
	saveZone(t, m, zone("z-s2", "S2", "RSV"))
	for _, l := range []repository.Location{
		loc("R-1", "z-rsv", ""), loc("R-2", "z-rsv", "Storage"), loc("R-3", "z-rsv", "Staging"),
		loc("R-4", "z-rsv", ""), loc("B-1", "z-bulk", ""), loc("F-1", "z-fwd", ""), loc("X-1", "z-s2", ""),
	} {
		saveLoc(t, m, l)
		stock(t, m, "S1", l.Code, "SKU-1", 5)
	}
	must(t, m.DecommissionLocation(bg, "R-4"))
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV"); !equalSlice(got, []string{"R-1", "R-2"}) {
		t.Fatalf("RSV storage, not decommissioned = %v", got)
	}
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV", "BULK"); !equalSlice(got, []string{"B-1", "R-1", "R-2"}) {
		t.Fatalf("two reserve zone codes = %v", got)
	}
	if got := reserveBins(t, m, "S1", "SKU-1"); len(got) != 0 {
		t.Fatalf("no zone codes, no reserve bins: %v", got)
	}
	if got := reserveBins(t, m, "S1", "SKU-1", "NOPE"); len(got) != 0 {
		t.Fatalf("an unknown zone code = %v", got)
	}
}

func reserveScope(t *testing.T, m ports.PickFaceReadModel) {
	saveZone(t, m, zone("z1", "S1", "RSV"))
	saveZone(t, m, zone("z2", "S2", "RSV"))
	saveLoc(t, m, loc("R-2", "z1", ""))
	saveLoc(t, m, loc("R-1", "z1", ""))
	saveLoc(t, m, loc("Q-1", "z2", ""))
	stock(t, m, "S1", "R-2", "SKU-1", 4)
	stock(t, m, "S1", "R-1", "SKU-1", 9)
	stock(t, m, "S1", "R-1", "SKU-2", 3)
	stock(t, m, "S2", "Q-1", "SKU-1", 7)
	// stock recorded under S1 for a bin whose zone belongs to S2 does not count
	stock(t, m, "S1", "Q-1", "SKU-1", 1)
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV"); !equalSlice(got, []string{"R-1", "R-2"}) {
		t.Fatalf("S1 / SKU-1 = %v", got)
	}
	if got := reserveBins(t, m, "S1", "SKU-2", "RSV"); !equalSlice(got, []string{"R-1"}) {
		t.Fatalf("S1 / SKU-2 = %v", got)
	}
	if got := reserveBins(t, m, "S2", "SKU-1", "RSV"); !equalSlice(got, []string{"Q-1"}) {
		t.Fatalf("S2 / SKU-1 = %v", got)
	}
	levels, err := m.ReserveBins(bg, "S1", "SKU-1", []string{"RSV"})
	must(t, err)
	if levels[0] != level("S1", "R-1", "SKU-1", 9, 0, 1) {
		t.Fatalf("the level comes with the bin: %+v", levels[0])
	}
}

func reserveNeedsLayout(t *testing.T, m ports.PickFaceReadModel) {
	stock(t, m, "S1", "R-1", "SKU-1", 5)
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV"); len(got) != 0 {
		t.Fatalf("a bin whose location was never seen is not a reserve bin: %v", got)
	}
	saveLoc(t, m, loc("R-1", "z-rsv", ""))
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV"); len(got) != 0 {
		t.Fatalf("a bin whose zone was never seen is not a reserve bin: %v", got)
	}
	saveZone(t, m, zone("z-rsv", "S1", "RSV"))
	if got := reserveBins(t, m, "S1", "SKU-1", "RSV"); !equalSlice(got, []string{"R-1"}) {
		t.Fatalf("convergent once both arrive: %v", got)
	}
}

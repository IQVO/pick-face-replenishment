package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// RequestRepo runs the RequestRepo contract. newRepo must return an EMPTY
// repository on every call.
func RequestRepo(t *testing.T, newRepo func(t *testing.T) ports.RequestRepo) {
	t.Helper()
	cases := map[string]func(t *testing.T, r ports.RequestRepo){
		"an unknown request is not found":                          requestNotFound,
		"save then get round trips an open request":                requestRoundTripOpen,
		"completed and cancelled requests round trip their stamps": requestRoundTripClosed,
		"inserting an existing id is a concurrent modification":    requestInsertTwice,
		"a second open request for a pick face is refused":         requestOneOpenPerPickFace,
		"a closed request frees the pick face":                     requestClosedFreesPickFace,
		"the open-request rule is per site, bin and sku":           requestOpenRuleScope,
		"an update is guarded by the loaded version":               requestUpdateGuard,
		"a restored request has no pending events":                 requestNoPendingEvents,
		"open lookups return only Requested requests":              requestOpenLookups,
		"list is newest first, filtered and keyset paged":          requestListPaging,
		"list filters by site, sku and state":                      requestListFilters,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, newRepo(t)) })
	}
}

func save(t *testing.T, r ports.RequestRepo, req *replenishment.Request) {
	t.Helper()
	must(t, r.Save(bg, req, 0))
}

func get(t *testing.T, r ports.RequestRepo, id string) *replenishment.Request {
	t.Helper()
	req, err := r.Get(bg, id)
	must(t, err)
	return req
}

func ids(reqs []*replenishment.Request) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.ID()
	}
	return out
}

func requestNotFound(t *testing.T, r ports.RequestRepo) {
	_, err := r.Get(bg, "repl-404")
	wantErr(t, err, repository.ErrRequestNotFound)
}

func requestRoundTripOpen(t *testing.T, r ports.RequestRepo) {
	in := Planned(t, "repl-1", "F-01", T0)
	save(t, r, in)
	if got := get(t, r, "repl-1").Snapshot(); !sameSnapshot(got, in.Snapshot()) {
		t.Fatalf("got %+v\nwant %+v", got, in.Snapshot())
	}
}

func requestRoundTripClosed(t *testing.T, r ports.RequestRepo) {
	done := Planned(t, "repl-1", "F-01", T0)
	save(t, r, done)
	rel := replenishment.Relocation{RelocationID: "rel-1", SKU: "SKU-1", FromBin: "R-01", ToBin: "F-01", Quantity: 7, Reason: "REPLENISHMENT", SourceRef: "repl-1"}
	must(t, done.Complete(rel, T0.Add(time.Hour)))
	must(t, r.Save(bg, done, 1))
	cancelled := Planned(t, "repl-2", "F-02", T0)
	save(t, r, cancelled)
	must(t, cancelled.Cancel(replenishment.ReasonAssignmentChanged, T0.Add(2*time.Hour)))
	must(t, r.Save(bg, cancelled, 1))
	if got := get(t, r, "repl-1").Snapshot(); !sameSnapshot(got, done.Snapshot()) {
		t.Fatalf("completed: got %+v\nwant %+v", got, done.Snapshot())
	}
	if got := get(t, r, "repl-2").Snapshot(); !sameSnapshot(got, cancelled.Snapshot()) {
		t.Fatalf("cancelled: got %+v\nwant %+v", got, cancelled.Snapshot())
	}
}

func requestInsertTwice(t *testing.T, r ports.RequestRepo) {
	first := Planned(t, "repl-1", "F-01", T0)
	save(t, r, first)
	wantErr(t, r.Save(bg, first, 0), repository.ErrConcurrentModification)
}

func requestOneOpenPerPickFace(t *testing.T, r ports.RequestRepo) {
	save(t, r, Planned(t, "repl-1", "F-01", T0))
	wantErr(t, r.Save(bg, Planned(t, "repl-2", "F-01", T0), 0), repository.ErrOpenRequestExists)
	if _, err := r.Get(bg, "repl-2"); err == nil {
		t.Fatal("the refused request must not have been stored")
	}
}

func requestClosedFreesPickFace(t *testing.T, r ports.RequestRepo) {
	first := Planned(t, "repl-1", "F-01", T0)
	save(t, r, first)
	must(t, first.Cancel(replenishment.ReasonOperator, T0))
	must(t, r.Save(bg, first, 1))
	save(t, r, Planned(t, "repl-2", "F-01", T0.Add(time.Minute)))
	// and a second closed request for the same face is fine too
	second := get(t, r, "repl-2")
	must(t, second.Cancel(replenishment.ReasonOperator, T0))
	must(t, r.Save(bg, second, 1))
	save(t, r, Planned(t, "repl-3", "F-01", T0.Add(2*time.Minute)))
}

func requestOpenRuleScope(t *testing.T, r ports.RequestRepo) {
	save(t, r, Planned(t, "repl-1", "F-01", T0))
	save(t, r, Planned(t, "repl-2", "F-02", T0))
	save(t, r, PlannedFor(t, "repl-3", "S1", "SKU-2", "F-01", T0))
	save(t, r, PlannedFor(t, "repl-4", "S2", "SKU-1", "F-01", T0))
}

func requestUpdateGuard(t *testing.T, r ports.RequestRepo) {
	req := Planned(t, "repl-1", "F-01", T0)
	save(t, r, req)
	must(t, req.Cancel(replenishment.ReasonOperator, T0.Add(time.Hour)))
	wantErr(t, r.Save(bg, req, 7), repository.ErrConcurrentModification)
	must(t, r.Save(bg, req, 1))
	wantErr(t, r.Save(bg, req, 1), repository.ErrConcurrentModification)
	ghost := Planned(t, "repl-9", "F-09", T0)
	wantErr(t, r.Save(bg, ghost, 1), repository.ErrConcurrentModification)
}

func requestNoPendingEvents(t *testing.T, r ports.RequestRepo) {
	save(t, r, Planned(t, "repl-1", "F-01", T0))
	if ev := get(t, r, "repl-1").DomainEvents(); len(ev) != 0 {
		t.Fatalf("events = %v", ev)
	}
}

func requestOpenLookups(t *testing.T, r ports.RequestRepo) {
	save(t, r, Planned(t, "repl-2", "F-02", T0))
	save(t, r, Planned(t, "repl-1", "F-01", T0))
	closed := Planned(t, "repl-0", "F-03", T0)
	save(t, r, closed)
	must(t, closed.Cancel(replenishment.ReasonOperator, T0))
	must(t, r.Save(bg, closed, 1))
	save(t, r, PlannedFor(t, "repl-3", "S1", "SKU-2", "F-04", T0))
	save(t, r, PlannedFor(t, "repl-4", "S2", "SKU-1", "F-01", T0))

	got, found, err := r.OpenFor(bg, "S1", "F-01", "SKU-1")
	must(t, err)
	if !found || got.ID() != "repl-1" {
		t.Fatalf("OpenFor = %v %v", got, found)
	}
	if _, found, err = r.OpenFor(bg, "S1", "F-03", "SKU-1"); err != nil || found {
		t.Fatalf("a closed request is not open: %v %v", found, err)
	}
	bySKU, err := r.OpenForSKU(bg, "S1", "SKU-1")
	must(t, err)
	if !equalSlice(ids(bySKU), []string{"repl-1", "repl-2"}) {
		t.Fatalf("OpenForSKU = %v", ids(bySKU))
	}
	bySite, err := r.OpenForSite(bg, "S1")
	must(t, err)
	if !equalSlice(ids(bySite), []string{"repl-1", "repl-2", "repl-3"}) {
		t.Fatalf("OpenForSite = %v", ids(bySite))
	}
}

func seedForListing(t *testing.T, r ports.RequestRepo) {
	save(t, r, Planned(t, "repl-a", "F-01", T0))
	save(t, r, Planned(t, "repl-b", "F-02", T0.Add(time.Minute)))
	save(t, r, Planned(t, "repl-c", "F-03", T0.Add(time.Minute)))
	save(t, r, PlannedFor(t, "repl-d", "S2", "SKU-2", "F-01", T0.Add(2*time.Minute)))
	closed := get(t, r, "repl-a")
	must(t, closed.Cancel(replenishment.ReasonOperator, T0.Add(time.Hour)))
	must(t, r.Save(bg, closed, 1))
}

func listIDs(t *testing.T, r ports.RequestRepo, f repository.RequestFilter, after string, limit int) []string {
	t.Helper()
	got, err := r.List(bg, f, after, limit)
	must(t, err)
	return ids(got)
}

func requestListPaging(t *testing.T, r ports.RequestRepo) {
	seedForListing(t, r)
	want := []string{"repl-d", "repl-c", "repl-b", "repl-a"} // requested-at desc, then id desc
	if got := listIDs(t, r, repository.RequestFilter{}, "", 100); !equalSlice(got, want) {
		t.Fatalf("all = %v, want %v", got, want)
	}
	if got := listIDs(t, r, repository.RequestFilter{}, "", 2); !equalSlice(got, want[:2]) {
		t.Fatalf("first page = %v", got)
	}
	if got := listIDs(t, r, repository.RequestFilter{}, "repl-c", 2); !equalSlice(got, want[2:]) {
		t.Fatalf("second page = %v", got)
	}
	if got := listIDs(t, r, repository.RequestFilter{}, "repl-a", 2); len(got) != 0 {
		t.Fatalf("past the end = %v", got)
	}
	if got := listIDs(t, r, repository.RequestFilter{}, "repl-nope", 2); len(got) != 0 {
		t.Fatalf("an unknown cursor lists nothing, got %v", got)
	}
}

func requestListFilters(t *testing.T, r ports.RequestRepo) {
	seedForListing(t, r)
	cases := []struct {
		name   string
		filter repository.RequestFilter
		want   []string
	}{
		{"site", repository.RequestFilter{SiteID: "S2"}, []string{"repl-d"}},
		{"sku", repository.RequestFilter{SKU: "SKU-1"}, []string{"repl-c", "repl-b", "repl-a"}},
		{"state", repository.RequestFilter{State: replenishment.StateCancelled}, []string{"repl-a"}},
		{"open", repository.RequestFilter{State: replenishment.StateRequested}, []string{"repl-d", "repl-c", "repl-b"}},
		{"all three", repository.RequestFilter{SiteID: "S1", SKU: "SKU-1", State: replenishment.StateRequested}, []string{"repl-c", "repl-b"}},
	}
	for _, c := range cases {
		if got := listIDs(t, r, c.filter, "", 100); !equalSlice(got, c.want) {
			t.Fatalf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

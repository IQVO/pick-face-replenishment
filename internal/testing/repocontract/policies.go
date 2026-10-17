package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

func record(t *testing.T, site, sku string, minQty, maxQty, multiple int64) repository.PolicyRecord {
	t.Helper()
	p, err := policy.New(site, sku, minQty, maxQty, multiple)
	if err != nil {
		t.Fatal(err)
	}
	return repository.PolicyRecord{Policy: p, DeclaredAt: T0}
}

// PolicyRepo runs the PolicyRepo contract. newRepo must return an EMPTY
// repository on every call.
func PolicyRepo(t *testing.T, newRepo func(t *testing.T) ports.PolicyRepo) {
	t.Helper()
	cases := map[string]func(t *testing.T, r ports.PolicyRepo){
		"an unknown policy is not found":               policyNotFound,
		"save then get round trips every field":        policyRoundTrip,
		"inserting over an existing policy is refused": policyInsertTwice,
		"an update is guarded by the loaded version":   policyUpdateGuard,
		"a delete is guarded by the loaded version":    policyDeleteGuard,
		"a deleted policy can be declared again":       policyRedeclare,
		"list is ordered by site then sku and paged":   policyListPaging,
		"list narrows by site and by sku":              policyListFilters,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, newRepo(t)) })
	}
}

func policyNotFound(t *testing.T, r ports.PolicyRepo) {
	_, err := r.Get(bg, "S1", "SKU-1")
	wantErr(t, err, repository.ErrPolicyNotFound)
}

func policyRoundTrip(t *testing.T, r ports.PolicyRepo) {
	in := record(t, "S1", "SKU-1", 10, 60, 6)
	must(t, r.Save(bg, in, 0))
	got, err := r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy != in.Policy || !got.DeclaredAt.Equal(T0) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func policyInsertTwice(t *testing.T, r ports.PolicyRepo) {
	must(t, r.Save(bg, record(t, "S1", "SKU-1", 10, 60, 1), 0))
	wantErr(t, r.Save(bg, record(t, "S1", "SKU-1", 1, 9, 1), 0), repository.ErrPolicyAlreadyExists)
}

func policyUpdateGuard(t *testing.T, r ports.PolicyRepo) {
	first := record(t, "S1", "SKU-1", 10, 60, 1)
	must(t, r.Save(bg, first, 0))
	next, err := first.Policy.WithBounds(20, 80, 2)
	must(t, err)
	wantErr(t, r.Save(bg, repository.PolicyRecord{Policy: next, DeclaredAt: T0}, 7), repository.ErrConcurrentModification)
	later := T0.Add(2 * time.Hour)
	must(t, r.Save(bg, repository.PolicyRecord{Policy: next, DeclaredAt: later}, 1))
	got, err := r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy != next || !got.DeclaredAt.Equal(later) {
		t.Fatalf("after update: %+v", got)
	}
	wantErr(t, r.Save(bg, repository.PolicyRecord{Policy: next, DeclaredAt: T0}, 1), repository.ErrConcurrentModification)
	ghost := record(t, "S1", "SKU-9", 1, 9, 1)
	wantErr(t, r.Save(bg, ghost, 1), repository.ErrConcurrentModification)
}

func policyDeleteGuard(t *testing.T, r ports.PolicyRepo) {
	must(t, r.Save(bg, record(t, "S1", "SKU-1", 10, 60, 1), 0))
	wantErr(t, r.Delete(bg, "S1", "SKU-1", 9), repository.ErrConcurrentModification)
	wantErr(t, r.Delete(bg, "S1", "SKU-404", 1), repository.ErrPolicyNotFound)
	must(t, r.Delete(bg, "S1", "SKU-1", 1))
	_, err := r.Get(bg, "S1", "SKU-1")
	wantErr(t, err, repository.ErrPolicyNotFound)
}

func policyRedeclare(t *testing.T, r ports.PolicyRepo) {
	must(t, r.Save(bg, record(t, "S1", "SKU-1", 10, 60, 1), 0))
	must(t, r.Delete(bg, "S1", "SKU-1", 1))
	must(t, r.Save(bg, record(t, "S1", "SKU-1", 5, 30, 1), 0))
	got, err := r.Get(bg, "S1", "SKU-1")
	must(t, err)
	if got.Policy.MinQty != 5 || got.Policy.Version != 1 {
		t.Fatalf("got %+v", got)
	}
}

func keysOf(recs []repository.PolicyRecord) []repository.PolicyKey {
	out := make([]repository.PolicyKey, len(recs))
	for i, rec := range recs {
		out[i] = repository.PolicyKey{SiteID: rec.Policy.SiteID, SKU: rec.Policy.SKU}
	}
	return out
}

func listPolicies(t *testing.T, r ports.PolicyRepo, f repository.PolicyFilter, after *repository.PolicyKey, limit int) []repository.PolicyKey {
	t.Helper()
	got, err := r.List(bg, f, after, limit)
	must(t, err)
	return keysOf(got)
}

func seedPolicies(t *testing.T, r ports.PolicyRepo) {
	for _, k := range [][2]string{{"S2", "A"}, {"S1", "B"}, {"S1", "A"}, {"S1", "a"}} {
		must(t, r.Save(bg, record(t, k[0], k[1], 1, 9, 1), 0))
	}
}

func policyListPaging(t *testing.T, r ports.PolicyRepo) {
	seedPolicies(t, r)
	all := listPolicies(t, r, repository.PolicyFilter{}, nil, 100)
	want := []repository.PolicyKey{{SiteID: "S1", SKU: "A"}, {SiteID: "S1", SKU: "B"}, {SiteID: "S1", SKU: "a"}, {SiteID: "S2", SKU: "A"}}
	if !equalSlice(all, want) {
		t.Fatalf("byte order by site then sku:\n got %v\nwant %v", all, want)
	}
	page := listPolicies(t, r, repository.PolicyFilter{}, nil, 2)
	if !equalSlice(page, want[:2]) {
		t.Fatalf("first page = %v", page)
	}
	next := listPolicies(t, r, repository.PolicyFilter{}, &page[1], 2)
	if !equalSlice(next, want[2:]) {
		t.Fatalf("second page = %v", next)
	}
	if rest := listPolicies(t, r, repository.PolicyFilter{}, &next[1], 2); len(rest) != 0 {
		t.Fatalf("past the end = %v", rest)
	}
}

func policyListFilters(t *testing.T, r ports.PolicyRepo) {
	seedPolicies(t, r)
	if got := listPolicies(t, r, repository.PolicyFilter{SiteID: "S2"}, nil, 100); !equalSlice(got, []repository.PolicyKey{{SiteID: "S2", SKU: "A"}}) {
		t.Fatalf("by site = %v", got)
	}
	if got := listPolicies(t, r, repository.PolicyFilter{SKU: "A"}, nil, 100); !equalSlice(got, []repository.PolicyKey{{SiteID: "S1", SKU: "A"}, {SiteID: "S2", SKU: "A"}}) {
		t.Fatalf("by sku = %v", got)
	}
	if got := listPolicies(t, r, repository.PolicyFilter{SiteID: "S1", SKU: "B"}, nil, 100); !equalSlice(got, []repository.PolicyKey{{SiteID: "S1", SKU: "B"}}) {
		t.Fatalf("by both = %v", got)
	}
}

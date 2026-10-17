// Package repocontract holds the behavioural contract of the outbound
// repositories. The in-memory adapters and the Postgres adapters run the SAME
// cases, so what BDD and `go run ./cmd/api` without DATABASE_URL show is what
// production does (ADR 0005).
package repocontract

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// T0 is the fixed instant the cases build on. It has no sub-microsecond part,
// so a Postgres round trip returns it unchanged.
var T0 = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func equalSlice[T comparable](a, b []T) bool { return slices.Equal(a, b) }

// sameSnapshot compares two snapshots, instants by Equal (not by the struct's
// location pointer).
func sameSnapshot(a, b replenishment.Snapshot) bool {
	times := [][2]time.Time{{a.RequestedAt, b.RequestedAt}, {a.CompletedAt, b.CompletedAt}, {a.CancelledAt, b.CancelledAt}}
	for _, p := range times {
		if !p[0].Equal(p[1]) {
			return false
		}
	}
	a.RequestedAt, a.CompletedAt, a.CancelledAt = time.Time{}, time.Time{}, time.Time{}
	b.RequestedAt, b.CompletedAt, b.CancelledAt = time.Time{}, time.Time{}, time.Time{}
	return a == b
}

var bg = context.Background()

// Planned builds a Requested request at version 1 for site S1, SKU SKU-1,
// sourced from R-01, requested at the given instant.
func Planned(t *testing.T, id, forwardBin string, at time.Time) *replenishment.Request {
	t.Helper()
	return PlannedFor(t, id, "S1", "SKU-1", forwardBin, at)
}

// PlannedFor is Planned for any site and SKU.
func PlannedFor(t *testing.T, id, site, sku, forwardBin string, at time.Time) *replenishment.Request {
	t.Helper()
	r, err := replenishment.Plan(replenishment.PlanInput{
		RequestID: id, SiteID: site, SKU: sku, ForwardBin: forwardBin, Quantity: 10,
		Priority: replenishment.PriorityNormal, PolicyMin: 5, PolicyMax: 60, Projected: 2,
		Source: replenishment.Source{Bin: "R-01", SiteID: site, ReserveZone: true, Usable: 50}, At: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

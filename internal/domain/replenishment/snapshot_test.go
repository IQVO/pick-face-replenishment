package replenishment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

func requestedSnapshot() replenishment.Snapshot {
	return replenishment.Snapshot{
		ID:          "repl-1",
		SiteID:      "WH1",
		SKU:         "SKU-1",
		ForwardBin:  "WH1-FWD-A01-01-01-A",
		SourceBin:   "WH1-RSV-A07-03-02-B",
		Quantity:    24,
		Priority:    replenishment.PriorityNormal,
		State:       replenishment.StateRequested,
		PolicyMin:   10,
		PolicyMax:   60,
		RequestedAt: at0,
		Version:     1,
	}
}

func completedSnapshot() replenishment.Snapshot {
	s := requestedSnapshot()
	s.State = replenishment.StateCompleted
	s.MovedQuantity = 24
	s.CompletedAt = at1
	s.Version = 2
	return s
}

func cancelledSnapshot() replenishment.Snapshot {
	s := requestedSnapshot()
	s.State = replenishment.StateCancelled
	s.CancelReason = replenishment.ReasonAssignmentChanged
	s.CancelledAt = at1
	s.Version = 2
	return s
}

func TestSnapshotRoundTripsEveryState(t *testing.T) {
	for name, snap := range map[string]replenishment.Snapshot{
		"requested": requestedSnapshot(),
		"completed": completedSnapshot(),
		"cancelled": cancelledSnapshot(),
	} {
		t.Run(name, func(t *testing.T) {
			r, err := replenishment.Restore(snap)
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if r.Snapshot() != snap {
				t.Fatalf("round trip:\n got %+v\nwant %+v", r.Snapshot(), snap)
			}
			if len(r.DomainEvents()) != 0 {
				t.Fatalf("a restored request has no pending events, got %d", len(r.DomainEvents()))
			}
		})
	}
}

func TestSnapshotOfALiveRequestRestores(t *testing.T) {
	r := mustPlan(t, validInput())
	if err := r.Complete(matchingRelocation(), at1); err != nil {
		t.Fatal(err)
	}
	back, err := replenishment.Restore(r.Snapshot())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if back.Snapshot() != r.Snapshot() {
		t.Fatalf("got %+v, want %+v", back.Snapshot(), r.Snapshot())
	}
}

func TestRestoreNormalisesTimestampsToUTC(t *testing.T) {
	local := time.FixedZone("UTC-3", -3*3600)
	s := completedSnapshot()
	s.RequestedAt = at0.In(local)
	s.CompletedAt = at1.In(local)
	r, err := replenishment.Restore(s)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if got.RequestedAt.Location() != time.UTC || got.CompletedAt.Location() != time.UTC {
		t.Fatalf("timestamps not UTC: %v %v", got.RequestedAt.Location(), got.CompletedAt.Location())
	}
	c, err := replenishment.Restore(cancelledSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if c.Snapshot().CancelledAt.Location() != time.UTC {
		t.Fatal("CancelledAt not UTC")
	}
}

type snapshotCase struct {
	name    string
	base    func() replenishment.Snapshot
	mutate  func(*replenishment.Snapshot)
	wantErr error
}

func runSnapshotCases(t *testing.T, cases []snapshotCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.base()
			tc.mutate(&s)
			r, err := replenishment.Restore(s)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && r != nil {
				t.Fatal("a rejected snapshot must return no request")
			}
		})
	}
}

func TestRestoreRejectsACorruptIdentityOrSizing(t *testing.T) {
	b := requestedSnapshot
	runSnapshotCases(t, []snapshotCase{
		{"blank id", b, func(s *replenishment.Snapshot) { s.ID = " " }, replenishment.ErrBlankRequestID},
		{"blank site", b, func(s *replenishment.Snapshot) { s.SiteID = "" }, replenishment.ErrBlankSite},
		{"blank sku", b, func(s *replenishment.Snapshot) { s.SKU = "" }, replenishment.ErrBlankSKU},
		{"blank forward bin", b, func(s *replenishment.Snapshot) { s.ForwardBin = "" }, replenishment.ErrBlankForwardBin},
		{"blank source bin", b, func(s *replenishment.Snapshot) { s.SourceBin = "" }, replenishment.ErrBlankSourceBin},
		{"source equals forward", b, func(s *replenishment.Snapshot) { s.SourceBin = s.ForwardBin }, replenishment.ErrSourceIsForwardBin},
		{"zero quantity", b, func(s *replenishment.Snapshot) { s.Quantity = 0 }, replenishment.ErrQuantityBelowOne},
		{"min equals max", b, func(s *replenishment.Snapshot) { s.PolicyMin = 60 }, replenishment.ErrInvalidPolicyBounds},
		{"negative min", b, func(s *replenishment.Snapshot) { s.PolicyMin = -1 }, replenishment.ErrInvalidPolicyBounds},
		{"unknown priority", b, func(s *replenishment.Snapshot) { s.Priority = "HIGH" }, replenishment.ErrInvalidPriority},
		{"no requested time", b, func(s *replenishment.Snapshot) { s.RequestedAt = time.Time{} }, replenishment.ErrInvalidSnapshot},
		{"version zero", b, func(s *replenishment.Snapshot) { s.Version = 0 }, replenishment.ErrInvalidSnapshot},
		{"unknown state", b, func(s *replenishment.Snapshot) { s.State = "Open" }, replenishment.ErrInvalidState},
		{"quantity one is fine", b, func(s *replenishment.Snapshot) { s.Quantity = 1 }, nil},
		{"zero min is fine", b, func(s *replenishment.Snapshot) { s.PolicyMin = 0 }, nil},
		{"min one below max is fine", b, func(s *replenishment.Snapshot) { s.PolicyMin = 59 }, nil},
		{"min above max", b, func(s *replenishment.Snapshot) { s.PolicyMin = 61 }, replenishment.ErrInvalidPolicyBounds},
		{"negative quantity", b, func(s *replenishment.Snapshot) { s.Quantity = -2 }, replenishment.ErrQuantityBelowOne},
	})
}

func TestRestoreRejectsInconsistentRequestedStamps(t *testing.T) {
	b := requestedSnapshot
	runSnapshotCases(t, []snapshotCase{
		{"completed time on a requested request", b, func(s *replenishment.Snapshot) { s.CompletedAt = at1 }, replenishment.ErrInvalidSnapshot},
		{"cancelled time on a requested request", b, func(s *replenishment.Snapshot) { s.CancelledAt = at1 }, replenishment.ErrInvalidSnapshot},
		{"moved quantity on a requested request", b, func(s *replenishment.Snapshot) { s.MovedQuantity = 1 }, replenishment.ErrInvalidSnapshot},
		{"cancel reason on a requested request", b, func(s *replenishment.Snapshot) { s.CancelReason = replenishment.ReasonOperator }, replenishment.ErrInvalidSnapshot},
	})
}

func TestRestoreRejectsInconsistentCompletedStamps(t *testing.T) {
	b := completedSnapshot
	runSnapshotCases(t, []snapshotCase{
		{"no completed time", b, func(s *replenishment.Snapshot) { s.CompletedAt = time.Time{} }, replenishment.ErrInvalidSnapshot},
		{"also cancelled", b, func(s *replenishment.Snapshot) { s.CancelledAt = at1 }, replenishment.ErrInvalidSnapshot},
		{"nothing moved", b, func(s *replenishment.Snapshot) { s.MovedQuantity = 0 }, replenishment.ErrInvalidSnapshot},
		{"cancel reason on a completed request", b, func(s *replenishment.Snapshot) { s.CancelReason = replenishment.ReasonOperator }, replenishment.ErrInvalidSnapshot},
		{"one unit moved is fine", b, func(s *replenishment.Snapshot) { s.MovedQuantity = 1 }, nil},
	})
}

func TestRestoreRejectsInconsistentCancelledStamps(t *testing.T) {
	b := cancelledSnapshot
	runSnapshotCases(t, []snapshotCase{
		{"unknown reason", b, func(s *replenishment.Snapshot) { s.CancelReason = "BORED" }, replenishment.ErrInvalidCancelReason},
		{"no reason", b, func(s *replenishment.Snapshot) { s.CancelReason = "" }, replenishment.ErrInvalidCancelReason},
		{"no cancelled time", b, func(s *replenishment.Snapshot) { s.CancelledAt = time.Time{} }, replenishment.ErrInvalidSnapshot},
		{"also completed", b, func(s *replenishment.Snapshot) { s.CompletedAt = at1 }, replenishment.ErrInvalidSnapshot},
		{"moved quantity on a cancelled request", b, func(s *replenishment.Snapshot) { s.MovedQuantity = 5 }, replenishment.ErrInvalidSnapshot},
	})
}

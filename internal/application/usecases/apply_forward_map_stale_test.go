package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func withAssignments(c usecases.ForwardMapChange, as ...usecases.SlotAssignment) usecases.ForwardMapChange {
	c.Assignments = as
	return c
}

func mapTo(planID string, at time.Time, bin string) usecases.ForwardMapChange {
	return usecases.ForwardMapChange{
		SiteID: "S1", PlanID: planID, ApprovedAt: at,
		Assignments: []usecases.SlotAssignment{{SKU: "SKU-1", Slot: bin}},
	}
}

func applyMap(t *testing.T, e *env, eventID string, c usecases.ForwardMapChange) usecases.Outcome {
	t.Helper()
	out, err := (&usecases.ApplyForwardAssignmentMap{Intake: e.intake, Eval: e.eval}).Handle(bg, eventID, c)
	if err != nil {
		t.Fatalf("Handle(%s) = %v", eventID, err)
	}
	return out
}

func forwardBinOf(t *testing.T, e *env) string {
	t.Helper()
	bin, ok, err := e.read.ForwardBin(bg, "S1", "SKU-1")
	if err != nil || !ok {
		t.Fatalf("forward bin = %q %v %v", bin, ok, err)
	}
	return bin
}

// The seed map is stamped plan-seed at t0.

func TestAMapApprovedBeforeTheRecordedOneIsStale(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	if out := applyMap(t, e, "evt-old", mapTo("plan-old", t0.Add(-time.Hour), "F-02")); out != usecases.OutcomeStale {
		t.Fatalf("outcome = %q, want stale", out)
	}
	if bin := forwardBinOf(t, e); bin != "F-01" {
		t.Fatalf("a stale map must not replace the assignments, forward bin = %q", bin)
	}
	if got := e.log.Names(); len(got) != 1 || got[0] != "ReplenishmentRequested" {
		t.Fatalf("a stale map must cancel nothing, events = %v", got)
	}
	stamp, _, _ := e.read.ForwardMapStamp(bg, "S1")
	if stamp.PlanID != "plan-seed" || !stamp.ApprovedAt.Equal(t0) {
		t.Fatalf("a stale map must not move the stamp: %+v", stamp)
	}
}

func TestAMapApprovedAtTheRecordedInstantIsAnIdempotentNoOp(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	for _, planID := range []string{"plan-seed", "plan-twin"} {
		if out := applyMap(t, e, "evt-"+planID, mapTo(planID, t0, "F-02")); out != usecases.OutcomeStale {
			t.Fatalf("plan %s at the recorded instant: outcome = %q, want stale", planID, out)
		}
	}
	if bin := forwardBinOf(t, e); bin != "F-01" {
		t.Fatalf("forward bin = %q", bin)
	}
}

func TestANewerMapReplacesTheMapAndMovesTheStamp(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	newer := t0.Add(time.Minute)
	if out := applyMap(t, e, "evt-new", mapTo("plan-new", newer, "F-02")); out != usecases.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", out)
	}
	if bin := forwardBinOf(t, e); bin != "F-02" {
		t.Fatalf("forward bin = %q", bin)
	}
	stamp, ok, _ := e.read.ForwardMapStamp(bg, "S1")
	if !ok || stamp.PlanID != "plan-new" || !stamp.ApprovedAt.Equal(newer) {
		t.Fatalf("stamp = %+v %v", stamp, ok)
	}
}

func TestTheFirstMapOfASiteIsNeverStale(t *testing.T) {
	e := newEnv()
	c := mapTo("plan-1", t0.Add(-24*365*time.Hour), "F-09")
	c.SiteID = "S9"
	if out := applyMap(t, e, "evt-1", c); out != usecases.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", out)
	}
}

func TestStaleMapsAreTrackedPerSite(t *testing.T) {
	e := newEnv()
	e.seed(t)
	other := mapTo("plan-s2", t0.Add(-time.Hour), "G-01")
	other.SiteID = "S2"
	if out := applyMap(t, e, "evt-s2", other); out != usecases.OutcomeApplied {
		t.Fatalf("another site's older instant is not stale here: %q", out)
	}
}

func TestAStaleMapStillClaimsItsEventID(t *testing.T) {
	e := newEnv()
	e.seed(t)
	old := mapTo("plan-old", t0.Add(-time.Hour), "F-02")
	if out := applyMap(t, e, "evt-old", old); out != usecases.OutcomeStale {
		t.Fatalf("first delivery = %q", out)
	}
	if out := applyMap(t, e, "evt-old", old); out != usecases.OutcomeDuplicate {
		t.Fatalf("replay = %q, want duplicate", out)
	}
}

func TestTheMapStampIsStoredInUTC(t *testing.T) {
	e := newEnv()
	zone := time.FixedZone("BRT", -3*3600)
	at := t0.Add(time.Hour).In(zone)
	applyMap(t, e, "evt-1", mapTo("plan-1", at, "F-09"))
	stamp, _, _ := e.read.ForwardMapStamp(bg, "S1")
	if stamp.ApprovedAt.Location() != time.UTC {
		t.Fatalf("stamp location = %v", stamp.ApprovedAt.Location())
	}
}

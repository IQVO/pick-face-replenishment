package kafka_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

func assignmentsOf(t *testing.T, w *world) []repository.Assignment {
	t.Helper()
	got, err := w.read.Assignments(bg, "S1")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func bins(as []repository.Assignment) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.SKU + "=" + a.Bin
	}
	return out
}

func TestSlotting_ReplacesTheForwardMapWholesaleAndRecordsPlanAndApprovalTime(t *testing.T) {
	w := newWorld()
	w.seed(t)
	handle(t, w.slottingConsumer(), slotPlanMsg("e1", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-02", "SKU-2=F-03"))

	if got, want := bins(assignmentsOf(t, w)), []string{"SKU-1=F-02", "SKU-2=F-03"}; !slices.Equal(got, want) {
		t.Fatalf("assignments = %v, want %v", got, want)
	}
	stamp, found, err := w.read.ForwardMapStamp(bg, "S1")
	if err != nil || !found {
		t.Fatalf("stamp = %+v, %v, %v", stamp, found, err)
	}
	if want := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC); stamp.PlanID != "plan-2" || !stamp.ApprovedAt.Equal(want) {
		t.Fatalf("stamp = %+v: plan_id and approved_at must come from the payload", stamp)
	}
}

func TestSlotting_AMapApprovedBeforeTheRecordedOneIsStale(t *testing.T) {
	w := newWorld()
	w.seed(t)
	handle(t, w.slottingConsumer(), slotPlanMsg("e1", "plan-old", "2026-09-01T12:00:00Z", "SKU-9=F-09"))

	if got, want := bins(assignmentsOf(t, w)), []string{"SKU-1=F-01"}; !slices.Equal(got, want) {
		t.Fatalf("assignments = %v, want the seeded map %v: an older approval must not replace it", got, want)
	}
	if !strings.Contains(w.logs.String(), "outcome=stale") {
		t.Fatalf("the stale map must be logged as stale:\n%s", w.logs.String())
	}
}

func TestSlotting_DuplicateEventIDIsAppliedOnce(t *testing.T) {
	w := newWorld()
	w.seed(t)
	c := w.slottingConsumer()
	handle(t, c, slotPlanMsg("e1", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-02"))
	handle(t, c, slotPlanMsg("e1", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-02"))

	if !strings.Contains(w.logs.String(), "outcome=duplicate") {
		t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
	}
	if got := bins(assignmentsOf(t, w)); !slices.Equal(got, []string{"SKU-1=F-02"}) {
		t.Fatalf("assignments = %v", got)
	}
}

func TestSlotting_AnAssignmentThatMovesCancelsTheOpenRequest(t *testing.T) {
	w := newWorld()
	w.seed(t)
	handle(t, w.inventoryConsumer(), belowMin("e1", 2))
	id := w.openRequestID(t)

	handle(t, w.slottingConsumer(), slotPlanMsg("e2", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-02"))

	if names := w.log.Names(); !slices.Contains(names, "ReplenishmentCancelled") {
		t.Fatalf("events = %v, want the request cancelled (ASSIGNMENT_CHANGED)", names)
	}
	if again := w.openRequestID(t); again == id {
		t.Fatal("the cancelled request must no longer be the open one")
	}
}

func TestSlotting_UnknownTypeIsIgnoredSilently(t *testing.T) {
	w := newWorld()
	generated := ce("e1", "/warehouse/slotting-optimization", "com.warehouse.wms.slotting-optimization.slotplan.SlotPlanGenerated", "plan-2",
		"urn:warehouse:slotting-optimization:events:SlotPlanGenerated:v1", `{"plan_id":"plan-2"}`)
	handle(t, w.slottingConsumer(), generated)
	if w.uow.count() != 0 || strings.Contains(w.logs.String(), "level=WARN") {
		t.Fatalf("an unknown type must be ignored silently (uow calls %d):\n%s", w.uow.count(), w.logs.String())
	}
}

func TestSlotting_LegacyFlatMessageIsSkippedWithWarnAndCommittedPast(t *testing.T) {
	w := newWorld()
	w.seed(t)
	legacy := `{"event_id":"e-1","event_type":"SlotPlanApproved","occurred_at":"2026-10-17T09:00:00Z","plan_id":"plan-2","site_id":"S1"}`
	reader := newFakeReader([]byte(legacy), slotPlanMsg("e1", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-02"))
	if err := runUntilCommits(t, w.slottingConsumer(), reader, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if !strings.Contains(w.logs.String(), "level=WARN") || !strings.Contains(w.logs.String(), "not a valid CloudEvent") {
		t.Fatalf("the legacy flat message must be skipped with a WARN:\n%s", w.logs.String())
	}
	if got := bins(assignmentsOf(t, w)); !slices.Equal(got, []string{"SKU-1=F-02"}) {
		t.Fatalf("assignments = %v: the message after the legacy one must still be applied", got)
	}
}

func TestSlotting_PayloadsThatCanNeverBeAppliedAreDeadLettered(t *testing.T) {
	w := newWorld()
	w.seed(t)
	noApprovedAt := ce("e1", "/warehouse/slotting-optimization", typeSlotPlan, "plan-2",
		"urn:warehouse:slotting-optimization:events:SlotPlanApproved:v1", `{"plan_id":"plan-2","site_id":"S1","assignments":[{"sku":"SKU-1","slot":"F-02"}]}`)
	twice := slotPlanMsg("e2", "plan-3", "2026-10-18T12:00:00Z", "SKU-1=F-02", "SKU-1=F-03")
	reader := newFakeReader(noApprovedAt, twice)
	dlq := &fakeDLQWriter{}
	c := w.slottingConsumer()
	c.DLQ, c.DeadLetterInvalid = dlq, true

	if err := runUntilCommits(t, c, reader, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if got := dlq.published(); len(got) != 2 || got[0].Topic != kafkaconsumer.SlottingTopic+".dlq" {
		t.Fatalf("dead-lettered = %+v, want both poison payloads on the .dlq topic", got)
	}
	if got := bins(assignmentsOf(t, w)); !slices.Equal(got, []string{"SKU-1=F-01"}) {
		t.Fatalf("assignments = %v: a poison payload must change nothing", got)
	}
}

func TestSlotting_ConstructorBuildsTheConsumerWithoutDialling(t *testing.T) {
	w := newWorld()
	c := kafkaconsumer.NewSlottingConsumer([]string{"127.0.0.1:1"}, "group-from-env", slottingUseCase(w), w.logger())
	assertConstructed(t, c, kafkaconsumer.SlottingTopic, kafkaconsumer.TypeSlotPlanApproved)
}

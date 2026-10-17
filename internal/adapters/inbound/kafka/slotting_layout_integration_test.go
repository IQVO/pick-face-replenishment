//go:build integration

package kafka_test

import (
	"context"
	"slices"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
)

// slottingScript is the whole slotting topic of the test, in order:
//
//  1. e-1: plan-1 assigns SKU-1 to F-01 (which raises repl-it-0001) and SKU-2 to F-02
//  2. a legacy flat message: not a CloudEvent, skipped with a WARN
//  3. e-1 again with ANOTHER, newer map: deduped on the id
//  4. an unknown type of the topic: ignored
//  5. a plan approved before plan-1: stale, changes nothing
//  6. a plan without approved_at: poison, dead-lettered
//  7. plan-2 moves SKU-1 to F-03: cancels repl-it-0001, raises repl-it-0002
func slottingScript() (messages []kafkatest.Message, poison []byte) {
	poison = ce("e-poison", "/warehouse/slotting-optimization", typeSlotPlan, "plan-x",
		"urn:warehouse:slotting-optimization:events:SlotPlanApproved:v1", `{"plan_id":"plan-x","site_id":"S1","assignments":[{"sku":"SKU-1","slot":"F-09"}]}`)
	generated := ce("e-gen", "/warehouse/slotting-optimization", "com.warehouse.wms.slotting-optimization.slotplan.SlotPlanGenerated", "plan-1",
		"urn:warehouse:slotting-optimization:events:SlotPlanGenerated:v1", `{"plan_id":"plan-1"}`)
	messages = keyed("S1",
		slotPlanMsg("e-1", "plan-1", "2026-10-17T10:00:00Z", "SKU-1=F-01", "SKU-2=F-02"),
		[]byte(`{"event_id":"e-1","event_type":"SlotPlanApproved","occurred_at":"2026-10-17T09:00:00Z","plan_id":"plan-1"}`),
		slotPlanMsg("e-1", "plan-9", "2026-10-17T11:00:00Z", "SKU-9=F-09"),
		generated,
		slotPlanMsg("e-stale", "plan-0", "2026-10-16T10:00:00Z", "SKU-3=F-03"),
		poison,
		slotPlanMsg("e-last", "plan-2", "2026-10-17T12:00:00Z", "SKU-1=F-03"),
	)
	return messages, poison
}

// TestSlottingConsumer_RealKafkaAndPostgres: the approved map replaces the
// site's forward assignments, stamped with its plan_id and approved_at; a
// replayed id, an older plan, an unknown type and a legacy flat message change
// nothing and are committed past; a newer plan that moves a SKU cancels its
// open request; the poison plan is the only message on the dead-letter topic.
func TestSlottingConsumer_RealKafkaAndPostgres(t *testing.T) {
	ctx := context.Background()
	w := newPGWorld(t)
	w.seedLayout(t)
	w.seedStock(t)

	topic, dlq := newTopic(t, kafkaconsumer.SlottingTopic)
	messages, poison := slottingScript()
	kafkatest.Produce(t, topic, messages...)

	group := groupFor("slotting", topic)
	uc := &usecases.ApplyForwardAssignmentMap{Intake: w.intake(), Eval: w.eval()}
	runConsumer(t, kafkaconsumer.NewSlottingConsumerForTopic(kafkatest.Brokers(t), topic, group, uc, itLogger(t)))

	eventually(t, "plan-2 applied", func() bool {
		s, found, err := w.read.ForwardMapStamp(ctx, "S1")
		return err == nil && found && s.PlanID == "plan-2"
	})
	assertCommittedPast(t, group, topic, int64(len(messages)))
	assertSlottingOutcome(t, w)
	assertDeadLettered(t, topic, dlq, poison)
}

func assertSlottingOutcome(t *testing.T, w *pgWorld) {
	t.Helper()
	ctx := context.Background()
	as, err := w.read.Assignments(ctx, "S1")
	if err != nil || !slices.Equal(bins(as), []string{"SKU-1=F-03"}) {
		t.Fatalf("assignments = %v, %v: want plan-2's map only (the replay and the stale plan must not apply)", bins(as), err)
	}
	stamp, _, _ := w.read.ForwardMapStamp(ctx, "S1")
	if want := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC); !stamp.ApprovedAt.Equal(want) {
		t.Fatalf("stamp = %+v, want approved_at %v from the payload", stamp, want)
	}
	if got := w.requestState(t, "repl-it-0001"); got != replenishment.StateCancelled {
		t.Fatalf("repl-it-0001 is %s, want Cancelled (ASSIGNMENT_CHANGED)", got)
	}
	if got := w.requestState(t, "repl-it-0002"); got != replenishment.StateRequested {
		t.Fatalf("repl-it-0002 is %s, want Requested for the new pick face F-03", got)
	}
	// Requested, Cancelled, Requested: each on the integration and the
	// analytics topic.
	if n := w.count("outbox_events"); n != 6 {
		t.Fatalf("outbox rows = %d, want 6", n)
	}
	// e-1, e-stale (claimed, then found stale) and e-last.
	if n := processedRows(w); n != 3 {
		t.Fatalf("processed_events rows = %d, want 3", n)
	}
}

// layoutScript is the whole facility topic of the test, in order.
func layoutScript() (messages []kafkatest.Message, poison []byte) {
	poison = facilityMsg("e-poison", typeLocation, "R-99", `{"locationCode":"R-99"}`)
	geometry := facilityMsg("e-geo", typeGeometry, "R-07", `{"locationCode":"R-07","xM":1,"yM":2,"zM":0}`)
	messages = keyed("k",
		zoneMsg("e-z1", "z-rsv", "RSV"),
		[]byte(`{"event_id":"e-z1","event_type":"ZoneRegistered","zoneId":"z-rsv"}`),
		zoneMsg("e-z1", "z-rsv", "OTHER"),
		geometry,
		locationMsg("e-l1", "R-07", "z-rsv"),
		poison,
		facilityMsg("e-l2", typeLocation, "R-08", `{"locationCode":"R-08","zoneId":"z-rsv","role":"Dock"}`),
		decommissionedMsg("e-d1", "R-07"),
	)
	return messages, poison
}

// TestLayoutConsumer_RealKafkaAndPostgres: zones and locations feed the layout
// copy; a replayed zone id (carrying another zone code), a geometry event and
// a legacy flat message change nothing and are committed past; a decommission
// is final; the location without a zone is the only dead letter.
func TestLayoutConsumer_RealKafkaAndPostgres(t *testing.T) {
	ctx := context.Background()
	w := newPGWorld(t)

	topic, dlq := newTopic(t, kafkaconsumer.LayoutTopic)
	messages, poison := layoutScript()
	kafkatest.Produce(t, topic, messages...)

	group := groupFor("layout", topic)
	uc := kafkaconsumer.LayoutUseCases{
		Zone:           &usecases.ApplyZone{Intake: w.intake(), Read: w.read},
		Registered:     &usecases.ApplyLocationRegistered{Intake: w.intake(), Read: w.read},
		Decommissioned: &usecases.ApplyLocationDecommissioned{Intake: w.intake(), Read: w.read},
	}
	runConsumer(t, kafkaconsumer.NewLayoutConsumerForTopic(kafkatest.Brokers(t), topic, group, uc, itLogger(t)))

	eventually(t, "R-07 decommissioned", func() bool {
		p, known, err := w.read.Place(ctx, "R-07")
		return err == nil && known && p.Decommissioned
	})
	assertCommittedPast(t, group, topic, int64(len(messages)))
	r07, _, _ := w.read.Place(ctx, "R-07")
	if r07.SiteCode != "S1" || r07.ZoneCode != "RSV" {
		t.Fatalf("R-07 = %+v, want zone RSV at S1: the replayed zone id must not rewrite the zone", r07)
	}
	if r08, _, _ := w.read.Place(ctx, "R-08"); r08.Role != "Dock" {
		t.Fatalf("R-08 = %+v, want role Dock", r08)
	}
	if _, known, _ := w.read.Place(ctx, "R-99"); known {
		t.Fatal("the poison location must not be stored")
	}
	// e-z1, e-l1, e-l2 and e-d1.
	if n := processedRows(w); n != 4 {
		t.Fatalf("processed_events rows = %d, want 4", n)
	}
	assertDeadLettered(t, topic, dlq, poison)
}

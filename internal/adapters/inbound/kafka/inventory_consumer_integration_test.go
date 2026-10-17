//go:build integration

package kafka_test

import (
	"context"
	"testing"

	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
)

// The broker itself comes from internal/testing/kafkatest, which starts it
// through testcontainers-go/modules/kafka (referenced here so the module import
// stays reachable from the Kafka-touching test files).
var _ = tckafka.Run

const itRequest = "repl-it-0001"

func (w *pgWorld) inventoryUseCases() (*usecases.ApplyBinLevel, *usecases.ApplyRelocation) {
	return &usecases.ApplyBinLevel{Intake: w.intake(), Eval: w.eval()},
		&usecases.ApplyRelocation{Intake: w.intake(), Requests: w.requests, Events: w.events, Clock: systemClock{}}
}

func keyed(key string, values ...[]byte) []kafkatest.Message {
	out := make([]kafkatest.Message, len(values))
	for i, v := range values {
		out[i] = kafkatest.Message{Key: key, Value: v}
	}
	return out
}

// inventoryScript is the whole inventory topic of the test, in order:
//
//  1. e-1: SKU-1's pick face is below its minimum, which raises repl-it-0001
//  2. a legacy flat message: not a CloudEvent, skipped with a WARN
//  3. e-1 again with ANOTHER payload (a higher version): deduped on the id
//  4. an unknown type of the topic: ignored
//  5. a relocation of another reason that names the request: ignored
//  6. a poison level (negative on_hand): dead-lettered
//  7. the REPLENISHMENT relocation: completes the request
//  8. the same relocation again: deduped
//  9. the stock arriving in the pick face, then a sentinel on the reserve bin
func inventoryScript() (messages []kafkatest.Message, poison []byte) {
	poison = binLevelMsg("e-poison", "F-01", "SKU-1", -5, 0, 2)
	picked := inventoryMsg("e-pick", "com.warehouse.wms.inventory-storage.stock.StockPicked", "b", `{"sku":"SKU-1"}`)
	done := relocatedMsg("e-done", itRequest, "SKU-1", "R-01", "F-01", 52, "REPLENISHMENT")
	messages = keyed("k",
		belowMin("e-1", 2),
		[]byte(legacyFlat),
		binLevelMsg("e-1", "F-01", "SKU-1", 60, 0, 9),
		picked,
		relocatedMsg("e-foreign", itRequest, "SKU-1", "R-01", "F-01", 52, "SLOTTING"),
		poison,
		done,
		done,
		binLevelMsg("e-after", "F-01", "SKU-1", 60, 0, 3),
		binLevelMsg("e-last", "R-01", "SKU-1", 48, 0, 4),
	)
	return messages, poison
}

// TestInventoryConsumer_RealKafkaAndPostgres: a below-minimum level raises one
// request; a replay of its id with another payload, an unknown type, a legacy
// flat message and a foreign relocation change nothing and are committed past;
// the REPLENISHMENT relocation completes the request exactly once; the poison
// level is the only message on the dead-letter topic.
func TestInventoryConsumer_RealKafkaAndPostgres(t *testing.T) {
	ctx := context.Background()
	w := newPGWorld(t)
	w.seedLayout(t)
	w.seedStock(t)
	stamp := repository.MapStamp{PlanID: "plan-seed", ApprovedAt: t0}
	if err := w.read.ReplaceAssignments(ctx, "S1", stamp, []repository.Assignment{{SiteID: "S1", SKU: "SKU-1", Bin: "F-01"}}); err != nil {
		t.Fatal(err)
	}

	topic, dlq := newTopic(t, kafkaconsumer.InventoryTopic)
	messages, poison := inventoryScript()
	kafkatest.Produce(t, topic, messages...)

	group := groupFor("inventory", topic)
	level, reloc := w.inventoryUseCases()
	runConsumer(t, kafkaconsumer.NewInventoryConsumerForTopic(kafkatest.Brokers(t), topic, group, level, reloc, itLogger(t)))

	eventually(t, "the sentinel level of R-01", func() bool {
		l, err := w.read.BinLevel(ctx, "S1", "R-01", "SKU-1")
		return err == nil && l.Version == 4
	})
	assertCommittedPast(t, group, topic, int64(len(messages)))
	assertInventoryOutcome(t, w)
	assertDeadLettered(t, topic, dlq, poison)
}

func assertInventoryOutcome(t *testing.T, w *pgWorld) {
	t.Helper()
	if got := w.requestState(t, itRequest); got != replenishment.StateCompleted {
		t.Fatalf("request %s is %s, want Completed", itRequest, got)
	}
	if n := w.count("replenishment_requests"); n != 1 {
		t.Fatalf("%d requests, want exactly 1: nothing else may raise one", n)
	}
	// Requested + Completed, each on the integration and the analytics topic.
	if n := w.count("outbox_events"); n != 4 {
		t.Fatalf("outbox rows = %d, want 4: the replays must not enqueue anything", n)
	}
	l, err := w.read.BinLevel(context.Background(), "S1", "F-01", "SKU-1")
	if err != nil || l.OnHand != 60 || l.Version != 3 {
		t.Fatalf("F-01 level = %+v, %v: the replayed id must not have applied version 9", l, err)
	}
	// e-1, e-done, e-after and e-last: the replays, the foreign relocation and
	// the poison message claim nothing.
	if n := processedRows(w); n != 4 {
		t.Fatalf("processed_events rows = %d, want 4", n)
	}
}

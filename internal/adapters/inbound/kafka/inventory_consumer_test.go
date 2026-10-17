package kafka_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
)

// belowMin is a BinStockLevelChanged that puts SKU-1's pick face (F-01) at 8,
// under the seeded minimum of 10.
func belowMin(id string, version int) []byte { return binLevelMsg(id, "F-01", "SKU-1", 8, 0, version) }

func TestInventory_BelowMinLevelRaisesOneRequest(t *testing.T) {
	w := newWorld()
	w.seed(t)
	handle(t, w.inventoryConsumer(), belowMin("e1", 2))

	if got := w.log.Names(); !slices.Equal(got, []string{"ReplenishmentRequested"}) {
		t.Fatalf("events = %v, want one ReplenishmentRequested", got)
	}
	level, _ := w.read.BinLevel(bg, "S1", "F-01", "SKU-1")
	if level.OnHand != 8 || level.Version != 2 {
		t.Fatalf("level = %+v, want on_hand 8 at version 2", level)
	}
	if id := w.openRequestID(t); !strings.HasPrefix(id, "repl-") {
		t.Fatalf("request id = %q", id)
	}
}

func TestInventory_DuplicateEventIDIsAppliedOnce(t *testing.T) {
	w := newWorld()
	w.seed(t)
	c := w.inventoryConsumer()
	handle(t, c, belowMin("e1", 2))
	handle(t, c, belowMin("e1", 2)) // redelivery

	if got := w.log.Names(); len(got) != 1 {
		t.Fatalf("events = %v: the redelivered event must not raise a second request", got)
	}
	if !strings.Contains(w.logs.String(), "outcome=duplicate") {
		t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
	}
}

func TestInventory_LowerVersionIsStale(t *testing.T) {
	w := newWorld()
	w.seed(t)
	c := w.inventoryConsumer()
	handle(t, c, binLevelMsg("e1", "F-01", "SKU-1", 50, 0, 5))
	handle(t, c, binLevelMsg("e2", "F-01", "SKU-1", 8, 0, 4)) // older version, new id

	level, _ := w.read.BinLevel(bg, "S1", "F-01", "SKU-1")
	if level.OnHand != 50 || level.Version != 5 {
		t.Fatalf("level = %+v: an older version must not overwrite a newer one", level)
	}
	if n := len(w.log.Names()); n != 0 {
		t.Fatalf("events = %v, want none", w.log.Names())
	}
}

func TestInventory_ReplenishmentRelocationCompletesTheRequest(t *testing.T) {
	w := newWorld()
	w.seed(t)
	c := w.inventoryConsumer()
	handle(t, c, belowMin("e1", 2))
	id := w.openRequestID(t)

	handle(t, c, relocatedMsg("e2", id, "SKU-1", "R-01", "F-01", 52, "REPLENISHMENT"))

	want := []string{"ReplenishmentRequested", "ReplenishmentCompleted"}
	if got := w.log.Names(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if open, _ := w.requests.OpenForSite(bg, "S1"); len(open) != 0 {
		t.Fatalf("%d requests still open after the relocation", len(open))
	}
}

func TestInventory_ForeignRelocationIsIgnored(t *testing.T) {
	w := newWorld()
	w.seed(t)
	c := w.inventoryConsumer()
	handle(t, c, belowMin("e1", 2))
	id := w.openRequestID(t)
	before := w.uow.count()

	// Another context's move, a request this service never raised, a wrong SKU.
	handle(t, c, relocatedMsg("e2", id, "SKU-1", "R-01", "F-01", 52, "SLOTTING"))
	handle(t, c, relocatedMsg("e3", "repl-unknown", "SKU-1", "R-01", "F-01", 5, "REPLENISHMENT"))
	handle(t, c, relocatedMsg("e4", id, "SKU-2", "R-01", "F-01", 5, "REPLENISHMENT"))

	if got := w.log.Names(); len(got) != 1 {
		t.Fatalf("events = %v: a foreign relocation must not complete anything", got)
	}
	if w.openRequestID(t) != id {
		t.Fatal("the request must still be open")
	}
	if w.uow.count() == before {
		t.Fatal("the two relocations that name a REPLENISHMENT source_ref must have been looked at")
	}
}

func TestInventory_UnknownTypeIsIgnoredSilently(t *testing.T) {
	w := newWorld()
	picked := inventoryMsg("e1", "com.warehouse.wms.inventory-storage.stock.StockPicked", "b1", `{"sku":"SKU-1"}`)
	handle(t, w.inventoryConsumer(), picked)

	if n := w.uow.count(); n != 0 {
		t.Fatalf("an unknown type opened %d units of work, want 0", n)
	}
	if strings.Contains(w.logs.String(), "level=WARN") {
		t.Fatalf("an unknown type must not warn:\n%s", w.logs.String())
	}
}

func TestInventory_ShortTypeNameIsNotAMatch(t *testing.T) {
	w := newWorld()
	w.seed(t)
	handle(t, w.inventoryConsumer(), inventoryMsg("e1", "BinStockLevelChanged", "b", `{"site_code":"S1","bin":"F-01","sku":"SKU-1","on_hand":8,"version":2}`))
	if w.uow.count() != 0 {
		t.Fatal("dispatch must use the FULL type, never a short name")
	}
}

func TestInventory_LegacyFlatMessageIsSkippedWithWarnAndCommittedPast(t *testing.T) {
	w := newWorld()
	w.seed(t)
	reader := newFakeReader([]byte(legacyFlat), belowMin("e1", 2))
	if err := runUntilCommits(t, w.inventoryConsumer(), reader, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	logs := w.logs.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "not a valid CloudEvent") {
		t.Fatalf("the legacy flat message must be skipped with a WARN:\n%s", logs)
	}
	if got := w.log.Names(); !slices.Equal(got, []string{"ReplenishmentRequested"}) {
		t.Fatalf("events = %v: the message after the legacy one must still be applied", got)
	}
	if want := []string{"fetch:0", "commit:0", "fetch:1", "commit:1"}; !slices.Equal(reader.log(), want) {
		t.Fatalf("reader log = %v, want %v", reader.log(), want)
	}
}

func TestInventory_PoisonPayloadsAreDeadLettered(t *testing.T) {
	w := newWorld()
	w.seed(t)
	notAnObject := inventoryMsg("e2", typeBinLevel, "b", `"oops"`)
	negative := binLevelMsg("e1", "F-01", "SKU-1", -1, 0, 2)
	reader := newFakeReader(negative, notAnObject, belowMin("e3", 2))
	dlq := &fakeDLQWriter{}
	c := w.inventoryConsumer()
	c.DLQ, c.DeadLetterInvalid = dlq, true

	if err := runUntilCommits(t, c, reader, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	got := dlq.published()
	if len(got) != 2 {
		t.Fatalf("dead-lettered %d messages, want 2 (the two poison payloads)", len(got))
	}
	if got[0].Topic != kafkaconsumer.InventoryTopic+".dlq" {
		t.Fatalf("dead-letter topic = %q", got[0].Topic)
	}
	if n := len(w.log.Names()); n != 1 {
		t.Fatalf("events = %d, want the valid message after the poison ones applied", n)
	}
}

func TestInventory_PoisonPayloadWithoutADLQIsSkippedWithWarn(t *testing.T) {
	w := newWorld()
	c := w.inventoryConsumer()
	handle(t, c, binLevelMsg("e1", "F-01", "SKU-1", -1, 0, 2))
	if !strings.Contains(w.logs.String(), "skipping an event that can never be applied") {
		t.Fatalf("a poison payload without a DLQ must be skipped with a WARN:\n%s", w.logs.String())
	}
	if w.uow.count() != 0 {
		t.Fatal("a payload that can never be applied must not open a unit of work")
	}
}

func TestInventory_TransientFailureRetriesTheSameMessageThenApplies(t *testing.T) {
	w := newWorld()
	w.seed(t)
	w.read.fail = 2
	reader := newFakeReader(belowMin("e1", 2))
	dlq := &fakeDLQWriter{}
	c := w.inventoryConsumer()
	c.DLQ = dlq

	if err := runUntilCommits(t, c, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if n := w.read.applyCalls(); n != 3 {
		t.Fatalf("ApplyBinLevel called %d times, want 3 (fail, fail, succeed)", n)
	}
	if got := w.log.Names(); len(got) != 1 {
		t.Fatalf("events = %v, want exactly one request: the rolled-back claim must not block the retry", got)
	}
	if len(dlq.published()) != 0 {
		t.Fatal("a message that eventually succeeds must not be dead-lettered")
	}
	if want := []string{"fetch:0", "commit:0"}; !slices.Equal(reader.log(), want) {
		t.Fatalf("reader log = %v, want exactly one commit after the success", reader.log())
	}
}

func TestInventory_ExhaustedAttemptsAreDeadLetteredAndTheClaimIsRolledBack(t *testing.T) {
	w := newWorld()
	w.seed(t)
	w.read.fail = 1 << 20
	reader := newFakeReader(belowMin("e1", 2))
	dlq := &fakeDLQWriter{}
	c := w.inventoryConsumer()
	c.DLQ = dlq

	if err := runUntilCommits(t, c, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if n := w.read.applyCalls(); n != 5 {
		t.Fatalf("ApplyBinLevel called %d times, want the 5 attempts", n)
	}
	if got := dlq.published(); len(got) != 1 || got[0].Topic != kafkaconsumer.InventoryTopic+".dlq" {
		t.Fatalf("dead-lettered = %+v, want one message on the .dlq topic", got)
	}
	w.read.mu.Lock()
	w.read.fail = 0
	w.read.mu.Unlock()
	handle(t, c, belowMin("e1", 2)) // the id was NOT claimed by the failed attempts
	if got := w.log.Names(); len(got) != 1 {
		t.Fatalf("events = %v: the rolled-back claim must let a later redelivery apply", got)
	}
}

// A handler that fails because the process is stopping (ctx cancelled
// mid-handle) must never be dead-lettered nor committed: the message is
// redelivered after the restart.
func TestInventory_FailureDuringShutdownIsNeitherDeadLetteredNorCommitted(t *testing.T) {
	w := newWorld()
	w.seed(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.read.fail = 1 << 20
	w.read.onApply = cancel
	reader := newFakeReader(belowMin("e1", 2))
	dlq := &fakeDLQWriter{}
	c := w.inventoryConsumer()
	c.Reader, c.DLQ = reader, dlq

	if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if len(dlq.published()) != 0 {
		t.Fatal("a shutdown must never dead-letter a good message")
	}
	if reader.commits() != 0 {
		t.Fatal("a shutdown must leave the offset uncommitted")
	}
}

func TestInventory_ConstructorBuildsTheConsumerWithoutDialling(t *testing.T) {
	w := newWorld()
	level := inventoryUseCases(w)
	c := kafkaconsumer.NewInventoryConsumer([]string{"127.0.0.1:1"}, "group-from-env", level.level, level.reloc, w.logger())
	assertConstructed(t, c, kafkaconsumer.InventoryTopic, kafkaconsumer.TypeBinStockLevelChanged, kafkaconsumer.TypeStockRelocated)
}

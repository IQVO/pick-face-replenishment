package kafka_test

// Event consumer contract tests (E1-E4) for the inventory-storage edge: this
// repo's inventory consumer subscribes to warehouse.inventory.events
// (kafkaconsumer.InventoryTopic — the topic constant its reader config wires)
// and acts on BinStockLevelChanged and StockRelocated. The contract is the
// PINNED copy of inventory-storage's apis/asyncapi.yaml
// (testdata/inventory-storage.asyncapi.yaml, registered in
// contracts/pinned.json; the pinned-contracts CI job fails when the copy
// drifts from IQVO/inventory-storage@develop).
//
// Golden fixtures are the producer spec's OWN examples. The StockRelocated
// completion path uses an ADAPTED clone of the pinned example (site/bins
// rewritten to the seeded world, fresh CloudEvents id — the id is the
// idempotency key and the original stays with the verbatim golden run).

import (
	"slices"
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
)

// inventoryGoldenExamples returns the pinned spec's own examples for one
// inventory-storage message.
func inventoryGoldenExamples(t *testing.T, message string) []pinnedExample {
	t.Helper()
	doc := loadPinnedAsyncAPI(t, pinnedInventorySpec)
	msg := doc.message(t, message)
	if len(msg.Examples) == 0 {
		t.Fatalf("pinned inventory-storage spec documents %s without examples", message)
	}
	return msg.Examples
}

// E1: the topic the reader config subscribes to (InventoryTopic) is
// documented by the producer's pinned spec as a 'subscribe:' channel, and
// the message list on that channel documents both CloudEvents types the
// inventory consumer routes on.
func TestPinnedAsyncAPICoversSubscriptions_InventoryEvents(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedInventorySpec)
	documented := doc.documentedTypes(t, kafkaconsumer.InventoryTopic)

	for _, typ := range []string{kafkaconsumer.TypeBinStockLevelChanged, kafkaconsumer.TypeStockRelocated} {
		if _, ok := documented[typ]; !ok {
			t.Errorf("pinned inventory-storage spec does not document %s on %s (documented: %v)",
				typ, kafkaconsumer.InventoryTopic, documented)
		}
	}
}

// E2: the golden BinStockLevelChanged decodes through the REAL decode and
// dispatch and lands in the bin-level read model exactly once, carrying the
// wire's absolute levels and version; a replayed CloudEvents id applies
// nothing more.
func TestGoldenEventsDecode_InventoryBinLevel(t *testing.T) {
	for _, ex := range inventoryGoldenExamples(t, "BinStockLevelChanged") {
		t.Run(ex.Name, func(t *testing.T) {
			w := newWorld()
			c := w.inventoryConsumer()
			handle(t, c, goldenJSON(t, ex.Payload))

			data := goldenData(t, ex)
			bin, _ := data["bin"].(string)
			sku, _ := data["sku"].(string)
			site, _ := data["site_code"].(string)
			level, err := w.read.BinLevel(bg, site, bin, sku)
			if err != nil {
				t.Fatalf("golden event left no level for %s/%s/%s: %v", site, bin, sku, err)
			}
			if level.OnHand != numberOf(data["on_hand"]) || level.Reserved != numberOf(data["reserved"]) {
				t.Fatalf("level = %+v, want the wire's absolute on_hand %v / reserved %v",
					level, numberOf(data["on_hand"]), numberOf(data["reserved"]))
			}
			if level.Version != numberOf(data["version"]) {
				t.Fatalf("level = %+v, want the wire's version %v", level, numberOf(data["version"]))
			}

			handle(t, c, goldenJSON(t, ex.Payload)) // redelivery
			if !strings.Contains(w.logs.String(), "outcome=duplicate") {
				t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
			}
			if again, _ := w.read.BinLevel(bg, site, bin, sku); again != level {
				t.Fatalf("level after replay = %+v, want unchanged %+v", again, level)
			}
		})
	}
}

// E2: the golden StockRelocated examples. A SLOTTING move is some other
// context's relocation (ignored, no error); a REPLENISHMENT move whose
// source_ref names no open request of ours is ignored the same way; the
// ADAPTED clone completing a real request folds the wire's relocation into
// exactly one ReplenishmentCompleted.
func TestGoldenEventsDecode_InventoryRelocations(t *testing.T) {
	for _, ex := range inventoryGoldenExamples(t, "StockRelocated") {
		t.Run(ex.Name, func(t *testing.T) {
			w := newWorld()
			handle(t, w.inventoryConsumer(), goldenJSON(t, ex.Payload))
			if got := w.log.Names(); len(got) != 0 {
				t.Fatalf("a golden relocation with no matching open request raised %v", got)
			}
		})
	}

	t.Run("replenishmentRelocationCompletesTheRequest", func(t *testing.T) {
		w := newWorld()
		w.seed(t)
		c := w.inventoryConsumer()
		handle(t, c, belowMin("e-golden-below", 2)) // raises one open request
		id := w.openRequestID(t)

		golden := inventoryGoldenExamples(t, "StockRelocated")[0].Payload
		clone := clonePayload(golden)
		clone["id"] = "golden-reloc-completion-1" // fresh id: the original belongs to the verbatim run
		data := clone["data"].(map[string]any)
		data["site_code"], data["sku"] = "S1", "SKU-1"
		data["from_bin"], data["to_bin"] = "R-01", "F-01"
		data["quantity"], data["reason"], data["source_ref"] = 52, "REPLENISHMENT", id
		handle(t, c, goldenJSON(t, clone))

		want := []string{"ReplenishmentRequested", "ReplenishmentCompleted"}
		if got := w.log.Names(); !slices.Equal(got, want) {
			t.Fatalf("events = %v, want %v", got, want)
		}
		if open, _ := w.requests.OpenForSite(bg, "S1"); len(open) != 0 {
			t.Fatalf("%d requests still open after the golden relocation", len(open))
		}
	})
}

// E3: every golden fixture carries the fleet-mandatory CloudEvents 1.0
// envelope, and the pinned spec's envelope schemas require the full
// attribute set (dataschema is pinned as a pattern on this spec, so the
// exact value is asserted from the documented v1 urn).
func TestCloudEventsEnvelopeInvariants_Inventory(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedInventorySpec)
	for _, message := range []string{"BinStockLevelChanged", "StockRelocated"} {
		msg := doc.message(t, message)
		consts := doc.payloadConsts(t, msg.Payload)
		for _, ex := range msg.Examples {
			evt := assertGoldenEnvelope(t, ex.Name, goldenJSON(t, ex.Payload), "/warehouse/inventory-storage",
				"urn:warehouse:inventory-storage:events:"+msg.Name+":v1")
			if evt.Type() != consts["type"] {
				t.Errorf("%s: type %q, spec pins const %q", ex.Name, evt.Type(), consts["type"])
			}
		}
		doc.assertEnvelopeRequired(t, message)
	}
	if doc.DefaultContentType != "application/cloudevents+json" {
		t.Errorf("pinned spec defaultContentType %q, want application/cloudevents+json", doc.DefaultContentType)
	}
}

// E4: unknown ADDITIVE attributes in data (including the owner_ref-sharing
// additive fields the spec may grow) must not change the outcome; an unknown
// event TYPE on the shared topic is silently skipped; the retired flat
// envelope is skipped with a WARN, never an error.
func TestUnknownAttributesTolerated_Inventory(t *testing.T) {
	golden := inventoryGoldenExamples(t, "BinStockLevelChanged")[0].Payload

	tolerant := clonePayload(golden)
	data := tolerant["data"].(map[string]any)
	data["future_additive_field"] = "unknown-to-this-consumer"
	data["nested_extension"] = map[string]any{"deep": []any{1, "two"}}

	w := newWorld()
	c := w.inventoryConsumer()
	handle(t, c, goldenJSON(t, tolerant))
	src := goldenData(t, inventoryGoldenExamples(t, "BinStockLevelChanged")[0])
	bin, _ := src["bin"].(string)
	if _, err := w.read.BinLevel(bg, "WH1", bin, "SKU-1"); err != nil {
		t.Fatalf("unknown data attributes broke the level apply: %v", err)
	}

	unknownType := clonePayload(golden)
	unknownType["type"] = "com.warehouse.wms.inventory-storage.stock.StockReserved"
	handle(t, c, goldenJSON(t, unknownType)) // must be skipped, not an error

	handle(t, c, []byte(legacyFlat)) // not a CloudEvent: skipped with a WARN, committed past
	if !strings.Contains(w.logs.String(), "not a valid CloudEvent") {
		t.Fatalf("the flat envelope must be skipped with a WARN:\n%s", w.logs.String())
	}
}

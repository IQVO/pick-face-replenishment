package kafka_test

// Event consumer contract tests (E1-E4) for the product-master edge: this
// repo's product-master consumer subscribes to
// warehouse.product-master.events (kafkaconsumer.ProductMasterTopic — the
// topic constant its reader config wires) and acts on
// ProductPackLevelDeclared and ProductPackLevelRetired. The contract is the
// PINNED copy of product-master's apis/asyncapi.yaml
// (testdata/product-master.asyncapi.yaml, registered in
// contracts/pinned.json; the pinned-contracts CI job fails when the copy
// drifts from IQVO/product-master@develop).
//
// Golden fixtures are the producer spec's OWN examples. The wire is
// snake_case (units_per_pack, effective_from, effective_to) and the consumer
// DTO reads exactly those keys — the contract tests would catch any drift
// back to a camelCase DTO as a failed golden apply.

import (
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
)

// productMasterGoldenExamples returns the pinned spec's own examples for one
// product-master message.
func productMasterGoldenExamples(t *testing.T, message string) []pinnedExample {
	t.Helper()
	doc := loadPinnedAsyncAPI(t, pinnedProductMasterSpec)
	msg := doc.message(t, message)
	if len(msg.Examples) == 0 {
		t.Fatalf("pinned product-master spec documents %s without examples", message)
	}
	return msg.Examples
}

// E1: the topic the reader config subscribes to (ProductMasterTopic) is
// documented by the producer's pinned spec as a 'subscribe:' channel, and
// the message list on that channel documents both CloudEvents types the
// product-master consumer routes on (this spec pins `type` in each
// message's description, not as a schema const).
func TestPinnedAsyncAPICoversSubscriptions_ProductMasterEvents(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedProductMasterSpec)
	documented := doc.documentedTypes(t, kafkaconsumer.ProductMasterTopic)

	for _, typ := range []string{kafkaconsumer.TypeProductPackLevelDeclared, kafkaconsumer.TypeProductPackLevelRetired} {
		if _, ok := documented[typ]; !ok {
			t.Errorf("pinned product-master spec does not document %s on %s (documented: %v)",
				typ, kafkaconsumer.ProductMasterTopic, documented)
		}
	}
}

// E2: the golden ProductPackLevelDeclared decodes through the REAL decode
// and dispatch and lands in the pack-level view exactly once, carrying the
// wire's own case facts; a replayed CloudEvents id applies nothing more.
func TestGoldenEventsDecode_ProductMasterDeclared(t *testing.T) {
	for _, ex := range productMasterGoldenExamples(t, "ProductPackLevelDeclared") {
		t.Run(ex.Name, func(t *testing.T) {
			w := newWorld()
			c := w.productMasterConsumer()
			handle(t, c, goldenJSON(t, ex.Payload))

			data := goldenData(t, ex)
			l, ok, err := w.levels.Get(bg, "SKU-1", packlevel.Case)
			if err != nil || !ok {
				t.Fatalf("golden declaration left no CASE level: ok=%v err=%v", ok, err)
			}
			if l.UnitsPerPack != numberOf(data["units_per_pack"]) || l.GTIN != data["gtin"] {
				t.Fatalf("level = %+v, want the wire's units_per_pack %v and gtin %v",
					l, numberOf(data["units_per_pack"]), data["gtin"])
			}

			handle(t, c, goldenJSON(t, ex.Payload)) // redelivery
			if !strings.Contains(w.logs.String(), "outcome=duplicate") {
				t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
			}
			if again, _, _ := w.levels.Get(bg, "SKU-1", packlevel.Case); again != l {
				t.Fatalf("level after replay = %+v, want unchanged %+v", again, l)
			}
		})
	}
}

// E2: the golden ProductPackLevelRetired applies over the golden declared
// level and flags the pack-derived policy stale (the wire's effective_to
// ends the level).
func TestGoldenEventsDecode_ProductMasterRetired(t *testing.T) {
	declared := productMasterGoldenExamples(t, "ProductPackLevelDeclared")[0].Payload
	for _, ex := range productMasterGoldenExamples(t, "ProductPackLevelRetired") {
		t.Run(ex.Name, func(t *testing.T) {
			w := newWorld()
			c := w.productMasterConsumer()
			handle(t, c, goldenJSON(t, declared))
			seedPackDerived(t, w)

			handle(t, c, goldenJSON(t, ex.Payload))

			rec, err := w.policies.Get(bg, "S1", "SKU-1")
			if err != nil || !rec.Policy.Stale {
				t.Fatalf("policy = %+v err=%v, want stale after the golden retirement", rec.Policy, err)
			}
		})
	}
}

// E3: the envelope contract: every golden fixture carries the
// fleet-mandatory CloudEvents 1.0 envelope, and the pinned spec's
// CloudEventEnvelope requires the full attribute set.
func TestCloudEventsEnvelopeInvariants_ProductMaster(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedProductMasterSpec)
	for _, message := range []string{"ProductPackLevelDeclared", "ProductPackLevelRetired"} {
		msg := doc.message(t, message)
		for _, ex := range msg.Examples {
			assertGoldenEnvelope(t, ex.Name, goldenJSON(t, ex.Payload), "/warehouse/product-master",
				"urn:warehouse:product-master:events:"+msg.Name+":v1")
		}
		doc.assertEnvelopeRequired(t, message)
	}
	if doc.DefaultContentType != "application/cloudevents+json" {
		t.Errorf("pinned spec defaultContentType %q, want application/cloudevents+json", doc.DefaultContentType)
	}
}

// E4: unknown ADDITIVE attributes in data must not change the apply; an
// unknown event TYPE on the shared topic is silently skipped; a
// non-CloudEvent message is skipped with a WARN.
func TestUnknownAttributesTolerated_ProductMaster(t *testing.T) {
	golden := productMasterGoldenExamples(t, "ProductPackLevelDeclared")[0].Payload

	tolerant := clonePayload(golden)
	tolerant["id"] = "golden-pm-tolerant-1" // fresh id: a perturbed clone never reuses a claimed id
	data := tolerant["data"].(map[string]any)
	data["future_additive_field"] = "unknown-to-this-consumer"
	data["nested_extension"] = map[string]any{"deep": []any{1, "two"}}

	w := newWorld()
	c := w.productMasterConsumer()
	handle(t, c, goldenJSON(t, tolerant))
	l, ok, _ := w.levels.Get(bg, "SKU-1", packlevel.Case)
	if !ok || l.UnitsPerPack != 12 {
		t.Fatalf("unknown data attributes changed the apply: %+v ok=%v", l, ok)
	}

	unknownType := clonePayload(golden)
	unknownType["id"] = "golden-pm-unknown-type-1"
	unknownType["type"] = "com.warehouse.wms.product-master.product.ProductMeasured"
	handle(t, c, goldenJSON(t, unknownType)) // must be skipped silently, not an error
	if strings.Contains(w.logs.String(), "ProductMeasured") {
		t.Fatalf("an unknown type must be ignored silently:\n%s", w.logs.String())
	}
	if again, _, _ := w.levels.Get(bg, "SKU-1", packlevel.Case); again.UnitsPerPack != 12 {
		t.Fatalf("an unknown type must not touch the view: %+v", again)
	}

	flat := []byte(`{"event_id":"e-flat","event_type":"ProductPackLevelDeclared","sku":"SKU-1","level":"CASE"}`)
	handle(t, c, flat) // not a CloudEvent: skipped with a WARN, committed past
	if !strings.Contains(w.logs.String(), "not a valid CloudEvent") {
		t.Fatalf("the flat envelope must be skipped with a WARN:\n%s", w.logs.String())
	}
}

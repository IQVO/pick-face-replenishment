package kafka_test

// Event consumer contract tests (E1-E4) for the slotting-optimization edge:
// this repo's slotting consumer subscribes to
// warehouse.slotting-optimization.events (kafkaconsumer.SlottingTopic — the
// topic constant its reader config wires) and acts on SlotPlanApproved. The
// contract is the PINNED copy of slotting-optimization's apis/asyncapi.yaml
// (testdata/slotting-optimization.asyncapi.yaml, registered in
// contracts/pinned.json; the pinned-contracts CI job fails when the copy
// drifts from IQVO/slotting-optimization@develop).
//
// Golden fixtures are the producer spec's OWN examples.

import (
	"slices"
	"strings"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// slottingGoldenExamples returns the pinned spec's own SlotPlanApproved
// examples.
func slottingGoldenExamples(t *testing.T) []pinnedExample {
	t.Helper()
	doc := loadPinnedAsyncAPI(t, pinnedSlottingSpec)
	msg := doc.message(t, "SlotPlanApproved")
	if len(msg.Examples) == 0 {
		t.Fatal("pinned slotting-optimization spec documents SlotPlanApproved without examples")
	}
	return msg.Examples
}

// E1: the topic the reader config subscribes to (SlottingTopic) is
// documented by the producer's pinned spec as a 'subscribe:' channel, and
// the message list on that channel documents the CloudEvents type the
// slotting consumer routes on (this spec pins `type` in the message
// description, not as a schema const).
func TestPinnedAsyncAPICoversSubscriptions_SlottingEvents(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedSlottingSpec)
	documented := doc.documentedTypes(t, kafkaconsumer.SlottingTopic)

	if _, ok := documented[kafkaconsumer.TypeSlotPlanApproved]; !ok {
		t.Errorf("pinned slotting-optimization spec does not document %s on %s (documented: %v)",
			kafkaconsumer.TypeSlotPlanApproved, kafkaconsumer.SlottingTopic, documented)
	}
}

// E2: the golden SlotPlanApproved decodes through the REAL decode and
// dispatch and replaces the site's forward map exactly once, carrying the
// wire's plan id, approval time and FULL assignment map; a replayed
// CloudEvents id applies nothing more.
func TestGoldenEventsDecode_SlotPlanApproved(t *testing.T) {
	for _, ex := range slottingGoldenExamples(t) {
		t.Run(ex.Name, func(t *testing.T) {
			w := newWorld()
			w.seed(t) // the seeded map (plan-seed, approved t0) is older than the golden plan
			c := w.slottingConsumer()
			handle(t, c, goldenJSON(t, ex.Payload))
			want := assertGoldenForwardMap(t, w, goldenData(t, ex))

			handle(t, c, goldenJSON(t, ex.Payload)) // redelivery
			if !strings.Contains(w.logs.String(), "outcome=duplicate") {
				t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
			}
			site, _ := goldenData(t, ex)["site_id"].(string)
			if again, _ := w.read.Assignments(bg, site); !slices.Equal(again, want) {
				t.Fatalf("assignments after replay = %v, want unchanged %v", again, want)
			}
		})
	}
}

// assertGoldenForwardMap asserts the golden plan's FULL assignment map, plan
// id and approval time landed in the forward-map copy, returning the map.
func assertGoldenForwardMap(t *testing.T, w *world, data map[string]any) []repository.Assignment {
	t.Helper()
	site, _ := data["site_id"].(string)
	want := slottingGoldenAssignments(t, data)
	got, err := w.read.Assignments(bg, site)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("assignments = %v, want the golden plan's FULL map %v", got, want)
	}
	stamp, found, err := w.read.ForwardMapStamp(bg, site)
	if err != nil || !found {
		t.Fatalf("stamp = %+v, %v, %v", stamp, found, err)
	}
	if stamp.PlanID != data["plan_id"] {
		t.Errorf("stamp = %+v, want the wire's plan_id %v", stamp, data["plan_id"])
	}
	approvedAt, _ := data["approved_at"].(string)
	wantAt, err := time.Parse(time.RFC3339, approvedAt)
	if err != nil || !stamp.ApprovedAt.Equal(wantAt) {
		t.Errorf("stamp = %+v, want the wire's approved_at %q", stamp, approvedAt)
	}
	return want
}

// slottingGoldenAssignments maps the golden payload's assignments array to
// repository.Assignment values in wire order.
func slottingGoldenAssignments(t *testing.T, data map[string]any) []repository.Assignment {
	t.Helper()
	raw, ok := data["assignments"].([]any)
	if !ok {
		t.Fatal("golden SlotPlanApproved carries no assignments array")
	}
	out := make([]repository.Assignment, 0, len(raw))
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		sku, _ := entry["sku"].(string)
		slot, _ := entry["slot"].(string)
		out = append(out, repository.Assignment{SiteID: data["site_id"].(string), SKU: sku, Bin: slot})
	}
	return out
}

// E3: the golden fixture carries the fleet-mandatory CloudEvents 1.0
// envelope, and the pinned spec's CloudEventEnvelope requires the full
// attribute set (dataschema asserted from the documented v1 urn).
func TestCloudEventsEnvelopeInvariants_Slotting(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedSlottingSpec)
	msg := doc.message(t, "SlotPlanApproved")
	for _, ex := range msg.Examples {
		evt := assertGoldenEnvelope(t, ex.Name, goldenJSON(t, ex.Payload), "/warehouse/slotting-optimization",
			"urn:warehouse:slotting-optimization:events:SlotPlanApproved:v1")
		if evt.Type() != kafkaconsumer.TypeSlotPlanApproved {
			t.Errorf("%s: type %q, want the routed constant %q", ex.Name, evt.Type(), kafkaconsumer.TypeSlotPlanApproved)
		}
	}
	doc.assertEnvelopeRequired(t, "SlotPlanApproved")
	if doc.DefaultContentType != "application/cloudevents+json" {
		t.Errorf("pinned spec defaultContentType %q, want application/cloudevents+json", doc.DefaultContentType)
	}
}

// E4: unknown ADDITIVE attributes in data (the spec's moves array is already
// ignored; a FUTURE additive field must be too) must not change the outcome;
// an unknown event TYPE on the shared topic is silently skipped; a
// non-CloudEvent message is skipped with a WARN.
func TestUnknownAttributesTolerated_Slotting(t *testing.T) {
	golden := slottingGoldenExamples(t)[0].Payload

	tolerant := clonePayload(golden)
	data := tolerant["data"].(map[string]any)
	data["future_additive_field"] = "unknown-to-this-consumer"
	data["nested_extension"] = map[string]any{"deep": []any{1, "two"}}

	w := newWorld()
	w.seed(t)
	c := w.slottingConsumer()
	handle(t, c, goldenJSON(t, tolerant))

	want := slottingGoldenAssignments(t, goldenData(t, slottingGoldenExamples(t)[0]))
	got, _ := w.read.Assignments(bg, "SITE-1")
	if !slices.Equal(got, want) {
		t.Fatalf("unknown data attributes changed the applied map: %v", got)
	}

	unknownType := clonePayload(golden)
	unknownType["type"] = "com.warehouse.wms.slotting-optimization.slotplan.SlotPlanRejected"
	handle(t, c, goldenJSON(t, unknownType)) // must be skipped, not an error

	flat := []byte(`{"event_id":"e-flat","event_type":"SlotPlanApproved","plan_id":"p1"}`)
	handle(t, c, flat) // not a CloudEvent: skipped with a WARN, committed past
	if !strings.Contains(w.logs.String(), "not a valid CloudEvent") {
		t.Fatalf("the flat envelope must be skipped with a WARN:\n%s", w.logs.String())
	}
}

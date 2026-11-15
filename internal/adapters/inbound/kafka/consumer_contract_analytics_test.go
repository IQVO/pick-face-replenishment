package kafka_test

// Event consumer contract tests (E1-E4) for the SELF-PINNED analytics edge:
// this repo's own projector (cmd/pfr-projector) reads
// warehouse.pick-face-replenishment.analytics (ownAnalyticsTopic below,
// mirroring the events.TopicAnalytics constant its reader config wires —
// inbound code never imports the outbound events package) and folds the four
// analytics kinds into the report projection. The contract is the PINNED
// copy of this repo's OWN apis/asyncapi.yaml
// (testdata/pick-face-replenishment.asyncapi.yaml): the pin registers this
// repo as its own producer, so the pinned-contracts CI job also fails when
// OUR spec drifts from the pin. Deterministic poison on this topic goes to
// warehouse.pick-face-replenishment.analytics.dlq (ADR 0008) — E4 covers
// that path with the spec's own golden example.
//
// Golden fixtures are the pinned spec's OWN examples. The projector has no
// CloudEvents-id dedupe of its own (the analytical store folds
// at-least-once), so E2 asserts exactly-once routing per golden event
// rather than a replay gate.

import (
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// ownAnalyticsGoldenExamples returns the pinned own spec's examples for one
// analytics message.
func ownAnalyticsGoldenExamples(t *testing.T, message string) []pinnedExample {
	t.Helper()
	doc := loadPinnedAsyncAPI(t, pinnedOwnSpec)
	msg := doc.message(t, message)
	if len(msg.Examples) == 0 {
		t.Fatalf("pinned own spec documents %s without examples", message)
	}
	return msg.Examples
}

// E1: the topic the projector's reader config subscribes to
// (ownAnalyticsTopic) is documented by the pinned own spec as a 'subscribe:'
// channel whose message list carries one message per type analyticsKinds
// routes on. The analytics twins reuse the integration channel's types (same
// `type` under the analytics dataschema, per the channel description), so
// the routed type is asserted through the same-named integration message;
// the channel must also document the .dlq destination for deterministic
// poison.
func TestPinnedAsyncAPICoversSubscriptions_OwnAnalytics(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedOwnSpec)

	names := map[string]bool{} // analytics channel message KEYS
	byName := map[string]string{}
	for _, ref := range doc.subscribeMessageRefs(t, ownAnalyticsTopic) {
		names[ref] = true
		byName[doc.message(t, ref).Name] = ref
	}
	integrationNames := map[string]bool{}
	for _, name := range doc.subscribeMessageRefs(t, ownEventsTopic) {
		integrationNames[name] = true
	}
	for _, kind := range []struct{ entity, name string }{
		{"replenishment", string(report.KindReplenishmentRequested)},
		{"replenishment", string(report.KindReplenishmentCompleted)},
		{"replenishment", string(report.KindReplenishmentCancelled)},
		{"policy", string(report.KindReplenishmentPolicyDeclared)},
	} {
		ref, listed := byName[kind.name]
		if !listed {
			t.Errorf("pinned own spec's %s channel lists no %s message (messages: %v)",
				ownAnalyticsTopic, kind.name, names)
			continue
		}
		// The twins document their type through the same-named integration
		// message; the analytics-only policy message documents its own.
		typ := doc.documentedType(t, ref)
		if typ == "" && integrationNames[kind.name] {
			typ = doc.documentedType(t, kind.name)
		}
		if typ != cloudevents.Type(kind.entity, kind.name) {
			t.Errorf("%s documents type %q, want the routed %q", kind.name, typ, cloudevents.Type(kind.entity, kind.name))
		}
	}
	if ch := doc.Channels[ownAnalyticsTopic]; !strings.Contains(ch.Description, ownAnalyticsTopic+".dlq") {
		t.Errorf("the %s channel description must document its .dlq poison destination", ownAnalyticsTopic)
	}
}

// E2: every golden analytics example decodes through the REAL decode and
// dispatch (AnalyticsConsumer.HandleMessage over a scripted projection) and
// is folded exactly once, carrying the wire's own facts.
func TestGoldenEventsDecode_OwnAnalytics(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    report.Event
	}{
		{"ReplenishmentRequestedAnalytics", report.Event{Kind: report.KindReplenishmentRequested,
			EventID: "6f1d2c3b-4a5e-4b7c-8d9e-0a1b2c3d4e5f", SiteID: "WH1", SKU: "SKU-1",
			RequestID: "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07", Quantity: 24, Priority: "NORMAL"}},
		{"ReplenishmentCompletedAnalytics", report.Event{Kind: report.KindReplenishmentCompleted,
			EventID: "0b9a8c7d-6e5f-4a3b-9c2d-1e0f9a8b7c6d", SiteID: "WH1", SKU: "SKU-1",
			RequestID: "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07", Quantity: 24}},
		{"ReplenishmentCancelledAnalytics", report.Event{Kind: report.KindReplenishmentCancelled,
			EventID: "4d3c2b1a-0f9e-4d8c-8b7a-6f5e4d3c2b1a", SiteID: "WH1", SKU: "SKU-1",
			RequestID: "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07", CancelReason: "ASSIGNMENT_CHANGED"}},
		{"ReplenishmentPolicyDeclared", report.Event{Kind: report.KindReplenishmentPolicyDeclared,
			EventID: "2b1a0f9e-8d7c-4b6a-a594-837261504f3e", SiteID: "WH1", SKU: "SKU-1",
			Version: 1, MinQty: 10, MaxQty: 60, Multiple: 6}},
	} {
		t.Run(tc.message, func(t *testing.T) {
			ex := ownAnalyticsGoldenExamples(t, tc.message)[0]
			p := &fakeProjection{}
			c := newAnalyticsConsumer(p, &fakeDLQWriter{}, newFakeReader())
			if err := c.HandleMessage(bg, kafkago.Message{Value: goldenJSON(t, ex.Payload)}); err != nil {
				t.Fatalf("HandleMessage = %v, want nil", err)
			}
			if p.count() != 1 {
				t.Fatalf("applied %d events, want exactly one", p.count())
			}
			got := p.events()[0]
			if got.Kind != tc.want.Kind || got.EventID != tc.want.EventID || got.SiteID != tc.want.SiteID ||
				got.SKU != tc.want.SKU || got.RequestID != tc.want.RequestID {
				t.Fatalf("applied = %+v, want kind/id/site/sku/request from the wire", got)
			}
			if want := analyticsGoldenAt(t, ex); !got.At.Equal(want) {
				t.Errorf("applied At = %v, want the envelope time %v", got.At, want)
			}
		})
	}
}

// analyticsGoldenAt parses the golden example's envelope time.
func analyticsGoldenAt(t *testing.T, ex pinnedExample) time.Time {
	t.Helper()
	raw, _ := ex.Payload["time"].(string)
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("golden example %s carries no parseable time: %v", ex.Name, err)
	}
	return at
}

// E3: every golden fixture carries the fleet-mandatory CloudEvents 1.0
// envelope with the ANALYTICS dataschema stream segment (the twins reuse the
// integration event names; only the stream segment differs), and the pinned
// spec's CloudEventEnvelope requires the full attribute set.
func TestCloudEventsEnvelopeInvariants_OwnAnalytics(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedOwnSpec)
	for _, message := range []string{
		"ReplenishmentRequestedAnalytics", "ReplenishmentCompletedAnalytics",
		"ReplenishmentCancelledAnalytics", "ReplenishmentPolicyDeclared",
	} {
		msg := doc.message(t, message)
		for _, ex := range msg.Examples {
			assertGoldenEnvelope(t, ex.Name, goldenJSON(t, ex.Payload), "/warehouse/pick-face-replenishment",
				"urn:warehouse:pick-face-replenishment:analytics:"+msg.Name+":v1")
		}
		doc.assertEnvelopeRequired(t, message)
	}
	if doc.DefaultContentType != "application/cloudevents+json" {
		t.Errorf("pinned spec defaultContentType %q, want application/cloudevents+json", doc.DefaultContentType)
	}
}

// E4: unknown ADDITIVE attributes in data must not change the fold; an
// unknown event TYPE is ignored; a non-CloudEvent message is skipped (never
// retried, never dead-lettered); and DETERMINISTIC POISON — a known type
// whose payload contradicts the envelope subject — is dead-lettered AT ONCE
// to the documented .dlq topic with the x-dlq-* headers (ADR 0008).
func TestUnknownAttributesTolerated_OwnAnalytics(t *testing.T) {
	golden := ownAnalyticsGoldenExamples(t, "ReplenishmentRequestedAnalytics")[0].Payload

	tolerant := clonePayload(golden)
	data := tolerant["data"].(map[string]any)
	data["future_additive_field"] = "unknown-to-this-consumer"
	data["nested_extension"] = map[string]any{"deep": []any{1, "two"}}

	p := &fakeProjection{}
	c := newAnalyticsConsumer(p, &fakeDLQWriter{}, newFakeReader())
	if err := c.HandleMessage(bg, kafkago.Message{Value: goldenJSON(t, tolerant)}); err != nil {
		t.Fatalf("HandleMessage with unknown data attributes = %v", err)
	}
	if p.count() != 1 || p.events()[0].Quantity != 24 {
		t.Fatalf("unknown data attributes changed the fold: %+v", p.events())
	}

	unknownType := clonePayload(golden)
	unknownType["type"] = "com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRescheduled"
	if err := c.HandleMessage(bg, kafkago.Message{Value: goldenJSON(t, unknownType)}); err != nil {
		t.Fatalf("unknown type must be ignored, not an error: %v", err)
	}

	flat := kafkago.Message{Value: []byte(`{"event_type":"ReplenishmentRequested","request_id":"r1"}`)}
	if err := c.HandleMessage(bg, flat); err != nil {
		t.Fatalf("a non-CloudEvent must be skipped, not an error: %v", err)
	}
	if p.count() != 1 {
		t.Fatalf("applied %d events, want only the tolerant golden", p.count())
	}

	// Poison: payload names a different request than the envelope subject.
	poison := clonePayload(golden)
	poison["id"] = "golden-poison-fresh-id-1" // fresh id: the original belongs to the verbatim run
	poison["data"].(map[string]any)["request_id"] = "repl-a-different-request"
	dlq := &fakeDLQWriter{}
	pc := newAnalyticsConsumer(&fakeProjection{}, dlq, newFakeReader())
	msg := kafkago.Message{Partition: 1, Offset: 9, Value: goldenJSON(t, poison)}
	if err := pc.HandleMessage(bg, msg); err != nil {
		t.Fatalf("poison must be dead-lettered, not an error: %v", err)
	}
	published := dlq.published()
	if len(published) != 1 || published[0].Topic != pc.DLQTopic || string(published[0].Value) != string(msg.Value) {
		t.Fatalf("dead-lettered = %+v, want the raw poison on %s", published, pc.DLQTopic)
	}
	if headerOf(published[0], "x-dlq-error") == "" || headerOf(published[0], "x-dlq-failed-at") == "" {
		t.Fatal("the dead-lettered message must carry x-dlq-error and x-dlq-failed-at")
	}
}

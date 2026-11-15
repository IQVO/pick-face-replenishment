package kafka_test

// Event consumer contract tests (E1-E4) for the facility-layout edge: this
// repo's layout consumer subscribes to warehouse.facility.events
// (kafkaconsumer.LayoutTopic — the topic constant its reader config wires)
// and acts on ZoneRegistered, LocationSlotRegistered and
// LocationSlotDecommissioned. The contract is the PINNED copy of
// facility-layout's apis/asyncapi.yaml (testdata/facility-layout.asyncapi.yaml,
// registered in contracts/pinned.json; the pinned-contracts CI job fails when
// the copy drifts from IQVO/facility-layout@develop).
//
// Golden fixtures are the producer spec's OWN examples. NOTE:
// LocationSlotDecommissioned is documented WITHOUT examples, so it gets E1
// (and the envelope-schema half of E3) only — flagged in the PR body for the
// producer example backfill.

import (
	"strconv"
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// facilityGoldenExamples returns the pinned spec's own examples for one
// facility-layout message.
func facilityGoldenExamples(t *testing.T, message string) []pinnedExample {
	t.Helper()
	doc := loadPinnedAsyncAPI(t, pinnedFacilitySpec)
	msg := doc.message(t, message)
	if len(msg.Examples) == 0 {
		t.Fatalf("pinned facility-layout spec documents %s without examples", message)
	}
	return msg.Examples
}

// E1: the topic the reader config subscribes to (LayoutTopic) is documented
// by the producer's pinned spec as a 'subscribe:' channel, and the message
// list on that channel documents every CloudEvents type the layout consumer
// routes on (plus the types it must silently ignore).
func TestPinnedAsyncAPICoversSubscriptions_FacilityEvents(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedFacilitySpec)
	documented := doc.documentedTypes(t, kafkaconsumer.LayoutTopic)

	for _, typ := range []string{
		kafkaconsumer.TypeZoneRegistered,
		kafkaconsumer.TypeLocationSlotRegistered,
		kafkaconsumer.TypeLocationSlotDecommissioned,
	} {
		if _, ok := documented[typ]; !ok {
			t.Errorf("pinned facility-layout spec does not document %s on %s (documented: %v)",
				typ, kafkaconsumer.LayoutTopic, documented)
		}
	}
	t.Logf("NOTE: %s (%s in the documented set) carries no examples in the pinned spec: E1-only coverage, producer example backfill pending",
		kafkaconsumer.TypeLocationSlotDecommissioned, documented[kafkaconsumer.TypeLocationSlotDecommissioned])
}

// E2: every golden example decodes through the REAL decode+dispatch
// (Consumer.HandleMessage over the real use cases) and lands in the layout
// read model exactly once, carrying the wire's own facts; a replayed
// CloudEvents id applies nothing more.
func TestGoldenEventsDecode_FacilityLayout(t *testing.T) {
	for _, ex := range facilityGoldenExamples(t, "zoneRegistered") {
		t.Run("zoneRegistered/"+ex.Name, func(t *testing.T) {
			w := newWorld()
			handle(t, w.layoutConsumer(), goldenJSON(t, ex.Payload))

			zoneID, _ := goldenData(t, ex)["zoneId"].(string)
			place := facilityPlaceOf(t, w, "golden-zone-probe", zoneID)
			if place.SiteCode != goldenData(t, ex)["siteCode"] || place.ZoneCode != goldenData(t, ex)["zoneCode"] {
				t.Fatalf("place = %+v, want the golden zone's own site/zone codes", place)
			}
			facilityAssertReplayDuplicate(t, w, goldenJSON(t, ex.Payload))
		})
	}
	for _, ex := range facilityGoldenExamples(t, "locationSlotRegistered") {
		t.Run("locationSlotRegistered/"+ex.Name, func(t *testing.T) {
			w := newWorld()
			c := w.layoutConsumer()
			facilitySeedGoldenZones(t, c)
			handle(t, c, goldenJSON(t, ex.Payload))

			data := goldenData(t, ex)
			code, _ := data["locationCode"].(string)
			place := facilityPlaceOf(t, w, code, "")
			if place.SiteCode != "WH1" {
				t.Fatalf("place = %+v, want the golden zone's site WH1", place)
			}
			if want, _ := data["role"].(string); want != "" && place.Role != want {
				t.Fatalf("place = %+v, want role %q from the wire", place, want)
			}
			facilityAssertReplayDuplicate(t, w, goldenJSON(t, ex.Payload))
		})
	}
}

// facilitySeedGoldenZones applies the pinned zoneRegistered examples (with
// FRESH CloudEvents ids — every facility-layout example in the pinned spec
// shares ONE id, and a seed must not claim the location goldens' own id)
// so the golden location examples' zones (WH1-YARD-NORTH, WH1-STOR-AMB)
// exist.
func facilitySeedGoldenZones(t *testing.T, c *kafkaconsumer.Consumer) {
	t.Helper()
	for i, ex := range facilityGoldenExamples(t, "zoneRegistered") {
		seed := clonePayload(ex.Payload)
		seed["id"] = "golden-zone-seed-" + strconv.Itoa(i)
		handle(t, c, goldenJSON(t, seed))
	}
}

// facilityPlaceOf resolves a location's place in the layout read model,
// seeding a probe location in zoneID first when a non-empty zoneID is given.
func facilityPlaceOf(t *testing.T, w *world, code, zoneID string) repository.Place {
	t.Helper()
	if zoneID != "" {
		if err := w.read.SaveLocation(bg, repository.Location{Code: code, ZoneID: zoneID}); err != nil {
			t.Fatal(err)
		}
	}
	place, known, err := w.read.Place(bg, code)
	if err != nil || !known {
		t.Fatalf("Place(%s) = %+v, known=%v, err=%v", code, place, known, err)
	}
	return place
}

// facilityAssertReplayDuplicate redelivers value and asserts the CloudEvents
// id gate applied nothing more (duplicate outcome logged).
func facilityAssertReplayDuplicate(t *testing.T, w *world, value []byte) {
	t.Helper()
	handle(t, w.layoutConsumer(), value)
	if !strings.Contains(w.logs.String(), "outcome=duplicate") {
		t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
	}
}

// E3: every golden fixture carries the fleet-mandatory CloudEvents 1.0
// envelope the way the pinned spec documents it: specversion 1.0, the full
// attribute set, source /warehouse/facility-layout, datacontenttype
// application/json, and the dataschema const the spec's overlay pins.
func TestCloudEventsEnvelopeInvariants_Facility(t *testing.T) {
	doc := loadPinnedAsyncAPI(t, pinnedFacilitySpec)
	for _, message := range []string{"zoneRegistered", "locationSlotRegistered", "locationSlotDecommissioned"} {
		msg := doc.message(t, message)
		consts := doc.payloadConsts(t, msg.Payload)
		for _, ex := range msg.Examples {
			value := goldenJSON(t, ex.Payload)
			evt := assertGoldenEnvelope(t, ex.Name, value, "/warehouse/facility-layout", consts["dataschema"])
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

// E4: unknown ADDITIVE attributes in data must not change the decode or the
// outcome; an unknown event TYPE on the shared topic is silently skipped;
// a message that is not a CloudEvent at all is skipped with a WARN, never an
// error (this repo's documented policy: deterministic skips commit past).
func TestUnknownAttributesTolerated_Facility(t *testing.T) {
	golden := facilityGoldenExamples(t, "locationSlotRegistered")[0].Payload

	tolerant := clonePayload(golden)
	tolerant["id"] = "golden-fac-tolerant-1" // fresh id: a perturbed clone never reuses a claimed id
	data := tolerant["data"].(map[string]any)
	data["future_additive_field"] = "unknown-to-this-consumer"
	data["nested_extension"] = map[string]any{"deep": []any{1, "two"}}

	w := newWorld()
	c := w.layoutConsumer()
	facilitySeedGoldenZones(t, c)
	handle(t, c, goldenJSON(t, tolerant))
	code, _ := golden["data"].(map[string]any)["locationCode"].(string)
	if _, known, _ := w.read.Place(bg, code); !known {
		t.Fatalf("unknown data attributes broke the location registration of %q", code)
	}

	unknownType := clonePayload(golden)
	unknownType["id"] = "golden-fac-unknown-type-1"
	unknownType["type"] = "com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated"
	handle(t, c, goldenJSON(t, unknownType)) // must be skipped, not an error

	flat := []byte(`{"event_id":"e-flat","event_type":"LocationSlotRegistered","locationCode":"X","zoneId":"WH1-STOR-AMB"}`)
	handle(t, c, flat) // not a CloudEvent: skipped with a WARN, committed past
}

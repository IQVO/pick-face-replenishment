package kafka_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
)

func TestLayout_ZonesAndLocationsFeedTheLayoutCopy(t *testing.T) {
	w := newWorld()
	c := w.layoutConsumer()
	handle(t, c, zoneMsg("e1", "z-rsv", "RSV"))
	handle(t, c, locationMsg("e2", "R-07", "z-rsv"))

	place, known, err := w.read.Place(bg, "R-07")
	if err != nil || !known {
		t.Fatalf("Place = %+v, %v, %v", place, known, err)
	}
	if place.SiteCode != "S1" || place.ZoneCode != "RSV" || !place.IsStorage() || place.Decommissioned {
		t.Fatalf("place = %+v, want an active Storage location of zone RSV at S1", place)
	}
}

func TestLayout_ARoleOtherThanStorageIsKept(t *testing.T) {
	w := newWorld()
	c := w.layoutConsumer()
	handle(t, c, zoneMsg("e1", "z-rsv", "RSV"))
	handle(t, c, facilityMsg("e2", typeLocation, "R-08", `{"locationCode":"R-08","zoneId":"z-rsv","role":"Dock"}`))

	place, _, _ := w.read.Place(bg, "R-08")
	if place.Role != "Dock" || place.IsStorage() {
		t.Fatalf("place = %+v, want role Dock", place)
	}
}

func TestLayout_DecommissionRetiresTheLocation(t *testing.T) {
	w := newWorld()
	c := w.layoutConsumer()
	handle(t, c, zoneMsg("e1", "z-rsv", "RSV"))
	handle(t, c, locationMsg("e2", "R-07", "z-rsv"))
	handle(t, c, decommissionedMsg("e3", "R-07"))
	handle(t, c, locationMsg("e4", "R-07", "z-rsv")) // a later registration does not bring it back

	if place, _, _ := w.read.Place(bg, "R-07"); !place.Decommissioned {
		t.Fatalf("place = %+v, want decommissioned for good", place)
	}
}

func TestLayout_DuplicateEventIDIsAppliedOnce(t *testing.T) {
	w := newWorld()
	c := w.layoutConsumer()
	handle(t, c, zoneMsg("e1", "z-rsv", "RSV"))
	handle(t, c, zoneMsg("e1", "z-rsv", "OTHER")) // same id: the replay must not rewrite the zone

	handle(t, c, locationMsg("e2", "R-07", "z-rsv"))
	if place, _, _ := w.read.Place(bg, "R-07"); place.ZoneCode != "RSV" {
		t.Fatalf("zone code = %q, want RSV: the duplicate id must be skipped", place.ZoneCode)
	}
	if !strings.Contains(w.logs.String(), "outcome=duplicate") {
		t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
	}
}

func TestLayout_UnknownTypeIsIgnoredSilently(t *testing.T) {
	w := newWorld()
	geometry := facilityMsg("e1", typeGeometry, "R-07", `{"locationCode":"R-07","xM":1,"yM":2,"zM":0}`)
	handle(t, w.layoutConsumer(), geometry)
	if w.uow.count() != 0 || strings.Contains(w.logs.String(), "level=WARN") {
		t.Fatalf("an unknown type must be ignored silently (uow calls %d):\n%s", w.uow.count(), w.logs.String())
	}
}

func TestLayout_LegacyFlatMessageIsSkippedWithWarnAndCommittedPast(t *testing.T) {
	w := newWorld()
	legacy := `{"event_id":"e-1","event_type":"ZoneRegistered","occurred_at":"2026-10-17T09:00:00Z","zoneId":"z-rsv","siteCode":"S1","zoneCode":"RSV"}`
	reader := newFakeReader([]byte(legacy), zoneMsg("e1", "z-rsv", "RSV"), locationMsg("e2", "R-07", "z-rsv"))
	if err := runUntilCommits(t, w.layoutConsumer(), reader, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if !strings.Contains(w.logs.String(), "level=WARN") || !strings.Contains(w.logs.String(), "not a valid CloudEvent") {
		t.Fatalf("the legacy flat message must be skipped with a WARN:\n%s", w.logs.String())
	}
	if place, known, _ := w.read.Place(bg, "R-07"); !known || place.ZoneCode != "RSV" {
		t.Fatalf("place = %+v, known %v: the messages after the legacy one must still be applied", place, known)
	}
}

func TestLayout_PayloadsThatCanNeverBeAppliedAreDeadLettered(t *testing.T) {
	w := newWorld()
	noZone := facilityMsg("e1", typeLocation, "R-07", `{"locationCode":"R-07"}`)
	noCode := facilityMsg("e2", typeDecom, "x", `{}`)
	noZoneCode := facilityMsg("e3", typeZone, "z-1", `{"zoneId":"z-1","siteCode":"S1"}`)
	reader := newFakeReader(noZone, noCode, noZoneCode)
	dlq := &fakeDLQWriter{}
	c := w.layoutConsumer()
	c.DLQ, c.DeadLetterInvalid = dlq, true

	if err := runUntilCommits(t, c, reader, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if got := dlq.published(); len(got) != 3 || got[0].Topic != kafkaconsumer.LayoutTopic+".dlq" {
		t.Fatalf("dead-lettered = %+v, want the three poison payloads on the .dlq topic", got)
	}
}

func TestLayout_ConstructorBuildsTheConsumerWithoutDialling(t *testing.T) {
	w := newWorld()
	c := kafkaconsumer.NewLayoutConsumer([]string{"127.0.0.1:1"}, "group-from-env", layoutUseCases(w), w.logger())
	assertConstructed(t, c, kafkaconsumer.LayoutTopic,
		kafkaconsumer.TypeZoneRegistered, kafkaconsumer.TypeLocationSlotRegistered, kafkaconsumer.TypeLocationSlotDecommissioned)
}

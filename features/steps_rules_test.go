package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// registerRaiseRulesSteps adds the vocabulary of raise_rules.feature: delivery
// under a FIXED CloudEvents id (the claim names the consumer, so the same id may
// reach two consumers) and the timestamp stamp of the last request fact. It
// reuses every step of the earlier suites.
func registerRaiseRulesSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a bin level of (-?\d+) on hand and (-?\d+) reserved at version (-?\d+) for "([^"]*)" in bin "([^"]*)" of site "([^"]*)" is delivered with the event id "([^"]*)"$`, w.levelWithEventID)
	sc.Step(`^a zone registration with id "([^"]*)", site "([^"]*)" and code "([^"]*)" is delivered with the event id "([^"]*)"$`, w.zoneWithEventID)
	sc.Step(`^the last (ReplenishmentRequested|ReplenishmentCompleted|ReplenishmentCancelled) fact is stamped "([^"]*)"$`, w.lastFactStamped)
}

// deliverDocEvent delivers one consumed event under a FIXED CloudEvents id, so
// a scenario can prove the claim is per (consumer, event id), and remembers how
// to deliver the same event again (world.deliver mints a fresh id every call).
func (w *world) deliverDocEvent(eventID string, handle func(id string) (usecases.Outcome, error)) {
	w.redeliver = func() {
		w.run(func() error {
			var err error
			w.outcome, err = handle(eventID)
			return err
		})
	}
	w.redeliver()
}

func (w *world) levelWithEventID(onHand, reserved, version int64, sku, bin, site, eventID string) error {
	w.deliverDocEvent(eventID, func(id string) (usecases.Outcome, error) {
		return w.uc.binLevel.Handle(w.ctx, id, usecases.BinLevelChange{
			SiteCode: site, Bin: bin, SKU: sku, OnHand: onHand, Reserved: reserved, Version: version,
		})
	})
	return nil
}

func (w *world) zoneWithEventID(id, site, code, eventID string) error {
	w.deliverDocEvent(eventID, func(eid string) (usecases.Outcome, error) {
		return w.uc.zone.Handle(w.ctx, eid, usecases.ZoneChange{ZoneID: id, SiteCode: site, ZoneCode: code})
	})
	return nil
}

// lastFactStamped pins that the newest fact of the kind carries the service
// clock's instant: requested_at, completed_at and cancelled_at on the wire.
func (w *world) lastFactStamped(name, want string) error {
	if _, err := time.Parse(time.RFC3339, want); err != nil {
		return fmt.Errorf("stamp %q: %w", want, err)
	}
	stamp := func(at time.Time) error {
		return equal("occurred at of "+name, at.UTC().Format(time.RFC3339), want)
	}
	switch name {
	case "ReplenishmentRequested":
		f, ok := lastFact[replenishment.ReplenishmentRequested](w)
		if !ok {
			return fmt.Errorf("no %s fact was published", name)
		}
		return stamp(f.OccurredAt())
	case "ReplenishmentCompleted":
		f, ok := lastFact[replenishment.ReplenishmentCompleted](w)
		if !ok {
			return fmt.Errorf("no %s fact was published", name)
		}
		return stamp(f.OccurredAt())
	case "ReplenishmentCancelled":
		f, ok := lastFact[replenishment.ReplenishmentCancelled](w)
		if !ok {
			return fmt.Errorf("no %s fact was published", name)
		}
		return stamp(f.OccurredAt())
	}
	return fmt.Errorf("unknown fact %q", name)
}

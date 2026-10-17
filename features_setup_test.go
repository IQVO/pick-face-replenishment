package main_test

import (
	"fmt"
	"regexp"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

const (
	forwardZoneID = "z-fwd"
	reserveZoneID = "z-rsv"
)

func registerSetupSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^site "([^"]*)" has forward bin "([^"]*)" for "([^"]*)" and reserve bins "([^"]*)"$`, w.siteHasForwardBin)
	sc.Step(`^the forward location "([^"]*)" is registered$`, w.forwardLocation)
	sc.Step(`^the location "([^"]*)" is decommissioned$`, w.decommission)
	sc.Step(`^the approved slot plan of site "([^"]*)" assigns (".*")$`, w.slotPlanAssigns)
	sc.Step(`^the approved slot plan of site "([^"]*)" assigns nothing$`, w.slotPlanAssignsNothing)
	sc.Step(`^bin "([^"]*)" of site "([^"]*)" holds (-?\d+) on hand and (\d+) reserved of "([^"]*)"$`, w.binHolds)
	sc.Step(`^bin "([^"]*)" of site "([^"]*)" holds (\d+) of "([^"]*)"$`, w.binHoldsUnreserved)
	sc.Step(`^bin "([^"]*)" of site "([^"]*)" reports a level of (\d+) on hand for "([^"]*)" that is not newer$`, w.binNotNewer)
	sc.Step(`^the last inventory event is delivered again$`, func() error { w.redeliver(); return nil })
}

// siteHasForwardBin registers the forward and reserve zones of the site, the
// forward bin and the reserve bins, and approves a slot plan that assigns the
// SKU to the forward bin.
func (w *world) siteHasForwardBin(site, forward, sku, reserve string) error {
	for _, z := range []usecases.ZoneChange{
		{ZoneID: forwardZoneID, SiteCode: site, ZoneCode: "FWD"},
		{ZoneID: reserveZoneID, SiteCode: site, ZoneCode: "RSV"},
	} {
		w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.zone.Handle(w.ctx, id, z) })
	}
	if err := w.forwardLocation(forward); err != nil {
		return err
	}
	for _, code := range splitList(reserve) {
		c := usecases.LocationChange{Code: code, ZoneID: reserveZoneID}
		w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.location.Handle(w.ctx, id, c) })
	}
	return w.slotPlan(site, []usecases.SlotAssignment{{SKU: sku, Slot: forward}})
}

func (w *world) forwardLocation(code string) error {
	c := usecases.LocationChange{Code: code, ZoneID: forwardZoneID}
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.location.Handle(w.ctx, id, c) })
	return nil
}

func (w *world) decommission(code string) error {
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.decommissioned.Handle(w.ctx, id, code) })
	return nil
}

var assignmentPair = regexp.MustCompile(`"([^"]*)" to "([^"]*)"`)

func (w *world) slotPlanAssigns(site, pairs string) error {
	var as []usecases.SlotAssignment
	for _, m := range assignmentPair.FindAllStringSubmatch(pairs, -1) {
		as = append(as, usecases.SlotAssignment{SKU: m[1], Slot: m[2]})
	}
	return w.slotPlan(site, as)
}

func (w *world) slotPlanAssignsNothing(site string) error { return w.slotPlan(site, nil) }

func (w *world) slotPlan(site string, as []usecases.SlotAssignment) error {
	c := usecases.ForwardMapChange{SiteID: site, Assignments: as}
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.forwardMap.Handle(w.ctx, id, c) })
	return nil
}

// nextVersion is the per-(site, bin, SKU) version of the next level message.
func (w *world) nextVersion(site, bin, sku string) int64 {
	key := fmt.Sprintf("%s/%s/%s", site, bin, sku)
	w.versions[key]++
	return w.versions[key]
}

func (w *world) deliverLevel(c usecases.BinLevelChange) {
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.binLevel.Handle(w.ctx, id, c) })
}

func (w *world) binHolds(bin, site string, onHand, reserved int64, sku string) error {
	w.deliverLevel(usecases.BinLevelChange{
		SiteCode: site, Bin: bin, SKU: sku, OnHand: onHand, Reserved: reserved,
		Version: w.nextVersion(site, bin, sku),
	})
	return nil
}

func (w *world) binHoldsUnreserved(bin, site string, onHand int64, sku string) error {
	return w.binHolds(bin, site, onHand, 0, sku)
}

// binNotNewer replays the version the bin is already at.
func (w *world) binNotNewer(bin, site string, onHand int64, sku string) error {
	w.deliverLevel(usecases.BinLevelChange{
		SiteCode: site, Bin: bin, SKU: sku, OnHand: onHand,
		Version: w.versions[fmt.Sprintf("%s/%s/%s", site, bin, sku)],
	})
	return nil
}

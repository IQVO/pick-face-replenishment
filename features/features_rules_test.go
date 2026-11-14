package features_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// registerRulesSteps adds the vocabulary of the documentation-derived scenarios
// (detection_rules, lifecycle_rules, consumer_rules, pack_derived_rules). It
// reuses every step of the original suite and only adds what they lack.
func registerRulesSteps(sc *godog.ScenarioContext, w *world) {
	registerLayoutRuleSteps(sc, w)
	registerRawEventSteps(sc, w)
	registerQuerySteps(sc, w)
	registerSweepAndRequestSteps(sc, w)
	registerFactSteps(sc, w)
	registerFaceViewSteps(sc, w)
	registerPackSteps(sc, w)
}

func registerLayoutRuleSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the zone "([^"]*)" of site "([^"]*)" has code "([^"]*)"$`, w.zoneHasCode)
	sc.Step(`^a zone registration with id "([^"]*)", site "([^"]*)" and code "([^"]*)" is delivered$`, w.zoneHasCode)
	sc.Step(`^the location "([^"]*)" is registered in zone "([^"]*)"$`, func(code, zone string) error { return w.registerLocation(code, zone, "") })
	sc.Step(`^the location "([^"]*)" is registered in zone "([^"]*)" with role "([^"]*)"$`, w.registerLocation)
	sc.Step(`^a location registration with code "([^"]*)" and zone "([^"]*)" is delivered$`, func(code, zone string) error { return w.registerLocation(code, zone, "") })
	sc.Step(`^a decommission of location "([^"]*)" is delivered$`, w.decommission)
	sc.Step(`^a candidate bin "([^"]*)" at site "([^"]*)" in zone "([^"]*)" with role "([^"]*)" holds (\d+) of "([^"]*)"$`, w.candidateBin)
	sc.Step(`^the last event is delivered again$`, func() error { w.redeliver(); return nil })
}

func (w *world) zoneHasCode(id, site, code string) error {
	z := usecases.ZoneChange{ZoneID: id, SiteCode: site, ZoneCode: code}
	w.deliver(func(eventID string) (usecases.Outcome, error) { return w.uc.zone.Handle(w.ctx, eventID, z) })
	return nil
}

func (w *world) registerLocation(code, zone, role string) error {
	c := usecases.LocationChange{Code: code, ZoneID: zone, Role: role}
	w.deliver(func(eventID string) (usecases.Outcome, error) { return w.uc.location.Handle(w.ctx, eventID, c) })
	return nil
}

// candidateBin registers a location in the given zone and reports its stock.
func (w *world) candidateBin(bin, site, zone, role string, onHand int64, sku string) error {
	if err := w.registerLocation(bin, zone, role); err != nil {
		return err
	}
	return w.binHoldsUnreserved(bin, site, onHand, sku)
}

func registerRawEventSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a level of (-?\d+) on hand and (-?\d+) reserved at version (-?\d+) for "([^"]*)" in bin "([^"]*)" of site "([^"]*)" is delivered$`, w.rawLevel)
	sc.Step(`^a slot plan "([^"]*)" of site "([^"]*)" approved at "([^"]*)" assigning "([^"]*)" to "([^"]*)" is delivered$`, w.rawSlotPlan)
}

func (w *world) rawLevel(onHand, reserved, version int64, sku, bin, site string) error {
	w.deliverLevel(usecases.BinLevelChange{
		SiteCode: site, Bin: bin, SKU: sku, OnHand: onHand, Reserved: reserved, Version: version,
	})
	return nil
}

func (w *world) rawSlotPlan(planID, site, at, sku, slot string) error {
	var when time.Time
	if at != "" {
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return fmt.Errorf("approval time %q: %w", at, err)
		}
		when = parsed
	}
	return w.slotPlanAt(site, planID, when, []usecases.SlotAssignment{{SKU: sku, Slot: slot}})
}

func registerQuerySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I cancel the request "([^"]*)"$`, w.cancelByID)
	sc.Step(`^I get the policy of "([^"]*)" at site "([^"]*)"$`, w.getPolicyOf)
	sc.Step(`^I list the requests with the limit (-?\d+)$`, func(n int) error { return w.listRequestsOf("", "", n) })
	sc.Step(`^I list the pick faces of site "([^"]*)" with the limit (-?\d+)$`, func(site string, n int) error { return w.listFaces(site, "", n) })
	sc.Step(`^I list policies with the limit (-?\d+)$`, w.listPoliciesPage)
	sc.Step(`^I list the requests with the cursor "([^"]*)"$`, w.requestsWithCursor)
	sc.Step(`^I list the pick faces of site "([^"]*)" with the cursor "([^"]*)"$`, w.facesWithCursor)
	sc.Step(`^I list policies with the cursor "([^"]*)"$`, w.policiesWithCursor)
	sc.Step(`^I list policies of site "([^"]*)"$`, func(site string) error { return w.listPoliciesFiltered(site, "") })
	sc.Step(`^I list policies of sku "([^"]*)"$`, func(sku string) error { return w.listPoliciesFiltered("", sku) })
	sc.Step(`^the command is (accepted|refused)$`, w.commandIs)
	sc.Step(`^the policy of "([^"]*)" at site "([^"]*)" (exists|does not exist)$`, w.policyExists)
}

func (w *world) cancelByID(id string) error {
	w.run(func() error {
		_, err := w.uc.cancelRequest.Handle(w.ctx, id)
		return err
	})
	return nil
}

func (w *world) getPolicyOf(sku, site string) error {
	w.run(func() error {
		_, err := w.uc.getPolicy.Handle(w.ctx, site, sku)
		return err
	})
	return nil
}

func (w *world) requestsWithCursor(cursor string) error {
	w.reqQuery = usecases.ListRequestsQuery{Cursor: cursor}
	return w.runRequests()
}

func (w *world) facesWithCursor(site, cursor string) error {
	w.faceQuery = usecases.PickFaceQuery{SiteID: site, Cursor: cursor}
	return w.runFaces()
}

func (w *world) policiesWithCursor(cursor string) error {
	w.policyQuery = usecases.ListPoliciesQuery{Cursor: cursor}
	return w.runPolicies()
}

func (w *world) listPoliciesFiltered(site, sku string) error {
	w.policyQuery = usecases.ListPoliciesQuery{SiteID: site, SKU: sku}
	return w.runPolicies()
}

// commandIs marks the last command as looked at and checks whether it was
// accepted, whatever the reason of a refusal.
func (w *world) commandIs(verdict string) error {
	w.errChecked = true
	if verdict == "accepted" && w.err != nil {
		return fmt.Errorf("command was refused: %w", w.err)
	}
	if verdict == "refused" && w.err == nil {
		return errors.New("command was accepted, want it refused")
	}
	return nil
}

func (w *world) policyExists(sku, site, verdict string) error {
	_, err := w.uc.getPolicy.Handle(w.ctx, site, sku)
	if verdict == "exists" && err != nil {
		return fmt.Errorf("policy of %s at %s: %w", sku, site, err)
	}
	if verdict == "does not exist" && err == nil {
		return fmt.Errorf("policy of %s at %s exists, want none", sku, site)
	}
	return nil
}

func registerSweepAndRequestSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the sweep evaluated (\d+) pick faces$`, func(want int) error {
		return equal("evaluated faces", fmt.Sprint(w.summary.Evaluated), fmt.Sprint(want))
	})
	sc.Step(`^the sweep raised the requests "([^"]*)"$`, func(want string) error {
		return equal("sweep request ids", strings.Join(w.summary.RequestIDs, ","), strings.Join(splitList(want), ","))
	})
	sc.Step(`^the request "([^"]*)" moves (\d+) units of "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.requestByIDMoves)
	sc.Step(`^the request for "([^"]*)" at site "([^"]*)" is at version (\d+)$`, w.requestVersion)
	sc.Step(`^the oldest request for "([^"]*)" at site "([^"]*)" is "([^"]*)"$`, w.oldestIs)
	sc.Step(`^the units requested for "([^"]*)" at site "([^"]*)" are (\d+)$`, w.unitsRequested)
}

func (w *world) requestByIDMoves(id string, qty int64, sku, from, to string) error {
	r, err := w.uc.getRequest.Handle(w.ctx, id)
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%d %s %s>%s", r.Quantity(), r.SKU(), r.SourceBin(), r.ForwardBin())
	return equal("request "+id, got, fmt.Sprintf("%d %s %s>%s", qty, sku, from, to))
}

func (w *world) requestVersion(sku, site string, want int64) error {
	r, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	return equal("request version", fmt.Sprint(r.Version()), fmt.Sprint(want))
}

func (w *world) oldestIs(sku, site, state string) error {
	r, err := w.oldest(site, sku)
	if err != nil {
		return err
	}
	return equal("request state", string(r.State()), state)
}

// unitsRequested sums the quantity of the open requests of the SKU at the site.
func (w *world) unitsRequested(sku, site string, want int64) error {
	page, err := w.uc.listRequests.Handle(w.ctx, usecases.ListRequestsQuery{
		SiteID: site, SKU: sku, State: string(replenishment.StateRequested), Limit: usecases.MaxListLimit,
	})
	if err != nil {
		return err
	}
	var total int64
	for _, r := range page.Items {
		total += r.Quantity()
	}
	return equal("units requested", fmt.Sprint(total), fmt.Sprint(want))
}

func registerFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the last ReplenishmentRequested fact moves (\d+) units from "([^"]*)" to "([^"]*)" with priority "([^"]*)" sized by policy min (\d+) max (\d+) at version (\d+)$`, w.lastRequestedFact)
	sc.Step(`^the last ReplenishmentCompleted fact reports (\d+) units moved from "([^"]*)" to "([^"]*)" at version (\d+)$`, w.lastCompletedFact)
	sc.Step(`^the last ReplenishmentCancelled fact has reason "([^"]*)" at version (\d+)$`, w.lastCancelledFact)
	sc.Step(`^the last policy declaration of "([^"]*)" at site "([^"]*)" has version (\d+), multiple (\d+) and source "([^"]*)"$`, w.lastDeclaration)
	sc.Step(`^there are (\d+) policy declarations$`, func(want int) error {
		return equal("policy declarations", fmt.Sprint(len(w.log.Declarations())), fmt.Sprint(want))
	})
}

// lastFact returns the newest published request fact of type T.
func lastFact[T replenishment.Event](w *world) (T, bool) {
	events := w.log.RequestEvents()
	for _, e := range slices.Backward(events) {
		if f, ok := e.(T); ok {
			return f, true
		}
	}
	var zero T
	return zero, false
}

func (w *world) lastRequestedFact(qty int64, from, to, priority string, minQty, maxQty, version int64) error {
	f, ok := lastFact[replenishment.ReplenishmentRequested](w)
	if !ok {
		return errors.New("no ReplenishmentRequested fact was published")
	}
	got := fmt.Sprintf("%d %s>%s %s %d-%d v%d", f.Quantity, f.SourceBin, f.ForwardBin, f.Priority, f.PolicyMin, f.PolicyMax, f.RequestVersion())
	return equal("ReplenishmentRequested", got, fmt.Sprintf("%d %s>%s %s %d-%d v%d", qty, from, to, priority, minQty, maxQty, version))
}

func (w *world) lastCompletedFact(qty int64, from, to string, version int64) error {
	f, ok := lastFact[replenishment.ReplenishmentCompleted](w)
	if !ok {
		return errors.New("no ReplenishmentCompleted fact was published")
	}
	got := fmt.Sprintf("%d %s>%s v%d", f.Quantity, f.SourceBin, f.ForwardBin, f.RequestVersion())
	return equal("ReplenishmentCompleted", got, fmt.Sprintf("%d %s>%s v%d", qty, from, to, version))
}

func (w *world) lastCancelledFact(reason string, version int64) error {
	f, ok := lastFact[replenishment.ReplenishmentCancelled](w)
	if !ok {
		return errors.New("no ReplenishmentCancelled fact was published")
	}
	return equal("ReplenishmentCancelled", fmt.Sprintf("%s v%d", f.Reason, f.RequestVersion()), fmt.Sprintf("%s v%d", reason, version))
}

func (w *world) lastDeclaration(sku, site string, version, multiple int64, source string) error {
	decls := w.log.Declarations()
	for _, d := range slices.Backward(decls) {
		if d.SKU != sku || d.SiteID != site {
			continue
		}
		got := fmt.Sprintf("v%d x%d %s", d.Version, d.Multiple, d.Source)
		return equal("last declaration", got, fmt.Sprintf("v%d x%d %s", version, multiple, source))
	}
	return fmt.Errorf("no policy declaration for %s at %s", sku, site)
}

func registerFaceViewSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" shows (\d+) on hand, (\d+) reserved and (\d+) available$`, w.faceShowsLevels)
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" shows policy min (\d+) and max (\d+)$`, w.faceShowsBounds)
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" shows no policy bounds$`, w.faceShowsNoBounds)
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" shows the open request "([^"]*)"$`, w.faceShowsOpen)
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" shows no open request$`, func(bin, site string) error { return w.faceShowsOpen(bin, site, "") })
}

func (w *world) faceView(bin, site string) (usecases.PickFaceView, error) {
	page, err := w.uc.listPickFaces.Handle(w.ctx, usecases.PickFaceQuery{SiteID: site, Limit: usecases.MaxListLimit})
	if err != nil {
		return usecases.PickFaceView{}, err
	}
	for _, f := range page.Items {
		if f.ForwardBin == bin {
			return f, nil
		}
	}
	return usecases.PickFaceView{}, fmt.Errorf("no pick face %q at site %s", bin, site)
}

func (w *world) faceShowsLevels(bin, site string, onHand, reserved, available int64) error {
	f, err := w.faceView(bin, site)
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%d/%d/%d", f.OnHand, f.Reserved, f.Available)
	return equal("on hand/reserved/available of "+bin, got, fmt.Sprintf("%d/%d/%d", onHand, reserved, available))
}

func (w *world) faceShowsBounds(bin, site string, minQty, maxQty int64) error {
	f, err := w.faceView(bin, site)
	if err != nil {
		return err
	}
	if f.MinQty == nil || f.MaxQty == nil {
		return fmt.Errorf("pick face %s shows no policy bounds, want %d to %d", bin, minQty, maxQty)
	}
	return equal("bounds of "+bin, fmt.Sprintf("%d-%d", *f.MinQty, *f.MaxQty), fmt.Sprintf("%d-%d", minQty, maxQty))
}

func (w *world) faceShowsNoBounds(bin, site string) error {
	f, err := w.faceView(bin, site)
	if err != nil {
		return err
	}
	if f.MinQty != nil || f.MaxQty != nil {
		return fmt.Errorf("pick face %s shows bounds, want none", bin)
	}
	return nil
}

func (w *world) faceShowsOpen(bin, site, requestID string) error {
	f, err := w.faceView(bin, site)
	if err != nil {
		return err
	}
	return equal("open request of "+bin, f.OpenRequestID, requestID)
}

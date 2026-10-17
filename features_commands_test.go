package main_test

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

func registerCommandSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I evaluate site "([^"]*)"$`, w.evaluate)
	sc.Step(`^I request a replenishment of "([^"]*)" at site "([^"]*)"$`, w.requestSized)
	sc.Step(`^I request a replenishment of (\d+) units of "([^"]*)" at site "([^"]*)"$`, w.requestExplicit)
	sc.Step(`^I cancel the request for "([^"]*)" at site "([^"]*)"$`, w.cancelNewest)
	sc.Step(`^I get the request "([^"]*)"$`, w.getRequest)
	sc.Step(`^inventory relocates (\d+) units of "([^"]*)" from "([^"]*)" to "([^"]*)" at site "([^"]*)" with reason "([^"]*)" referencing the request for "([^"]*)"$`, w.relocateOurs)
	sc.Step(`^inventory relocates (\d+) units of "([^"]*)" from "([^"]*)" to "([^"]*)" at site "([^"]*)" with reason "([^"]*)" referencing "([^"]*)"$`, w.relocate)
}

func (w *world) evaluate(site string) error {
	w.run(func() error {
		var err error
		w.summary, err = w.uc.evaluateSite.Handle(w.ctx, site)
		return err
	})
	return nil
}

func (w *world) requestSized(sku, site string) error {
	w.run(func() error {
		_, err := w.uc.raiseManual.Handle(w.ctx, usecases.RequestReplenishmentInput{SiteID: site, SKU: sku})
		return err
	})
	return nil
}

func (w *world) requestExplicit(quantity int64, sku, site string) error {
	w.run(func() error {
		_, err := w.uc.raiseManual.Handle(w.ctx, usecases.RequestReplenishmentInput{SiteID: site, SKU: sku, Quantity: &quantity})
		return err
	})
	return nil
}

func (w *world) cancelNewest(sku, site string) error {
	req, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	w.run(func() error {
		_, err := w.uc.cancelRequest.Handle(w.ctx, req.ID())
		return err
	})
	return nil
}

func (w *world) getRequest(id string) error {
	w.run(func() error {
		_, err := w.uc.getRequest.Handle(w.ctx, id)
		return err
	})
	return nil
}

func (w *world) relocateOurs(quantity int64, sku, from, to, site, reason, requestSKU string) error {
	req, err := w.newest(site, requestSKU)
	if err != nil {
		return err
	}
	return w.relocate(quantity, sku, from, to, site, reason, req.ID())
}

func (w *world) relocate(quantity int64, sku, from, to, site, reason, ref string) error {
	c := usecases.RelocationChange{
		RelocationID: fmt.Sprintf("reloc-%d", w.eventSeq+1), SiteCode: site, SKU: sku,
		FromBin: from, ToBin: to, Quantity: quantity, Reason: reason, SourceRef: ref,
	}
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.relocation.Handle(w.ctx, id, c) })
	return nil
}

// requestsOf returns the requests of the SKU at the site, newest first.
func (w *world) requestsOf(site, sku string) ([]*replenishment.Request, error) {
	page, err := w.uc.listRequests.Handle(w.ctx, usecases.ListRequestsQuery{SiteID: site, SKU: sku, Limit: usecases.MaxListLimit})
	return page.Items, err
}

func (w *world) newest(site, sku string) (*replenishment.Request, error) {
	reqs, err := w.requestsOf(site, sku)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("no request for %s at site %s", sku, site)
	}
	return reqs[0], nil
}

func (w *world) oldest(site, sku string) (*replenishment.Request, error) {
	reqs, err := w.requestsOf(site, sku)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("no request for %s at site %s", sku, site)
	}
	return reqs[len(reqs)-1], nil
}

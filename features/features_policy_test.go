package features_test

import (
	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func registerPolicySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I declare a policy for "([^"]*)" at site "([^"]*)" with min (-?\d+) max (-?\d+) and multiple (\d+)$`, w.declare)
	sc.Step(`^a policy for "([^"]*)" at site "([^"]*)" with min (\d+) max (\d+) and multiple (\d+)$`, w.declare)
	sc.Step(`^I change the policy of "([^"]*)" at site "([^"]*)" from version (\d+) to min (\d+) max (\d+) and multiple (\d+)$`, w.change)
	sc.Step(`^I remove the policy of "([^"]*)" at site "([^"]*)" at version (\d+)$`, w.remove)
}

func (w *world) declare(sku, site string, minQty, maxQty, multiple int64) error {
	w.run(func() error {
		_, err := w.uc.declarePolicy.Handle(w.ctx, usecases.DeclarePolicyInput{
			SiteID: site, SKU: sku, MinQty: minQty, MaxQty: maxQty, Multiple: multiple,
		})
		return err
	})
	return nil
}

func (w *world) change(sku, site string, version, minQty, maxQty, multiple int64) error {
	w.run(func() error {
		_, err := w.uc.declarePolicy.Handle(w.ctx, usecases.DeclarePolicyInput{
			SiteID: site, SKU: sku, MinQty: minQty, MaxQty: maxQty, Multiple: multiple, IfMatch: &version,
		})
		return err
	})
	return nil
}

func (w *world) remove(sku, site string, version int64) error {
	w.run(func() error {
		n, err := w.uc.removePolicy.Handle(w.ctx, site, sku, version)
		w.removed = n
		return err
	})
	return nil
}

package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// registerPackSteps is the vocabulary of ADR 0009: pack levels fed by
// product-master and the PACK_DERIVED policy that follows the CASE level.
func registerPackSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^product-master declares the ([A-Za-z]*) pack level of "([^"]*)" as gtin "([^"]*)" with (-?\d+) units per pack effective "([^"]*)" at version (-?\d+)$`, w.packDeclares)
	sc.Step(`^product-master retires the ([A-Za-z]*) pack level of "([^"]*)" with gtin "([^"]*)" effective to "([^"]*)" at version (-?\d+)$`, w.packRetires)
	sc.Step(`^I declare a pack-derived policy for "([^"]*)" at site "([^"]*)" with min (\d+) max (\d+)$`, func(sku, site string, minQty, maxQty int64) error {
		return w.declareDerived(sku, site, minQty, maxQty, 0, nil)
	})
	sc.Step(`^I declare a pack-derived policy for "([^"]*)" at site "([^"]*)" with min (\d+) max (\d+) and multiple (\d+)$`, func(sku, site string, minQty, maxQty, multiple int64) error {
		return w.declareDerived(sku, site, minQty, maxQty, multiple, nil)
	})
	sc.Step(`^I change the policy of "([^"]*)" at site "([^"]*)" from version (\d+) to pack-derived min (\d+) max (\d+)$`, func(sku, site string, version, minQty, maxQty int64) error {
		return w.declareDerived(sku, site, minQty, maxQty, 0, &version)
	})
	sc.Step(`^the policy of "([^"]*)" at site "([^"]*)" is pack-derived at version (\d+) with min (\d+) max (\d+) multiple (\d+) and is (current|stale)$`, w.derivedPolicyIs)
	sc.Step(`^the policy of "([^"]*)" at site "([^"]*)" is typed at version (\d+) with multiple (\d+)$`, w.typedPolicyIs)
}

func parseWhen(at string) (time.Time, error) {
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, fmt.Errorf("time %q: %w", at, err)
	}
	return when, nil
}

func (w *world) packDeclares(level, sku, gtin string, units int64, effective string, version int64) error {
	from, err := parseWhen(effective)
	if err != nil {
		return err
	}
	c := usecases.PackLevelDeclaredChange{
		SKU: sku, Level: level, GTIN: gtin, UnitsPerPack: units, EffectiveFrom: from, Version: version,
	}
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.packDeclared.Handle(w.ctx, id, c) })
	return nil
}

func (w *world) packRetires(level, sku, gtin, effectiveTo string, version int64) error {
	to, err := parseWhen(effectiveTo)
	if err != nil {
		return err
	}
	c := usecases.PackLevelRetiredChange{SKU: sku, Level: level, GTIN: gtin, EffectiveTo: to, Version: version}
	w.deliver(func(id string) (usecases.Outcome, error) { return w.uc.packRetired.Handle(w.ctx, id, c) })
	return nil
}

func (w *world) declareDerived(sku, site string, minQty, maxQty, multiple int64, ifMatch *int64) error {
	w.run(func() error {
		_, err := w.uc.declarePolicy.Handle(w.ctx, usecases.DeclarePolicyInput{
			SiteID: site, SKU: sku, MinQty: minQty, MaxQty: maxQty, Multiple: multiple,
			Source: policy.SourcePackDerived, IfMatch: ifMatch,
		})
		return err
	})
	return nil
}

func (w *world) derivedPolicyIs(sku, site string, version, minQty, maxQty, multiple int64, freshness string) error {
	rec, err := w.uc.getPolicy.Handle(w.ctx, site, sku)
	if err != nil {
		return err
	}
	p := rec.Policy
	got := fmt.Sprintf("%s v%d %d-%d x%d stale=%t", p.Source, p.Version, p.MinQty, p.MaxQty, p.Multiple, p.Stale)
	want := fmt.Sprintf("PACK_DERIVED v%d %d-%d x%d stale=%t", version, minQty, maxQty, multiple, freshness == "stale")
	return equal("policy of "+sku, got, want)
}

func (w *world) typedPolicyIs(sku, site string, version, multiple int64) error {
	rec, err := w.uc.getPolicy.Handle(w.ctx, site, sku)
	if err != nil {
		return err
	}
	p := rec.Policy
	got := fmt.Sprintf("%s v%d x%d stale=%t", p.Source, p.Version, p.Multiple, p.Stale)
	return equal("policy of "+sku, got, fmt.Sprintf("DECLARED v%d x%d stale=false", version, multiple))
}

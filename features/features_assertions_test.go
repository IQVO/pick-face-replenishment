package features_test

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

func registerAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the command succeeds$`, w.commandSucceeds)
	sc.Step(`^the command fails with "([^"]*)"$`, w.commandFails)
	sc.Step(`^the consumer outcome is "([^"]*)"$`, w.outcomeIs)
	sc.Step(`^the published events are "([^"]*)"$`, w.eventsAre)
	sc.Step(`^the policy of "([^"]*)" at site "([^"]*)" is at version (\d+) with min (\d+) max (\d+) and multiple (\d+)$`, w.policyIs)
	sc.Step(`^(\d+) request was cancelled by the removal$`, w.removalCancelled)
	sc.Step(`^the listed policies are "([^"]*)"$`, func(want string) error { return equal("policies", w.listedPolicies(), want) })
	sc.Step(`^the listed requests are "([^"]*)"$`, func(want string) error { return equal("requests", w.listedRequests(), want) })
	sc.Step(`^the pick faces are "([^"]*)"$`, func(want string) error { return equal("pick faces", w.listedFaces(), want) })
	sc.Step(`^there is a next page$`, w.hasNextPage)
	sc.Step(`^there is no next page$`, w.hasNoNextPage)
	registerRequestAssertions(sc, w)
}

func registerRequestAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^there are (\d+) requests for "([^"]*)" at site "([^"]*)"$`, w.requestCount)
	sc.Step(`^the request for "([^"]*)" at site "([^"]*)" is "([^"]*)"$`, w.requestIs)
	sc.Step(`^the request for "([^"]*)" at site "([^"]*)" moves (\d+) units from "([^"]*)" to "([^"]*)" with priority "([^"]*)"$`, w.requestMoves)
	sc.Step(`^the request for "([^"]*)" at site "([^"]*)" was cancelled because "([^"]*)"$`, w.newestCancelled)
	sc.Step(`^the oldest request for "([^"]*)" at site "([^"]*)" was cancelled because "([^"]*)"$`, w.oldestCancelled)
	sc.Step(`^the request for "([^"]*)" at site "([^"]*)" was completed with (\d+) units moved$`, w.requestCompleted)
	sc.Step(`^the open request for "([^"]*)" at site "([^"]*)" has forward bin "([^"]*)"$`, w.openRequestBin)
	sc.Step(`^the sweep raised (\d+), found (\d+) already open, (\d+) healthy, (\d+) without policy and (\d+) without source$`, w.sweepIs)
	sc.Step(`^the pick face "([^"]*)" of site "([^"]*)" has status "([^"]*)"$`, w.faceHasStatus)
	sc.Step(`^there are (\d+) pick faces at site "([^"]*)"$`, w.faceCount)
}

func equal(what, got, want string) error {
	if got != want {
		return fmt.Errorf("%s = %q, want %q", what, got, want)
	}
	return nil
}

func (w *world) commandSucceeds() error {
	w.errChecked = true
	if w.err != nil {
		return fmt.Errorf("command failed: %w", w.err)
	}
	return nil
}

func (w *world) commandFails(fragment string) error {
	w.errChecked = true
	if w.err == nil {
		return fmt.Errorf("command succeeded, want an error containing %q", fragment)
	}
	if !strings.Contains(w.err.Error(), fragment) {
		return fmt.Errorf("command failed with %q, want it to contain %q", w.err, fragment)
	}
	return nil
}

func (w *world) outcomeIs(want string) error {
	if err := w.commandSucceeds(); err != nil {
		return err
	}
	return equal("consumer outcome", string(w.outcome), want)
}

func (w *world) eventsAre(want string) error {
	return equal("published events", strings.Join(w.log.Names(), ","), strings.Join(splitList(want), ","))
}

func (w *world) policyIs(sku, site string, version, minQty, maxQty, multiple int64) error {
	rec, err := w.uc.getPolicy.Handle(w.ctx, site, sku)
	if err != nil {
		return err
	}
	p := rec.Policy
	if p.Version != version || p.MinQty != minQty || p.MaxQty != maxQty || p.Multiple != multiple {
		return fmt.Errorf("policy = %+v, want v%d min %d max %d multiple %d", p, version, minQty, maxQty, multiple)
	}
	return nil
}

func (w *world) removalCancelled(want int) error {
	if w.removed != want {
		return fmt.Errorf("removal cancelled %d requests, want %d", w.removed, want)
	}
	return nil
}

func (w *world) hasNextPage() error {
	if w.nextCursor == "" {
		return fmt.Errorf("no next page")
	}
	return nil
}

func (w *world) hasNoNextPage() error {
	if w.nextCursor != "" {
		return fmt.Errorf("unexpected next page %q", w.nextCursor)
	}
	return nil
}

func (w *world) requestCount(want int, sku, site string) error {
	reqs, err := w.requestsOf(site, sku)
	if err != nil {
		return err
	}
	if len(reqs) != want {
		return fmt.Errorf("%d requests for %s at %s, want %d", len(reqs), sku, site, want)
	}
	return nil
}

func (w *world) requestIs(sku, site, state string) error {
	r, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	return equal("request state", string(r.State()), state)
}

func (w *world) requestMoves(sku, site string, qty int64, from, to, priority string) error {
	r, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%d %s>%s %s", r.Quantity(), r.SourceBin(), r.ForwardBin(), r.Priority())
	return equal("request", got, fmt.Sprintf("%d %s>%s %s", qty, from, to, priority))
}

func (w *world) newestCancelled(sku, site, reason string) error {
	r, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	return cancelledBecause(r, reason)
}

func (w *world) oldestCancelled(sku, site, reason string) error {
	r, err := w.oldest(site, sku)
	if err != nil {
		return err
	}
	return cancelledBecause(r, reason)
}

func cancelledBecause(r *replenishment.Request, reason string) error {
	if r.State() != replenishment.StateCancelled {
		return fmt.Errorf("request %s is %s, want Cancelled", r.ID(), r.State())
	}
	return equal("cancel reason", string(r.Snapshot().CancelReason), reason)
}

func (w *world) requestCompleted(sku, site string, moved int64) error {
	r, err := w.newest(site, sku)
	if err != nil {
		return err
	}
	if r.State() != replenishment.StateCompleted || r.MovedQuantity() != moved {
		return fmt.Errorf("request %s is %s with %d moved, want Completed with %d", r.ID(), r.State(), r.MovedQuantity(), moved)
	}
	return nil
}

func (w *world) openRequestBin(sku, site, bin string) error {
	page, err := w.uc.listRequests.Handle(w.ctx, usecases.ListRequestsQuery{SiteID: site, SKU: sku, State: string(replenishment.StateRequested)})
	if err != nil {
		return err
	}
	if len(page.Items) != 1 {
		return fmt.Errorf("%d open requests for %s at %s, want 1", len(page.Items), sku, site)
	}
	return equal("forward bin", page.Items[0].ForwardBin(), bin)
}

func (w *world) sweepIs(raised, alreadyOpen, healthy, noPolicy, noSource int) error {
	s := w.summary
	got := fmt.Sprintf("%d/%d/%d/%d/%d", s.Raised, s.AlreadyOpen, s.Healthy, s.NoPolicy, s.NoSource)
	want := fmt.Sprintf("%d/%d/%d/%d/%d", raised, alreadyOpen, healthy, noPolicy, noSource)
	if err := equal("sweep raised/alreadyOpen/healthy/noPolicy/noSource", got, want); err != nil {
		return err
	}
	if s.Evaluated != s.Raised+s.AlreadyOpen+s.Healthy+s.NoPolicy+s.NoSource || len(s.RequestIDs) != s.Raised {
		return fmt.Errorf("inconsistent summary %+v", s)
	}
	return nil
}

func (w *world) faceHasStatus(bin, site, status string) error {
	got, err := w.faceStatus(bin, site)
	if err != nil {
		return err
	}
	return equal("status of "+bin, got, status)
}

func (w *world) faceCount(want int, site string) error {
	page, err := w.uc.listPickFaces.Handle(w.ctx, usecases.PickFaceQuery{SiteID: site, Limit: usecases.MaxListLimit})
	if err != nil {
		return err
	}
	if len(page.Items) != want {
		return fmt.Errorf("%d pick faces at %s, want %d", len(page.Items), site, want)
	}
	return nil
}

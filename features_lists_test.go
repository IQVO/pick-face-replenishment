package main_test

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func registerListSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I list policies with a page size of (\d+)$`, w.listPoliciesPage)
	sc.Step(`^I list the next page of policies$`, w.nextPolicies)
	sc.Step(`^I list the requests of site "([^"]*)"$`, func(site string) error { return w.listRequestsOf(site, "", 0) })
	sc.Step(`^I list the requests of site "([^"]*)" in state "([^"]*)"$`, func(site, state string) error { return w.listRequestsOf(site, state, 0) })
	sc.Step(`^I list the requests of site "([^"]*)" with a page size of (\d+)$`, func(site string, n int) error { return w.listRequestsOf(site, "", n) })
	sc.Step(`^I list the next page of requests$`, w.nextRequests)
	sc.Step(`^I list the pick faces of site "([^"]*)"$`, func(site string) error { return w.listFaces(site, "", 0) })
	sc.Step(`^I list the pick faces of site "([^"]*)" with status "([^"]*)"$`, func(site, status string) error { return w.listFaces(site, status, 0) })
	sc.Step(`^I list the pick faces of site "([^"]*)" with a page size of (\d+)$`, func(site string, n int) error { return w.listFaces(site, "", n) })
	sc.Step(`^I list the next page of pick faces$`, w.nextFaces)
}

func (w *world) listPoliciesPage(limit int) error {
	w.policyQuery = usecases.ListPoliciesQuery{Limit: limit}
	return w.runPolicies()
}

func (w *world) nextPolicies() error {
	w.policyQuery.Cursor = w.nextCursor
	return w.runPolicies()
}

func (w *world) runPolicies() error {
	w.run(func() error {
		var err error
		w.policy, err = w.uc.listPolicies.Handle(w.ctx, w.policyQuery)
		w.nextCursor = w.policy.NextCursor
		return err
	})
	return nil
}

func (w *world) listRequestsOf(site, state string, limit int) error {
	w.reqQuery = usecases.ListRequestsQuery{SiteID: site, State: state, Limit: limit}
	return w.runRequests()
}

func (w *world) nextRequests() error {
	w.reqQuery.Cursor = w.nextCursor
	return w.runRequests()
}

func (w *world) runRequests() error {
	w.run(func() error {
		var err error
		w.requests, err = w.uc.listRequests.Handle(w.ctx, w.reqQuery)
		w.nextCursor = w.requests.NextCursor
		return err
	})
	return nil
}

func (w *world) listFaces(site, status string, limit int) error {
	w.faceQuery = usecases.PickFaceQuery{SiteID: site, Status: status, Limit: limit}
	return w.runFaces()
}

func (w *world) nextFaces() error {
	w.faceQuery.Cursor = w.nextCursor
	return w.runFaces()
}

func (w *world) runFaces() error {
	w.run(func() error {
		var err error
		w.faces, err = w.uc.listPickFaces.Handle(w.ctx, w.faceQuery)
		w.nextCursor = w.faces.NextCursor
		return err
	})
	return nil
}

// faceStatus asks for the whole list of the site and finds the bin's status.
func (w *world) faceStatus(bin, site string) (string, error) {
	page, err := w.uc.listPickFaces.Handle(w.ctx, usecases.PickFaceQuery{SiteID: site, Limit: usecases.MaxListLimit})
	if err != nil {
		return "", err
	}
	for _, f := range page.Items {
		if f.ForwardBin == bin {
			return f.Status, nil
		}
	}
	return "", fmt.Errorf("no pick face %q at site %s", bin, site)
}

func (w *world) listedPolicies() string {
	out := make([]string, len(w.policy.Items))
	for i, rec := range w.policy.Items {
		out[i] = rec.Policy.SiteID + "/" + rec.Policy.SKU
	}
	return strings.Join(out, ",")
}

func (w *world) listedRequests() string {
	out := make([]string, len(w.requests.Items))
	for i, r := range w.requests.Items {
		out[i] = r.ID()
	}
	return strings.Join(out, ",")
}

func (w *world) listedFaces() string {
	out := make([]string, len(w.faces.Items))
	for i, f := range w.faces.Items {
		out[i] = f.ForwardBin
	}
	return strings.Join(out, ",")
}

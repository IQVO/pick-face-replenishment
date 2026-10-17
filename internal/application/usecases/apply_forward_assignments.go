package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// SlotAssignment is one `{sku, slot}` entry of SlotPlanApproved.assignments.
type SlotAssignment struct {
	SKU  string
	Slot string
}

// ForwardMapChange is a decoded SlotPlanApproved payload reduced to what this
// service acts on: assignments is the FULL forward map of the site; plan_id and
// approved_at stamp it for the stale-map guard.
type ForwardMapChange struct {
	SiteID      string
	PlanID      string
	ApprovedAt  time.Time
	Assignments []SlotAssignment
}

// ApplyForwardAssignmentMap replaces the site's forward assignments wholesale,
// cancels every open request whose bin no longer holds its SKU
// (ASSIGNMENT_CHANGED, invariant 5) and evaluates the pick faces that are new
// or moved. A map approved no later than the one already applied is stale and
// changes nothing (outcome stale).
type ApplyForwardAssignmentMap struct {
	Intake
	Eval Evaluator
}

// Handle runs the use case. A payload that can never be applied is wrapped in
// ErrInvalidEvent before anything is claimed.
func (uc *ApplyForwardAssignmentMap) Handle(ctx context.Context, eventID string, c ForwardMapChange) (Outcome, error) {
	next, err := assignmentsOf(c)
	if err != nil {
		return "", invalid(err)
	}
	return uc.apply(ctx, SlottingConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		stale, err := uc.isStale(ctx, c)
		if err != nil {
			return "", err
		}
		if stale {
			return OutcomeStale, nil
		}
		if err := uc.replace(ctx, c, next); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}

// isStale is the stale-map guard (ADR 0005): a SlotPlanApproved whose
// approved_at is not after the one recorded for the site is an older (or the
// same) map delivered late, e.g. by a replay or a rebalance; applying it would
// bring back assignments a newer plan already dropped.
func (uc *ApplyForwardAssignmentMap) isStale(ctx context.Context, c ForwardMapChange) (bool, error) {
	stamp, found, err := uc.Eval.Read.ForwardMapStamp(ctx, c.SiteID)
	if err != nil {
		return false, fmt.Errorf("read forward map stamp: %w", err)
	}
	if !found {
		return false, nil
	}
	return !c.ApprovedAt.After(stamp.ApprovedAt), nil
}

func (uc *ApplyForwardAssignmentMap) replace(ctx context.Context, c ForwardMapChange, next []repository.Assignment) error {
	siteID := c.SiteID
	read := uc.Eval.Read
	prev, err := read.Assignments(ctx, siteID)
	if err != nil {
		return fmt.Errorf("read forward assignments: %w", err)
	}
	stamp := repository.MapStamp{PlanID: c.PlanID, ApprovedAt: c.ApprovedAt.UTC()}
	if err := read.ReplaceAssignments(ctx, siteID, stamp, next); err != nil {
		return fmt.Errorf("replace forward assignments: %w", err)
	}
	if err := uc.cancelStale(ctx, siteID, next); err != nil {
		return err
	}
	for _, a := range next {
		if holds(prev, a) {
			continue
		}
		if err := uc.Eval.evaluateIfPickFace(ctx, siteID, a.Bin, a.SKU); err != nil {
			return err
		}
	}
	return nil
}

// cancelStale cancels the open requests of the site that the new map no longer
// backs.
func (uc *ApplyForwardAssignmentMap) cancelStale(ctx context.Context, siteID string, next []repository.Assignment) error {
	open, err := uc.Eval.Requests.OpenForSite(ctx, siteID)
	if err != nil {
		return fmt.Errorf("read open requests: %w", err)
	}
	c := canceller{uc.Eval.Requests, uc.Eval.Events, uc.Eval.Clock}
	for _, r := range open {
		if holds(next, repository.Assignment{SiteID: siteID, SKU: r.SKU(), Bin: r.ForwardBin()}) {
			continue
		}
		if err := c.cancel(ctx, r, replenishment.ReasonAssignmentChanged); err != nil {
			return err
		}
	}
	return nil
}

// holds reports whether the map assigns a.SKU to a.Bin.
func holds(m []repository.Assignment, a repository.Assignment) bool {
	for _, x := range m {
		if x.SKU == a.SKU && x.Bin == a.Bin {
			return true
		}
	}
	return false
}

func assignmentsOf(c ForwardMapChange) ([]repository.Assignment, error) {
	if shared.IsBlank(c.SiteID) {
		return nil, errors.New("site_id is required")
	}
	if shared.IsBlank(c.PlanID) {
		return nil, errors.New("plan_id is required")
	}
	if c.ApprovedAt.IsZero() {
		return nil, errors.New("approved_at is required")
	}
	seen := make(map[string]bool, len(c.Assignments))
	out := make([]repository.Assignment, 0, len(c.Assignments))
	for _, a := range c.Assignments {
		if shared.IsBlank(a.SKU) || shared.IsBlank(a.Slot) {
			return nil, errors.New("every assignment needs a sku and a slot")
		}
		if seen[a.SKU] {
			return nil, fmt.Errorf("sku %q is assigned twice", a.SKU)
		}
		seen[a.SKU] = true
		out = append(out, repository.Assignment{SiteID: c.SiteID, SKU: a.SKU, Bin: a.Slot})
	}
	return out, nil
}

package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// Evaluator is the shared core of detection: it reads the local copies of one
// pick face, runs the pure detector and, when the verdict is Raise, plans and
// stores the request. Every caller (the sweep, the consumers, the manual
// request, the pick-face view) goes through it so they cannot disagree.
type Evaluator struct {
	Policies ports.PolicyRepo
	Requests ports.RequestRepo
	Read     ports.PickFaceReadModel
	Events   ports.EventSink
	Clock    ports.Clock
	IDs      ports.IDGenerator
	Zones    ZoneConfig
	// Source picks the reserve bin; nil means fullest-reserve-bin-v1.
	Source detection.SourceBinPolicy
}

// Assessment is the read-only result of running the detector on one pick face.
type Assessment struct {
	SiteID     string
	ForwardBin string
	SKU        string
	Level      repository.BinLevel
	// Policy is nil when none is declared.
	Policy *policy.Policy
	// Open is the request already open for the face, or nil.
	Open *replenishment.Request
	// Reserve is the candidate source bins (empty without a policy or when a
	// request is already open).
	Reserve []detection.ReserveBin
	Outcome detection.Outcome
}

func (e Evaluator) source() detection.SourceBinPolicy {
	if e.Source == nil {
		return detection.FullestReserve{}
	}
	return e.Source
}

// Assess runs the detector on the pick face without changing anything.
func (e Evaluator) Assess(ctx context.Context, siteID, bin, sku string) (Assessment, error) {
	a := Assessment{SiteID: siteID, ForwardBin: bin, SKU: sku}
	level, err := e.Read.BinLevel(ctx, siteID, bin, sku)
	if err != nil {
		return a, fmt.Errorf("read bin level: %w", err)
	}
	a.Level = level
	if a.Policy, err = e.policyOf(ctx, siteID, sku); err != nil {
		return a, err
	}
	if a.Open, err = e.openOf(ctx, siteID, bin, sku); err != nil {
		return a, err
	}
	if a.Policy != nil && a.Open == nil {
		if a.Reserve, err = e.reserveOf(ctx, siteID, sku); err != nil {
			return a, err
		}
	}
	a.Outcome = detection.Detect(a.input(), e.source())
	return a, nil
}

func (a Assessment) input() detection.Input {
	in := detection.Input{
		Face: detection.PickFace{
			Site: a.SiteID, ForwardBin: a.ForwardBin, SKU: a.SKU,
			OnHand: a.Level.OnHand, Reserved: a.Level.Reserved,
		},
		Policy:  a.Policy,
		Reserve: a.Reserve,
	}
	if a.Open != nil {
		in.Open = &detection.OpenRequest{Quantity: a.Open.Quantity()}
	}
	return in
}

func (e Evaluator) policyOf(ctx context.Context, siteID, sku string) (*policy.Policy, error) {
	rec, err := e.Policies.Get(ctx, siteID, sku)
	if err != nil {
		if errors.Is(err, repository.ErrPolicyNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read policy: %w", err)
	}
	return &rec.Policy, nil
}

func (e Evaluator) openOf(ctx context.Context, siteID, bin, sku string) (*replenishment.Request, error) {
	open, found, err := e.Requests.OpenFor(ctx, siteID, bin, sku)
	if err != nil {
		return nil, fmt.Errorf("read open request: %w", err)
	}
	if !found {
		return nil, nil
	}
	return open, nil
}

func (e Evaluator) reserveOf(ctx context.Context, siteID, sku string) ([]detection.ReserveBin, error) {
	bins, err := e.Read.ReserveBins(ctx, siteID, sku, e.Zones.withDefaults().Reserve)
	if err != nil {
		return nil, fmt.Errorf("read reserve bins: %w", err)
	}
	out := make([]detection.ReserveBin, len(bins))
	for i, b := range bins {
		out[i] = detection.ReserveBin{Bin: b.Bin, OnHand: b.OnHand, Reserved: b.Reserved}
	}
	return out, nil
}

// Faces returns the site's pick faces ordered by forward bin: its forward
// assignments, minus those whose bin is known to be decommissioned or known to
// sit outside the forward zones. A bin the layout copy has not seen yet counts
// as a pick face (permissive until the layout arrives).
func (e Evaluator) Faces(ctx context.Context, siteID string) ([]repository.Assignment, error) {
	all, err := e.Read.Assignments(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("read forward assignments: %w", err)
	}
	faces := make([]repository.Assignment, 0, len(all))
	for _, a := range all {
		ok, err := e.isPickFace(ctx, a.Bin)
		if err != nil {
			return nil, err
		}
		if ok {
			faces = append(faces, a)
		}
	}
	return faces, nil
}

func (e Evaluator) isPickFace(ctx context.Context, bin string) (bool, error) {
	place, known, err := e.Read.Place(ctx, bin)
	if err != nil {
		return false, fmt.Errorf("read location: %w", err)
	}
	if !known {
		return true, nil
	}
	if place.Decommissioned {
		return false, nil
	}
	if place.ZoneCode == "" {
		return true, nil
	}
	return slices.Contains(e.Zones.withDefaults().Forward, place.ZoneCode), nil
}

// Evaluate assesses the pick face and raises a request when the detector says
// so. It returns the assessment and the request raised (nil when none).
func (e Evaluator) Evaluate(ctx context.Context, siteID, bin, sku string) (Assessment, *replenishment.Request, error) {
	a, err := e.Assess(ctx, siteID, bin, sku)
	if err != nil {
		return a, nil, err
	}
	if a.Outcome.Kind != detection.KindRaise {
		return a, nil, nil
	}
	req, err := e.raise(ctx, a)
	return a, req, err
}

// raise plans the request the detector asked for in a (Kind must be Raise).
func (e Evaluator) raise(ctx context.Context, a Assessment) (*replenishment.Request, error) {
	p := a.Outcome.Params
	return e.plan(ctx, a, planSpec{
		source:   replenishment.Source{Bin: p.SourceBin, SiteID: a.SiteID, ReserveZone: true, Usable: p.SourceUsable},
		quantity: p.Quantity,
		priority: p.Priority,
	})
}

// EvaluateSKU evaluates the pick face the site's forward map gives the SKU, if
// it has one and it is a pick face.
func (e Evaluator) EvaluateSKU(ctx context.Context, siteID, sku string) error {
	bin, ok, err := e.Read.ForwardBin(ctx, siteID, sku)
	if err != nil {
		return fmt.Errorf("read forward bin: %w", err)
	}
	if !ok {
		return nil
	}
	return e.evaluateIfPickFace(ctx, siteID, bin, sku)
}

func (e Evaluator) evaluateIfPickFace(ctx context.Context, siteID, bin, sku string) error {
	ok, err := e.isPickFace(ctx, bin)
	if err != nil || !ok {
		return err
	}
	_, _, err = e.Evaluate(ctx, siteID, bin, sku)
	return err
}

// planSpec is what plan needs beyond the assessment.
type planSpec struct {
	source   replenishment.Source
	quantity int64
	priority replenishment.Priority
}

// plan creates the request, stores it and hands its facts to the outbox.
func (e Evaluator) plan(ctx context.Context, a Assessment, s planSpec) (*replenishment.Request, error) {
	req, err := replenishment.Plan(replenishment.PlanInput{
		RequestID:  e.IDs.NewRequestID(),
		SiteID:     a.SiteID,
		SKU:        a.SKU,
		ForwardBin: a.ForwardBin,
		Quantity:   s.quantity,
		Priority:   s.priority,
		PolicyMin:  a.Policy.MinQty,
		PolicyMax:  a.Policy.MaxQty,
		Projected:  a.Outcome.Projected,
		Source:     s.source,
		At:         e.Clock.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	if err := e.Requests.Save(ctx, req, 0); err != nil {
		return nil, err
	}
	if err := e.Events.PublishRequestEvents(ctx, req.DomainEvents()...); err != nil {
		return nil, fmt.Errorf("enqueue events: %w", err)
	}
	return req, nil
}

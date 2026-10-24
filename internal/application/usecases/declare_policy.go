package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// DeclarePolicyInput is the input of DeclarePolicy. Multiple 0 means the
// default of 1. IfMatch nil creates the policy; a value replaces the policy
// only when it is the current version.
//
// Source "" or DECLARED keeps today's behaviour. PACK_DERIVED takes the
// Multiple from the SKU's active CASE level in the local pack-level view, so
// Multiple must then be 0 (ADR 0009).
type DeclarePolicyInput struct {
	SiteID   string
	SKU      string
	MinQty   int64
	MaxQty   int64
	Multiple int64
	Source   policy.Source
	IfMatch  *int64
}

// DeclarePolicy declares or changes the MIN_MAX policy of a site and SKU and
// enqueues the analytics-only ReplenishmentPolicyDeclared fact.
type DeclarePolicy struct {
	Policies ports.PolicyRepo
	Events   ports.EventSink
	UoW      ports.UnitOfWork
	Clock    ports.Clock
	// PackLevels is the local pack-level view a PACK_DERIVED declaration reads.
	PackLevels ports.PackLevelView
}

// Handle runs the use case. Without IfMatch an existing policy is
// repository.ErrPolicyAlreadyExists; with IfMatch a missing policy or a
// different version is repository.ErrConcurrentModification. Invalid bounds are
// the policy package's errors. A PACK_DERIVED declaration for a SKU with no
// active CASE level is ErrNoActiveCaseLevel.
func (uc *DeclarePolicy) Handle(ctx context.Context, in DeclarePolicyInput) (repository.PolicyRecord, error) {
	var rec repository.PolicyRecord
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		var err error
		rec, err = uc.declare(ctx, in)
		return err
	})
	if err != nil {
		return repository.PolicyRecord{}, err
	}
	return rec, nil
}

func (uc *DeclarePolicy) declare(ctx context.Context, in DeclarePolicyInput) (repository.PolicyRecord, error) {
	next, loaded, err := uc.next(ctx, in)
	if err != nil {
		return repository.PolicyRecord{}, err
	}
	now := uc.Clock.Now().UTC()
	rec := repository.PolicyRecord{Policy: next, DeclaredAt: now}
	if err := uc.Policies.Save(ctx, rec, loaded); err != nil {
		return repository.PolicyRecord{}, err
	}
	if err := uc.Events.PublishPolicyDeclared(ctx, next.Declaration(now)); err != nil {
		return repository.PolicyRecord{}, fmt.Errorf("enqueue events: %w", err)
	}
	return rec, nil
}

// next builds the policy to store and the version the save is guarded by.
func (uc *DeclarePolicy) next(ctx context.Context, in DeclarePolicyInput) (policy.Policy, int64, error) {
	derived, multiple, err := uc.multiple(ctx, in)
	if err != nil {
		return policy.Policy{}, 0, err
	}
	if in.IfMatch == nil {
		if derived {
			p, err := policy.NewPackDerived(in.SiteID, in.SKU, in.MinQty, in.MaxQty, multiple)
			return p, 0, err
		}
		p, err := policy.New(in.SiteID, in.SKU, in.MinQty, in.MaxQty, multiple)
		return p, 0, err
	}
	cur, err := uc.Policies.Get(ctx, in.SiteID, in.SKU)
	if errors.Is(err, repository.ErrPolicyNotFound) {
		return policy.Policy{}, 0, repository.ErrConcurrentModification
	}
	if err != nil {
		return policy.Policy{}, 0, err
	}
	if cur.Policy.Version != *in.IfMatch {
		return policy.Policy{}, 0, repository.ErrConcurrentModification
	}
	if derived {
		p, err := cur.Policy.WithPackDerivedBounds(in.MinQty, in.MaxQty, multiple)
		return p, cur.Policy.Version, err
	}
	p, err := cur.Policy.WithBounds(in.MinQty, in.MaxQty, multiple)
	return p, cur.Policy.Version, err
}

// multiple resolves the multiple to store: the typed one (0 = 1) for DECLARED,
// units_per_pack of the active CASE level for PACK_DERIVED.
func (uc *DeclarePolicy) multiple(ctx context.Context, in DeclarePolicyInput) (derived bool, multiple int64, err error) {
	switch in.Source {
	case "", policy.SourceDeclared:
		if in.Multiple == 0 {
			return false, 1, nil
		}
		return false, in.Multiple, nil
	case policy.SourcePackDerived:
	default:
		return false, 0, policy.ErrInvalidSource
	}
	if in.Multiple != 0 {
		return false, 0, policy.ErrMultipleNotAllowed
	}
	if uc.PackLevels == nil {
		return false, 0, errMissingPackLevels
	}
	active, found, err := ActiveCase(ctx, uc.PackLevels, in.SKU, uc.Clock.Now().UTC())
	if err != nil {
		return false, 0, err
	}
	if !found {
		return false, 0, ErrNoActiveCaseLevel
	}
	return true, active.UnitsPerPack, nil
}

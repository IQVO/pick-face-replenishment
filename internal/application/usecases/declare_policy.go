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
type DeclarePolicyInput struct {
	SiteID   string
	SKU      string
	MinQty   int64
	MaxQty   int64
	Multiple int64
	IfMatch  *int64
}

// DeclarePolicy declares or changes the MIN_MAX policy of a site and SKU and
// enqueues the analytics-only ReplenishmentPolicyDeclared fact.
type DeclarePolicy struct {
	Policies ports.PolicyRepo
	Events   ports.EventSink
	UoW      ports.UnitOfWork
	Clock    ports.Clock
}

// Handle runs the use case. Without IfMatch an existing policy is
// repository.ErrPolicyAlreadyExists; with IfMatch a missing policy or a
// different version is repository.ErrConcurrentModification. Invalid bounds are
// the policy package's errors.
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
	multiple := in.Multiple
	if multiple == 0 {
		multiple = 1
	}
	if in.IfMatch == nil {
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
	p, err := cur.Policy.WithBounds(in.MinQty, in.MaxQty, multiple)
	return p, cur.Policy.Version, err
}

package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// GetPolicy returns the declared policy of a site and SKU.
type GetPolicy struct {
	Policies ports.PolicyRepo
}

// Handle runs the use case; an unknown policy is repository.ErrPolicyNotFound.
func (uc *GetPolicy) Handle(ctx context.Context, siteID, sku string) (repository.PolicyRecord, error) {
	if err := requireSiteSKU(siteID, sku); err != nil {
		return repository.PolicyRecord{}, err
	}
	return uc.Policies.Get(ctx, siteID, sku)
}

// ListPoliciesQuery is the input of ListPolicies. Limit 0 means the default;
// Cursor is the opaque nextCursor of the previous page; SiteID and SKU ""
// mean "no filter".
type ListPoliciesQuery struct {
	Limit  int
	Cursor string
	SiteID string
	SKU    string
}

// PolicyPage is one page of policies ordered by site and SKU. NextCursor is
// empty on the last page.
type PolicyPage struct {
	Items      []repository.PolicyRecord
	NextCursor string
}

// ListPolicies lists the declared policies a page at a time.
type ListPolicies struct {
	Policies ports.PolicyRepo
}

// Handle runs the use case.
func (uc *ListPolicies) Handle(ctx context.Context, q ListPoliciesQuery) (PolicyPage, error) {
	limit, err := listLimit(q.Limit)
	if err != nil {
		return PolicyPage{}, err
	}
	after, err := decodePolicyCursor(q.Cursor)
	if err != nil {
		return PolicyPage{}, err
	}
	items, err := uc.Policies.List(ctx, repository.PolicyFilter{SiteID: q.SiteID, SKU: q.SKU}, after, limit+1)
	if err != nil {
		return PolicyPage{}, fmt.Errorf("list policies: %w", err)
	}
	page := PolicyPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1].Policy
		page.NextCursor = encodePolicyCursor(repository.PolicyKey{SiteID: last.SiteID, SKU: last.SKU})
	}
	return page, nil
}

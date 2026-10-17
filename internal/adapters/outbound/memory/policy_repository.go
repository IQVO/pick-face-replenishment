package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// PolicyRepo is the in-memory ports.PolicyRepo.
type PolicyRepo struct {
	mu   sync.Mutex
	rows map[repository.PolicyKey]repository.PolicyRecord
}

// NewPolicyRepo constructs an empty PolicyRepo.
func NewPolicyRepo() *PolicyRepo {
	return &PolicyRepo{rows: map[repository.PolicyKey]repository.PolicyRecord{}}
}

// Get implements ports.PolicyRepo.
func (r *PolicyRepo) Get(_ context.Context, siteID, sku string) (repository.PolicyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.rows[repository.PolicyKey{SiteID: siteID, SKU: sku}]
	if !ok {
		return repository.PolicyRecord{}, repository.ErrPolicyNotFound
	}
	return rec, nil
}

// Save implements ports.PolicyRepo.
func (r *PolicyRepo) Save(_ context.Context, rec repository.PolicyRecord, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := repository.PolicyKey{SiteID: rec.Policy.SiteID, SKU: rec.Policy.SKU}
	cur, exists := r.rows[key]
	if loadedVersion == 0 && exists {
		return repository.ErrPolicyAlreadyExists
	}
	if loadedVersion != 0 && (!exists || cur.Policy.Version != loadedVersion) {
		return repository.ErrConcurrentModification
	}
	r.rows[key] = rec
	return nil
}

// Delete implements ports.PolicyRepo.
func (r *PolicyRepo) Delete(_ context.Context, siteID, sku string, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := repository.PolicyKey{SiteID: siteID, SKU: sku}
	cur, exists := r.rows[key]
	if !exists {
		return repository.ErrPolicyNotFound
	}
	if cur.Policy.Version != loadedVersion {
		return repository.ErrConcurrentModification
	}
	delete(r.rows, key)
	return nil
}

// List implements ports.PolicyRepo.
func (r *PolicyRepo) List(_ context.Context, filter repository.PolicyFilter, after *repository.PolicyKey, limit int) ([]repository.PolicyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []repository.PolicyRecord
	for key, rec := range r.rows {
		if filter.SiteID != "" && key.SiteID != filter.SiteID {
			continue
		}
		if filter.SKU != "" && key.SKU != filter.SKU {
			continue
		}
		if after != nil && comparePolicyKey(key, *after) <= 0 {
			continue
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b repository.PolicyRecord) int {
		return comparePolicyKey(
			repository.PolicyKey{SiteID: a.Policy.SiteID, SKU: a.Policy.SKU},
			repository.PolicyKey{SiteID: b.Policy.SiteID, SKU: b.Policy.SKU})
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func comparePolicyKey(a, b repository.PolicyKey) int {
	if c := strings.Compare(a.SiteID, b.SiteID); c != 0 {
		return c
	}
	return strings.Compare(a.SKU, b.SKU)
}

// Snapshot implements Snapshotter.
func (r *PolicyRepo) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := maps.Clone(r.rows)
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.rows = saved
	}
}

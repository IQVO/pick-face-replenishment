package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// RequestRepo is the in-memory ports.RequestRepo. It stores snapshots, so a
// caller never shares a live aggregate with the store.
type RequestRepo struct {
	mu   sync.Mutex
	rows map[string]replenishment.Snapshot
}

// NewRequestRepo constructs an empty RequestRepo.
func NewRequestRepo() *RequestRepo {
	return &RequestRepo{rows: map[string]replenishment.Snapshot{}}
}

// Get implements ports.RequestRepo.
func (r *RequestRepo) Get(_ context.Context, id string) (*replenishment.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, repository.ErrRequestNotFound
	}
	return restore(s)
}

// Save implements ports.RequestRepo.
func (r *RequestRepo) Save(_ context.Context, req *replenishment.Request, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := req.Snapshot()
	cur, exists := r.rows[snap.ID]
	if loadedVersion == 0 && exists {
		return repository.ErrConcurrentModification
	}
	if loadedVersion != 0 && (!exists || cur.Version != loadedVersion) {
		return repository.ErrConcurrentModification
	}
	if snap.State == replenishment.StateRequested && r.openHolder(snap) {
		return repository.ErrOpenRequestExists
	}
	r.rows[snap.ID] = snap
	return nil
}

// openHolder reports whether ANOTHER Requested request holds the same
// (site, forward bin, SKU): the partial unique index.
func (r *RequestRepo) openHolder(s replenishment.Snapshot) bool {
	for id, o := range r.rows {
		if id == s.ID || o.State != replenishment.StateRequested {
			continue
		}
		if o.SiteID == s.SiteID && o.ForwardBin == s.ForwardBin && o.SKU == s.SKU {
			return true
		}
	}
	return false
}

// OpenFor implements ports.RequestRepo.
func (r *RequestRepo) OpenFor(_ context.Context, siteID, forwardBin, sku string) (*replenishment.Request, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.rows {
		if s.State == replenishment.StateRequested && s.SiteID == siteID && s.ForwardBin == forwardBin && s.SKU == sku {
			req, err := restore(s)
			return req, err == nil, err
		}
	}
	return nil, false, nil
}

// OpenForSKU implements ports.RequestRepo.
func (r *RequestRepo) OpenForSKU(_ context.Context, siteID, sku string) ([]*replenishment.Request, error) {
	return r.open(func(s replenishment.Snapshot) bool { return s.SiteID == siteID && s.SKU == sku })
}

// OpenForSite implements ports.RequestRepo.
func (r *RequestRepo) OpenForSite(_ context.Context, siteID string) ([]*replenishment.Request, error) {
	return r.open(func(s replenishment.Snapshot) bool { return s.SiteID == siteID })
}

func (r *RequestRepo) open(match func(replenishment.Snapshot) bool) ([]*replenishment.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var snaps []replenishment.Snapshot
	for _, s := range r.rows {
		if s.State == replenishment.StateRequested && match(s) {
			snaps = append(snaps, s)
		}
	}
	slices.SortFunc(snaps, func(a, b replenishment.Snapshot) int { return strings.Compare(a.ID, b.ID) })
	return restoreAll(snaps)
}

// List implements ports.RequestRepo.
func (r *RequestRepo) List(_ context.Context, f repository.RequestFilter, after string, limit int) ([]*replenishment.Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var snaps []replenishment.Snapshot
	for _, s := range r.rows {
		if matches(s, f) {
			snaps = append(snaps, s)
		}
	}
	// newest first: requested-at desc, id desc
	slices.SortFunc(snaps, func(a, b replenishment.Snapshot) int {
		if c := b.RequestedAt.Compare(a.RequestedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	if after != "" {
		i := slices.IndexFunc(snaps, func(s replenishment.Snapshot) bool { return s.ID == after })
		if i < 0 {
			snaps = nil
		} else {
			snaps = snaps[i+1:]
		}
	}
	if len(snaps) > limit {
		snaps = snaps[:limit]
	}
	return restoreAll(snaps)
}

func matches(s replenishment.Snapshot, f repository.RequestFilter) bool {
	if f.SiteID != "" && s.SiteID != f.SiteID {
		return false
	}
	if f.SKU != "" && s.SKU != f.SKU {
		return false
	}
	return f.State == "" || s.State == f.State
}

func restore(s replenishment.Snapshot) (*replenishment.Request, error) {
	return replenishment.Restore(s)
}

func restoreAll(snaps []replenishment.Snapshot) ([]*replenishment.Request, error) {
	out := make([]*replenishment.Request, 0, len(snaps))
	for _, s := range snaps {
		req, err := restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

// Snapshot implements Snapshotter.
func (r *RequestRepo) Snapshot() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := maps.Clone(r.rows)
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.rows = saved
	}
}

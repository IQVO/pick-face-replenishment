package memory

import (
	"context"
	"maps"
	"sync"

	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
)

type packLevelKey struct {
	sku   string
	level packlevel.Level
}

// PackLevelView is the in-memory ports.PackLevelView: one row per (SKU,
// level).
type PackLevelView struct {
	mu   sync.Mutex
	rows map[packLevelKey]packlevel.PackLevel
}

// NewPackLevelView constructs an empty PackLevelView.
func NewPackLevelView() *PackLevelView {
	return &PackLevelView{rows: map[packLevelKey]packlevel.PackLevel{}}
}

// Get implements ports.PackLevelView.
func (v *PackLevelView) Get(_ context.Context, sku string, level packlevel.Level) (packlevel.PackLevel, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	l, ok := v.rows[packLevelKey{sku, level}]
	return l, ok, nil
}

// Save implements ports.PackLevelView.
func (v *PackLevelView) Save(_ context.Context, l packlevel.PackLevel) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.rows[packLevelKey{l.SKU, l.Level}] = l
	return nil
}

// Snapshot implements Snapshotter.
func (v *PackLevelView) Snapshot() func() {
	v.mu.Lock()
	defer v.mu.Unlock()
	saved := maps.Clone(v.rows)
	return func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		v.rows = saved
	}
}

package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

type levelKey struct{ site, bin, sku string }

type assignmentKey struct{ site, sku string }

type locationRow struct {
	loc            repository.Location
	decommissioned bool
}

// ReadModel is the in-memory ports.PickFaceReadModel: the bin stock levels,
// the forward assignments and the layout (zones and locations) of ADR 0003.
type ReadModel struct {
	mu          sync.Mutex
	levels      map[levelKey]repository.BinLevel
	assignments map[assignmentKey]string
	stamps      map[string]repository.MapStamp
	zones       map[string]repository.Zone
	locations   map[string]locationRow
}

// NewReadModel constructs an empty ReadModel.
func NewReadModel() *ReadModel {
	return &ReadModel{
		levels:      map[levelKey]repository.BinLevel{},
		assignments: map[assignmentKey]string{},
		stamps:      map[string]repository.MapStamp{},
		zones:       map[string]repository.Zone{},
		locations:   map[string]locationRow{},
	}
}

// BinLevel implements ports.PickFaceReadModel.
func (m *ReadModel) BinLevel(_ context.Context, siteCode, bin, sku string) (repository.BinLevel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.levels[levelKey{siteCode, bin, sku}]; ok {
		return l, nil
	}
	return repository.BinLevel{SiteCode: siteCode, Bin: bin, SKU: sku}, nil
}

// ReserveBins implements ports.PickFaceReadModel.
func (m *ReadModel) ReserveBins(_ context.Context, siteCode, sku string, reserveZoneCodes []string) ([]repository.BinLevel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.BinLevel
	for key, level := range m.levels {
		if key.site != siteCode || key.sku != sku {
			continue
		}
		place, ok := m.placeLocked(key.bin)
		if ok && isReserve(place, siteCode, reserveZoneCodes) {
			out = append(out, level)
		}
	}
	slices.SortFunc(out, func(a, b repository.BinLevel) int { return strings.Compare(a.Bin, b.Bin) })
	return out, nil
}

func isReserve(p repository.Place, siteCode string, reserveZoneCodes []string) bool {
	if p.Decommissioned || !p.IsStorage() || p.SiteCode != siteCode {
		return false
	}
	return slices.Contains(reserveZoneCodes, p.ZoneCode)
}

// Assignments implements ports.PickFaceReadModel.
func (m *ReadModel) Assignments(_ context.Context, siteID string) ([]repository.Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.Assignment
	for key, bin := range m.assignments {
		if key.site == siteID {
			out = append(out, repository.Assignment{SiteID: siteID, SKU: key.sku, Bin: bin})
		}
	}
	slices.SortFunc(out, func(a, b repository.Assignment) int {
		if c := strings.Compare(a.Bin, b.Bin); c != 0 {
			return c
		}
		return strings.Compare(a.SKU, b.SKU)
	})
	return out, nil
}

// ForwardBin implements ports.PickFaceReadModel.
func (m *ReadModel) ForwardBin(_ context.Context, siteID, sku string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bin, ok := m.assignments[assignmentKey{siteID, sku}]
	return bin, ok, nil
}

// Place implements ports.PickFaceReadModel.
func (m *ReadModel) Place(_ context.Context, code string) (repository.Place, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.placeLocked(code)
	return p, ok, nil
}

func (m *ReadModel) placeLocked(code string) (repository.Place, bool) {
	row, ok := m.locations[code]
	if !ok {
		return repository.Place{}, false
	}
	p := repository.Place{Code: code, Role: row.loc.Role, Decommissioned: row.decommissioned}
	if z, known := m.zones[row.loc.ZoneID]; known {
		p.SiteCode, p.ZoneCode = z.SiteCode, z.ZoneCode
	}
	return p, true
}

// ApplyBinLevel implements ports.PickFaceReadModel.
func (m *ReadModel) ApplyBinLevel(_ context.Context, level repository.BinLevel) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := levelKey{level.SiteCode, level.Bin, level.SKU}
	if cur, ok := m.levels[key]; ok && cur.Version >= level.Version {
		return false, nil
	}
	m.levels[key] = level
	return true, nil
}

// ForwardMapStamp implements ports.PickFaceReadModel.
func (m *ReadModel) ForwardMapStamp(_ context.Context, siteID string) (repository.MapStamp, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stamps[siteID]
	return s, ok, nil
}

// ReplaceAssignments implements ports.PickFaceReadModel.
func (m *ReadModel) ReplaceAssignments(_ context.Context, siteID string, stamp repository.MapStamp, assignments []repository.Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stamps[siteID] = stamp
	for key := range m.assignments {
		if key.site == siteID {
			delete(m.assignments, key)
		}
	}
	for _, a := range assignments {
		m.assignments[assignmentKey{siteID, a.SKU}] = a.Bin
	}
	return nil
}

// SaveZone implements ports.PickFaceReadModel.
func (m *ReadModel) SaveZone(_ context.Context, z repository.Zone) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.zones[z.ID] = z
	return nil
}

// SaveLocation implements ports.PickFaceReadModel.
func (m *ReadModel) SaveLocation(_ context.Context, l repository.Location) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.locations[l.Code]
	row.loc = l
	m.locations[l.Code] = row
	return nil
}

// DecommissionLocation implements ports.PickFaceReadModel.
func (m *ReadModel) DecommissionLocation(_ context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.locations[code]
	row.loc.Code = code
	row.decommissioned = true
	m.locations[code] = row
	return nil
}

// Snapshot implements Snapshotter.
func (m *ReadModel) Snapshot() func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	levels, assignments, stamps := maps.Clone(m.levels), maps.Clone(m.assignments), maps.Clone(m.stamps)
	zones, locations := maps.Clone(m.zones), maps.Clone(m.locations)
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.levels, m.assignments, m.stamps, m.zones, m.locations = levels, assignments, stamps, zones, locations
	}
}

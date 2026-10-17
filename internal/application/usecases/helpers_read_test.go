package usecases_test

import (
	"context"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// failRead fails the methods named in faults.
type failRead struct {
	ports.PickFaceReadModel
	f faults
}

func (m *failRead) BinLevel(ctx context.Context, site, bin, sku string) (repository.BinLevel, error) {
	if err := m.f.hit("ReadModel.BinLevel"); err != nil {
		return repository.BinLevel{}, err
	}
	return m.PickFaceReadModel.BinLevel(ctx, site, bin, sku)
}

func (m *failRead) ReserveBins(ctx context.Context, site, sku string, zones []string) ([]repository.BinLevel, error) {
	if err := m.f.hit("ReadModel.ReserveBins"); err != nil {
		return nil, err
	}
	return m.PickFaceReadModel.ReserveBins(ctx, site, sku, zones)
}

func (m *failRead) Assignments(ctx context.Context, site string) ([]repository.Assignment, error) {
	if err := m.f.hit("ReadModel.Assignments"); err != nil {
		return nil, err
	}
	return m.PickFaceReadModel.Assignments(ctx, site)
}

func (m *failRead) ForwardBin(ctx context.Context, site, sku string) (string, bool, error) {
	if err := m.f.hit("ReadModel.ForwardBin"); err != nil {
		return "", false, err
	}
	return m.PickFaceReadModel.ForwardBin(ctx, site, sku)
}

func (m *failRead) Place(ctx context.Context, code string) (repository.Place, bool, error) {
	if err := m.f.hit("ReadModel.Place"); err != nil {
		return repository.Place{}, false, err
	}
	return m.PickFaceReadModel.Place(ctx, code)
}

func (m *failRead) ApplyBinLevel(ctx context.Context, l repository.BinLevel) (bool, error) {
	if err := m.f.hit("ReadModel.ApplyBinLevel"); err != nil {
		return false, err
	}
	return m.PickFaceReadModel.ApplyBinLevel(ctx, l)
}

func (m *failRead) ReplaceAssignments(ctx context.Context, site string, as []repository.Assignment) error {
	if err := m.f.hit("ReadModel.ReplaceAssignments"); err != nil {
		return err
	}
	return m.PickFaceReadModel.ReplaceAssignments(ctx, site, as)
}

func (m *failRead) SaveZone(ctx context.Context, z repository.Zone) error {
	if err := m.f.hit("ReadModel.SaveZone"); err != nil {
		return err
	}
	return m.PickFaceReadModel.SaveZone(ctx, z)
}

func (m *failRead) SaveLocation(ctx context.Context, l repository.Location) error {
	if err := m.f.hit("ReadModel.SaveLocation"); err != nil {
		return err
	}
	return m.PickFaceReadModel.SaveLocation(ctx, l)
}

func (m *failRead) DecommissionLocation(ctx context.Context, code string) error {
	if err := m.f.hit("ReadModel.DecommissionLocation"); err != nil {
		return err
	}
	return m.PickFaceReadModel.DecommissionLocation(ctx, code)
}

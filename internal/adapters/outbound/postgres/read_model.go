package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// ReadModel is the pgxpool-backed ports.PickFaceReadModel: the bin stock
// levels, the forward assignments (with the stamp of the map that produced
// them) and the layout of ADR 0003.
type ReadModel struct {
	pool *pgxpool.Pool
}

// NewReadModel constructs a ReadModel over pool.
func NewReadModel(pool *pgxpool.Pool) *ReadModel { return &ReadModel{pool: pool} }

// BinLevel implements ports.PickFaceReadModel. A bin never seen reads as an
// all-zero level (ADR 0005).
func (m *ReadModel) BinLevel(ctx context.Context, siteCode, bin, sku string) (repository.BinLevel, error) {
	level := repository.BinLevel{SiteCode: siteCode, Bin: bin, SKU: sku}
	err := queryFor(ctx, m.pool).QueryRow(ctx, `
		SELECT on_hand, reserved, version FROM bin_stock_levels
		WHERE site_code = $1 AND bin = $2 AND sku = $3
	`, siteCode, bin, sku).Scan(&level.OnHand, &level.Reserved, &level.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return level, nil
	}
	if err != nil {
		return repository.BinLevel{}, fmt.Errorf("read bin level: %w", err)
	}
	return level, nil
}

// ReserveBins implements ports.PickFaceReadModel: the levels of the SKU in the
// site's known, not decommissioned Storage locations whose zone code is one of
// reserveZoneCodes, ordered by bin.
func (m *ReadModel) ReserveBins(ctx context.Context, siteCode, sku string, reserveZoneCodes []string) ([]repository.BinLevel, error) {
	rows, err := queryFor(ctx, m.pool).Query(ctx, `
		SELECT l.site_code, l.bin, l.sku, l.on_hand, l.reserved, l.version
		FROM bin_stock_levels l
		JOIN locations loc ON loc.location_code = l.bin
		JOIN zones z ON z.zone_id = loc.zone_id
		WHERE l.site_code = $1 AND l.sku = $2
		  AND z.site_code = $1 AND z.zone_code = ANY($3::text[])
		  AND NOT loc.decommissioned
		  AND loc.role IN ('', $4)
		ORDER BY l.bin
	`, siteCode, sku, reserveZoneCodes, repository.RoleStorage)
	if err != nil {
		return nil, fmt.Errorf("read reserve bins: %w", err)
	}
	defer rows.Close()
	var out []repository.BinLevel
	for rows.Next() {
		var l repository.BinLevel
		if err := rows.Scan(&l.SiteCode, &l.Bin, &l.SKU, &l.OnHand, &l.Reserved, &l.Version); err != nil {
			return nil, fmt.Errorf("scan reserve bin: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Assignments implements ports.PickFaceReadModel.
func (m *ReadModel) Assignments(ctx context.Context, siteID string) ([]repository.Assignment, error) {
	rows, err := queryFor(ctx, m.pool).Query(ctx, `
		SELECT sku, bin FROM forward_assignments WHERE site_id = $1 ORDER BY bin, sku
	`, siteID)
	if err != nil {
		return nil, fmt.Errorf("read forward assignments: %w", err)
	}
	defer rows.Close()
	var out []repository.Assignment
	for rows.Next() {
		a := repository.Assignment{SiteID: siteID}
		if err := rows.Scan(&a.SKU, &a.Bin); err != nil {
			return nil, fmt.Errorf("scan forward assignment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ForwardBin implements ports.PickFaceReadModel.
func (m *ReadModel) ForwardBin(ctx context.Context, siteID, sku string) (string, bool, error) {
	var bin string
	err := queryFor(ctx, m.pool).QueryRow(ctx,
		`SELECT bin FROM forward_assignments WHERE site_id = $1 AND sku = $2`, siteID, sku).Scan(&bin)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read forward bin: %w", err)
	}
	return bin, true, nil
}

// Place implements ports.PickFaceReadModel. SiteCode and ZoneCode are empty
// while the location's zone has not been seen.
func (m *ReadModel) Place(ctx context.Context, code string) (repository.Place, bool, error) {
	p := repository.Place{Code: code}
	var siteCode, zoneCode *string
	err := queryFor(ctx, m.pool).QueryRow(ctx, `
		SELECT loc.role, loc.decommissioned, z.site_code, z.zone_code
		FROM locations loc
		LEFT JOIN zones z ON z.zone_id = loc.zone_id
		WHERE loc.location_code = $1
	`, code).Scan(&p.Role, &p.Decommissioned, &siteCode, &zoneCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.Place{}, false, nil
	}
	if err != nil {
		return repository.Place{}, false, fmt.Errorf("read location: %w", err)
	}
	if siteCode != nil && zoneCode != nil {
		p.SiteCode, p.ZoneCode = *siteCode, *zoneCode
	}
	return p, true, nil
}

// ApplyBinLevel implements ports.PickFaceReadModel: the upsert happens only
// when the message version is greater than the stored one.
func (m *ReadModel) ApplyBinLevel(ctx context.Context, l repository.BinLevel) (bool, error) {
	tag, err := queryFor(ctx, m.pool).Exec(ctx, `
		INSERT INTO bin_stock_levels (site_code, bin, sku, on_hand, reserved, version)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (site_code, bin, sku) DO UPDATE
		SET on_hand = EXCLUDED.on_hand, reserved = EXCLUDED.reserved,
		    version = EXCLUDED.version, updated_at = now()
		WHERE bin_stock_levels.version < EXCLUDED.version
	`, l.SiteCode, l.Bin, l.SKU, l.OnHand, l.Reserved, l.Version)
	if err != nil {
		return false, fmt.Errorf("apply bin level: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ForwardMapStamp implements ports.PickFaceReadModel.
func (m *ReadModel) ForwardMapStamp(ctx context.Context, siteID string) (repository.MapStamp, bool, error) {
	var s repository.MapStamp
	err := queryFor(ctx, m.pool).QueryRow(ctx,
		`SELECT plan_id, approved_at FROM forward_assignment_maps WHERE site_id = $1`, siteID).Scan(&s.PlanID, &s.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.MapStamp{}, false, nil
	}
	if err != nil {
		return repository.MapStamp{}, false, fmt.Errorf("read forward map stamp: %w", err)
	}
	s.ApprovedAt = s.ApprovedAt.UTC()
	return s, true, nil
}

// ReplaceAssignments implements ports.PickFaceReadModel: the site's
// assignments and the stamp of the plan that produced them are replaced in one
// transaction (the caller's unit of work when there is one).
func (m *ReadModel) ReplaceAssignments(ctx context.Context, siteID string, stamp repository.MapStamp, assignments []repository.Assignment) error {
	skus, bins := make([]string, len(assignments)), make([]string, len(assignments))
	for i, a := range assignments {
		skus[i], bins[i] = a.SKU, a.Bin
	}
	return inTx(ctx, m.pool, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO forward_assignment_maps (site_id, plan_id, approved_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (site_id) DO UPDATE
			SET plan_id = EXCLUDED.plan_id, approved_at = EXCLUDED.approved_at, applied_at = now()
		`, siteID, stamp.PlanID, stamp.ApprovedAt.UTC()); err != nil {
			return fmt.Errorf("stamp forward map: %w", err)
		}
		if _, err := q.Exec(ctx, `DELETE FROM forward_assignments WHERE site_id = $1`, siteID); err != nil {
			return fmt.Errorf("clear forward assignments: %w", err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO forward_assignments (site_id, sku, bin)
			SELECT $1, sku, bin FROM unnest($2::text[], $3::text[]) AS t(sku, bin)
		`, siteID, skus, bins); err != nil {
			return fmt.Errorf("insert forward assignments: %w", err)
		}
		return nil
	})
}

// SaveZone implements ports.PickFaceReadModel.
func (m *ReadModel) SaveZone(ctx context.Context, z repository.Zone) error {
	_, err := queryFor(ctx, m.pool).Exec(ctx, `
		INSERT INTO zones (zone_id, site_code, zone_code) VALUES ($1, $2, $3)
		ON CONFLICT (zone_id) DO UPDATE
		SET site_code = EXCLUDED.site_code, zone_code = EXCLUDED.zone_code, updated_at = now()
	`, z.ID, z.SiteCode, z.ZoneCode)
	if err != nil {
		return fmt.Errorf("save zone: %w", err)
	}
	return nil
}

// SaveLocation implements ports.PickFaceReadModel. A decommissioned location
// stays decommissioned.
func (m *ReadModel) SaveLocation(ctx context.Context, l repository.Location) error {
	_, err := queryFor(ctx, m.pool).Exec(ctx, `
		INSERT INTO locations (location_code, zone_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (location_code) DO UPDATE
		SET zone_id = EXCLUDED.zone_id, role = EXCLUDED.role, updated_at = now()
	`, l.Code, l.ZoneID, l.Role)
	if err != nil {
		return fmt.Errorf("save location: %w", err)
	}
	return nil
}

// DecommissionLocation implements ports.PickFaceReadModel: irreversible, and
// also recorded for a location never seen so a late registration cannot bring
// it back.
func (m *ReadModel) DecommissionLocation(ctx context.Context, code string) error {
	_, err := queryFor(ctx, m.pool).Exec(ctx, `
		INSERT INTO locations (location_code, decommissioned) VALUES ($1, true)
		ON CONFLICT (location_code) DO UPDATE SET decommissioned = true, updated_at = now()
	`, code)
	if err != nil {
		return fmt.Errorf("decommission location: %w", err)
	}
	return nil
}

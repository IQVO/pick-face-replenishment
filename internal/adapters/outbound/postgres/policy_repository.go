package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// PolicyRepo is the pgxpool-backed ports.PolicyRepo.
type PolicyRepo struct {
	pool *pgxpool.Pool
}

// NewPolicyRepo constructs a PolicyRepo over pool.
func NewPolicyRepo(pool *pgxpool.Pool) *PolicyRepo { return &PolicyRepo{pool: pool} }

const policyColumns = `site_id, sku, min_qty, max_qty, multiple, version, declared_at, multiple_source, stale`

// Get implements ports.PolicyRepo.
func (r *PolicyRepo) Get(ctx context.Context, siteID, sku string) (repository.PolicyRecord, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+policyColumns+` FROM policies WHERE site_id = $1 AND sku = $2`, siteID, sku)
	rec, err := scanPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.PolicyRecord{}, repository.ErrPolicyNotFound
	}
	return rec, err
}

// Save implements ports.PolicyRepo.
func (r *PolicyRepo) Save(ctx context.Context, rec repository.PolicyRecord, loadedVersion int64) error {
	if loadedVersion == 0 {
		return r.insert(ctx, rec)
	}
	return r.update(ctx, rec, loadedVersion)
}

func (r *PolicyRepo) insert(ctx context.Context, rec repository.PolicyRecord) error {
	p := rec.Policy
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO policies (`+policyColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, p.SiteID, p.SKU, p.MinQty, p.MaxQty, p.Multiple, p.Version, rec.DeclaredAt.UTC(), string(p.Source), p.Stale)
	if isUniqueViolation(err, "policies_pkey") {
		return repository.ErrPolicyAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert policy: %w", err)
	}
	return nil
}

func (r *PolicyRepo) update(ctx context.Context, rec repository.PolicyRecord, loadedVersion int64) error {
	p := rec.Policy
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		UPDATE policies
		SET min_qty = $3, max_qty = $4, multiple = $5, version = $6, declared_at = $7,
		    multiple_source = $8, stale = $9
		WHERE site_id = $1 AND sku = $2 AND version = $10
	`, p.SiteID, p.SKU, p.MinQty, p.MaxQty, p.Multiple, p.Version, rec.DeclaredAt.UTC(), string(p.Source), p.Stale, loadedVersion)
	if err != nil {
		return fmt.Errorf("update policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrConcurrentModification
	}
	return nil
}

// Delete implements ports.PolicyRepo.
func (r *PolicyRepo) Delete(ctx context.Context, siteID, sku string, loadedVersion int64) error {
	q := queryFor(ctx, r.pool)
	tag, err := q.Exec(ctx, `DELETE FROM policies WHERE site_id = $1 AND sku = $2 AND version = $3`, siteID, sku, loadedVersion)
	if err != nil {
		return fmt.Errorf("delete policy: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM policies WHERE site_id = $1 AND sku = $2)`, siteID, sku).Scan(&exists); err != nil {
		return fmt.Errorf("check policy: %w", err)
	}
	if !exists {
		return repository.ErrPolicyNotFound
	}
	return repository.ErrConcurrentModification
}

// List implements ports.PolicyRepo.
func (r *PolicyRepo) List(ctx context.Context, filter repository.PolicyFilter, after *repository.PolicyKey, limit int) ([]repository.PolicyRecord, error) {
	afterSite, afterSKU := "", ""
	if after != nil {
		afterSite, afterSKU = after.SiteID, after.SKU
	}
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT `+policyColumns+` FROM policies
		WHERE ($1 = '' OR site_id = $1)
		  AND ($2 = '' OR sku = $2)
		  AND ($3::boolean IS FALSE OR (site_id, sku) > ($4::text COLLATE "C", $5::text COLLATE "C"))
		ORDER BY site_id, sku
		LIMIT $6
	`, filter.SiteID, filter.SKU, after != nil, afterSite, afterSKU, limit)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	defer rows.Close()
	out := []repository.PolicyRecord{}
	for rows.Next() {
		rec, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanPolicy(row pgx.Row) (repository.PolicyRecord, error) {
	var (
		siteID, sku                       string
		minQty, maxQty, multiple, version int64
		declaredAt                        time.Time
		source                            string
		stale                             bool
	)
	if err := row.Scan(&siteID, &sku, &minQty, &maxQty, &multiple, &version, &declaredAt, &source, &stale); err != nil {
		return repository.PolicyRecord{}, err
	}
	p, err := policy.RestoreWith(siteID, sku, minQty, maxQty, multiple, version, policy.Source(source), stale)
	if err != nil {
		return repository.PolicyRecord{}, fmt.Errorf("stored policy %s/%s is invalid: %w", siteID, sku, err)
	}
	return repository.PolicyRecord{Policy: p, DeclaredAt: declaredAt.UTC()}, nil
}

// isUniqueViolation reports whether err is a unique-constraint violation of
// the named constraint or index.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// openRequestIndex is the partial unique index behind invariant 1 (ADR 0002).
const openRequestIndex = "uq_requests_one_open_per_pick_face"

// RequestRepo is the pgxpool-backed ports.RequestRepo.
type RequestRepo struct {
	pool *pgxpool.Pool
}

// NewRequestRepo constructs a RequestRepo over pool.
func NewRequestRepo(pool *pgxpool.Pool) *RequestRepo { return &RequestRepo{pool: pool} }

const requestColumns = `request_id, site_id, sku, forward_bin, source_bin, quantity, moved_quantity,
	priority, state, policy_min, policy_max, requested_at, completed_at, cancelled_at, cancel_reason, version`

// Get implements ports.RequestRepo.
func (r *RequestRepo) Get(ctx context.Context, id string) (*replenishment.Request, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+requestColumns+` FROM replenishment_requests WHERE request_id = $1`, id)
	req, err := scanRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrRequestNotFound
	}
	return req, err
}

// Save implements ports.RequestRepo.
func (r *RequestRepo) Save(ctx context.Context, req *replenishment.Request, loadedVersion int64) error {
	s := req.Snapshot()
	if loadedVersion == 0 {
		return r.insert(ctx, s)
	}
	return r.update(ctx, s, loadedVersion)
}

func (r *RequestRepo) insert(ctx context.Context, s replenishment.Snapshot) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO replenishment_requests (`+requestColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, requestArgs(s)...)
	return saveError(err, "insert")
}

func (r *RequestRepo) update(ctx context.Context, s replenishment.Snapshot, loadedVersion int64) error {
	args := append(requestArgs(s), loadedVersion)
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		UPDATE replenishment_requests
		SET site_id = $2, sku = $3, forward_bin = $4, source_bin = $5, quantity = $6, moved_quantity = $7,
		    priority = $8, state = $9, policy_min = $10, policy_max = $11, requested_at = $12,
		    completed_at = $13, cancelled_at = $14, cancel_reason = $15, version = $16, updated_at = now()
		WHERE request_id = $1 AND version = $17
	`, args...)
	if err := saveError(err, "update"); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrConcurrentModification
	}
	return nil
}

// saveError maps the two unique violations a request write can hit to the
// domain errors: the primary key means another writer created the id first,
// the partial index means another request is open for the pick face.
func saveError(err error, op string) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err, "replenishment_requests_pkey") {
		return repository.ErrConcurrentModification
	}
	if isUniqueViolation(err, openRequestIndex) {
		return repository.ErrOpenRequestExists
	}
	return fmt.Errorf("%s replenishment request: %w", op, err)
}

func requestArgs(s replenishment.Snapshot) []any {
	return []any{
		s.ID, s.SiteID, s.SKU, s.ForwardBin, s.SourceBin, s.Quantity, s.MovedQuantity,
		string(s.Priority), string(s.State), s.PolicyMin, s.PolicyMax, s.RequestedAt.UTC(),
		nullTime(s.CompletedAt), nullTime(s.CancelledAt), nullString(string(s.CancelReason)), s.Version,
	}
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// OpenFor implements ports.RequestRepo.
func (r *RequestRepo) OpenFor(ctx context.Context, siteID, forwardBin, sku string) (*replenishment.Request, bool, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+requestColumns+` FROM replenishment_requests
		WHERE site_id = $1 AND forward_bin = $2 AND sku = $3 AND state = 'Requested'
	`, siteID, forwardBin, sku)
	req, err := scanRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return req, true, nil
}

// OpenForSKU implements ports.RequestRepo.
func (r *RequestRepo) OpenForSKU(ctx context.Context, siteID, sku string) ([]*replenishment.Request, error) {
	return r.query(ctx, `
		SELECT `+requestColumns+` FROM replenishment_requests
		WHERE site_id = $1 AND sku = $2 AND state = 'Requested'
		ORDER BY request_id
	`, siteID, sku)
}

// OpenForSite implements ports.RequestRepo.
func (r *RequestRepo) OpenForSite(ctx context.Context, siteID string) ([]*replenishment.Request, error) {
	return r.query(ctx, `
		SELECT `+requestColumns+` FROM replenishment_requests
		WHERE site_id = $1 AND state = 'Requested'
		ORDER BY request_id
	`, siteID)
}

// List implements ports.RequestRepo.
func (r *RequestRepo) List(ctx context.Context, f repository.RequestFilter, after string, limit int) ([]*replenishment.Request, error) {
	return r.query(ctx, `
		SELECT `+requestColumns+` FROM replenishment_requests
		WHERE ($1 = '' OR site_id = $1)
		  AND ($2 = '' OR sku = $2)
		  AND ($3 = '' OR state = $3)
		  AND ($4 = '' OR (requested_at, request_id) < (
		        SELECT a.requested_at, a.request_id FROM replenishment_requests a WHERE a.request_id = $4))
		ORDER BY requested_at DESC, request_id DESC
		LIMIT $5
	`, f.SiteID, f.SKU, string(f.State), after, limit)
}

func (r *RequestRepo) query(ctx context.Context, sql string, args ...any) ([]*replenishment.Request, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query replenishment requests: %w", err)
	}
	defer rows.Close()
	out := []*replenishment.Request{}
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func scanRequest(row pgx.Row) (*replenishment.Request, error) {
	var (
		s                    replenishment.Snapshot
		priority, state      string
		completed, cancelled *time.Time
		cancelReason         *string
	)
	if err := row.Scan(&s.ID, &s.SiteID, &s.SKU, &s.ForwardBin, &s.SourceBin, &s.Quantity, &s.MovedQuantity,
		&priority, &state, &s.PolicyMin, &s.PolicyMax, &s.RequestedAt, &completed, &cancelled, &cancelReason, &s.Version); err != nil {
		return nil, err
	}
	s.Priority, s.State = replenishment.Priority(priority), replenishment.State(state)
	s.RequestedAt = s.RequestedAt.UTC()
	if completed != nil {
		s.CompletedAt = completed.UTC()
	}
	if cancelled != nil {
		s.CancelledAt = cancelled.UTC()
	}
	if cancelReason != nil {
		s.CancelReason = replenishment.CancelReason(*cancelReason)
	}
	req, err := replenishment.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("stored request %s is invalid: %w", s.ID, err)
	}
	return req, nil
}

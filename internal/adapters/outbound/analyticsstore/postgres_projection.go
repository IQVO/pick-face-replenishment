package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and folds the event into the fact tables in ONE transaction: the id
// is recorded if and only if its effect is, so a failure anywhere leaves
// nothing behind and the redelivered event is applied afresh, while a replay
// of an applied id is a no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

// Every request upsert keeps the EARLIEST candidate of each milestone: LEAST
// ignores NULLs, so a NULL parameter (the event does not mark that milestone)
// keeps the stored value, and the row converges whatever order the events
// arrive in. The descriptive columns keep the first non-NULL value.
const upsertRequestSQL = `
	INSERT INTO replenishment_facts (request_id, site_id, sku, priority, requested_quantity, completed_quantity,
	                                 cancel_reason, requested_at, completed_at, cancelled_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8::timestamptz, $9::timestamptz, $10::timestamptz)
	ON CONFLICT (request_id) DO UPDATE SET
		priority           = COALESCE(replenishment_facts.priority, EXCLUDED.priority),
		requested_quantity = COALESCE(replenishment_facts.requested_quantity, EXCLUDED.requested_quantity),
		completed_quantity = COALESCE(replenishment_facts.completed_quantity, EXCLUDED.completed_quantity),
		cancel_reason      = COALESCE(replenishment_facts.cancel_reason, EXCLUDED.cancel_reason),
		requested_at       = LEAST(replenishment_facts.requested_at, EXCLUDED.requested_at),
		completed_at       = LEAST(replenishment_facts.completed_at, EXCLUDED.completed_at),
		cancelled_at       = LEAST(replenishment_facts.cancelled_at, EXCLUDED.cancelled_at)`

const upsertPolicySQL = `
	INSERT INTO policy_declaration_facts (site_id, sku, version, min_qty, max_qty, multiple, declared_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (site_id, sku, version) DO UPDATE SET
		declared_at = LEAST(policy_declaration_facts.declared_at, EXCLUDED.declared_at)`

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		// A pure check of the decoded event: deterministic, the same bytes
		// always fail (the consumer dead-letters it).
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At.UTC())
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	if err := fold(ctx, tx, e); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.EventID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// fold writes the facts of one event inside tx.
func fold(ctx context.Context, tx pgx.Tx, e report.Event) error {
	if e.Kind == report.KindReplenishmentPolicyDeclared {
		_, err := tx.Exec(ctx, upsertPolicySQL, e.SiteID, e.SKU, e.Version, e.MinQty, e.MaxQty, e.Multiple, e.At.UTC())
		return err
	}
	m := report.MilestonesOf(e)
	var priority, reason any
	var requestedQty, completedQty any
	switch e.Kind {
	case report.KindReplenishmentRequested:
		priority, requestedQty = e.Priority, e.Quantity
	case report.KindReplenishmentCompleted:
		completedQty = e.Quantity
	case report.KindReplenishmentCancelled:
		reason = e.CancelReason
	case report.KindReplenishmentPolicyDeclared:
	}
	_, err := tx.Exec(ctx, upsertRequestSQL, e.RequestID, e.SiteID, e.SKU, priority, requestedQty, completedQty, reason,
		m.Requested, m.Completed, m.Cancelled)
	return err
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error in report.ErrRejected: the same event
// can never succeed, so the consumer dead-letters it instead of retrying
// forever. Everything else (connection loss, timeouts, locks, failovers) stays
// transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) >= 2 && (pgErr.Code[:2] == "22" || pgErr.Code[:2] == "23") {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

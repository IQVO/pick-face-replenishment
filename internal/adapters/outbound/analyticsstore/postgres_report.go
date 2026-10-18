package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Days are UTC calendar days;
// every window is half-open [lo, hi). SQL counts, sums and takes percentiles
// (percentile_cont, the definition report.Percentile mirrors); the range
// rules, the site scope and the freshness lag live in internal/analytics/report.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// funnelDaysSQL builds the same windows as report.DayWindows: one per UTC day
// intersecting [$1, $2), clipped to the range, optionally narrowed to one site
// ($3, ” = every site). The day series runs over UTC-naive timestamps so a
// session time zone with DST cannot shift a day. A negative duration (events
// recorded out of order) counts as zero.
const funnelDaysSQL = `
	WITH days AS (
		SELECT d::date AS day,
		       GREATEST(d AT TIME ZONE 'UTC', $1::timestamptz)                    AS lo,
		       LEAST((d + interval '1 day') AT TIME ZONE 'UTC', $2::timestamptz) AS hi
		FROM generate_series(
		         date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC'),
		         ($2::timestamptz - interval '1 microsecond') AT TIME ZONE 'UTC',
		         interval '1 day') AS d
		WHERE $1::timestamptz < $2::timestamptz
	)
	SELECT days.day,
	       requested.n, requested.units,
	       completed.n, completed.units,
	       (SELECT count(*) FROM replenishment_facts r WHERE ($3 = '' OR r.site_id = $3) AND r.cancelled_at >= days.lo AND r.cancelled_at < days.hi),
	       latency.n, latency.p50, latency.p95,
	       (SELECT count(*) FROM policy_declaration_facts p WHERE ($3 = '' OR p.site_id = $3) AND p.declared_at >= days.lo AND p.declared_at < days.hi)
	FROM days
	CROSS JOIN LATERAL (
		SELECT count(*) AS n, COALESCE(sum(r.requested_quantity), 0)::bigint AS units
		FROM replenishment_facts r
		WHERE ($3 = '' OR r.site_id = $3) AND r.requested_at >= days.lo AND r.requested_at < days.hi
	) requested
	CROSS JOIN LATERAL (
		SELECT count(*) AS n, COALESCE(sum(r.completed_quantity), 0)::bigint AS units
		FROM replenishment_facts r
		WHERE ($3 = '' OR r.site_id = $3) AND r.completed_at >= days.lo AND r.completed_at < days.hi
	) completed
	CROSS JOIN LATERAL (
		SELECT count(*) AS n,
		       percentile_cont(0.50) WITHIN GROUP (ORDER BY s.secs) AS p50,
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY s.secs) AS p95
		FROM (SELECT GREATEST(0, EXTRACT(EPOCH FROM r.completed_at - r.requested_at))::float8 AS secs
		      FROM replenishment_facts r
		      WHERE ($3 = '' OR r.site_id = $3) AND r.requested_at IS NOT NULL AND r.completed_at >= days.lo AND r.completed_at < days.hi) s
	) latency
	ORDER BY days.day`

// FunnelDays implements report.Reader.
func (r *Reader) FunnelDays(ctx context.Context, q report.FunnelQuery) ([]report.FunnelDay, error) {
	rows, err := r.pool.Query(ctx, funnelDaysSQL, q.Range.From.UTC(), q.Range.To.UTC(), q.SiteID)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: funnel days: %w", err)
	}
	defer rows.Close()
	out := []report.FunnelDay{}
	for rows.Next() {
		var d report.FunnelDay
		if err := rows.Scan(&d.Day, &d.Requested, &d.UnitsRequested, &d.Completed, &d.UnitsReplenished, &d.Cancelled,
			&d.CompletionLatency.Count, &d.CompletionLatency.P50, &d.CompletionLatency.P95, &d.PoliciesDeclared); err != nil {
			return nil, fmt.Errorf("analyticsstore: scan funnel day: %w", err)
		}
		y, m, dd := d.Day.Date()
		d.Day = time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: funnel days rows: %w", err)
	}
	return out, nil
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

package analyticsstore

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// store is what the contract exercises: the writer and the reader over one
// model. The SAME contract runs against Memory (unit) and Postgres
// (integration), so the two stores cannot drift.
type store interface {
	report.Projection
	report.Reader
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func reqEv(id, request, site, when string, qty int64) report.Event {
	return report.Event{Kind: report.KindReplenishmentRequested, EventID: id, At: at(when), SiteID: site, SKU: "SKU-1",
		RequestID: request, Quantity: qty, Priority: "NORMAL"}
}

func doneEv(id, request, site, when string, qty int64) report.Event {
	return report.Event{Kind: report.KindReplenishmentCompleted, EventID: id, At: at(when), SiteID: site, SKU: "SKU-1",
		RequestID: request, Quantity: qty}
}

func cancelEv(id, request, site, when string) report.Event {
	return report.Event{Kind: report.KindReplenishmentCancelled, EventID: id, At: at(when), SiteID: site, SKU: "SKU-1",
		RequestID: request, CancelReason: "OPERATOR"}
}

func policyEv(id, site, sku, when string, version int64) report.Event {
	return report.Event{Kind: report.KindReplenishmentPolicyDeclared, EventID: id, At: at(when), SiteID: site, SKU: sku,
		Version: version, MinQty: 10, MaxQty: 60, Multiple: 6}
}

func mustApply(t *testing.T, s store, events ...report.Event) {
	t.Helper()
	for _, e := range events {
		if _, err := s.Apply(context.Background(), e); err != nil {
			t.Fatalf("apply %s %s: %v", e.Kind, e.EventID, err)
		}
	}
}

func funnel(t *testing.T, s store, from, to, site string) []report.FunnelDay {
	t.Helper()
	got, err := s.FunnelDays(context.Background(), report.FunnelQuery{
		Range: report.Range{From: at(from), To: at(to)}, SiteID: site, Granularity: report.GranularityDay,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func approx(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(*a-*b) < 1e-6
}

func f(v float64) *float64 { return &v }

// counts is the integer part of a day row: requested, units requested,
// completed, units replenished, cancelled, policies declared.
type counts [6]int64

func countsOf(d report.FunnelDay) counts {
	return counts{int64(d.Requested), d.UnitsRequested, int64(d.Completed), d.UnitsReplenished, int64(d.Cancelled), int64(d.PoliciesDeclared)}
}

func wantCounts(t *testing.T, what string, d report.FunnelDay, want counts) {
	t.Helper()
	if got := countsOf(d); got != want {
		t.Fatalf("%s = %+v (counts %v), want %v", what, d, got, want)
	}
}

func wantLatency(t *testing.T, what string, got report.Timing, count int, p50, p95 *float64) {
	t.Helper()
	if got.Count != count || !approx(got.P50, p50) || !approx(got.P95, p95) {
		t.Fatalf("%s latency = %+v, want count %d p50 %v p95 %v", what, got, count, p50, p95)
	}
}

// runContract is the shared behavioural contract of the analytical store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	runWriterContract(t, newStore)
	runMilestonesContract(t, newStore)
	runDayRowContract(t, newStore)
	runSiteScopeContract(t, newStore)
	runPolicyContract(t, newStore)
	runPercentileContract(t, newStore)
	runFreshnessContract(t, newStore)
}

// runWriterContract pins the writer semantics: deterministic rejection of
// invalid events and idempotent replay.
func runWriterContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("invalid events are rejected deterministically", func(t *testing.T) {
		s := newStore(t)
		bad := reqEv("", "repl-1", "SITE-1", "2026-10-05T10:00:00Z", 24)
		if _, err := s.Apply(context.Background(), bad); !errors.Is(err, report.ErrRejected) {
			t.Fatalf("err = %v, want ErrRejected", err)
		}
	})

	t.Run("a replayed event id is a no-op", func(t *testing.T) {
		s := newStore(t)
		e := reqEv("e1", "repl-1", "SITE-1", "2026-10-05T10:00:00Z", 24)
		if applied, err := s.Apply(context.Background(), e); err != nil || !applied {
			t.Fatalf("first apply = %v, %v", applied, err)
		}
		if applied, err := s.Apply(context.Background(), e); err != nil || applied {
			t.Fatalf("replay = %v, %v; want false, nil", applied, err)
		}
		days := funnel(t, s, "2026-10-05T00:00:00Z", "2026-10-06T00:00:00Z", "")
		if len(days) != 1 || days[0].Requested != 1 || days[0].UnitsRequested != 24 {
			t.Fatalf("days = %+v, want one request counted once", days)
		}
	})
}

// runMilestonesContract pins that milestones converge whatever order the
// events arrive in.
func runMilestonesContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("milestones converge whatever order the events arrive in", func(t *testing.T) {
		s := newStore(t)
		mustApply(t, s,
			doneEv("e3", "repl-1", "SITE-1", "2026-10-05T12:00:00Z", 24),
			cancelEv("e4", "repl-2", "SITE-1", "2026-10-06T09:00:00Z"),
			reqEv("e1", "repl-1", "SITE-1", "2026-10-05T10:00:00Z", 24),
			reqEv("e2", "repl-2", "SITE-1", "2026-10-06T08:00:00Z", 12),
		)
		days := funnel(t, s, "2026-10-05T00:00:00Z", "2026-10-07T00:00:00Z", "")
		if len(days) != 2 {
			t.Fatalf("days = %d, want 2", len(days))
		}
		wantCounts(t, "day 5", days[0], counts{1, 24, 1, 24, 0, 0})
		wantCounts(t, "day 6", days[1], counts{1, 12, 0, 0, 1, 0})
		// repl-1 was requested and completed on day 5 with a 2h latency; the
		// events were applied out of order and still agree.
		wantLatency(t, "day 5", days[0].CompletionLatency, 1, f(7200), f(7200))
	})

	t.Run("a completion without a seen request counts but has no latency", func(t *testing.T) {
		s := newStore(t)
		mustApply(t, s, doneEv("e1", "repl-9", "SITE-1", "2026-10-06T11:00:00Z", 6))
		days := funnel(t, s, "2026-10-06T00:00:00Z", "2026-10-07T00:00:00Z", "")
		wantCounts(t, "day", days[0], counts{0, 0, 1, 6, 0, 0})
		wantLatency(t, "day", days[0].CompletionLatency, 0, nil, nil)
	})
}

// runDayRowContract pins that days are dense and windowed.
func runDayRowContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("days are dense and windowed, not per event", func(t *testing.T) {
		s := newStore(t)
		mustApply(t, s, reqEv("e1", "repl-1", "SITE-1", "2026-10-05T23:30:00Z", 24))
		// A range that starts and ends mid-day: the counts cover only the
		// part of the day inside it, and empty days still appear.
		days := funnel(t, s, "2026-10-05T23:00:00Z", "2026-10-08T01:00:00Z", "")
		if len(days) != 4 {
			t.Fatalf("days = %d, want 4 (three whole days plus the partials)", len(days))
		}
		if days[0].Requested != 1 || days[1].Requested != 0 || days[3].Requested != 0 {
			t.Fatalf("dense days = %+v", days)
		}
	})
}

// runSiteScopeContract pins the optional site scope.
func runSiteScopeContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("the site scope narrows every counter", func(t *testing.T) {
		s := newStore(t)
		mustApply(t, s,
			reqEv("e1", "repl-1", "SITE-1", "2026-10-05T10:00:00Z", 24),
			reqEv("e2", "repl-2", "SITE-2", "2026-10-05T10:30:00Z", 12),
			doneEv("e3", "repl-2", "SITE-2", "2026-10-05T11:00:00Z", 12),
			doneEv("e4", "repl-3", "SITE-3", "2026-10-05T11:30:00Z", 6),
			policyEv("e5", "SITE-1", "SKU-1", "2026-10-05T09:00:00Z", 1),
		)
		day := func(site string) report.FunnelDay {
			return funnel(t, s, "2026-10-05T00:00:00Z", "2026-10-06T00:00:00Z", site)[0]
		}
		wantCounts(t, "unscoped day", day(""), counts{2, 36, 2, 18, 0, 1})
		wantCounts(t, "SITE-1 day", day("SITE-1"), counts{1, 24, 0, 0, 0, 1})
		wantCounts(t, "SITE-2 day", day("SITE-2"), counts{1, 12, 1, 12, 0, 0})
		// The 30-minute latency of SITE-2 is counted.
		wantLatency(t, "SITE-2", day("SITE-2").CompletionLatency, 1, f(1800), f(1800))
		// A completion without a seen request has no latency.
		wantCounts(t, "SITE-3 day", day("SITE-3"), counts{0, 0, 1, 6, 0, 0})
		wantLatency(t, "SITE-3", day("SITE-3").CompletionLatency, 0, nil, nil)
	})
}

// runPolicyContract pins the analytics-only policy declarations: one fact per
// policy version, earliest time wins.
func runPolicyContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("policy declarations count once per version", func(t *testing.T) {
		s := newStore(t)
		mustApply(t, s,
			policyEv("p1", "SITE-1", "SKU-1", "2026-10-05T09:00:00Z", 1),
			policyEv("p2", "SITE-1", "SKU-1", "2026-10-06T09:00:00Z", 2),
			policyEv("p3", "SITE-1", "SKU-2", "2026-10-06T10:00:00Z", 1),
			// The same version under another event id and a later time: one fact, earliest wins.
			policyEv("p4", "SITE-1", "SKU-1", "2026-10-07T09:00:00Z", 2),
		)
		days := funnel(t, s, "2026-10-05T00:00:00Z", "2026-10-08T00:00:00Z", "")
		if days[0].PoliciesDeclared != 1 || days[1].PoliciesDeclared != 2 || days[2].PoliciesDeclared != 0 {
			t.Fatalf("policies per day = %d, %d, %d; want 1, 2, 0", days[0].PoliciesDeclared, days[1].PoliciesDeclared, days[2].PoliciesDeclared)
		}
	})
}

// runPercentileContract pins the latency percentile definition.
func runPercentileContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("completion latency percentiles are linear-interpolated", func(t *testing.T) {
		s := newStore(t)
		// Four requests requested and completed on one day: 1h, 2h, 3h, 4h.
		for i := 0; i < 4; i++ {
			n := string(rune('1' + i))
			mustApply(t, s,
				reqEv("r"+n, "repl-"+n, "SITE-1", "2026-10-05T08:00:00Z", 10),
				doneEv("d"+n, "repl-"+n, "SITE-1", at("2026-10-05T08:00:00Z").Add(time.Duration(i+1)*time.Hour).Format(time.RFC3339), 10),
			)
		}
		days := funnel(t, s, "2026-10-05T00:00:00Z", "2026-10-06T00:00:00Z", "")
		lat := days[0].CompletionLatency
		if lat.Count != 4 || !approx(lat.P50, f(9000)) || !approx(lat.P95, f(13860)) {
			t.Fatalf("latency = %+v, want count 4, p50 9000 (2h30), p95 13860 (3h51) by linear interpolation", lat)
		}
	})
}

// runFreshnessContract pins the freshness basis: the newest applied event.
func runFreshnessContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Helper()
	t.Run("freshness is the newest applied event time", func(t *testing.T) {
		s := newStore(t)
		if got, err := s.LastEventAt(context.Background()); err != nil || got != nil {
			t.Fatalf("empty LastEventAt = %v, %v; want nil", got, err)
		}
		mustApply(t, s,
			reqEv("e1", "repl-1", "SITE-1", "2026-10-05T10:00:00Z", 24),
			cancelEv("e2", "repl-2", "SITE-1", "2026-10-07T09:00:00Z"),
			policyEv("e3", "SITE-1", "SKU-1", "2026-10-06T09:00:00Z", 1),
		)
		got, err := s.LastEventAt(context.Background())
		if err != nil || got == nil || !got.Equal(at("2026-10-07T09:00:00Z")) {
			t.Fatalf("LastEventAt = %v, %v; want 2026-10-07T09:00:00Z", got, err)
		}
	})
}

func TestMemoryContract(t *testing.T) {
	runContract(t, func(*testing.T) store { return NewMemory() })
}

package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// Memory is the in-memory twin of the analytical store: the same Projection
// and the same Reader semantics (earliest milestone wins, dedupe on the
// CloudEvents id, dense day rows, percentile_cont percentiles), so the same
// contract test can run against it and against Postgres. It is NOT the
// production store: nothing in cmd/ ever wires it.
type Memory struct {
	mu       sync.Mutex
	applied  map[string]time.Time // event id -> CloudEvents time
	requests map[string]*memoryRequest
	policies map[policyKey]*memoryPolicy
}

// memoryRequest mirrors one replenishment_facts row.
type memoryRequest struct {
	site         string
	priority     string
	requestedQty *int64
	completedQty *int64
	cancelReason string
	requested    *time.Time
	completed    *time.Time
	cancelled    *time.Time
}

type policyKey struct {
	site, sku string
	version   int64
}

// memoryPolicy mirrors one policy_declaration_facts row.
type memoryPolicy struct {
	site     string
	declared time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{applied: map[string]time.Time{}, requests: map[string]*memoryRequest{}, policies: map[policyKey]*memoryPolicy{}}
}

var _ report.Projection = (*Memory)(nil)
var _ report.Reader = (*Memory)(nil)

func least(existing, candidate *time.Time) *time.Time {
	if candidate == nil {
		return existing // LEAST ignores NULLs: a milestone this event does not mark keeps the stored value
	}
	if existing == nil || candidate.Before(*existing) {
		return candidate
	}
	return existing
}

func firstQty(existing *int64, candidate int64) *int64 {
	if existing != nil {
		return existing
	}
	return &candidate
}

// Apply implements report.Projection with the same first-writer-wins
// semantics as the SQL store.
func (m *Memory) Apply(_ context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, seen := m.applied[e.EventID]; seen {
		return false, nil
	}
	m.applied[e.EventID] = e.At.UTC()
	if e.Kind == report.KindReplenishmentPolicyDeclared {
		m.applyPolicy(e)
		return true, nil
	}
	r, ok := m.requests[e.RequestID]
	if !ok {
		r = &memoryRequest{site: e.SiteID}
		m.requests[e.RequestID] = r
	}
	ms := report.MilestonesOf(e)
	r.requested = least(r.requested, ms.Requested)
	r.completed = least(r.completed, ms.Completed)
	r.cancelled = least(r.cancelled, ms.Cancelled)
	switch e.Kind {
	case report.KindReplenishmentRequested:
		if r.priority == "" {
			r.priority = e.Priority
		}
		r.requestedQty = firstQty(r.requestedQty, e.Quantity)
	case report.KindReplenishmentCompleted:
		r.completedQty = firstQty(r.completedQty, e.Quantity)
	case report.KindReplenishmentCancelled:
		if r.cancelReason == "" {
			r.cancelReason = e.CancelReason
		}
	case report.KindReplenishmentPolicyDeclared:
	}
	return true, nil
}

func (m *Memory) applyPolicy(e report.Event) {
	k := policyKey{site: e.SiteID, sku: e.SKU, version: e.Version}
	at := e.At.UTC()
	if p, ok := m.policies[k]; ok {
		if at.Before(p.declared) {
			p.declared = at
		}
		return
	}
	m.policies[k] = &memoryPolicy{site: e.SiteID, declared: at}
}

// in reports whether t falls in the half-open window [w.From, w.To); a nil
// time never does.
func in(t *time.Time, w report.DayWindow) bool {
	return t != nil && !t.Before(w.From) && t.Before(w.To)
}

func inTime(t time.Time, w report.DayWindow) bool { return in(&t, w) }

func sum(q *int64) int64 {
	if q == nil {
		return 0
	}
	return *q
}

// countDay folds the request and policy facts of the site scope into the
// single window w, exactly like funnelDaysSQL.
func (m *Memory) countDay(siteID string, w report.DayWindow) report.FunnelDay {
	d := report.FunnelDay{Day: w.Day}
	var latencies []float64
	for _, r := range m.requests {
		if siteID != "" && r.site != siteID {
			continue
		}
		if in(r.requested, w) {
			d.Requested++
			d.UnitsRequested += sum(r.requestedQty)
		}
		if in(r.completed, w) {
			d.Completed++
			d.UnitsReplenished += sum(r.completedQty)
			if r.requested != nil {
				latencies = append(latencies, max(0, r.completed.Sub(*r.requested).Seconds()))
			}
		}
		if in(r.cancelled, w) {
			d.Cancelled++
		}
	}
	for _, p := range m.policies {
		if (siteID == "" || p.site == siteID) && inTime(p.declared, w) {
			d.PoliciesDeclared++
		}
	}
	sort.Float64s(latencies)
	d.CompletionLatency = report.TimingOf(latencies)
	return d
}

// FunnelDays implements report.Reader: one dense row per DayWindow.
func (m *Memory) FunnelDays(_ context.Context, q report.FunnelQuery) ([]report.FunnelDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []report.FunnelDay
	for _, w := range report.DayWindows(q.Range) {
		out = append(out, m.countDay(q.SiteID, w))
	}
	return out, nil
}

// LastEventAt implements report.Reader: the newest applied CloudEvents time.
func (m *Memory) LastEventAt(_ context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest *time.Time
	for _, at := range m.applied {
		at := at
		if newest == nil || at.After(*newest) {
			newest = &at
		}
	}
	if newest != nil {
		utc := newest.UTC()
		return &utc, nil
	}
	return nil, nil
}

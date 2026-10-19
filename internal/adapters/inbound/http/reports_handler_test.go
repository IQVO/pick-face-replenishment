package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// fakeReportReader is a scripted report.Reader.
type fakeReportReader struct {
	days    []report.FunnelDay
	query   report.FunnelQuery
	asOf    *time.Time
	readErr error
}

func (f *fakeReportReader) FunnelDays(_ context.Context, q report.FunnelQuery) ([]report.FunnelDay, error) {
	f.query = q
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.days == nil {
		return []report.FunnelDay{}, nil
	}
	return f.days, nil
}

func (f *fakeReportReader) LastEventAt(context.Context) (*time.Time, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.asOf, nil
}

var reportNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func newReportsServer(r *fakeReportReader) http.Handler {
	return inboundhttp.NewReportsRouter(&inboundhttp.ReportsServer{Reader: r, Now: func() time.Time { return reportNow }})
}

func getJSON(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
		}
	}
	return rec, body
}

func TestReplenishmentFunnelReport(t *testing.T) {
	p50, p95 := 1800.0, 3600.0
	reader := &fakeReportReader{days: []report.FunnelDay{{
		Day:       time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Requested: 5, UnitsRequested: 120, Completed: 3, UnitsReplenished: 72, Cancelled: 1, PoliciesDeclared: 2,
		CompletionLatency: report.Timing{Count: 3, P50: &p50, P95: &p95},
	}}}
	h := newReportsServer(reader)

	rec, body := getJSON(t, h, "/reports/replenishment-funnel?from=2026-10-05T00:00:00Z&to=2026-10-06T00:00:00Z&siteId=SITE-1&granularity=day")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if reader.query.SiteID != "SITE-1" || reader.query.Granularity != report.GranularityDay ||
		!reader.query.Range.From.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("query = %+v", reader.query)
	}
	if body["site_id"] != "SITE-1" || body["granularity"] != "day" {
		t.Fatalf("body head = %v", body)
	}
	days := body["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("days = %v", days)
	}
	day := days[0].(map[string]any)
	if day["day"] != "2026-10-05" {
		t.Fatalf("day = %v", day["day"])
	}
	for name, want := range map[string]float64{
		"requests_raised": 5, "units_requested": 120, "requests_completed": 3, "units_replenished": 72,
		"requests_cancelled": 1, "policies_declared": 2,
	} {
		if day[name] != want {
			t.Fatalf("%s = %v, want %v", name, day[name], want)
		}
	}
	latency := day["completion_latency"].(map[string]any)
	if latency["count"] != float64(3) || latency["p50_seconds"] != p50 || latency["p95_seconds"] != p95 {
		t.Fatalf("latency = %v", latency)
	}
}

func TestReplenishmentFunnelDefaultsAndValidation(t *testing.T) {
	h := newReportsServer(&fakeReportReader{})

	// No params: the 30 days ending now, day granularity, every site.
	rec, body := getJSON(t, h, "/reports/replenishment-funnel")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["from"] != "2026-09-18T12:00:00Z" || body["to"] != "2026-10-18T12:00:00Z" {
		t.Fatalf("default range = %v..%v", body["from"], body["to"])
	}
	if body["granularity"] != "day" {
		t.Fatalf("default granularity = %v", body["granularity"])
	}
	if _, present := body["site_id"]; present {
		t.Fatalf("site_id must be omitted when no site was given: %v", body)
	}

	for name, tc := range map[string]struct{ path, slug string }{
		"bad from":        {"/reports/replenishment-funnel?from=nope", "invalid-report-range"},
		"bad to":          {"/reports/replenishment-funnel?to=nope", "invalid-report-range"},
		"inverted range":  {"/reports/replenishment-funnel?from=2026-10-08T00:00:00Z&to=2026-10-07T00:00:00Z", "invalid-report-range"},
		"range too large": {"/reports/replenishment-funnel?from=2025-01-01T00:00:00Z&to=2026-10-08T00:00:00Z", "invalid-report-range"},
		"bad granularity": {"/reports/replenishment-funnel?granularity=hour", "invalid-report-granularity"},
	} {
		rec, body := getJSON(t, h, tc.path)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("%s: status = %d %s", name, rec.Code, rec.Header().Get("Content-Type"))
		}
		if body["type"] != "https://errors.pick-face-replenishment.warehouse-systems.dev/"+tc.slug {
			t.Fatalf("%s: problem type = %v", name, body["type"])
		}
	}
}

func TestReportsFreshness(t *testing.T) {
	asOf := time.Date(2026, 10, 18, 11, 59, 55, 0, time.UTC)
	rec, body := getJSON(t, newReportsServer(&fakeReportReader{asOf: &asOf}), "/reports/freshness")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body["as_of"] != "2026-10-18T11:59:55Z" || body["lag_seconds"] != float64(5) {
		t.Fatalf("freshness = %v", body)
	}

	// Nothing applied yet: both fields null.
	rec, body = getJSON(t, newReportsServer(&fakeReportReader{}), "/reports/freshness")
	if rec.Code != http.StatusOK || body["as_of"] != nil || body["lag_seconds"] != nil {
		t.Fatalf("empty freshness = %d %v", rec.Code, body)
	}
}

func TestReportsStoreErrorIs500(t *testing.T) {
	h := newReportsServer(&fakeReportReader{readErr: errors.New("connection refused")})
	for _, path := range []string{"/reports/replenishment-funnel", "/reports/freshness"} {
		rec, body := getJSON(t, h, path)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		if body["type"] != "https://errors.pick-face-replenishment.warehouse-systems.dev/report-store-error" {
			t.Fatalf("%s: problem type = %v", path, body["type"])
		}
		if rec.Body.String() != "" && json.Valid(rec.Body.Bytes()) {
			if d, _ := body["detail"].(string); d == "" || d == "connection refused" {
				t.Fatalf("%s: detail = %q; it must be generic", path, d)
			}
		}
	}
}

func TestReportsHealthz(t *testing.T) {
	rec, _ := getJSON(t, newReportsServer(&fakeReportReader{}), "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

//go:build integration

// End-to-end proof of the REAL entry point of cmd/pfr-reports on
// testcontainers: the analytical database is migrated and seeded with the
// repo's own analytics writer (the projection cmd/pfr-projector owns), the
// binary boots with nothing but its documented environment, the documented
// probe answers, the documented replenishment funnel and freshness reports
// answer the seeded facts, and SIGTERM drains cleanly.
//
// Doc sources: docs/adr/0008-analytics-read-side.md (cmd/pfr-reports is the
// read-only HTTP reader of the analytics read side: ANALYTICS_DATABASE_URL
// REQUIRED, HTTP_ADDR default :8092; GET /reports/replenishment-funnel and
// GET /reports/freshness and /healthz),
// docs/docs/api-reference/rest/get-replenishment-funnel.api.mdx and
// docs/docs/api-reference/rest/get-reports-freshness.api.mdx (the documented
// wire contract asserted below).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// TestMain holds the package-level SIGTERM registration for the whole test
// run (the real run() installs — and on return RESTORES — its own signal
// handler, so a SIGTERM arriving outside a live run() would otherwise kill
// the test binary itself) and terminates the shared Postgres container.
func TestMain(m *testing.M) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// service is one booted instance of the real run().
type service struct {
	base     string
	done     <-chan error
	stopOnce sync.Once
	stopErr  error
}

// itBootReports sets the environment a deployment sets, starts the REAL
// run() in-process and waits for the documented probe /healthz.
func itBootReports(t *testing.T, env map[string]string) *service {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)
	done := make(chan error, 1)
	go func() { done <- run() }()
	s := &service{base: "http://" + addr, done: done}
	t.Cleanup(func() { itAssertCleanDrain(t, s.stop()) })
	waitUntil(t, "the documented probe /healthz to answer 200", func() bool {
		code, _ := getJSON(t, s.base+"/healthz")
		return code == http.StatusOK
	})
	return s
}

// stop drives the binary's real shutdown path (SIGTERM — run() installs its
// own signal.NotifyContext); safe because TestMain keeps a signal sink.
func (s *service) stop() error {
	s.stopOnce.Do(func() {
		select {
		case s.stopErr = <-s.done: // already stopped on its own
		default:
		}
		if s.stopErr == nil {
			if killErr := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); killErr != nil {
				s.stopErr = killErr
				return
			}
			select {
			case s.stopErr = <-s.done:
			case <-time.After(60 * time.Second):
				s.stopErr = fmt.Errorf("the reports server did not stop")
			}
		}
	})
	return s.stopErr
}

// itAssertCleanDrain checks run()'s shutdown outcome: a SIGTERM-driven stop
// is a clean shutdown and run() returns nil.
func itAssertCleanDrain(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the reports server must drain cleanly on SIGTERM, got %v", err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// TestReportsServeTheProjectionAndStopCleanly proves the shipped reader's
// contract: the analytical migrations + the repo's own writer seed the
// projection (distinct, non-zero operands), then env only -> /healthz ->
// the documented funnel day and freshness answer the seeded facts ->
// SIGTERM drains cleanly.
func TestReportsServeTheProjectionAndStopCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatalf("run analytics migrations: %v", err)
	}
	ctx := context.Background()
	writer, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	projection := analyticsstore.NewProjection(writer)

	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	events := []report.Event{
		{Kind: report.KindReplenishmentPolicyDeclared, EventID: "rep-e1", At: day.Add(7 * time.Hour),
			SiteID: "SITE-REPORTS-1", SKU: "SKU-1", Version: 1, MinQty: 10, MaxQty: 60, Multiple: 6},
		{Kind: report.KindReplenishmentRequested, EventID: "rep-e2", At: day.Add(8 * time.Hour),
			SiteID: "SITE-REPORTS-1", SKU: "SKU-1", RequestID: "repl-reports-1", Quantity: 24, Priority: "NORMAL"},
		{Kind: report.KindReplenishmentCompleted, EventID: "rep-e3", At: day.Add(9 * time.Hour),
			SiteID: "SITE-REPORTS-1", SKU: "SKU-1", RequestID: "repl-reports-1", Quantity: 24},
		{Kind: report.KindReplenishmentRequested, EventID: "rep-e4", At: day.Add(10 * time.Hour),
			SiteID: "SITE-REPORTS-2", SKU: "SKU-2", RequestID: "repl-reports-2", Quantity: 12, Priority: "URGENT"},
	}
	for _, e := range events {
		if _, err := projection.Apply(ctx, e); err != nil {
			t.Fatalf("seed %s: %v", e.EventID, err)
		}
	}

	s := itBootReports(t, map[string]string{
		"ANALYTICS_DATABASE_URL": url,
		"LOG_LEVEL":              "error",
	})

	from, to := day.Format(time.RFC3339), day.Add(24*time.Hour).Format(time.RFC3339)
	code, body := getJSON(t, s.base+"/reports/replenishment-funnel?from="+from+"&to="+to+"&siteId=SITE-REPORTS-1")
	if code != http.StatusOK {
		t.Fatalf("replenishment-funnel = %d %v", code, body)
	}
	days, _ := body["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("funnel days = %v (the siteId scope must leave one day)", body)
	}
	d, _ := days[0].(map[string]any)
	if d["requests_raised"] != float64(1) || d["units_requested"] != float64(24) ||
		d["requests_completed"] != float64(1) || d["units_replenished"] != float64(24) ||
		d["requests_cancelled"] != float64(0) || d["policies_declared"] != float64(1) {
		t.Fatalf("funnel day = %v", d)
	}
	lat, _ := d["completion_latency"].(map[string]any)
	if lat["count"] != float64(1) || lat["p50_seconds"] != float64(3600) {
		t.Fatalf("completion latency = %v (want one completion after 3600s)", lat)
	}

	// The same window WITHOUT the site scope sees the second site's open
	// request too: the scoping is the reader's, not a fixture accident.
	code, body = getJSON(t, s.base+"/reports/replenishment-funnel?from="+from+"&to="+to)
	if code != http.StatusOK {
		t.Fatalf("replenishment-funnel (all sites) = %d %v", code, body)
	}
	days, _ = body["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("funnel days (all sites) = %v", body)
	}
	d, _ = days[0].(map[string]any)
	if d["requests_raised"] != float64(2) || d["units_requested"] != float64(36) || d["requests_completed"] != float64(1) {
		t.Fatalf("funnel day (all sites) = %v", d)
	}

	code, body = getJSON(t, s.base+"/reports/freshness")
	if code != http.StatusOK || body["as_of"] == nil || body["lag_seconds"] == nil {
		t.Fatalf("freshness = %d %v", code, body)
	}
	if body["as_of"] != day.Add(10*time.Hour).Format("2006-01-02T15:04:05Z07:00") {
		t.Fatalf("as_of = %v (want the newest seeded event %s)", body["as_of"], day.Add(10*time.Hour))
	}

	itAssertCleanDrain(t, s.stop())
}

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// run() reads its configuration from the environment and stops on SIGINT or
// SIGTERM (it takes no context), so the drain step of the test below sends a
// real SIGTERM to this process. TestMain therefore holds the test binary's OWN
// SIGTERM registration from before any run() starts until process exit: with
// it, the signal is delivered to both handlers and the process survives;
// signal.Stop is never called, because a stopped registration would restore
// the default disposition mid-run and kill the test binary.
func TestMain(m *testing.M) {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
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

func waitHealthz(t *testing.T, base string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code, body := getJSON(t, base+"/healthz"); code == http.StatusOK && body["status"] == "ok" {
			return
		}
	}
	t.Fatal("/healthz never answered 200 {\"status\":\"ok\"}")
}

// seedFacts folds one day of the analytics stream into the empty analytical
// database with the repo's own writer (the projection cmd/pfr-projector runs):
// a policy version, a request that completes one hour later, and a cancellation
// on a second site, plus a replay of the request event.
func seedFacts(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	writer, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	projection := analyticsstore.NewProjection(writer)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	events := []report.Event{
		{Kind: report.KindReplenishmentPolicyDeclared, EventID: "ev-policy", At: day.Add(7 * time.Hour),
			SiteID: "SITE-1", SKU: "SKU-1", Version: 1, MinQty: 10, MaxQty: 60, Multiple: 6},
		{Kind: report.KindReplenishmentRequested, EventID: "ev-requested", At: day.Add(8 * time.Hour),
			SiteID: "SITE-1", SKU: "SKU-1", RequestID: "repl-1", Quantity: 24, Priority: "NORMAL"},
		{Kind: report.KindReplenishmentCompleted, EventID: "ev-completed", At: day.Add(9 * time.Hour),
			SiteID: "SITE-1", SKU: "SKU-1", RequestID: "repl-1", Quantity: 24},
		{Kind: report.KindReplenishmentCancelled, EventID: "ev-cancelled", At: day.Add(10 * time.Hour),
			SiteID: "SITE-2", SKU: "SKU-1", RequestID: "repl-2", CancelReason: "no-longer-needed"},
	}
	for _, e := range append(events, events[1] /* a replay of the request event */) {
		if _, err := projection.Apply(ctx, e); err != nil {
			t.Fatalf("apply %s %s: %v", e.Kind, e.EventID, err)
		}
	}
}

func funnelDay(t *testing.T, body map[string]any, i int) map[string]any {
	t.Helper()
	days, _ := body["days"].([]any)
	if len(days) <= i {
		t.Fatalf("days = %v, want at least %d", body["days"], i+1)
	}
	d, _ := days[i].(map[string]any)
	return d
}

// The real entry point over an analytically-migrated testcontainers database:
// the documented /healthz probe answers, the funnel report answers from the
// seeded facts (dense days, site narrowing, replay-safe counters, latency
// percentiles, the documented 400), freshness reports the newest applied
// event, and a SIGTERM drains the server cleanly.
func TestReportsAnswerFromTheSeedAndDrainOnSigterm(t *testing.T) {
	url := pgtest.EmptyURL(t) // no migrations: the projector's boot step applies them
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatal(err)
	}
	seedFacts(t, url)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("ANALYTICS_DATABASE_URL", url)
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("LOG_LEVEL", "error")
	// No Collector in the test run: keep the exporters off so shutdown does not
	// wait on a final flush.
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	done := make(chan error, 1)
	go func() { done <- run() }()

	base := "http://" + addr
	waitHealthz(t, base)

	query := "?from=2026-11-03T00:00:00Z&to=2026-11-05T00:00:00Z" // two UTC days: the second must be a dense zero day
	code, body := getJSON(t, base+"/reports/replenishment-funnel"+query)
	if code != http.StatusOK || len(body["days"].([]any)) != 2 {
		t.Fatalf("funnel = %d %v (days must be dense over [from, to))", code, body)
	}
	d := funnelDay(t, body, 0)
	if d["requests_raised"] != float64(1) || d["units_requested"] != float64(24) || d["requests_completed"] != float64(1) ||
		d["units_replenished"] != float64(24) || d["requests_cancelled"] != float64(1) || d["policies_declared"] != float64(1) {
		t.Fatalf("funnel day 0 = %v (a replay of the request event must not double count)", d)
	}
	lat, _ := d["completion_latency"].(map[string]any)
	if lat["count"] != float64(1) || lat["p50_seconds"] != float64(3600) {
		t.Fatalf("completion_latency = %v, want one completion after 3600s", lat)
	}
	zero := funnelDay(t, body, 1)
	lat1, _ := zero["completion_latency"].(map[string]any)
	if zero["requests_raised"] != float64(0) || lat1["count"] != float64(0) || lat1["p50_seconds"] != nil {
		t.Fatalf("funnel day 1 = %v, want a zero day with null percentiles", zero)
	}

	// siteId narrows every counter to one site: the cancellation was SITE-2.
	_, scoped := getJSON(t, base+"/reports/replenishment-funnel"+query+"&siteId=SITE-1")
	if sd := funnelDay(t, scoped, 0); sd["requests_raised"] != float64(1) || sd["requests_cancelled"] != float64(0) {
		t.Fatalf("site-scoped funnel day = %v", sd)
	}

	// Any granularity but day is the documented 400 invalid-report-granularity.
	if code, _ := getJSON(t, base+"/reports/replenishment-funnel?granularity=hour"); code != http.StatusBadRequest {
		t.Fatalf("granularity=hour = %d, want 400", code)
	}

	// Freshness: as_of is the newest applied CloudEvents time; the lag is never negative.
	code, fresh := getJSON(t, base+"/reports/freshness")
	if code != http.StatusOK || fresh["as_of"] != "2026-11-03T10:00:00Z" {
		t.Fatalf("freshness = %d %v", code, fresh)
	}
	if lag, _ := fresh["lag_seconds"].(float64); lag < 0 {
		t.Fatalf("lag_seconds = %v, want it never negative", fresh["lag_seconds"])
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a signalled reports server must drain cleanly, got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the reports server did not stop")
	}
}

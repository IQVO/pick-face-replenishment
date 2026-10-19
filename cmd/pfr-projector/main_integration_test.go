//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"encoding/json"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func message(t *testing.T, id, entity, name, subject string, at time.Time, data map[string]any) kafkatest.Message {
	t.Helper()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: entity, EventName: name, Subject: subject, Time: at,
		Stream: cloudevents.StreamAnalytics, Version: 1, Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafkatest.Message{Key: subject, Value: value}
}

// The projector end to end on real infrastructure: analytics-stream messages go
// in through Kafka, the consumer folds them into a real analytical Postgres
// (migrated by the projector's own boot step), a poison message lands on the
// DLQ, and the funnel the reports router serves from a READ-ONLY pool shows the
// result.
func TestProjectorFoldsTheAnalyticsStreamAndTheReportServesIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	url := pgtest.EmptyURL(t) // no migrations: the projector's boot step applies them
	pool, err := openAnalyticsPool(ctx, logger, url)
	if err != nil {
		t.Fatalf("openAnalyticsPool: %v", err)
	}
	defer pool.Close()

	topic := kafkatest.Topic(t, "pfr.analytics")
	dlqTopic := topic + inboundkafka.DLQSuffix
	kafkatest.CreateTopic(t, dlqTopic)
	group := "pfr-projector-" + topic

	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	requested := message(t, "ev-1", "replenishment", "ReplenishmentRequested", "repl-1", day.Add(8*time.Hour), map[string]any{
		"request_id": "repl-1", "site_id": "SITE-1", "sku": "SKU-1", "forward_bin": "FWD-1", "source_bin": "RSV-1",
		"quantity": 24, "priority": "NORMAL", "policy_min": 10, "policy_max": 60, "requested_at": day.Add(8 * time.Hour).Format(time.RFC3339),
	})
	completed := message(t, "ev-2", "replenishment", "ReplenishmentCompleted", "repl-1", day.Add(9*time.Hour), map[string]any{
		"request_id": "repl-1", "site_id": "SITE-1", "sku": "SKU-1", "forward_bin": "FWD-1", "source_bin": "RSV-1",
		"quantity": 24, "completed_at": day.Add(9 * time.Hour).Format(time.RFC3339),
	})
	policy := message(t, "ev-3", "policy", "ReplenishmentPolicyDeclared", "SITE-1/SKU-1", day.Add(7*time.Hour), map[string]any{
		"site_id": "SITE-1", "sku": "SKU-1", "min_qty": 10, "max_qty": 60, "multiple": 6, "version": 1,
		"declared_at": day.Add(7 * time.Hour).Format(time.RFC3339),
	})
	// A known type whose payload contradicts its subject can never be projected.
	tampered := message(t, "ev-bad", "replenishment", "ReplenishmentRequested", "repl-other", day.Add(8*time.Hour), map[string]any{
		"request_id": "repl-1", "site_id": "SITE-1", "sku": "SKU-1", "quantity": 24, "priority": "NORMAL",
	})
	kafkatest.Produce(t, topic, policy, requested, tampered, completed, requested /* a replay of ev-1 */)

	consumer := inboundkafka.NewAnalyticsConsumer(kafkatest.Brokers(t), topic, group, analyticsstore.NewProjection(pool), logger)
	consumer.Retry = inboundkafka.RetryPolicy{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the consumer did not stop")
		}
		_ = consumer.Close()
	})

	// The reports binary's router over a read-only pool.
	ro, err := analyticsstore.NewReadOnlyPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	srv := httptest.NewServer(inboundhttp.NewReportsRouter(&inboundhttp.ReportsServer{Reader: analyticsstore.NewReader(ro)}))
	defer srv.Close()

	from, to := day.Format(time.RFC3339), day.Add(24*time.Hour).Format(time.RFC3339)
	deadline := time.Now().Add(60 * time.Second)
	var got map[string]any
	for time.Now().Before(deadline) {
		got = getJSON(t, srv.URL+"/reports/replenishment-funnel?from="+from+"&to="+to+"&siteId=SITE-1")
		if days, _ := got["days"].([]any); len(days) == 1 && days[0].(map[string]any)["requests_completed"] == float64(1) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	d := got["days"].([]any)[0].(map[string]any)
	if d["requests_raised"] != float64(1) || d["units_requested"] != float64(24) || d["requests_completed"] != float64(1) ||
		d["units_replenished"] != float64(24) || d["policies_declared"] != float64(1) || d["requests_cancelled"] != float64(0) {
		t.Fatalf("funnel day = %v (a replay must not double count)", d)
	}
	if lat := d["completion_latency"].(map[string]any); lat["count"] != float64(1) || lat["p50_seconds"] != float64(3600) {
		t.Fatalf("latency = %v, want one completion after 3600s", lat)
	}

	// The poison message was dead-lettered with its context, and the offsets moved past it.
	dead := kafkatest.ReadN(t, dlqTopic, 1, 30*time.Second)
	if string(dead[0].Value) != string(tampered.Value) || dead[0].Headers["x-dlq-error"] == "" {
		t.Fatalf("dlq = %+v", dead[0])
	}
	fresh := getJSON(t, srv.URL+"/reports/freshness")
	if fresh["as_of"] == nil || fresh["lag_seconds"] == nil {
		t.Fatalf("freshness after projection = %v", fresh)
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return out
}

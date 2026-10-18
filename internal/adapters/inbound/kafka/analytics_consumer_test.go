package kafka_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
)

// fakeProjection is a scripted report.Projection for the analytics consumer.
type fakeProjection struct {
	mu      sync.Mutex
	applied []report.Event
	fail    int // transient failures before success
	reject  bool
}

func (p *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reject {
		return false, errors.Join(report.ErrRejected, errors.New("integrity violation"))
	}
	if p.fail > 0 {
		p.fail--
		return false, errBoom
	}
	p.applied = append(p.applied, e)
	return true, nil
}

func (p *fakeProjection) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.applied)
}

func (p *fakeProjection) events() []report.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]report.Event(nil), p.applied...)
}

var analyticsWhen = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

// analyticsBytes builds one analytics-stream message value the way the outbox
// sink does: same envelope, the analytics dataschema.
func analyticsBytes(t *testing.T, id, entity, name, subject string, data map[string]any) []byte {
	t.Helper()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: entity, EventName: name, Subject: subject, Time: analyticsWhen,
		Stream: cloudevents.StreamAnalytics, Version: 1, Data: data,
	})
	if err != nil {
		t.Fatalf("build analytics message: %v", err)
	}
	return value
}

func requestedBytes(t *testing.T, id, request string) []byte {
	return analyticsBytes(t, id, "replenishment", "ReplenishmentRequested", request, map[string]any{
		"request_id": request, "site_id": "SITE-1", "sku": "SKU-1", "forward_bin": "FWD-1", "source_bin": "RSV-1",
		"quantity": 24, "priority": "NORMAL", "policy_min": 10, "policy_max": 60, "requested_at": "2026-10-17T09:30:00Z",
	})
}

func completedBytes(t *testing.T, id, request string) []byte {
	return analyticsBytes(t, id, "replenishment", "ReplenishmentCompleted", request, map[string]any{
		"request_id": request, "site_id": "SITE-1", "sku": "SKU-1", "forward_bin": "FWD-1", "source_bin": "RSV-1",
		"quantity": 24, "completed_at": "2026-10-17T09:50:00Z",
	})
}

func cancelledBytes(t *testing.T, id, request string) []byte {
	return analyticsBytes(t, id, "replenishment", "ReplenishmentCancelled", request, map[string]any{
		"request_id": request, "site_id": "SITE-1", "sku": "SKU-1", "reason": "OPERATOR", "cancelled_at": "2026-10-17T09:50:00Z",
	})
}

func policyBytes(t *testing.T, id string) []byte {
	return analyticsBytes(t, id, "policy", "ReplenishmentPolicyDeclared", "SITE-1/SKU-1", map[string]any{
		"site_id": "SITE-1", "sku": "SKU-1", "min_qty": 10, "max_qty": 60, "multiple": 6, "version": 2, "declared_at": "2026-10-17T09:00:00Z",
	})
}

// newAnalyticsConsumer wires a consumer over the scripted reader and DLQ.
func newAnalyticsConsumer(p *fakeProjection, dlq kafkaconsumer.DeadLetterWriter, reader *fakeReader) *kafkaconsumer.AnalyticsConsumer {
	return &kafkaconsumer.AnalyticsConsumer{
		Reader: reader, Projection: p, DLQ: dlq, DLQTopic: "warehouse.pick-face-replenishment.analytics.dlq",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Retry: fastRetry,
	}
}

func TestAnalyticsConsumerAppliesTheFourEventKinds(t *testing.T) {
	ctx := context.Background()
	p := &fakeProjection{}
	c := newAnalyticsConsumer(p, &fakeDLQWriter{}, newFakeReader())
	for _, value := range [][]byte{
		requestedBytes(t, "e1", "repl-1"), completedBytes(t, "e2", "repl-1"), cancelledBytes(t, "e3", "repl-2"), policyBytes(t, "e4"),
	} {
		if err := c.HandleMessage(ctx, kafkago.Message{Value: value}); err != nil {
			t.Fatalf("err = %v", err)
		}
	}
	want := []report.Event{
		{Kind: report.KindReplenishmentRequested, EventID: "e1", At: analyticsWhen, SiteID: "SITE-1", SKU: "SKU-1",
			RequestID: "repl-1", Quantity: 24, Priority: "NORMAL"},
		{Kind: report.KindReplenishmentCompleted, EventID: "e2", At: analyticsWhen, SiteID: "SITE-1", SKU: "SKU-1",
			RequestID: "repl-1", Quantity: 24},
		{Kind: report.KindReplenishmentCancelled, EventID: "e3", At: analyticsWhen, SiteID: "SITE-1", SKU: "SKU-1",
			RequestID: "repl-2", CancelReason: "OPERATOR"},
		{Kind: report.KindReplenishmentPolicyDeclared, EventID: "e4", At: analyticsWhen, SiteID: "SITE-1", SKU: "SKU-1",
			Version: 2, MinQty: 10, MaxQty: 60, Multiple: 6},
	}
	if got := p.events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %+v\nwant     %+v", got, want)
	}
}

func TestAnalyticsConsumerSkipsWhatItCannotRoute(t *testing.T) {
	ctx := context.Background()

	// A non-CloudEvents message is skipped, not retried.
	p := &fakeProjection{}
	dlq := &fakeDLQWriter{}
	c := newAnalyticsConsumer(p, dlq, newFakeReader())
	if err := c.HandleMessage(ctx, kafkago.Message{Value: []byte(`{"event_type":"flat"}`)}); err != nil {
		t.Fatalf("err = %v, want nil (skipped)", err)
	}
	if p.count() != 0 || len(dlq.published()) != 0 {
		t.Fatalf("applied = %d, dead-lettered = %d; want none", p.count(), len(dlq.published()))
	}

	// An event of another type is ignored: a CONSUMED type (inventory-storage's)
	// is a valid CloudEvent of an unknown kind on this topic.
	p2 := &fakeProjection{}
	c2 := newAnalyticsConsumer(p2, &fakeDLQWriter{}, newFakeReader())
	foreign := `{"specversion":"1.0","id":"x","source":"/warehouse/inventory-storage",` +
		`"type":"com.warehouse.wes.inventory-storage.stock.BinStockLevelChanged",` +
		`"subject":"bin:sku","time":"2026-10-17T09:30:00Z","datacontenttype":"application/json",` +
		`"dataschema":"urn:warehouse:inventory-storage:events:BinStockLevelChanged:v1","data":{}}`
	if err := c2.HandleMessage(ctx, kafkago.Message{Value: []byte(foreign)}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if p2.count() != 0 {
		t.Fatalf("applied = %d, want 0", p2.count())
	}
}

func TestAnalyticsConsumerDeadLettersDeterministicFailures(t *testing.T) {
	ctx := context.Background()

	// A payload whose request id contradicts the subject is dead-lettered.
	p := &fakeProjection{}
	dlq := &fakeDLQWriter{}
	c := newAnalyticsConsumer(p, dlq, newFakeReader())
	msg := kafkago.Message{Partition: 3, Offset: 7, Value: tamperSubject(t, "repl-1", "repl-other")}
	if err := c.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("err = %v, want nil (dead-lettered)", err)
	}
	if p.count() != 0 {
		t.Fatalf("applied = %d, want 0", p.count())
	}
	published := dlq.published()
	if len(published) != 1 || published[0].Topic != c.DLQTopic || string(published[0].Value) != string(msg.Value) {
		t.Fatalf("dead-lettered = %+v", published)
	}
	if headerOf(published[0], "x-dlq-error") == "" || headerOf(published[0], "x-dlq-failed-at") == "" {
		t.Fatal("the dead-lettered message must carry x-dlq-error and x-dlq-failed-at")
	}

	// A payload that fails validation (a zero quantity) is dead-lettered.
	dlqInvalid := &fakeDLQWriter{}
	ci := newAnalyticsConsumer(&fakeProjection{}, dlqInvalid, newFakeReader())
	invalid := analyticsBytes(t, "e-bad", "replenishment", "ReplenishmentRequested", "repl-1", map[string]any{
		"request_id": "repl-1", "site_id": "SITE-1", "sku": "SKU-1", "quantity": 0, "priority": "NORMAL",
	})
	if err := ci.HandleMessage(ctx, kafkago.Message{Value: invalid}); err != nil || len(dlqInvalid.published()) != 1 {
		t.Fatalf("invalid payload: err = %v, dead-lettered = %d", err, len(dlqInvalid.published()))
	}

	// A store rejection is dead-lettered at once.
	p2 := &fakeProjection{reject: true}
	dlq2 := &fakeDLQWriter{}
	c2 := newAnalyticsConsumer(p2, dlq2, newFakeReader())
	if err := c2.HandleMessage(ctx, kafkago.Message{Value: requestedBytes(t, "e1", "repl-1")}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(dlq2.published()) != 1 {
		t.Fatalf("dead-lettered = %d, want 1", len(dlq2.published()))
	}
}

func TestAnalyticsConsumerRetriesTransientFailures(t *testing.T) {
	ctx := context.Background()
	p := &fakeProjection{fail: 1}
	dlq := &fakeDLQWriter{}
	c := newAnalyticsConsumer(p, dlq, newFakeReader())
	err := c.HandleMessage(ctx, kafkago.Message{Value: requestedBytes(t, "e1", "repl-1")})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the injected transient error", err)
	}
	if len(dlq.published()) != 0 {
		t.Fatalf("dead-lettered = %d, want 0", len(dlq.published()))
	}

	// A failed DLQ write is a transient error too (the poison is retried, never lost).
	p2 := &fakeProjection{reject: true}
	c2 := newAnalyticsConsumer(p2, &failingDLQWriter{}, newFakeReader())
	err = c2.HandleMessage(ctx, kafkago.Message{Value: requestedBytes(t, "e1", "repl-1")})
	if err == nil || errors.Is(err, report.ErrRejected) {
		t.Fatalf("err = %v, want the DLQ write failure", err)
	}
}

func headerOf(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// tamperSubject re-encodes a valid analytics message with the payload's
// request_id pointing at a different request than the envelope subject.
func tamperSubject(t *testing.T, payloadRequest, subjectRequest string) []byte {
	t.Helper()
	value := string(requestedBytes(t, "e-tamper", payloadRequest))
	old := `"subject":"` + payloadRequest + `"`
	if !strings.Contains(value, old) {
		t.Fatalf("no subject to tamper with in %s", value)
	}
	return []byte(strings.Replace(value, old, `"subject":"`+subjectRequest+`"`, 1))
}

// failingDLQWriter always fails, like an unreachable DLQ topic.
type failingDLQWriter struct{}

func (failingDLQWriter) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("dlq unreachable")
}
func (failingDLQWriter) Close() error { return nil }

// TestAnalyticsConsumerRunCommitsPastPoisonAndRetriesTransient: through the
// real run loop, a poison message is dead-lettered and its offset committed; a
// message whose projection fails transiently is retried until it succeeds and
// only then committed.
func TestAnalyticsConsumerRunCommitsPastPoisonAndRetriesTransient(t *testing.T) {
	poison := tamperSubject(t, "repl-1", "repl-other")
	transient := requestedBytes(t, "e2", "repl-2")
	reader := newFakeReader(poison, transient)
	// The first Apply of the SECOND message fails once; the poison never
	// reaches Apply (it fails validation).
	p := &fakeProjection{fail: 1}
	dlq := &fakeDLQWriter{}
	c := newAnalyticsConsumer(p, dlq, reader)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(10 * time.Second)
	for reader.commits() < 2 {
		select {
		case err := <-done:
			t.Fatalf("Run returned early: %v", err)
		case <-deadline:
			t.Fatalf("timed out; events = %v, applied = %d, dlq = %d", reader.log(), p.count(), len(dlq.published()))
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if p.count() != 1 {
		t.Fatalf("applied = %d, want only the healthy message", p.count())
	}
	if len(dlq.published()) != 1 || dlq.published()[0].Topic != c.DLQTopic {
		t.Fatalf("dead-lettered = %+v, want the poison message once", dlq.published())
	}
	if got := reader.log(); got[len(got)-1] != "commit:1" {
		t.Fatalf("events = %v; the last commit must be the retried message", got)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

// Every skipped message returns nil (committed past); the sampler's own
// contract is pinned by the skipSampler test in analytics_consumer_internal_test.go.
func TestAnalyticsConsumerSkipsNeverFail(t *testing.T) {
	p := &fakeProjection{}
	c := newAnalyticsConsumer(p, &fakeDLQWriter{}, newFakeReader())
	c.Now = func() time.Time { return analyticsWhen }
	for range 3 {
		if err := c.HandleMessage(context.Background(), kafkago.Message{Value: []byte("nope")}); err != nil {
			t.Fatalf("a skipped message returned %v, want nil", err)
		}
	}
	if p.count() != 0 {
		t.Fatalf("applied = %d, want 0", p.count())
	}
}

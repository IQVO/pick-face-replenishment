package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/events"
	"github.com/claudioed/pick-face-replenishment/internal/application/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

const (
	requestID = "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07"
	typePfx   = "com.warehouse.wms.pick-face-replenishment."
)

// capture is a fake outbox: it records every Insert call.
type capture struct {
	calls [][]outbox.Message
	err   error
}

func (c *capture) Insert(_ context.Context, msgs ...outbox.Message) error {
	if c.err != nil {
		return c.err
	}
	c.calls = append(c.calls, msgs)
	return nil
}

func (c *capture) rows() []outbox.Message {
	var all []outbox.Message
	for _, call := range c.calls {
		all = append(all, call...)
	}
	return all
}

func sinkWithID(out events.Inserter, ids ...string) *events.Sink {
	next := 0
	s := events.NewSink(out)
	s.NewID = func() string {
		id := ids[next%len(ids)]
		next++
		return id
	}
	return s
}

func at(h, m int) time.Time { return time.Date(2026, 10, 17, h, m, 0, 0, time.UTC) }

func requested() replenishment.ReplenishmentRequested {
	return replenishment.ReplenishmentRequested{
		Header: replenishment.Header{Request: requestID, Site: "WH1", Version: 1, At: at(9, 30)},
		SKU:    "SKU-1", ForwardBin: "WH1-FWD-A01-01-01-A", SourceBin: "WH1-RSV-A07-03-02-B",
		Quantity: 24, Priority: replenishment.PriorityNormal, PolicyMin: 10, PolicyMax: 60,
	}
}

func completed() replenishment.ReplenishmentCompleted {
	return replenishment.ReplenishmentCompleted{
		Header: replenishment.Header{Request: requestID, Site: "WH1", Version: 2, At: at(10, 5)},
		SKU:    "SKU-1", ForwardBin: "WH1-FWD-A01-01-01-A", SourceBin: "WH1-RSV-A07-03-02-B", Quantity: 18,
	}
}

func cancelled() replenishment.ReplenishmentCancelled {
	return replenishment.ReplenishmentCancelled{
		Header: replenishment.Header{Request: requestID, Site: "WH1", Version: 2, At: at(9, 50)},
		SKU:    "SKU-1", Reason: replenishment.ReasonAssignmentChanged,
	}
}

func declaration() policy.Declaration {
	return policy.Declaration{SiteID: "WH1", SKU: "SKU-1", MinQty: 10, MaxQty: 60, Multiple: 6, Version: 1, DeclaredAt: at(9, 0)}
}

// goldenRow is what one outbox row must be, byte for byte on the JSON side.
type goldenRow struct {
	topic, schema, golden string
}

type goldenCase struct {
	name      string
	id        string
	eventType string
	subject   string
	publish   func(*events.Sink) error
	rows      []goldenRow
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			name: "ReplenishmentRequested", id: "6f1d2c3b-4a5e-4b7c-8d9e-0a1b2c3d4e5f",
			eventType: typePfx + "replenishment.ReplenishmentRequested", subject: requestID,
			publish: func(s *events.Sink) error {
				return s.PublishRequestEvents(context.Background(), requested())
			},
			rows: []goldenRow{
				{events.TopicEvents, "urn:warehouse:pick-face-replenishment:events:ReplenishmentRequested:v1", "requested.events.golden.json"},
				{events.TopicAnalytics, "urn:warehouse:pick-face-replenishment:analytics:ReplenishmentRequested:v1", "requested.analytics.golden.json"},
			},
		},
		{
			name: "ReplenishmentCompleted", id: "0b9a8c7d-6e5f-4a3b-9c2d-1e0f9a8b7c6d",
			eventType: typePfx + "replenishment.ReplenishmentCompleted", subject: requestID,
			publish: func(s *events.Sink) error {
				return s.PublishRequestEvents(context.Background(), completed())
			},
			rows: []goldenRow{
				{events.TopicEvents, "urn:warehouse:pick-face-replenishment:events:ReplenishmentCompleted:v1", "completed.events.golden.json"},
				{events.TopicAnalytics, "urn:warehouse:pick-face-replenishment:analytics:ReplenishmentCompleted:v1", "completed.analytics.golden.json"},
			},
		},
		{
			name: "ReplenishmentCancelled", id: "4d3c2b1a-0f9e-4d8c-8b7a-6f5e4d3c2b1a",
			eventType: typePfx + "replenishment.ReplenishmentCancelled", subject: requestID,
			publish: func(s *events.Sink) error {
				return s.PublishRequestEvents(context.Background(), cancelled())
			},
			rows: []goldenRow{
				{events.TopicEvents, "urn:warehouse:pick-face-replenishment:events:ReplenishmentCancelled:v1", "cancelled.events.golden.json"},
				{events.TopicAnalytics, "urn:warehouse:pick-face-replenishment:analytics:ReplenishmentCancelled:v1", "cancelled.analytics.golden.json"},
			},
		},
		{
			name: "ReplenishmentPolicyDeclared", id: "2b1a0f9e-8d7c-4b6a-a594-837261504f3e",
			eventType: typePfx + "policy.ReplenishmentPolicyDeclared", subject: "WH1/SKU-1",
			publish: func(s *events.Sink) error {
				return s.PublishPolicyDeclared(context.Background(), declaration())
			},
			rows: []goldenRow{
				{events.TopicAnalytics, "urn:warehouse:pick-face-replenishment:analytics:ReplenishmentPolicyDeclared:v1", "policy_declared.analytics.golden.json"},
			},
		},
	}
}

func readGolden(t *testing.T, name string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return decode(t, name, raw)
}

func decode(t *testing.T, what string, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", what, err, raw)
	}
	return v
}

// assertRow checks one outbox row against its golden file: every CloudEvents
// attribute and the whole data object (exact JSON), plus the row columns the
// relay publishes with (topic, key, type, subject, dataschema, headers).
func assertRow(t *testing.T, c goldenCase, got outbox.Message, want goldenRow) {
	t.Helper()
	if got.Topic != want.topic {
		t.Errorf("topic = %q, want %q", got.Topic, want.topic)
	}
	if got.EventID != c.id || got.EventType != c.eventType || got.Subject != c.subject || got.DataSchema != want.schema {
		t.Errorf("row = id %q type %q subject %q schema %q", got.EventID, got.EventType, got.Subject, got.DataSchema)
	}
	if string(got.Key) != c.subject {
		t.Errorf("key = %q, want %q", got.Key, c.subject)
	}
	wantHeaders := []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}}
	if !reflect.DeepEqual(got.Headers, wantHeaders) {
		t.Errorf("headers = %+v, want %+v", got.Headers, wantHeaders)
	}
	if gotJSON, wantJSON := decode(t, "value", got.Value), readGolden(t, want.golden); !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Errorf("%s: value differs from the golden\n got: %s\nwant: %v", want.golden, got.Value, wantJSON)
	}
}

func TestSink_GoldenJSONPerType(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			out := &capture{}
			if err := c.publish(sinkWithID(out, c.id)); err != nil {
				t.Fatalf("publish: %v", err)
			}
			if len(out.calls) != 1 {
				t.Fatalf("Insert called %d times, want once (one atomic write)", len(out.calls))
			}
			rows := out.rows()
			if len(rows) != len(c.rows) {
				t.Fatalf("%d rows, want %d", len(rows), len(c.rows))
			}
			for i, want := range c.rows {
				assertRow(t, c, rows[i], want)
			}
		})
	}
}

// Every written value is a valid CloudEvents 1.0 event the repo's own decoder
// accepts, with the service source and the application/json data type.
func TestSink_RowsDecodeAsCloudEvents(t *testing.T) {
	for _, c := range goldenCases() {
		out := &capture{}
		if err := c.publish(sinkWithID(out, c.id)); err != nil {
			t.Fatalf("%s: publish: %v", c.name, err)
		}
		for _, row := range out.rows() {
			e, err := cloudevents.Decode(row.Value)
			if err != nil {
				t.Fatalf("%s: decode: %v", c.name, err)
			}
			if e.Source() != "/warehouse/pick-face-replenishment" || e.DataContentType() != "application/json" || e.ID() != c.id {
				t.Errorf("%s: source %q datacontenttype %q id %q", c.name, e.Source(), e.DataContentType(), e.ID())
			}
		}
	}
}

// The integration row and its analytics copy of one occurrence share the
// CloudEvents id; two occurrences never do.
func TestSink_OneIDPerOccurrenceSharedByBothTopics(t *testing.T) {
	out := &capture{}
	if err := sinkWithID(out, "id-1", "id-2").PublishRequestEvents(context.Background(), requested(), completed()); err != nil {
		t.Fatal(err)
	}
	rows := out.rows()
	if len(rows) != 4 {
		t.Fatalf("%d rows, want 4 (two events x two topics)", len(rows))
	}
	for i, want := range []struct{ id, topic string }{
		{"id-1", events.TopicEvents}, {"id-1", events.TopicAnalytics},
		{"id-2", events.TopicEvents}, {"id-2", events.TopicAnalytics},
	} {
		if rows[i].EventID != want.id || rows[i].Topic != want.topic {
			t.Errorf("row %d = id %q topic %q, want id %q topic %q", i, rows[i].EventID, rows[i].Topic, want.id, want.topic)
		}
	}
	if len(out.calls) != 1 {
		t.Errorf("Insert called %d times, want once", len(out.calls))
	}
}

// ReplenishmentPolicyDeclared is analytics-only: a single row, on the
// analytics topic, never on the integration topic.
func TestSink_PolicyDeclaredIsAnalyticsOnly(t *testing.T) {
	out := &capture{}
	if err := sinkWithID(out, "p-1").PublishPolicyDeclared(context.Background(), declaration()); err != nil {
		t.Fatal(err)
	}
	rows := out.rows()
	if len(rows) != 1 || rows[0].Topic != events.TopicAnalytics {
		t.Fatalf("rows = %+v, want exactly one on %s", rows, events.TopicAnalytics)
	}
}

func TestSink_TopicOverridesAndDefaults(t *testing.T) {
	out := &capture{}
	s := events.NewSink(out)
	s.EventsTopic, s.AnalyticsTopic = "t.events", "t.analytics"
	if err := s.PublishRequestEvents(context.Background(), cancelled()); err != nil {
		t.Fatal(err)
	}
	rows := out.rows()
	if len(rows) != 2 || rows[0].Topic != "t.events" || rows[1].Topic != "t.analytics" {
		t.Fatalf("rows = %+v", rows)
	}
	if events.TopicEvents != "warehouse.pick-face-replenishment.events" || events.TopicAnalytics != "warehouse.pick-face-replenishment.analytics" {
		t.Fatalf("topics = %q, %q", events.TopicEvents, events.TopicAnalytics)
	}
}

// A zero-NewID sink and NewSink both mint random UUIDs, a fresh one per call.
func TestSink_MintsUUIDsWhenNewIDIsNil(t *testing.T) {
	out := &capture{}
	s := &events.Sink{Outbox: out}
	for range 2 {
		if err := s.PublishRequestEvents(context.Background(), completed()); err != nil {
			t.Fatal(err)
		}
	}
	rows := out.rows()
	if len(rows) != 4 || rows[0].EventID == "" || rows[0].EventID == rows[2].EventID {
		t.Fatalf("ids = %q, %q", rows[0].EventID, rows[2].EventID)
	}
	if len(rows[0].EventID) != 36 {
		t.Errorf("id %q is not a UUID", rows[0].EventID)
	}
}

type bogusEvent struct{ replenishment.Header }

func (bogusEvent) EventName() string { return "Bogus" }

// An event the service does not publish is a programming error: the call
// fails and NOTHING (not even the events before it) is written.
func TestSink_UnknownEventFailsWholeCall(t *testing.T) {
	out := &capture{}
	err := sinkWithID(out, "x").PublishRequestEvents(context.Background(), requested(), bogusEvent{})
	if err == nil {
		t.Fatal("want an error for an unpublished event type")
	}
	if len(out.calls) != 0 {
		t.Fatalf("Insert was called despite the error: %+v", out.calls)
	}
}

// The envelope refuses an empty subject: a request event without a request id
// cannot be encoded.
func TestSink_EncodeErrorsAreReturnedBeforeAnyWrite(t *testing.T) {
	out := &capture{}
	s := sinkWithID(out, "x")
	ev := requested()
	ev.Request = ""
	if err := s.PublishRequestEvents(context.Background(), ev); err == nil {
		t.Fatal("want an error for an empty request id")
	}
	if len(out.calls) != 0 {
		t.Fatalf("Insert was called despite the encode error: %+v", out.calls)
	}
}

func TestSink_OutboxErrorsAreWrapped(t *testing.T) {
	boom := errors.New("boom")
	out := &capture{err: boom}
	s := sinkWithID(out, "x")
	if err := s.PublishRequestEvents(context.Background(), requested()); !errors.Is(err, boom) {
		t.Errorf("PublishRequestEvents err = %v, want it to wrap boom", err)
	}
	if err := s.PublishPolicyDeclared(context.Background(), declaration()); !errors.Is(err, boom) {
		t.Errorf("PublishPolicyDeclared err = %v, want it to wrap boom", err)
	}
}

// A request event list may be empty: nothing is encoded and the outbox still
// sees a no-op Insert.
func TestSink_EmptyEventListWritesNothing(t *testing.T) {
	out := &capture{}
	if err := sinkWithID(out, "x").PublishRequestEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(out.rows()) != 0 {
		t.Fatalf("rows = %+v", out.rows())
	}
}

// The policy event's subject and Kafka key are `<site_id>/<sku>`.
func TestSink_PolicySubjectIsSiteSlashSKU(t *testing.T) {
	out := &capture{}
	d := declaration()
	d.SiteID, d.SKU = "WH2", "SKU-9"
	if err := sinkWithID(out, "x").PublishPolicyDeclared(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	row := out.rows()[0]
	if row.Subject != "WH2/SKU-9" || string(row.Key) != "WH2/SKU-9" {
		t.Fatalf("subject %q key %q", row.Subject, row.Key)
	}
}

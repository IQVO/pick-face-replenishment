// Package events is the outbound event publisher: it maps the facts the
// application raises (replenishment request events and the analytics-only
// policy declaration) to CloudEvents 1.0 outbox messages and hands them to the
// transactional outbox (ADR 0004). Envelopes are built ONLY through
// internal/adapters/kafka/cloudevents; payloads are exactly the ones pinned in
// apis/asyncapi.yaml.
package events

import (
	"fmt"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/pick-face-replenishment/internal/application/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// The two topics this service publishes to. The outbox relay sets
// Message.Topic per row; the Kafka writer carries no Topic.
const (
	// TopicEvents is the integration topic.
	TopicEvents = "warehouse.pick-face-replenishment.events"
	// TopicAnalytics is the analytics topic: the three request events again,
	// plus the analytics-only policy declaration.
	TopicAnalytics = "warehouse.pick-face-replenishment.analytics"
)

// The `<entity>` segment of every published type:
// com.warehouse.wms.pick-face-replenishment.<entity>.<EventName>.
const (
	// EntityReplenishment is the segment of the request events.
	EntityReplenishment = "replenishment"
	// EntityPolicy is the segment of the policy event.
	EntityPolicy = "policy"
)

// policyDeclaredName is the CloudEvents event name of the analytics-only
// policy fact (the policy package has no event type of its own).
const policyDeclaredName = "ReplenishmentPolicyDeclared"

// schemaVersion is the dataschema version of every payload below
// (urn:warehouse:pick-face-replenishment:<events|analytics>:<EventName>:v1).
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, snake_case JSON). Timestamps are RFC 3339
// UTC.

type requestedData struct {
	RequestID   string `json:"request_id"`
	SiteID      string `json:"site_id"`
	SKU         string `json:"sku"`
	ForwardBin  string `json:"forward_bin"`
	SourceBin   string `json:"source_bin"`
	Quantity    int64  `json:"quantity"`
	Priority    string `json:"priority"`
	PolicyMin   int64  `json:"policy_min"`
	PolicyMax   int64  `json:"policy_max"`
	RequestedAt string `json:"requested_at"`
}

type completedData struct {
	RequestID   string `json:"request_id"`
	SiteID      string `json:"site_id"`
	SKU         string `json:"sku"`
	ForwardBin  string `json:"forward_bin"`
	SourceBin   string `json:"source_bin"`
	Quantity    int64  `json:"quantity"`
	CompletedAt string `json:"completed_at"`
}

type cancelledData struct {
	RequestID   string `json:"request_id"`
	SiteID      string `json:"site_id"`
	SKU         string `json:"sku"`
	Reason      string `json:"reason"`
	CancelledAt string `json:"cancelled_at"`
}

type policyDeclaredData struct {
	SiteID   string `json:"site_id"`
	SKU      string `json:"sku"`
	MinQty   int64  `json:"min_qty"`
	MaxQty   int64  `json:"max_qty"`
	Multiple int64  `json:"multiple"`
	// MultipleSource is DECLARED or PACK_DERIVED; absent on a fact that does not say.
	MultipleSource string `json:"multiple_source,omitempty"`
	Version        int64  `json:"version"`
	DeclaredAt     string `json:"declared_at"`
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// fact is one thing to publish: everything the envelope needs, independent of
// the domain type it came from.
type fact struct {
	entity  string
	name    string
	subject string
	at      time.Time
	data    any
	// analyticsOnly facts are written to the analytics topic only.
	analyticsOnly bool
}

// requestFact maps a request event to its fact. An event type the service does
// not publish is a programming error.
func requestFact(ev replenishment.Event) (fact, error) {
	f := fact{entity: EntityReplenishment, name: ev.EventName(), subject: ev.RequestID(), at: ev.OccurredAt()}
	if e, ok := ev.(replenishment.ReplenishmentRequested); ok {
		f.data = requestedPayload(e)
		return f, nil
	}
	if e, ok := ev.(replenishment.ReplenishmentCompleted); ok {
		f.data = completedPayload(e)
		return f, nil
	}
	if e, ok := ev.(replenishment.ReplenishmentCancelled); ok {
		f.data = cancelledPayload(e)
		return f, nil
	}
	return fact{}, fmt.Errorf("events: %s is not a published event", ev.EventName())
}

func requestedPayload(e replenishment.ReplenishmentRequested) requestedData {
	return requestedData{
		RequestID: e.Request, SiteID: e.Site, SKU: e.SKU, ForwardBin: e.ForwardBin, SourceBin: e.SourceBin,
		Quantity: e.Quantity, Priority: string(e.Priority), PolicyMin: e.PolicyMin, PolicyMax: e.PolicyMax,
		RequestedAt: ts(e.At),
	}
}

func completedPayload(e replenishment.ReplenishmentCompleted) completedData {
	return completedData{
		RequestID: e.Request, SiteID: e.Site, SKU: e.SKU, ForwardBin: e.ForwardBin, SourceBin: e.SourceBin,
		Quantity: e.Quantity, CompletedAt: ts(e.At),
	}
}

func cancelledPayload(e replenishment.ReplenishmentCancelled) cancelledData {
	return cancelledData{
		RequestID: e.Request, SiteID: e.Site, SKU: e.SKU, Reason: string(e.Reason), CancelledAt: ts(e.At),
	}
}

// policyFact maps the policy declaration to its analytics-only fact. Subject
// and key are `<site_id>/<sku>`.
func policyFact(d policy.Declaration) fact {
	return fact{
		entity: EntityPolicy, name: policyDeclaredName, subject: d.SiteID + "/" + d.SKU, at: d.DeclaredAt,
		analyticsOnly: true,
		data: policyDeclaredData{
			SiteID: d.SiteID, SKU: d.SKU, MinQty: d.MinQty, MaxQty: d.MaxQty, Multiple: d.Multiple,
			MultipleSource: string(d.Source), Version: d.Version, DeclaredAt: ts(d.DeclaredAt),
		},
	}
}

// build encodes f as one outbox message per destination topic. The CloudEvents
// `id` is minted ONCE per occurrence and shared by both messages, so the
// analytics copy has the same id as the integration one and a relay retry
// republishes the same bytes.
func (s *Sink) build(f fact, id string) ([]outbox.Message, error) {
	streams := []stream{{cloudevents.StreamEvents, s.eventsTopic()}, {cloudevents.StreamAnalytics, s.analyticsTopic()}}
	if f.analyticsOnly {
		streams = streams[1:]
	}
	out := make([]outbox.Message, 0, len(streams))
	for _, st := range streams {
		msg, err := message(f, id, st)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// stream pairs a CloudEvents stream (dataschema segment) with its topic.
type stream struct {
	name  string
	topic string
}

func message(f fact, id string, st stream) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID:        id,
		Entity:    f.entity,
		EventName: f.name,
		Subject:   f.subject,
		Time:      f.at,
		Stream:    st.name,
		Version:   schemaVersion,
		Data:      f.data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", f.name, err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    id,
		Topic:      st.topic,
		EventType:  cloudevents.Type(f.entity, f.name),
		Subject:    f.subject,
		Key:        []byte(f.subject),
		DataSchema: cloudevents.DataSchema(st.name, f.name, schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

package kafka_test

// Shared support for the event consumer contract tests (E1-E4), which
// validate this repo's five inbound Kafka edges against PINNED copies of the
// producing side's AsyncAPI spec (contracts/pinned.json; the
// pinned-contracts CI job fails when a copy drifts from the producer's
// develop). The pinned copies are byte-for-byte producer files: never
// edit them — refresh = copy the producer's file over the pin.
//
// Fleet convention (channel-perspective): a producer's OWN topics sit under
// 'subscribe:', so reading a pinned producer spec, the 'subscribe:' channel
// keyed by the topic this repo's reader config subscribes to is exactly the
// producer's contract for that topic.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	cevent "github.com/cloudevents/sdk-go/v2/event"
	"gopkg.in/yaml.v3"

	cloudevents "github.com/claudioed/pick-face-replenishment/internal/adapters/kafka/cloudevents"
)

// The pinned spec copies next to this consuming adapter (see
// contracts/pinned.json for the producer repos and refs they must equal).
const (
	pinnedFacilitySpec      = "testdata/facility-layout.asyncapi.yaml"
	pinnedInventorySpec     = "testdata/inventory-storage.asyncapi.yaml"
	pinnedProductMasterSpec = "testdata/product-master.asyncapi.yaml"
	pinnedSlottingSpec      = "testdata/slotting-optimization.asyncapi.yaml"
	pinnedOwnSpec           = "testdata/pick-face-replenishment.asyncapi.yaml"
)

// ownAnalyticsTopic is the topic the projector's reader config subscribes to
// (cmd/pfr-projector wires events.TopicAnalytics into NewAnalyticsConsumer);
// ownEventsTopic is this repo's integration topic, where the same-named
// messages document the CloudEvents `type` each analytics twin reuses. Both
// mirrored here as literals, the way analytics_consumer.go mirrors the
// outbound constants: inbound adapters never import outbound ones.
const (
	ownAnalyticsTopic = "warehouse.pick-face-replenishment.analytics"
	ownEventsTopic    = "warehouse.pick-face-replenishment.events"
)

// pinnedAsyncAPI is the minimal slice of AsyncAPI 2.6 the contract tests
// read: channels (with their operations and descriptions), and the
// components/messages + components/schemas the channel message $refs point at.
type pinnedAsyncAPI struct {
	AsyncAPI           string                   `yaml:"asyncapi"`
	DefaultContentType string                   `yaml:"defaultContentType"`
	Channels           map[string]pinnedChannel `yaml:"channels"`
	Components         pinnedComponents         `yaml:"components"`
}

type pinnedChannel struct {
	Description string           `yaml:"description"`
	Subscribe   *pinnedOperation `yaml:"subscribe"`
	Publish     *pinnedOperation `yaml:"publish"`
}

type pinnedOperation struct {
	OperationID string            `yaml:"operationId"`
	Message     *pinnedMessageRef `yaml:"message"`
}

// pinnedMessageRef is either a single $ref or a oneOf list of $refs.
type pinnedMessageRef struct {
	Ref   string             `yaml:"$ref"`
	OneOf []pinnedMessageRef `yaml:"oneOf"`
}

type pinnedComponents struct {
	Messages map[string]pinnedMessage `yaml:"messages"`
	Schemas  map[string]any           `yaml:"schemas"`
}

type pinnedMessage struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	ContentType string          `yaml:"contentType"`
	Payload     any             `yaml:"payload"`
	Examples    []pinnedExample `yaml:"examples"`
}

// pinnedExample is a message example: a full wire payload (a structured
// CloudEvents JSON document, as YAML) plus optional Kafka headers.
type pinnedExample struct {
	Name    string            `yaml:"name"`
	Headers map[string]string `yaml:"headers"`
	Payload map[string]any    `yaml:"payload"`
}

// loadPinnedAsyncAPI reads and parses one pinned spec copy.
func loadPinnedAsyncAPI(t *testing.T, path string) *pinnedAsyncAPI {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pinned spec %s (copy it from the producer, never hand-write it): %v", path, err)
	}
	var doc pinnedAsyncAPI
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse pinned spec %s: %v", path, err)
	}
	return &doc
}

// subscribeMessageRefs returns the components/messages names a topic's
// 'subscribe:' operation documents (a producer's own topics live under
// 'subscribe:' — see the fleet convention note above).
func (d *pinnedAsyncAPI) subscribeMessageRefs(t *testing.T, topic string) []string {
	t.Helper()
	ch, ok := d.Channels[topic]
	if !ok {
		t.Fatalf("pinned spec has no channel for topic %q", topic)
	}
	if ch.Subscribe == nil {
		t.Fatalf("pinned spec documents topic %q without a 'subscribe:' operation", topic)
	}
	if ch.Subscribe.Message == nil {
		t.Fatalf("pinned spec's subscribe operation on %q carries no message", topic)
	}
	var refs []pinnedMessageRef
	if ch.Subscribe.Message.Ref != "" {
		refs = append(refs, *ch.Subscribe.Message)
	}
	refs = append(refs, ch.Subscribe.Message.OneOf...)
	if len(refs) == 0 {
		t.Fatalf("pinned spec's subscribe operation on %q lists no messages", topic)
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, refName(t, r.Ref))
	}
	return names
}

// message resolves one components/messages entry, failing on a dangling
// $ref (a spec whose channel points at a message it does not define).
func (d *pinnedAsyncAPI) message(t *testing.T, name string) pinnedMessage {
	t.Helper()
	msg, ok := d.Components.Messages[name]
	if !ok {
		t.Fatalf("pinned spec references undefined components/messages/%s", name)
	}
	return msg
}

// documentedTypes maps every CloudEvents type the channel's messages
// document to their components/messages name ("" names are skipped).
func (d *pinnedAsyncAPI) documentedTypes(t *testing.T, topic string) map[string]string {
	t.Helper()
	documented := map[string]string{}
	for _, name := range d.subscribeMessageRefs(t, topic) {
		if typ := d.documentedType(t, name); typ != "" {
			documented[typ] = name
		}
	}
	return documented
}

// documentedType is the CloudEvents `type` one message pins: a const in its
// payload schema, or the `type = `...“ its description states (specs whose
// shared envelope schema leaves `type` free document it there).
func (d *pinnedAsyncAPI) documentedType(t *testing.T, name string) string {
	t.Helper()
	msg := d.message(t, name)
	if c := d.payloadConsts(t, msg.Payload)["type"]; c != "" {
		return c
	}
	return typeFromDescription(msg.Description)
}

// typeFromDescription extracts the `type` = `...` a message description
// states, or "" when the description pins no type.
func typeFromDescription(desc string) string {
	const marker = "`type` = `"
	i := strings.Index(desc, marker)
	if i < 0 {
		return ""
	}
	rest := desc[i+len(marker):]
	if j := strings.Index(rest, "`"); j >= 0 {
		return rest[:j]
	}
	return ""
}

// payloadConsts collects the const values a message payload declares for
// CloudEvents envelope attributes (type, dataschema, ...), wherever the
// spec keeps them — directly under properties, in an allOf overlay on the
// shared envelope schema, or behind a payload-level $ref into
// components/schemas.
func (d *pinnedAsyncAPI) payloadConsts(t *testing.T, payload any) map[string]string {
	t.Helper()
	consts := map[string]string{}
	d.collectPayloadConsts(t, payload, consts, map[string]bool{})
	return consts
}

func (d *pinnedAsyncAPI) collectPayloadConsts(t *testing.T, node any, consts map[string]string, resolving map[string]bool) {
	v, ok := node.(map[string]any)
	if !ok {
		return
	}
	if schema, ok := d.resolveSchemaRef(t, v, resolving); ok {
		d.collectPayloadConsts(t, schema, consts, resolving)
		return
	}
	for attr, c := range propertyConsts(v) {
		if _, seen := consts[attr]; !seen {
			consts[attr] = c
		}
	}
	for _, item := range allOfItems(v) {
		d.collectPayloadConsts(t, item, consts, resolving)
	}
}

// resolveSchemaRef resolves node's top-level $ref into components/schemas
// (cycle-guarded). ok is false when node carries no $ref, or the ref is
// already being resolved.
func (d *pinnedAsyncAPI) resolveSchemaRef(t *testing.T, node map[string]any, resolving map[string]bool) (any, bool) {
	ref, ok := node["$ref"].(string)
	if !ok {
		return nil, false
	}
	name := refName(t, ref)
	if resolving[name] {
		return nil, false
	}
	resolving[name] = true
	schema, ok := d.Components.Schemas[name]
	if !ok {
		t.Fatalf("pinned spec: unresolved schema $ref %q", ref)
	}
	return schema, true
}

// propertyConsts returns the const string each directly-declared property
// of node pins (empty when node pins none).
func propertyConsts(node map[string]any) map[string]string {
	consts := map[string]string{}
	props, ok := node["properties"].(map[string]any)
	if !ok {
		return consts
	}
	for attr, p := range props {
		if pm, ok := p.(map[string]any); ok {
			if c, ok := pm["const"].(string); ok {
				consts[attr] = c
			}
		}
	}
	return consts
}

// allOfItems returns node's allOf entries (nil when none).
func allOfItems(node map[string]any) []any {
	items, _ := node["allOf"].([]any)
	return items
}

// envelopeRequired returns the required attribute names declared by a
// message payload's envelope schema (resolving top-level $refs into
// components/schemas). The subset naming CloudEvents attributes is what
// E3 asserts on.
func (d *pinnedAsyncAPI) envelopeRequired(t *testing.T, payload any) map[string]bool {
	t.Helper()
	required := map[string]bool{}
	d.collectRequired(t, payload, required, map[string]bool{})
	return required
}

func (d *pinnedAsyncAPI) collectRequired(t *testing.T, node any, required map[string]bool, resolving map[string]bool) {
	v, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, r := range requiredList(v) {
		required[r] = true
	}
	if schema, ok := d.resolveSchemaRef(t, v, resolving); ok {
		d.collectRequired(t, schema, required, resolving)
		return
	}
	for _, item := range allOfItems(v) {
		d.collectRequired(t, item, required, resolving)
	}
}

// requiredList returns node's required attribute names (nil when none).
func requiredList(node map[string]any) []string {
	raw, ok := node["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// refName extracts the last path segment of a local $ref
// ("#/components/messages/TaskCompleted" -> "TaskCompleted").
func refName(t *testing.T, ref string) string {
	t.Helper()
	i := strings.LastIndex(ref, "/")
	if i < 0 || i == len(ref)-1 {
		t.Fatalf("malformed local $ref %q", ref)
	}
	return ref[i+1:]
}

// goldenJSON marshals a pinned example's YAML-decoded payload into the
// JSON bytes a Kafka consumer actually receives (structured-mode
// CloudEvents: the message value IS the JSON envelope).
func goldenJSON(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal golden payload: %v", err)
	}
	return raw
}

// goldenData returns a golden payload's `data` object (failing when the
// example carries none). yaml.v3 decodes whole numbers as int: coerce with
// numberOf when comparing against int64 fields.
func goldenData(t *testing.T, ex pinnedExample) map[string]any {
	t.Helper()
	data, ok := ex.Payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("golden example %s carries no data object", ex.Name)
	}
	return data
}

// numberOf coerces a yaml.v3-decoded number (int | int64 | float64) to int64.
func numberOf(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// clonePayload deep-copies a golden example payload so a test can mutate it
// without touching the shared fixture. A clone that re-identifies the event
// MUST set a fresh CloudEvents id (the id is the idempotency key).
func clonePayload(p map[string]any) map[string]any {
	return cloneMap(p).(map[string]any)
}

func cloneMap(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = cloneMap(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = cloneMap(val)
		}
		return out
	default:
		return v
	}
}

// assertEnvelopeRequired fails unless the spec's envelope schema for every
// named message requires the full fleet-mandatory CloudEvents attribute set.
func (d *pinnedAsyncAPI) assertEnvelopeRequired(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		required := d.envelopeRequired(t, d.message(t, name).Payload)
		for _, attr := range []string{"specversion", "id", "source", "type", "subject", "time", "datacontenttype", "dataschema", "data"} {
			if !required[attr] {
				t.Errorf("pinned spec's %s envelope does not require %q (required: %v)", name, attr, required)
			}
		}
	}
}

// assertGoldenEnvelope decodes value through the REAL cloudevents.Decode and
// asserts the fleet-mandatory envelope: specversion 1.0, id/source/subject/
// time present, source == wantSource, datacontenttype application/json, and
// dataschema == wantDataschema (a prefix check on urn:warehouse: when the
// spec pins the value only as a pattern). It returns the decoded event.
func assertGoldenEnvelope(t *testing.T, name string, value []byte, wantSource, wantDataschema string) cevent.Event {
	t.Helper()
	evt, err := cloudevents.Decode(value)
	if err != nil {
		t.Fatalf("%s: decode golden example: %v", name, err)
	}
	if evt.SpecVersion() != cloudevents.SpecVersion {
		t.Errorf("%s: specversion %q, want %q", name, evt.SpecVersion(), cloudevents.SpecVersion)
	}
	if evt.ID() == "" || evt.Subject() == "" || evt.Time().IsZero() {
		t.Errorf("%s: id/subject/time must all be present (got %q/%q/%v)", name, evt.ID(), evt.Subject(), evt.Time())
	}
	if evt.Source() != wantSource {
		t.Errorf("%s: source %q, want %q", name, evt.Source(), wantSource)
	}
	if evt.DataMediaType() != cloudevents.DataContentType {
		t.Errorf("%s: datacontenttype %q, want %q", name, evt.DataMediaType(), cloudevents.DataContentType)
	}
	if wantDataschema != "" && evt.DataSchema() != wantDataschema {
		t.Errorf("%s: dataschema %q, want %q", name, evt.DataSchema(), wantDataschema)
	}
	if !strings.HasPrefix(evt.DataSchema(), "urn:warehouse:") {
		t.Errorf("%s: dataschema %q, want a urn:warehouse: urn", name, evt.DataSchema())
	}
	return evt
}

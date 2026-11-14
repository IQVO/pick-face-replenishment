package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
)

const (
	meterName = "github.com/claudioed/pick-face-replenishment"

	// requestRaisedName counts every attempt to raise a replenishment
	// request, by outcome. A rising outcome=failed share, and above all one
	// reason (replenishment-already-open, concurrent-modification,
	// internal-error), means detection and the consumers are fighting over the
	// same pick faces or the store is failing; a flat created series while
	// stock levels move means the detector is not raising at all.
	requestRaisedName = "replenishment.request.raised"

	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")
)

// ReplenishmentMetrics implements ports.ReplenishmentMetrics against the
// global MeterProvider (a no-op until Setup installs a real one).
type ReplenishmentMetrics struct {
	counter metric.Int64Counter
}

var _ ports.ReplenishmentMetrics = (*ReplenishmentMetrics)(nil)

// NewReplenishmentMetrics registers the counter. It only fails if the
// instrument name is invalid, a programming error.
func NewReplenishmentMetrics() (*ReplenishmentMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		requestRaisedName,
		metric.WithDescription("Attempts to raise a replenishment request, by outcome (created, failed) and, on failure, the problem slug as reason."),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}
	return &ReplenishmentMetrics{counter: counter}, nil
}

// RequestRaised adds one to the series of outcome (and reason, when set).
func (m *ReplenishmentMetrics) RequestRaised(ctx context.Context, outcome, reason string) {
	attrs := []attribute.KeyValue{outcomeKey.String(outcome)}
	if reason != "" {
		attrs = append(attrs, reasonKey.String(reason))
	}
	m.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

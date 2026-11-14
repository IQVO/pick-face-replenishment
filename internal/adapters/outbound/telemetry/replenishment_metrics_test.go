package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/telemetry"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
)

type point struct {
	outcome, reason string
	hasReason       bool
}

// collectRaised finds the replenishment.request.raised instrument in the
// reader.
func collectRaised(t *testing.T, reader *sdkmetric.ManualReader) metricdata.Metrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, md := range scope.Metrics {
			if md.Name == "replenishment.request.raised" {
				return md
			}
		}
	}
	t.Fatal("replenishment.request.raised was not exported")
	return metricdata.Metrics{}
}

// seriesOf reads the data points of a monotonic int64 Sum.
func seriesOf(t *testing.T, md metricdata.Metrics) map[point]int64 {
	t.Helper()
	sum, ok := md.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("replenishment.request.raised is a %T, want an int64 Sum", md.Data)
	}
	if !sum.IsMonotonic {
		t.Error("replenishment.request.raised must be monotonic (a counter)")
	}
	got := map[point]int64{}
	for _, dp := range sum.DataPoints {
		got[pointOf(dp.Attributes)] = dp.Value
	}
	return got
}

func pointOf(set attribute.Set) point {
	o, _ := set.Value("outcome")
	r, ok := set.Value("reason")
	return point{outcome: o.AsString(), reason: r.AsString(), hasReason: ok}
}

func TestReplenishmentMetrics_CountsByOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	m, err := telemetry.NewReplenishmentMetrics()
	if err != nil {
		t.Fatalf("NewReplenishmentMetrics: %v", err)
	}
	ctx := context.Background()
	m.RequestRaised(ctx, ports.OutcomeCreated, "")
	m.RequestRaised(ctx, ports.OutcomeCreated, "")
	m.RequestRaised(ctx, ports.OutcomeFailed, "replenishment-already-open")

	md := collectRaised(t, reader)
	if md.Unit != "{request}" {
		t.Errorf("unit = %q, want {request}", md.Unit)
	}
	got := seriesOf(t, md)
	want := map[point]int64{
		{outcome: "created"}: 2,
		{outcome: "failed", reason: "replenishment-already-open", hasReason: true}: 1,
	}
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("series %+v = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

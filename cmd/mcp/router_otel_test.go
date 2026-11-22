package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	inboundmcp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/mcp"
)

// TestRouterEmitsTheStandardHTTPServerMetrics proves the Tier 1 duration
// histogram (ADR 0013) is emitted for a request to this binary's router, with
// the chi route pattern as http.route.
func TestRouterEmitsTheStandardHTTPServerMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	h := newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{})), "pick-face-replenishment-mcp")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d", rec.Code)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var routes []string
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("http.server.request.duration is a %T, want a float64 Histogram", m.Data)
			}
			for _, dp := range hist.DataPoints {
				route, _ := dp.Attributes.Value("http.route")
				routes = append(routes, route.AsString())
			}
		}
	}
	if len(routes) != 1 || routes[0] != "/healthz" {
		t.Fatalf("http.server.request.duration routes = %v, want [/healthz]", routes)
	}
}

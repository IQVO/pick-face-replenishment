package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/analytics/report"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// useReader installs a ManualReader-backed global MeterProvider for the test.
// otelchimetric binds the global provider when the router is BUILT, so call
// this before building the router.
func useReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

// recordSpans installs a recording TracerProvider for the test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

// durationRoutes returns the http.route values of the data points of the
// http.server.request.duration histogram (and whether the histogram exists).
func durationRoutes(t *testing.T, reader sdkmetric.Reader) (routes map[string]bool, found bool) {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	routes = map[string]bool{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			found = true
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("http.server.request.duration is a %T, want a float64 Histogram", m.Data)
			}
			for _, dp := range hist.DataPoints {
				route, _ := dp.Attributes.Value(attribute.Key("http.route"))
				routes[route.AsString()] = true
			}
		}
	}
	return routes, found
}

// restRouter is the REST router over the in-memory policy repository; logger
// receives the request log.
func restRouter(logger *slog.Logger) http.Handler {
	policies := memory.NewPolicyRepo()
	return inboundhttp.NewRouter(&inboundhttp.Server{
		GetPolicy:    &usecases.GetPolicy{Policies: policies},
		ListPolicies: &usecases.ListPolicies{Policies: policies},
		Logger:       logger,
	})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestRouterEmitsTheRequestDurationHistogramLabelledByRoutePattern(t *testing.T) {
	reader := useReader(t)
	router := restRouter(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	if rec := get(router, "/policies/SITE-1/SKU-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown policy", rec.Code)
	}

	routes, found := durationRoutes(t, reader)
	if !found {
		t.Fatal("http.server.request.duration was not emitted: the router lacks otelchimetric")
	}
	if !routes["/policies/{siteId}/{sku}"] {
		t.Errorf("routes = %v, want the chi pattern /policies/{siteId}/{sku}", routes)
	}
	for route := range routes {
		if strings.Contains(route, "SITE-1") {
			t.Errorf("route label %q carries a raw id: the series would explode in cardinality", route)
		}
	}
}

func TestReportsRouterEmitsTheRequestDurationHistogramLabelledByRoutePattern(t *testing.T) {
	reader := useReader(t)
	router := inboundhttp.NewReportsRouter(&inboundhttp.ReportsServer{
		Reader: noReports{},
		Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
	})

	if rec := get(router, "/reports/freshness"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	routes, found := durationRoutes(t, reader)
	if !found {
		t.Fatal("http.server.request.duration was not emitted: the reports router lacks otelchimetric")
	}
	if !routes["/reports/freshness"] {
		t.Errorf("routes = %v, want /reports/freshness", routes)
	}
}

// noReports answers the reader port with an empty projection.
type noReports struct{}

func (noReports) FunnelDays(context.Context, report.FunnelQuery) ([]report.FunnelDay, error) {
	return nil, nil
}
func (noReports) LastEventAt(context.Context) (*time.Time, error) { return nil, nil }

func TestRouterServerSpanIsNamedAfterTheRoutePattern(t *testing.T) {
	recorder := recordSpans(t)
	router := restRouter(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	get(router, "/policies/SITE-with-a-distinct-id/SKU-1")

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].SpanKind() != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", spans[0].SpanKind())
	}
	if got := spans[0].Name(); !strings.HasSuffix(got, "/policies/{siteId}/{sku}") {
		t.Errorf("span name = %q, want the route pattern (one name per route, not per site id)", got)
	}
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestRouterRequestLogShape(t *testing.T) {
	var buf bytes.Buffer
	router := restRouter(slog.New(slog.NewJSONHandler(&buf, nil)))

	get(router, "/policies/SITE-1/SKU-1")

	lines := logLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want exactly the request line: %v", len(lines), lines)
	}
	line := lines[0]
	if line["msg"] != "http request" || line["method"] != "GET" || line["status"] != float64(404) || line["level"] != "WARN" {
		t.Errorf("request line = %v", line)
	}
	if line["route"] != "/policies/{siteId}/{sku}" {
		t.Errorf("route = %v, want the pattern, never the raw path", line["route"])
	}
	if id, _ := line["request_id"].(string); id == "" {
		t.Error("request_id is empty: RequestID must run before RequestLogger")
	}
}

func TestRouterDoesNotLogProbes(t *testing.T) {
	var buf bytes.Buffer
	router := restRouter(slog.New(slog.NewJSONHandler(&buf, nil)))

	get(router, "/healthz")
	get(router, "/readyz")

	if buf.Len() != 0 {
		t.Errorf("probes were logged: %s", buf.String())
	}
}

// A handler panic is still one request line, at ERROR: the logger sits OUTSIDE
// the recoverer, so the 500 the recoverer writes is what it records.
func TestRouterLogsAPanickedRequestAsA500(t *testing.T) {
	var buf bytes.Buffer
	// No GetPolicy wired: the handler dereferences nil and panics.
	router := inboundhttp.NewRouter(&inboundhttp.Server{Logger: slog.New(slog.NewJSONHandler(&buf, nil))})

	if rec := get(router, "/policies/SITE-1/SKU-1"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 from the recoverer", rec.Code)
	}

	var request map[string]any
	for _, line := range logLines(t, &buf) {
		if line["msg"] == "http request" {
			request = line
		}
	}
	if request == nil || request["status"] != float64(500) || request["level"] != "ERROR" {
		t.Fatalf("request line = %v, want status 500 at ERROR", request)
	}
}

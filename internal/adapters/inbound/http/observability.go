package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/requestlog"
)

// Names of the server spans and HTTP metrics when the composition root passes
// none: the api router and the reports router are two different services.
const (
	DefaultServiceName        = "pick-face-replenishment"
	DefaultReportsServiceName = "pick-face-replenishment-reports"
)

// useObservability installs the fleet observability middleware (ADR 0013), in
// the order the standard fixes, outermost first: RequestID, otelchi (the server
// span, named after the chi route pattern, so one span name per route and not
// per site id), the request-duration histogram, RequestLogger (it runs inside
// the span, so its record carries trace_id and span_id), then the recoverer (a
// panic is a 500 problem the log still sees).
//
// telemetry.Setup must have run before the router is built: otelchimetric
// binds the global MeterProvider when it is constructed.
func useObservability(r *chi.Mux, serviceName string, logger *slog.Logger) {
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(recoverPanic)
}

// orDefault returns name, or fallback when name is empty.
func orDefault(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, the
// Idempotency-Key middleware of the POSTs and the liveness/readiness
// endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// Server holds the use cases the handlers call.
type Server struct {
	DeclarePolicy *usecases.DeclarePolicy
	RemovePolicy  *usecases.RemovePolicy
	GetPolicy     *usecases.GetPolicy
	ListPolicies  *usecases.ListPolicies
	ListPickFaces *usecases.ListPickFaces
	RaiseManual   *usecases.RaiseManualRequest
	CancelRequest *usecases.CancelRequest
	GetRequest    *usecases.GetRequest
	ListRequests  *usecases.ListRequests
	EvaluateSite  *usecases.EvaluateSite

	// Idempotency wraps the three POSTs (the fleet Idempotency-Key middleware,
	// Idempotency.Middleware, built by cmd/ over the Postgres adapters); nil
	// leaves the routes unwrapped, which is the in-memory development mode.
	Idempotency func(http.Handler) http.Handler
	// Readiness backs GET /readyz; nil means always ready.
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml
// (.claude/rules/rest-api.md).
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(recoverPanic)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.Get("/policies", s.handleListPolicies)
	r.Put("/policies/{siteId}/{sku}", s.handleDeclarePolicy)
	r.Get("/policies/{siteId}/{sku}", s.handleGetPolicy)
	r.Delete("/policies/{siteId}/{sku}", s.handleRemovePolicy)

	r.Get("/pick-faces", s.handleListPickFaces)

	r.Get("/replenishments", s.handleListReplenishments)
	r.Get("/replenishments/{requestId}", s.handleGetReplenishment)

	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/replenishments", s.handleRequestReplenishment)
		r.Post("/replenishments/{requestId}/cancel", s.handleCancelReplenishment)
		r.Post("/sites/{siteId}/evaluate", s.handleEvaluateSite)
	})
	return r
}

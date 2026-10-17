package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/events"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	outboxrelay "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/system"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/bootretry"
)

// adapters is the set of outbound adapters the composition root wires.
type adapters struct {
	policies ports.PolicyRepo
	requests ports.RequestRepo
	read     ports.PickFaceReadModel
	events   ports.EventSink
	uow      ports.UnitOfWork
	clock    ports.Clock
	ids      ports.IDGenerator
	// processed is the consumers' processed-event claim table.
	processed ports.ProcessedEvents

	// outbox is the relay's view of the outbox table; nil in the in-memory
	// development mode, which has no relay.
	outbox outboxrelay.Store
	// idempotency is the Idempotency-Key middleware of the POSTs; nil in the
	// in-memory development mode, which does not enforce the header.
	idempotency func(http.Handler) http.Handler
}

// buildAdapters wires Postgres when databaseURL is set (running the embedded
// migrations first) or in-memory adapters otherwise. The returned func closes
// what was opened.
func buildAdapters(ctx context.Context, databaseURL, migrationsURL string, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		logger.Info("DATABASE_URL not configured; using in-memory adapters (no outbox relay, Idempotency-Key not enforced)")
		return memoryAdapters(), noop, nil
	}

	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsURL)
	}); err != nil {
		return adapters{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return adapters{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return adapters{}, noop, err
	}
	logger.Info("postgres adapters configured")
	return postgresAdapters(pool), pool.Close, nil
}

func memoryAdapters() adapters {
	policies, requests, read := memory.NewPolicyRepo(), memory.NewRequestRepo(), memory.NewReadModel()
	log, processed := memory.NewEventLog(), memory.NewProcessedEventRepo()
	return adapters{
		policies: policies, requests: requests, read: read, events: log, processed: processed,
		uow:   memory.NewUnitOfWork(policies, requests, read, log, processed),
		clock: system.Clock{}, ids: system.RandomIDs{},
	}
}

func postgresAdapters(pool *pgxpool.Pool) adapters {
	ob := postgres.NewOutboxRepo(pool)
	ad := adapters{
		policies: postgres.NewPolicyRepo(pool), requests: postgres.NewRequestRepo(pool), read: postgres.NewReadModel(pool),
		events: events.NewSink(ob), uow: postgres.NewUnitOfWork(pool),
		clock: system.Clock{}, ids: system.RandomIDs{},
		processed: postgres.NewProcessedEventRepo(pool),
		outbox:    ob,
	}
	ad.idempotency = inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), ad.uow, ad.clock).Middleware
	return ad
}

// evaluator is the shared detection core every use case that detects goes
// through.
func (a adapters) evaluator(zones usecases.ZoneConfig) usecases.Evaluator {
	return usecases.Evaluator{
		Policies: a.policies, Requests: a.requests, Read: a.read, Events: a.events,
		Clock: a.clock, IDs: a.ids, Zones: zones,
	}
}

// intake is the claim-and-apply skeleton every consumer use case shares.
func (a adapters) intake() usecases.Intake {
	return usecases.Intake{UoW: a.uow, Processed: a.processed}
}

// buildServer wires the use cases into the HTTP adapter.
func buildServer(a adapters, zones usecases.ZoneConfig, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	eval := a.evaluator(zones)
	return &inboundhttp.Server{
		DeclarePolicy: &usecases.DeclarePolicy{Policies: a.policies, Events: a.events, UoW: a.uow, Clock: a.clock},
		RemovePolicy:  &usecases.RemovePolicy{Policies: a.policies, Requests: a.requests, Events: a.events, UoW: a.uow, Clock: a.clock},
		GetPolicy:     &usecases.GetPolicy{Policies: a.policies},
		ListPolicies:  &usecases.ListPolicies{Policies: a.policies},
		ListPickFaces: &usecases.ListPickFaces{Eval: eval},
		RaiseManual:   &usecases.RaiseManualRequest{Eval: eval, UoW: a.uow},
		CancelRequest: &usecases.CancelRequest{Requests: a.requests, Events: a.events, UoW: a.uow, Clock: a.clock},
		GetRequest:    &usecases.GetRequest{Requests: a.requests},
		ListRequests:  &usecases.ListRequests{Requests: a.requests},
		EvaluateSite:  &usecases.EvaluateSite{Eval: eval, UoW: a.uow},
		Idempotency:   a.idempotency,
		Readiness:     readiness,
	}
}

// migrationsDatabaseURL returns MIGRATIONS_DATABASE_URL when set (a direct
// DSN: golang-migrate's advisory lock does not survive PgBouncer transaction
// pooling), else databaseURL.
func migrationsDatabaseURL(databaseURL string) string {
	return getenv(postgres.EnvMigrationsDatabaseURL, databaseURL)
}

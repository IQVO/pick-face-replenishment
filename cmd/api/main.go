// Command api is pick-face-replenishment's composition root: it wires env
// config into adapters, adapters into use cases, and use cases into the HTTP
// router, the three Kafka consumers and the outbox relay. Nothing else in the
// module imports it.
//
// Postgres and Kafka are the ONLY runtime (ADR 0006): there is no in-memory
// mode, no consumer that is off by default and no optional relay. A missing
// mandatory variable is a boot error naming it, raised before the listener
// binds. Topics are owned by infrastructure; the service never creates them.
//
// Environment:
//
//	DATABASE_URL              REQUIRED. Postgres DSN
//	MIGRATIONS_DATABASE_URL   direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS             REQUIRED. Comma-separated brokers
//	INVENTORY_CONSUMER_GROUP  REQUIRED. Stable group ids of the three consumers
//	SLOTTING_CONSUMER_GROUP   (inventory-storage's BinStockLevelChanged and
//	LAYOUT_CONSUMER_GROUP     StockRelocated, slotting-optimization's
//	                          SlotPlanApproved, facility-layout's zone and
//	                          location events; ADR 0003)
//	INVENTORY_MODE, SLOTTING_MODE, LAYOUT_MODE
//	                          kafka (default). permissive is REJECTED at boot:
//	                          every consumer always runs
//	OUTBOX_RELAY_INTERVAL     relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS       failures before the relay excludes a row (default 20, 0 = unlimited)
//	HTTP_ADDR                 listen address (default :8080)
//	FORWARD_ZONE_CODES        comma-separated zone codes of forward pick faces (default FWD)
//	RESERVE_ZONE_CODES        comma-separated zone codes of reserve bins (default RSV)
//	SHUTDOWN_DRAIN_DELAY      wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                 debug | info | warn | error (default info)
//
// /readyz is the drain gate AND a Postgres ping AND a Kafka metadata check
// (brokers reachable, every consumed and produced topic has a leader), cached
// for a few seconds. /healthz is liveness only.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/kafka"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the background
// workers to return after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

func main() {
	err := run(context.Background())
	if err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the in-process tests stop it that way), then shuts down in the
// fleet order. It reads the whole configuration from the environment.
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after the
	// HTTP drain and the relay have stopped.
	ad, closeAdapters, err := buildAdapters(ctx, cfg.databaseURL, cfg.migrationsURL, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()
	logTopics(logger, cfg.brokers)

	readiness := buildReadiness(ad, outboundkafka.NewBrokerCheck(cfg.brokers, runtimeTopics()).Check)
	zones := zoneConfigFromEnv()
	httpServer := &http.Server{
		Addr:              getenv("HTTP_ADDR", ":8080"),
		Handler:           inboundhttp.NewRouter(buildServer(ad, zones, readiness)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	consumers, stopConsumers := startConsumers(ad, zones, cfg.brokers, cfg.consumers, logger)
	relay, stopRelay, err := startOutboxRelay(ad.outbox, cfg.brokers, logger)
	if err != nil {
		stopWorkers(logger, consumers, stopConsumers, nil, func() {})
		return err
	}
	return serveUntilSignal(ctx, logger, httpServer, readiness, consumers, stopConsumers, relay, stopRelay)
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM, until ctx is
// cancelled, or until a listen error, then shuts down in the fleet order: (1)
// /readyz flips to 503, (2) wait the drain delay, (3) drain HTTP, (4) stop and
// await the consumers, (5) stop and await the relay LAST so nothing committed
// above is stranded; the caller's deferred pool.Close runs after all of it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	consumers []*worker, stopConsumers func(), relay *worker, stopRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		stopWorkers(logger, consumers, stopConsumers, relay, stopRelay)
		return err
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	stopWorkers(logger, consumers, stopConsumers, relay, stopRelay)
	return err
}

// stopWorkers stops and awaits the consumers, then the relay: the consumers
// may still enqueue outbox rows while they drain, so the relay goes last.
func stopWorkers(logger *slog.Logger, consumers []*worker, stopConsumers func(), relay *worker, stopRelay func()) {
	stopConsumers()
	for _, w := range consumers {
		await(logger, w, workerDrainTimeout)
	}
	stopRelay()
	await(logger, relay, workerDrainTimeout)
}

// await waits (bounded by timeout) for w to return; nil w is a no-op.
func await(logger *slog.Logger, w *worker, timeout time.Duration) {
	if w == nil {
		return
	}
	select {
	case <-w.done:
	case <-time.After(timeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}

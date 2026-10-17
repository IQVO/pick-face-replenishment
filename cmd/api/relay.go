package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	outboundkafka "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/outbox"
)

// worker is a started background loop and the channel closed once it
// returned.
type worker struct {
	name string
	done chan struct{}
}

// startOutboxRelay starts the relay goroutine when there is something to
// relay and somewhere to send it: a Postgres outbox (store != nil) and
// KAFKA_BROKERS. Without Postgres the service is in-memory development mode
// and has no outbox; without brokers the rows accumulate in outbox_events
// until the service is restarted with brokers (nothing is lost). A nil worker
// and a no-op stop are returned when the relay is off. Kafka is dialled lazily
// on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, brokers []string, logger *slog.Logger) (*worker, func()) {
	if store == nil {
		logger.Info("outbox relay off: in-memory mode has no outbox")
		return nil, func() {}
	}
	if len(brokers) == 0 {
		logger.Warn("outbox relay off: KAFKA_BROKERS is not set; events accumulate in outbox_events until it is")
		return nil, func() {}
	}
	sink := outboundkafka.NewRelaySink(brokers)
	interval := parseRelayInterval(os.Getenv("OUTBOX_RELAY_INTERVAL"), logger)
	maxAttempts := outboxMaxAttempts(os.Getenv("OUTBOX_MAX_ATTEMPTS"), logger)
	relay := outboxrelay.NewRelay(store, sink, logger, outboxrelay.WithInterval(interval), outboxrelay.WithMaxAttempts(maxAttempts))

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if err := sink.Close(); err != nil {
				logger.Error("kafka relay sink close failed", "error", err)
			}
		}()
		logger.Info("outbox relay running", "interval", interval, "max_attempts", maxAttempts, "brokers", brokers)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop
}

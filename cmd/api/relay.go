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

// startOutboxRelay starts the relay goroutine. The relay is not optional: the
// events leave the service only through it, so a missing outbox store or
// missing brokers is a boot error, never a silent degrade. Kafka is dialled
// lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, brokers []string, logger *slog.Logger) (*worker, func(), error) {
	if store == nil {
		return nil, nil, errors.New("outbox relay cannot be built: no outbox store (DATABASE_URL is required: this context runs on real Postgres)")
	}
	if len(brokers) == 0 {
		return nil, nil, errors.New("outbox relay cannot be built: KAFKA_BROKERS is required: this context runs on real Kafka")
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
	return w, stop, nil
}

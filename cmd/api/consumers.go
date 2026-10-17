package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	inboundkafka "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// Consumer modes (INVENTORY_MODE, SLOTTING_MODE, LAYOUT_MODE).
const (
	modePermissive = "permissive"
	modeKafka      = "kafka"
)

// parseConsumerMode validates a *_MODE value ("" and "permissive" = no
// consumer: the local copies are whatever the database holds).
func parseConsumerMode(name, raw string) (string, error) {
	switch raw {
	case "", modePermissive:
		return modePermissive, nil
	case modeKafka:
		return modeKafka, nil
	default:
		return "", fmt.Errorf("unknown %s %q (want kafka or permissive)", name, raw)
	}
}

// consumerSpec is one consumer: its mode and group variables and how to build
// it once both are resolved.
type consumerSpec struct {
	modeEnv, groupEnv string
	build             func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer
}

// consumerSpecs lists the three consumers (ADR 0003). The group ids come from
// the environment, never from a literal.
func consumerSpecs(ad adapters, zones usecases.ZoneConfig) []consumerSpec {
	intake, eval := ad.intake(), ad.evaluator(zones)
	return []consumerSpec{
		{"INVENTORY_MODE", "INVENTORY_CONSUMER_GROUP", func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			level := &usecases.ApplyBinLevel{Intake: intake, Eval: eval}
			relocation := &usecases.ApplyRelocation{Intake: intake, Requests: ad.requests, Events: ad.events, Clock: ad.clock}
			return inboundkafka.NewInventoryConsumer(brokers, group, level, relocation, logger)
		}},
		{"SLOTTING_MODE", "SLOTTING_CONSUMER_GROUP", func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewSlottingConsumer(brokers, group, &usecases.ApplyForwardAssignmentMap{Intake: intake, Eval: eval}, logger)
		}},
		{"LAYOUT_MODE", "LAYOUT_CONSUMER_GROUP", func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewLayoutConsumer(brokers, group, inboundkafka.LayoutUseCases{
				Zone:           &usecases.ApplyZone{Intake: intake, Read: ad.read},
				Registered:     &usecases.ApplyLocationRegistered{Intake: intake, Read: ad.read},
				Decommissioned: &usecases.ApplyLocationDecommissioned{Intake: intake, Read: ad.read},
			}, logger)
		}},
	}
}

// plannedConsumer is a consumer whose mode is kafka, with its resolved group.
type plannedConsumer struct {
	spec  consumerSpec
	group string
}

// planConsumers resolves every consumer's mode and group from the environment
// and refuses to boot on a misconfiguration: an unknown mode, or mode kafka
// without its group variable or without KAFKA_BROKERS.
func planConsumers(specs []consumerSpec, brokers []string) ([]plannedConsumer, error) {
	var planned []plannedConsumer
	for _, spec := range specs {
		mode, err := parseConsumerMode(spec.modeEnv, os.Getenv(spec.modeEnv))
		if err != nil {
			return nil, err
		}
		if mode != modeKafka {
			continue
		}
		group := os.Getenv(spec.groupEnv)
		if group == "" {
			return nil, fmt.Errorf("%s=kafka requires %s", spec.modeEnv, spec.groupEnv)
		}
		planned = append(planned, plannedConsumer{spec: spec, group: group})
	}
	if len(planned) > 0 && len(brokers) == 0 {
		return nil, errors.New("a consumer in kafka mode requires KAFKA_BROKERS")
	}
	return planned, nil
}

// startConsumers starts every consumer whose mode is kafka and returns their
// workers and the func that stops them. The readers dial lazily inside Run, so
// a broker outage never blocks boot; each worker closes its own reader and
// dead-letter writer once Run has returned. durable reports whether the local
// copies live in a database: in-memory copies are lost on restart while the
// committed offsets are not, so that combination is warned about.
func startConsumers(ad adapters, zones usecases.ZoneConfig, brokers []string, durable bool, logger *slog.Logger) ([]*worker, func(), error) {
	planned, err := planConsumers(consumerSpecs(ad, zones), brokers)
	if err != nil {
		return nil, nil, err
	}
	if len(planned) == 0 {
		logger.Info("no consumer is in kafka mode; the local copies are whatever the database holds")
		return nil, func() {}, nil
	}
	if !durable {
		logger.Warn("consumers run over in-memory adapters (DATABASE_URL is not set): the local copies are lost on restart while the committed offsets are kept")
	}
	ctx, stop := context.WithCancel(context.Background())
	workers := make([]*worker, 0, len(planned))
	for _, p := range planned {
		workers = append(workers, runConsumer(ctx, p.spec.build(brokers, p.group, logger), p, brokers, logger))
	}
	return workers, stop, nil
}

// runConsumer runs one consumer in its own goroutine until ctx is cancelled.
func runConsumer(ctx context.Context, consumer *inboundkafka.Consumer, p plannedConsumer, brokers []string, logger *slog.Logger) *worker {
	w := &worker{name: p.spec.modeEnv, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if err := consumer.Close(); err != nil {
				logger.Error("consumer close failed", "consumer", consumer.Name, "error", err)
			}
		}()
		logger.Info("consumer running", "consumer", consumer.Name, "topic", consumer.Topic, "group_id", p.group, "brokers", brokers)
		if err := consumer.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("consumer stopped", "consumer", consumer.Name, "error", err)
		}
	}()
	return w
}

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

// Consumer modes (INVENTORY_MODE, SLOTTING_MODE, LAYOUT_MODE). The variables
// stay (the fleet docs and the template rule files name them) but there is one
// runtime: every consumer reads real Kafka. "permissive" used to mean "do not
// start this consumer"; it is rejected at boot.
const (
	modeKafka      = "kafka"
	modePermissive = "permissive"
)

// parseConsumerMode validates a *_MODE value: unset or "kafka" is the one
// supported mode, "permissive" is refused with its own message and anything
// else is unknown.
func parseConsumerMode(name, raw string) (string, error) {
	if raw == "" || raw == modeKafka {
		return modeKafka, nil
	}
	if raw == modePermissive {
		return "", fmt.Errorf("%s=permissive is not supported: this context runs on real Kafka", name)
	}
	return "", fmt.Errorf("unknown %s %q (want kafka)", name, raw)
}

// consumerEnv names the two variables of one consumer.
type consumerEnv struct {
	modeEnv, groupEnv string
}

// consumerEnvs lists the four consumers (ADR 0003, ADR 0009). The group ids come from
// the environment, never from a literal.
var consumerEnvs = []consumerEnv{
	{"INVENTORY_MODE", "INVENTORY_CONSUMER_GROUP"},
	{"SLOTTING_MODE", "SLOTTING_CONSUMER_GROUP"},
	{"LAYOUT_MODE", "LAYOUT_CONSUMER_GROUP"},
	{"PACK_LEVEL_MODE", "PACK_LEVEL_CONSUMER_GROUP"},
}

// plannedConsumer is a consumer with its resolved group.
type plannedConsumer struct {
	env   consumerEnv
	group string
}

// planConsumers resolves every consumer's mode and group from the environment
// and refuses to boot on a misconfiguration: a mode other than kafka, or a
// missing group variable. Every problem is reported, not only the first.
func planConsumers() ([]plannedConsumer, error) {
	planned := make([]plannedConsumer, 0, len(consumerEnvs))
	var errs []error
	for _, env := range consumerEnvs {
		p, err := planConsumer(env)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		planned = append(planned, p)
	}
	return planned, errors.Join(errs...)
}

func planConsumer(env consumerEnv) (plannedConsumer, error) {
	if _, err := parseConsumerMode(env.modeEnv, os.Getenv(env.modeEnv)); err != nil {
		return plannedConsumer{}, err
	}
	group := os.Getenv(env.groupEnv)
	if group == "" {
		return plannedConsumer{}, fmt.Errorf("%s is required: this context runs on real Kafka", env.groupEnv)
	}
	return plannedConsumer{env: env, group: group}, nil
}

// consumerBuilder builds one consumer once its group is resolved.
type consumerBuilder func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer

// consumerBuilders maps each consumer's mode variable to its builder.
func consumerBuilders(ad adapters, zones usecases.ZoneConfig) map[string]consumerBuilder {
	intake, eval, derive := ad.intake(), ad.evaluator(zones), ad.packDerivation()
	return map[string]consumerBuilder{
		"INVENTORY_MODE": func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			level := &usecases.ApplyBinLevel{Intake: intake, Eval: eval}
			relocation := &usecases.ApplyRelocation{Intake: intake, Requests: ad.requests, Events: ad.events, Clock: ad.clock}
			return inboundkafka.NewInventoryConsumer(brokers, group, level, relocation, logger)
		},
		"SLOTTING_MODE": func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewSlottingConsumer(brokers, group, &usecases.ApplyForwardAssignmentMap{Intake: intake, Eval: eval}, logger)
		},
		"LAYOUT_MODE": func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewLayoutConsumer(brokers, group, inboundkafka.LayoutUseCases{
				Zone:           &usecases.ApplyZone{Intake: intake, Read: ad.read},
				Registered:     &usecases.ApplyLocationRegistered{Intake: intake, Read: ad.read},
				Decommissioned: &usecases.ApplyLocationDecommissioned{Intake: intake, Read: ad.read},
			}, logger)
		},
		"PACK_LEVEL_MODE": func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewProductMasterConsumer(brokers, group, inboundkafka.ProductMasterUseCases{
				Declared: &usecases.ApplyPackLevelDeclared{Intake: intake, PackLevels: ad.packLevels, Derive: derive},
				Retired:  &usecases.ApplyPackLevelRetired{Intake: intake, PackLevels: ad.packLevels, Derive: derive},
			}, logger)
		},
	}
}

// startConsumers starts every planned consumer and returns their workers and
// the func that stops them. The readers dial lazily inside Run, so a broker
// outage (or a topic that does not exist yet) never blocks boot or crashes the
// process: the consumer logs and retries, and /readyz reports not-ready only
// while the broker is unreachable (a missing topic shows as consumer retry
// logs, never as a readiness failure). Each worker closes its own reader and
// dead-letter writer once Run has returned.
func startConsumers(ad adapters, zones usecases.ZoneConfig, brokers []string, planned []plannedConsumer, logger *slog.Logger) ([]*worker, func()) {
	builders := consumerBuilders(ad, zones)
	ctx, stop := context.WithCancel(context.Background())
	workers := make([]*worker, 0, len(planned))
	for _, p := range planned {
		workers = append(workers, runConsumer(ctx, builders[p.env.modeEnv](brokers, p.group, logger), p, brokers, logger))
	}
	return workers, stop
}

// runConsumer runs one consumer in its own goroutine until ctx is cancelled.
func runConsumer(ctx context.Context, consumer *inboundkafka.Consumer, p plannedConsumer, brokers []string, logger *slog.Logger) *worker {
	w := &worker{name: p.env.modeEnv, done: make(chan struct{})}
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

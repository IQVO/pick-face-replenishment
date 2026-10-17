package main

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// setConsumerEnv sets the mode and group variables of the three consumers.
func setConsumerEnv(t *testing.T, mode, group string) {
	t.Helper()
	for _, name := range []string{"INVENTORY", "SLOTTING", "LAYOUT"} {
		t.Setenv(name+"_MODE", mode)
		t.Setenv(name+"_CONSUMER_GROUP", group)
	}
}

func TestParseConsumerMode(t *testing.T) {
	for raw, want := range map[string]string{"": modePermissive, "permissive": modePermissive, "kafka": modeKafka} {
		got, err := parseConsumerMode("INVENTORY_MODE", raw)
		if err != nil || got != want {
			t.Fatalf("parseConsumerMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	_, err := parseConsumerMode("INVENTORY_MODE", "Kafka")
	if err == nil || !strings.Contains(err.Error(), "INVENTORY_MODE") {
		t.Fatalf("an unknown mode must be a boot error naming the variable, got %v", err)
	}
}

// The env var names are part of the deployment contract (the chart and
// .claude/rules/integration-events.md): pin them.
func TestConsumerSpecsPinTheEnvNames(t *testing.T) {
	var got []string
	for _, s := range consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}) {
		got = append(got, s.modeEnv+":"+s.groupEnv)
	}
	want := "INVENTORY_MODE:INVENTORY_CONSUMER_GROUP,SLOTTING_MODE:SLOTTING_CONSUMER_GROUP,LAYOUT_MODE:LAYOUT_CONSUMER_GROUP"
	if strings.Join(got, ",") != want {
		t.Fatalf("specs = %v, want %s", got, want)
	}
}

func TestPlanConsumersDefaultsToPermissiveAndNeedsNothing(t *testing.T) {
	setConsumerEnv(t, "", "")
	planned, err := planConsumers(consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}), nil)
	if err != nil || len(planned) != 0 {
		t.Fatalf("planned = %v, %v; want nothing and no error without any mode", planned, err)
	}
}

func TestPlanConsumersKafkaModeNeedsItsGroup(t *testing.T) {
	for _, name := range []string{"INVENTORY", "SLOTTING", "LAYOUT"} {
		setConsumerEnv(t, "", "")
		t.Setenv(name+"_MODE", "kafka")
		_, err := planConsumers(consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}), []string{"b:9092"})
		if err == nil || !strings.Contains(err.Error(), name+"_CONSUMER_GROUP") {
			t.Fatalf("%s_MODE=kafka without its group = %v, want a boot error naming %s_CONSUMER_GROUP", name, err, name)
		}
	}
}

func TestPlanConsumersKafkaModeNeedsBrokers(t *testing.T) {
	setConsumerEnv(t, "", "")
	t.Setenv("LAYOUT_MODE", "kafka")
	t.Setenv("LAYOUT_CONSUMER_GROUP", "layout-group")
	_, err := planConsumers(consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}), nil)
	if err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("kafka mode without brokers = %v, want a boot error naming KAFKA_BROKERS", err)
	}
}

func TestPlanConsumersRejectsAnUnknownMode(t *testing.T) {
	setConsumerEnv(t, "", "")
	t.Setenv("SLOTTING_MODE", "strict")
	if _, err := planConsumers(consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}), []string{"b:9092"}); err == nil {
		t.Fatal("an unknown mode must be a boot error")
	}
}

func TestPlanConsumersResolvesTheGroupsFromTheEnvironment(t *testing.T) {
	setConsumerEnv(t, "kafka", "")
	t.Setenv("INVENTORY_CONSUMER_GROUP", "inv-g")
	t.Setenv("SLOTTING_CONSUMER_GROUP", "slot-g")
	t.Setenv("LAYOUT_CONSUMER_GROUP", "lay-g")
	planned, err := planConsumers(consumerSpecs(memoryAdapters(), usecases.ZoneConfig{}), []string{"b:9092"})
	if err != nil || len(planned) != 3 {
		t.Fatalf("planned = %d, %v; want the three consumers", len(planned), err)
	}
	var groups []string
	for _, p := range planned {
		groups = append(groups, p.group)
	}
	if strings.Join(groups, ",") != "inv-g,slot-g,lay-g" {
		t.Fatalf("groups = %v", groups)
	}
}

func TestStartConsumersStartsNothingInPermissiveMode(t *testing.T) {
	setConsumerEnv(t, "", "")
	logger, logs := quietLogger()
	workers, stop, err := startConsumers(memoryAdapters(), usecases.ZoneConfig{}, nil, false, logger)
	if err != nil || len(workers) != 0 {
		t.Fatalf("workers = %d, %v; want none", len(workers), err)
	}
	stop()
	if !strings.Contains(logs.String(), "no consumer is in kafka mode") {
		t.Fatalf("the reason must be logged: %s", logs.String())
	}
}

func TestStartConsumersRefusesToBootOnAMisconfiguration(t *testing.T) {
	setConsumerEnv(t, "", "")
	t.Setenv("INVENTORY_MODE", "kafka")
	if _, _, err := startConsumers(memoryAdapters(), usecases.ZoneConfig{}, []string{"b:9092"}, true, discardLogger()); err == nil {
		t.Fatal("kafka mode without a group must refuse to boot")
	}
}

// A consumer in kafka mode runs in its own goroutine and stops, closing its
// reader and dead-letter writer, when the stop func is called. The broker is
// unreachable: the readers dial lazily, so boot never blocks on Kafka.
func TestStartConsumersRunsAndStopsEveryConsumer(t *testing.T) {
	setConsumerEnv(t, "kafka", "")
	t.Setenv("INVENTORY_CONSUMER_GROUP", "inv-g")
	t.Setenv("SLOTTING_CONSUMER_GROUP", "slot-g")
	t.Setenv("LAYOUT_CONSUMER_GROUP", "lay-g")
	logger, logs := quietLogger()

	workers, stop, err := startConsumers(memoryAdapters(), usecases.ZoneConfig{}, []string{"127.0.0.1:1"}, false, logger)
	if err != nil || len(workers) != 3 {
		t.Fatalf("workers = %d, %v; want the three consumers", len(workers), err)
	}
	stop()
	for _, w := range workers {
		select {
		case <-w.done:
		case <-time.After(10 * time.Second):
			t.Fatalf("worker %s did not stop", w.name)
		}
	}
	for _, want := range []string{"in-memory adapters", "inventory consumer", "slotting consumer", "layout consumer"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs lack %q:\n%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "consumer stopped") || strings.Contains(logs.String(), "close failed") {
		t.Fatalf("a cancelled consumer must stop quietly:\n%s", logs.String())
	}
}

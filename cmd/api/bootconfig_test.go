package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// setBootEnv sets a complete, valid mandatory environment; tests then blank
// one variable at a time.
func setBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1")
	setConsumerEnv(t, "", "")
	t.Setenv("INVENTORY_CONSUMER_GROUP", "inv-g")
	t.Setenv("SLOTTING_CONSUMER_GROUP", "slot-g")
	t.Setenv("LAYOUT_CONSUMER_GROUP", "lay-g")
	t.Setenv("PACK_LEVEL_CONSUMER_GROUP", "pack-g")
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	setBootEnv(t)
	cfg, err := loadBootConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.databaseURL == "" || cfg.migrationsURL != cfg.databaseURL || len(cfg.brokers) != 1 || len(cfg.consumers) != 4 {
		t.Fatalf("config = %+v", cfg)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", "postgres://direct")
	if cfg, _ := loadBootConfig(); cfg.migrationsURL != "postgres://direct" {
		t.Fatalf("the direct migrations DSN must win, got %q", cfg.migrationsURL)
	}
}

// Each mandatory variable is a boot error that names it.
func TestLoadBootConfigRefusesEachMissingVariable(t *testing.T) {
	for _, name := range []string{
		"DATABASE_URL", "KAFKA_BROKERS",
		"INVENTORY_CONSUMER_GROUP", "SLOTTING_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "PACK_LEVEL_CONSUMER_GROUP",
	} {
		t.Run(name, func(t *testing.T) {
			setBootEnv(t)
			t.Setenv(name, "")
			_, err := loadBootConfig()
			if err == nil || !strings.Contains(err.Error(), name+" is required") {
				t.Fatalf("%s unset = %v, want a boot error naming it", name, err)
			}
		})
	}
}

func TestLoadBootConfigRefusesPermissive(t *testing.T) {
	setBootEnv(t)
	t.Setenv("SLOTTING_MODE", "permissive")
	_, err := loadBootConfig()
	if err == nil || !strings.Contains(err.Error(), "SLOTTING_MODE=permissive is not supported: this context runs on real Kafka") {
		t.Fatalf("permissive = %v", err)
	}
}

func TestLoadBootConfigReportsEveryMissingVariableAtOnce(t *testing.T) {
	setBootEnv(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", " , ")
	_, err := loadBootConfig()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("both missing = %v", err)
	}
}

func TestBuildAdaptersRefusesAnEmptyDatabaseURL(t *testing.T) {
	_, closeAdapters, err := buildAdapters(context.Background(), "", "", discardLogger())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("empty DATABASE_URL = %v; there is no in-memory fallback", err)
	}
	closeAdapters()
}

// run fails fast on a bad configuration: before it opens a connection and
// before it binds the listener.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	setBootEnv(t)
	t.Setenv("DATABASE_URL", "")
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	start := time.Now()
	err := run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("run = %v, want a boot error naming DATABASE_URL", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a configuration error must not wait on any dependency")
	}
	if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
		_ = c.Close()
		t.Fatal("the listener was bound although the configuration is invalid")
	}
}

func TestConsumedAndProducedTopicsAreTheContractTopics(t *testing.T) {
	got := strings.Join(append(consumedTopics(), producedTopics()...), ",")
	want := "warehouse.inventory.events,warehouse.slotting-optimization.events,warehouse.facility.events," +
		"warehouse.pick-face-replenishment.events,warehouse.pick-face-replenishment.analytics"
	if got != want {
		t.Fatalf("topics = %s, want %s", got, want)
	}
}

func TestLogTopicsNamesWhatTheServiceConsumesAndProduces(t *testing.T) {
	logger, logs := quietLogger()
	logTopics(logger, []string{"b:9092"})
	for _, want := range []string{"warehouse.inventory.events", "warehouse.pick-face-replenishment.events", "never creates", "informational"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("topics log lacks %q: %s", want, logs.String())
		}
	}
}

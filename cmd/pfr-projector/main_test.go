package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(envOf(map[string]string{
		"ANALYTICS_DATABASE_URL":   "postgres://a",
		"KAFKA_BROKERS":            " b1:9092, b2:9092 ,",
		"ANALYTICS_CONSUMER_GROUP": "pfr-analytics",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.analyticsURL != "postgres://a" || strings.Join(cfg.brokers, "|") != "b1:9092|b2:9092" ||
		cfg.group != "pfr-analytics" || cfg.adminAddr != ":8091" || cfg.logLevel != "info" {
		t.Fatalf("cfg = %+v", cfg)
	}
	cfg, err = loadConfig(envOf(map[string]string{
		"ANALYTICS_DATABASE_URL": "x", "KAFKA_BROKERS": "b", "ANALYTICS_CONSUMER_GROUP": "g", "ADMIN_ADDR": ":9", "LOG_LEVEL": "debug",
	}))
	if err != nil || cfg.adminAddr != ":9" || cfg.logLevel != "debug" {
		t.Fatalf("overrides: cfg = %+v, err = %v", cfg, err)
	}
}

// Every missing mandatory variable is named, all together; there is no default
// consumer group and no in-memory mode.
func TestLoadConfigNamesEveryMissingVariable(t *testing.T) {
	_, err := loadConfig(envOf(nil))
	if err == nil {
		t.Fatal("an empty environment must be a boot error")
	}
	for _, name := range []string{"ANALYTICS_DATABASE_URL", "KAFKA_BROKERS", "ANALYTICS_CONSUMER_GROUP"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not name %s", err, name)
		}
	}
	_, err = loadConfig(envOf(map[string]string{"ANALYTICS_DATABASE_URL": "x", "KAFKA_BROKERS": "b"}))
	if err == nil || !strings.Contains(err.Error(), "ANALYTICS_CONSUMER_GROUP") || strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("err = %v, want only the missing group named", err)
	}
}

func TestAdminMuxReadinessFlipsOnShutdown(t *testing.T) {
	var notReady atomic.Bool
	mux := newAdminMux(&notReady)
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/healthz"); code != 200 || body != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %s", code, body)
	}
	if code, body := get("/readyz"); code != 200 || body != `{"status":"ready"}` {
		t.Fatalf("readyz = %d %s", code, body)
	}
	notReady.Store(true)
	if code, body := get("/readyz"); code != 503 || body != `{"status":"not_ready"}` {
		t.Fatalf("draining readyz = %d %s", code, body)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("liveness must not flip: %d", code)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for _, level := range []string{"debug", "WARN", "warning", "error", "", "bogus"} {
		if newLogger(level) == nil {
			t.Fatalf("no logger for %q", level)
		}
	}
}

// A consumer loop that returns, or an admin server that fails, is a non-zero
// exit; only a signal is a clean stop.
func TestAwaitStopConsumerLoopExitIsAnError(t *testing.T) {
	never := make(chan struct{})
	consumerDone := make(chan error, 1)
	adminErr := make(chan error, 1)

	consumerDone <- errors.New("commit failed")
	if err := awaitStop(never, consumerDone, adminErr); err == nil || !strings.Contains(err.Error(), "commit failed") {
		t.Fatalf("a dead consumer = %v, want an error carrying its cause", err)
	}
	consumerDone <- nil
	if err := awaitStop(never, consumerDone, adminErr); err == nil {
		t.Fatal("a consumer that returned nil is still an unexpected stop")
	}
	adminErr <- errors.New("listen failed")
	if err := awaitStop(never, consumerDone, adminErr); err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("a failed admin server = %v, want an error", err)
	}
	signalled := make(chan struct{})
	close(signalled)
	if err := awaitStop(signalled, consumerDone, adminErr); err != nil {
		t.Fatalf("a signal is the clean stop, got %v", err)
	}
}

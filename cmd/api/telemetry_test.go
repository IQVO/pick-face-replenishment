package main

import (
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/telemetry"
)

func TestLoadTelemetryEnvDefaults(t *testing.T) {
	got := loadTelemetryEnv(func(string) string { return "" })
	if got.serviceName != "pick-face-replenishment" {
		t.Errorf("serviceName = %q, want pick-face-replenishment", got.serviceName)
	}
	if got.serviceVersion != telemetry.DefaultServiceVersion {
		t.Errorf("serviceVersion = %q, want %q", got.serviceVersion, telemetry.DefaultServiceVersion)
	}
	if got.otlpEndpoint != telemetry.DefaultEndpoint {
		t.Errorf("otlpEndpoint = %q, want %q", got.otlpEndpoint, telemetry.DefaultEndpoint)
	}
}

func TestLoadTelemetryEnvOverrides(t *testing.T) {
	env := map[string]string{
		"OTEL_SERVICE_NAME":           "custom",
		"SERVICE_VERSION":             "1.2.3",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
	}
	got := loadTelemetryEnv(func(k string) string { return env[k] })
	if got.serviceName != "custom" || got.serviceVersion != "1.2.3" || got.otlpEndpoint != "http://collector:4317" {
		t.Errorf("overrides not honoured: %+v", got)
	}
}

func TestStartTelemetryFlushesWithoutCollector(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	flush, err := startTelemetry(newLogger("error"), loadTelemetryEnv(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("startTelemetry: %v", err)
	}
	flush()
}

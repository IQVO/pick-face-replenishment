package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	outboxrelay "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/outbox"
)

func quietLogger() (*slog.Logger, *strings.Builder) {
	var out strings.Builder
	return slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})), &out
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestGetenvFallsBack(t *testing.T) {
	t.Setenv("PFR_TEST_SET", "x")
	t.Setenv("PFR_TEST_EMPTY", "")
	if getenv("PFR_TEST_SET", "d") != "x" || getenv("PFR_TEST_EMPTY", "d") != "d" || getenv("PFR_TEST_UNSET_XYZ", "d") != "d" {
		t.Fatal("getenv must return the value when set and non-empty, the fallback otherwise")
	}
}

func TestParseZoneCodes(t *testing.T) {
	cases := map[string][]string{
		"":              nil,
		" , ,":          nil,
		"FWD":           {"FWD"},
		"FWD, PICK ,A1": {"FWD", "PICK", "A1"},
	}
	for raw, want := range cases {
		got := parseZoneCodes(raw)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("parseZoneCodes(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestZoneConfigFromEnvKeepsTheDefaultsWhenUnset(t *testing.T) {
	t.Setenv("FORWARD_ZONE_CODES", "")
	t.Setenv("RESERVE_ZONE_CODES", "")
	if z := zoneConfigFromEnv(); z.Forward != nil || z.Reserve != nil {
		t.Fatalf("unset env must leave the defaults to usecases.ZoneConfig, got %+v", z)
	}
	t.Setenv("FORWARD_ZONE_CODES", "FWD,PICK")
	t.Setenv("RESERVE_ZONE_CODES", "RSV,BULK")
	z := zoneConfigFromEnv()
	if strings.Join(z.Forward, ",") != "FWD,PICK" || strings.Join(z.Reserve, ",") != "RSV,BULK" {
		t.Fatalf("zones = %+v", z)
	}
}

func TestParseRelayInterval(t *testing.T) {
	logger, logs := quietLogger()
	cases := map[string]time.Duration{
		"":      outboxrelay.DefaultInterval,
		"250ms": 250 * time.Millisecond,
		"2":     2 * time.Second,
		"0.5":   500 * time.Millisecond,
		"-1s":   outboxrelay.DefaultInterval,
		"0":     outboxrelay.DefaultInterval,
		"soon":  outboxrelay.DefaultInterval,
	}
	for raw, want := range cases {
		if got := parseRelayInterval(raw, logger); got != want {
			t.Fatalf("parseRelayInterval(%q) = %v, want %v", raw, got, want)
		}
	}
	if !strings.Contains(logs.String(), "invalid OUTBOX_RELAY_INTERVAL") {
		t.Fatal("an invalid interval must be logged")
	}
}

func TestOutboxMaxAttempts(t *testing.T) {
	logger, logs := quietLogger()
	cases := map[string]int{
		"":    outboxrelay.DefaultMaxAttempts,
		"20":  20,
		"5":   5,
		"7":   7,
		"0":   0, // unlimited is a deliberate choice
		"-1":  outboxrelay.DefaultMaxAttempts,
		"abc": outboxrelay.DefaultMaxAttempts,
	}
	for raw, want := range cases {
		if got := outboxMaxAttempts(raw, logger); got != want {
			t.Fatalf("outboxMaxAttempts(%q) = %d, want %d", raw, got, want)
		}
	}
	if !strings.Contains(logs.String(), "invalid OUTBOX_MAX_ATTEMPTS") {
		t.Fatal("an invalid cap must be logged, never silently dropped")
	}
	if outboxrelay.DefaultMaxAttempts != 20 {
		t.Fatalf("the fleet default cap is 20, got %d", outboxrelay.DefaultMaxAttempts)
	}
}

func TestShutdownDrainDelay(t *testing.T) {
	logger := discardLogger()
	cases := map[string]time.Duration{"": DefaultShutdownDrainDelay, "0": 0, "3s": 3 * time.Second, "-1s": DefaultShutdownDrainDelay, "x": DefaultShutdownDrainDelay}
	for raw, want := range cases {
		t.Setenv("SHUTDOWN_DRAIN_DELAY", raw)
		if got := shutdownDrainDelay(logger); got != want {
			t.Fatalf("SHUTDOWN_DRAIN_DELAY=%q -> %v, want %v", raw, got, want)
		}
	}
}

func TestBrokersFromEnv(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")
	if got := brokersFromEnv(); len(got) != 0 {
		t.Fatalf("unset brokers = %v", got)
	}
	t.Setenv("KAFKA_BROKERS", " a:9092, b:9092 ,,")
	if got := brokersFromEnv(); strings.Join(got, "|") != "a:9092|b:9092" {
		t.Fatalf("brokers = %v", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug, "warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "info": slog.LevelInfo, "": slog.LevelInfo, "bogus": slog.LevelInfo,
	} {
		l := newLogger(level)
		for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			if enabled := l.Enabled(t.Context(), lvl); enabled != (lvl >= want) {
				t.Fatalf("LOG_LEVEL=%q: level %v enabled=%v, want %v", level, lvl, enabled, lvl >= want)
			}
		}
	}
}

func TestMigrationsDatabaseURLPrefersTheDirectDSN(t *testing.T) {
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	if got := migrationsDatabaseURL("postgres://pooled"); got != "postgres://pooled" {
		t.Fatalf("fallback = %q", got)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", "postgres://direct")
	if got := migrationsDatabaseURL("postgres://pooled"); got != "postgres://direct" {
		t.Fatalf("direct = %q", got)
	}
}

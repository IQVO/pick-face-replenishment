package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	outboxrelay "github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive; "warning" is accepted like "warn" fleet-wide).
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// parseZoneCodes reads a comma-separated list of zone codes; an empty list is
// nil so usecases.ZoneConfig falls back to its default (FWD / RSV).
func parseZoneCodes(raw string) []string {
	var codes []string
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			codes = append(codes, c)
		}
	}
	return codes
}

// zoneConfigFromEnv reads FORWARD_ZONE_CODES (default FWD) and
// RESERVE_ZONE_CODES (default RSV).
func zoneConfigFromEnv() usecases.ZoneConfig {
	return usecases.ZoneConfig{
		Forward: parseZoneCodes(os.Getenv("FORWARD_ZONE_CODES")),
		Reserve: parseZoneCodes(os.Getenv("RESERVE_ZONE_CODES")),
	}
}

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL (Go duration or plain
// seconds), defaulting when unset, malformed or non-positive.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row
// may fail before the relay excludes it from claims. Unset = the fleet default
// (20); 0 = unlimited; negative or unparsable values log a warning and use the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// DefaultShutdownDrainDelay is how long shutdown waits, after flipping
// /readyz to not-ready, before closing the listener (SHUTDOWN_DRAIN_DELAY
// overrides it; "0" disables it).
const DefaultShutdownDrainDelay = 5 * time.Second

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset,
// negative or unparsable = the default).
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return DefaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", DefaultShutdownDrainDelay.String())
		return DefaultShutdownDrainDelay
	}
	return d
}

// brokersFromEnv reads KAFKA_BROKERS (comma separated); nil when unset.
func brokersFromEnv() []string {
	var brokers []string
	for _, b := range strings.Split(os.Getenv("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	return brokers
}

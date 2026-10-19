package main

import (
	"strings"
	"testing"
)

// The reports binary has no fallback store: without the analytical DSN it
// refuses to boot, naming the variable.
func TestRunRefusesWithoutTheAnalyticsDatabase(t *testing.T) {
	t.Setenv("ANALYTICS_DATABASE_URL", "")
	err := run()
	if err == nil || !strings.Contains(err.Error(), "ANALYTICS_DATABASE_URL") {
		t.Fatalf("run() = %v, want a boot error naming ANALYTICS_DATABASE_URL", err)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("PFR_REPORTS_TEST_SET", "v")
	t.Setenv("PFR_REPORTS_TEST_EMPTY", "")
	if got := getenv("PFR_REPORTS_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("PFR_REPORTS_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for _, level := range []string{"debug", "WARN", "warning", "error", "", "bogus"} {
		if newLogger(level) == nil {
			t.Fatalf("no logger for %q", level)
		}
	}
}

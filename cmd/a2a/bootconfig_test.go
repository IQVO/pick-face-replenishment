package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("A2A_AGENT_URL", "http://pick-face-replenishment-a2a.warehouse-systems.svc.cluster.local:8093/")
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	setBootEnv(t)
	cfg, err := loadBootConfig()
	if err != nil || cfg.databaseURL == "" || cfg.agentURL == "" || cfg.migrationsURL != cfg.databaseURL {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", "postgres://direct@127.0.0.1:1/db")
	cfg, err = loadBootConfig()
	if err != nil || cfg.migrationsURL != "postgres://direct@127.0.0.1:1/db" {
		t.Fatalf("migrations url = %+v, %v", cfg, err)
	}
	t.Setenv("A2A_AGENT_URL", "https://gw.example/api/a2a/pick-face-replenishment/")
	if _, err := loadBootConfig(); err != nil {
		t.Fatalf("an https agent url must be accepted: %v", err)
	}
}

// Each missing or rejected value is a boot error that names its variable.
func TestLoadBootConfigRefusesEachMissingOrRejectedValue(t *testing.T) {
	cases := []struct {
		name, variable, value, want string
	}{
		{"database unset", "DATABASE_URL", "", "DATABASE_URL is required: this context runs on real Postgres"},
		{"agent url unset", "A2A_AGENT_URL", "", "A2A_AGENT_URL is required: the Agent Card must advertise"},
		{"agent url blank", "A2A_AGENT_URL", "   ", "A2A_AGENT_URL is required"},
		{"agent url relative", "A2A_AGENT_URL", "/a2a", `A2A_AGENT_URL="/a2a" is not an absolute http(s) URL`},
		{"agent url schemeless", "A2A_AGENT_URL", "pick-face-replenishment-a2a:8093", "is not an absolute http(s) URL"},
		{"agent url wrong scheme", "A2A_AGENT_URL", "ftp://host/", "is not an absolute http(s) URL"},
		{"agent url without host", "A2A_AGENT_URL", "http:///a2a", "is not an absolute http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBootEnv(t)
			t.Setenv(tc.variable, tc.value)
			_, err := loadBootConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s=%q: %v, want a boot error containing %q", tc.variable, tc.value, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.variable) {
				t.Fatalf("the error does not name %s: %v", tc.variable, err)
			}
		})
	}
}

// run fails fast on a bad configuration: before any connection and before the
// listener binds.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	setBootEnv(t)
	t.Setenv("A2A_AGENT_URL", "")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("A2A_ADDR", addr)

	start := time.Now()
	if err := run(); err == nil || !strings.Contains(err.Error(), "A2A_AGENT_URL is required") {
		t.Fatalf("run = %v, want a boot error naming A2A_AGENT_URL", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a configuration error must not wait on any dependency")
	}
	if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
		_ = c.Close()
		t.Fatal("the listener was bound although the configuration is invalid")
	}
}

// The a2a binary has no in-memory mode (ADR 0006): a missing DATABASE_URL is a
// boot error naming it.
func TestBuildDepsRefusesWithoutADatabase(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "", usecases.ZoneConfig{})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want a boot error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDepsBadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	url := "postgres://u@127.0.0.1:1/db"
	if _, _, err := buildDeps(ctx, quietLogger(), url, url, usecases.ZoneConfig{}); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

func TestZoneConfigFromEnv(t *testing.T) {
	t.Setenv("FORWARD_ZONE_CODES", " F1, F2 ,")
	t.Setenv("RESERVE_ZONE_CODES", "")
	z := zoneConfigFromEnv()
	if strings.Join(z.Forward, "|") != "F1|F2" || z.Reserve != nil {
		t.Fatalf("zones = %+v, want forward F1|F2 and the default reserve", z)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("PFR_A2A_TEST_SET", "v")
	t.Setenv("PFR_A2A_TEST_EMPTY", "")
	if got := getenv("PFR_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("PFR_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}

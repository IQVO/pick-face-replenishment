// Command a2a is the composition root for the pick-face-replenishment A2A
// (Agent2Agent) agent (docs/adr/0010-a2a-inbound-adapter.md): it wires env
// config to the Postgres repositories, the repositories to the SAME read use
// cases the REST and MCP adapters use (ListPickFaces, ListRequests,
// GetRequest), and those to the inbound A2A adapter, then serves the Agent
// Card at /.well-known/agent-card.json and the A2A JSON-RPC endpoint at both
// "/" and "/a2a". It is a third, independent deployable alongside cmd/api and
// cmd/mcp; A2A carries DELEGATION of a goal, never data sync (facts stay
// CloudEvents on Kafka).
//
// The agent is READ-ONLY, like cmd/mcp (the write gate: MCP offers no write,
// so neither does A2A): no write use case is wired here at all, so this binary
// can never insert into the outbox (it builds no event sink, no unit of work
// and no outbox repository, hence no event publisher). It never starts the
// outbox relay, never runs the local-copy consumers and never dials Kafka (no
// KAFKA_BROKERS, no consumer groups, no *_MODE): those run in cmd/api only.
// Like cmd/api it has no in-memory mode (ADR 0006): without DATABASE_URL it
// refuses to boot.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	DATABASE_URL             REQUIRED. Postgres DSN
//	A2A_AGENT_URL            REQUIRED. Absolute http(s) URL of this agent's
//	                         JSON-RPC endpoint, advertised on the Agent Card
//	                         (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	A2A_ADDR                 listen address (default :8093)
//	FORWARD_ZONE_CODES       comma-separated zone codes of forward pick faces (default FWD)
//	RESERVE_ZONE_CODES       comma-separated zone codes of reserve bins (default RSV)
//	LOG_LEVEL                debug | info | warn | error (default info)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inbounda2a "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/a2a"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// cardVersion is the version advertised on the Agent Card, like the MCP
// server's serverVersion: the card is a contract, not a build id.
const cardVersion = "1.0.0"

// errNoDatabase is the boot error of a missing DATABASE_URL.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

// errNoAgentURL is the boot error of a missing A2A_AGENT_URL: the Agent Card
// must advertise a URL agent clients can dial, so there is no default.
var errNoAgentURL = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL of this agent's JSON-RPC endpoint (ADR 0010), e.g. http://<release>-a2a.<ns>.svc.cluster.local:8093/")

// bootConfig is the validated boot configuration, loaded before any
// connection is made or the listener binds.
type bootConfig struct {
	databaseURL   string
	migrationsURL string
	agentURL      string
}

// checkAgentURL validates A2A_AGENT_URL: an absolute http(s) URL with a host.
func checkAgentURL(raw string) error {
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL: the Agent Card must advertise the endpoint clients dial (ADR 0010)", raw)
	}
	return nil
}

// loadBootConfig reads and validates the required variables; every error
// names its variable.
func loadBootConfig() (bootConfig, error) {
	cfg := bootConfig{
		databaseURL: os.Getenv("DATABASE_URL"),
		agentURL:    strings.TrimSpace(os.Getenv("A2A_AGENT_URL")),
	}
	if cfg.databaseURL == "" {
		return bootConfig{}, errNoDatabase
	}
	if err := checkAgentURL(cfg.agentURL); err != nil {
		return bootConfig{}, err
	}
	cfg.migrationsURL = getenv(postgres.EnvMigrationsDatabaseURL, cfg.databaseURL)
	return cfg, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("a2a server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	deps, closeAdapters, err := buildDeps(context.Background(), logger, cfg.databaseURL, cfg.migrationsURL, zoneConfigFromEnv())
	if err != nil {
		return err
	}
	defer closeAdapters()

	card := inbounda2a.Card(inbounda2a.Config{AgentURL: cfg.agentURL, Version: cardVersion})
	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveA2A(logger, srv)
}

// buildDeps migrates and connects Postgres and wires the READ use cases over
// it. The returned close func releases the pool. An empty databaseURL is a
// boot error: there is no fallback runtime (ADR 0006).
//
// Like cmd/mcp this binary runs the idempotent embedded
// migrations on start, so it can boot against a fresh database before cmd/api
// has; golang-migrate's advisory lock makes concurrent starts safe.
// migrationsDatabaseURL is used ONLY for that step (a DIRECT Postgres DSN where
// DATABASE_URL points at PgBouncer, which cannot honour the session-scoped
// advisory lock); the runtime pool always uses databaseURL.
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string, zones usecases.ZoneConfig) (inbounda2a.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inbounda2a.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inbounda2a.Deps{}, noop, err
	}
	logger.Info("postgres repositories configured")

	policies, requests, read := postgres.NewPolicyRepo(pool), postgres.NewRequestRepo(pool), postgres.NewReadModel(pool)
	// The detector runs read-only here: no event sink, id generator or clock.
	eval := usecases.Evaluator{Policies: policies, Requests: requests, Read: read, Zones: zones}
	return inbounda2a.Deps{
		ListPickFaces: &usecases.ListPickFaces{Eval: eval},
		ListRequests:  &usecases.ListRequests{Requests: requests},
		GetRequest:    &usecases.GetRequest{Requests: requests},
	}, pool.Close, nil
}

// serveA2A runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveA2A(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("a2a server listening (JSON-RPC)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// zoneConfigFromEnv reads FORWARD_ZONE_CODES (default FWD) and
// RESERVE_ZONE_CODES (default RSV), as cmd/api does.
func zoneConfigFromEnv() usecases.ZoneConfig {
	return usecases.ZoneConfig{
		Forward: parseZoneCodes(os.Getenv("FORWARD_ZONE_CODES")),
		Reserve: parseZoneCodes(os.Getenv("RESERVE_ZONE_CODES")),
	}
}

// parseZoneCodes reads a comma-separated list; an empty list is nil so
// usecases.ZoneConfig falls back to its default.
func parseZoneCodes(raw string) []string {
	var codes []string
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			codes = append(codes, c)
		}
	}
	return codes
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|warning|error (case-insensitive; "warning" is accepted
// like "warn" fleet-wide), defaulting to Info.
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

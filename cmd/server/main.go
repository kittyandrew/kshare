// kshared: the kshare HTTP server. Topology in .claude/rules/001-architecture.md, production wiring in
// docs/deployment.md. The route table is in server.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/api"
	"github.com/kittyandrew/kshare/internal/store"
)

// Set by ldflags in nix/kshare.nix: Version is CalVer vYY.MM from the flake's source date, Commit the short
// git rev. Bare `go build` leaves both empty, which is how the boot log flags a non-Nix build.
var (
	Commit  string
	Version string
)

func main() {
	rootLog := newRootLogger()
	ctx := rootLog.WithContext(context.Background())
	if err := run(ctx, rootLog); err != nil {
		rootLog.Fatal().Err(err).Msg("server exited")
	}
}

// run is the boot path: env reads, container init, sweeper, listener. Returns on the first listener error or
// a signal-triggered shutdown.
func run(ctx context.Context, log zerolog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Hard-fail at boot, 10s ceiling: a misconfigured issuer should be loud, not deferred to first upload.
	authCtx, authCancel := context.WithTimeout(ctx, 10*time.Second)
	defer authCancel()
	auth, err := newAuthenticator(authCtx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		return fmt.Errorf("oidc init: %w", err)
	}

	container, err := store.NewContainer(ctx, store.Config{
		DataDir: cfg.DataDir,
		Logger:  log,
	})
	if err != nil {
		return fmt.Errorf("store init: %w", err)
	}
	defer func() { _ = container.Close() }()

	// Boot reap, in order: stale partials, files with no row, rows with no file. Each store method documents
	// the crash window it closes. The 5 min floor only shields a previous process still draining, since this
	// one has not started its listener yet.
	if removed, err := container.ReapPartials(5 * time.Minute); err != nil {
		log.Warn().Err(err).Msg("boot: reap partials failed")
	} else if removed > 0 {
		log.Info().Int("removed", removed).Msg("boot: reaped stale .partial files")
	}
	if removed, err := container.ReconcileFiles(ctx); err != nil {
		log.Warn().Err(err).Msg("boot: reconcile files failed")
	} else if removed > 0 {
		log.Info().Int("removed", removed).Msg("boot: reaped rowless files")
	}
	if removed, err := container.ReconcileRows(ctx); err != nil {
		log.Warn().Err(err).Msg("boot: reconcile rows failed")
	} else if removed > 0 {
		log.Info().Int("removed", removed).Msg("boot: reaped rows with missing files")
	}

	// Sweep cadence == MinTTL: expiry filtering in SQL already hides expired rows from readers, so sweep
	// latency only bounds how long an expired file lingers on disk, at most MinTTL.
	sweeperCtx, sweeperCancel := context.WithCancel(ctx)
	defer sweeperCancel()
	sweeperDone := make(chan struct{})
	go func() {
		container.RunSweeper(sweeperCtx, cfg.MinTTL)
		close(sweeperDone)
	}()

	srv := newServer(cfg, auth, container, log)

	log.Info().
		Str("listen", cfg.Listen).
		Str("data_dir", cfg.DataDir).
		Str("oidc_issuer", cfg.OIDCIssuer).
		Str("oidc_audience", cfg.OIDCAudience).
		Int64("max_upload_size", cfg.MaxUploadSize).
		Dur("default_ttl", cfg.DefaultTTL).
		Dur("min_ttl", cfg.MinTTL).
		Dur("max_ttl", cfg.MaxTTL).
		Dur("sweep_interval", cfg.MinTTL).
		Str("commit", Commit).
		Str("version", Version).
		Msg("kshared listening")

	// Listener runs in its own goroutine so main can block on signals. ListenAndServe returns
	// http.ErrServerClosed on graceful shutdown; treat that as a normal exit.
	listenErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		} else {
			listenErr <- nil
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-listenErr:
		return err
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("shutdown requested")
		return gracefulShutdown(srv, listenErr, sweeperCancel, sweeperDone, log)
	}
}

// gracefulShutdown drains in-flight requests and the sweeper after a SIGINT/SIGTERM. Returns nil on a clean
// drain, an error if srv.Shutdown fails. Ordering:
//  1. http.Server.Shutdown: stops accepting, lets in-flight requests finish. Bounded at 30s.
//  2. Wait for the listener goroutine to drain so we don't leak it.
//  3. Cancel and wait for the sweeper. Draining it prevents a torn DELETE-then-os.Remove from leaking an
//     orphan file across restart.
func gracefulShutdown(srv *http.Server, listenErr <-chan error, sweeperCancel context.CancelFunc, sweeperDone <-chan struct{}, log zerolog.Logger) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	<-listenErr
	sweeperCancel()
	select {
	case <-sweeperDone:
	case <-time.After(10 * time.Second):
		log.Warn().Msg("sweeper drain timed out; proceeding to exit")
	}
	return nil
}

// config is the boot-time settings bundle. All env reads happen in loadConfig; everything else takes a
// *config or specific fields. No CleanupInterval field: the sweeper's cadence derives from MinTTL (see run).
type config struct {
	Listen        string
	DataDir       string
	OIDCIssuer    string
	OIDCAudience  string
	MaxUploadSize int64
	DefaultTTL    time.Duration
	MinTTL        time.Duration
	MaxTTL        time.Duration
}

// loadConfig reads env and applies defaults. Issuer and audience are checked once here, so the rest of the
// boot path can assume they are non-empty. See .env.example for the full list of optional knobs.
func loadConfig() (*config, error) {
	cfg := &config{
		Listen: envOr("KSHARE_LISTEN", ":6980"),
		// `./data` not `/data`: bare `go run` outside the container cannot write /data without root. The
		// OCI image sets KSHARE_DATA=/data explicitly (nix/kshare.nix).
		DataDir:      envOr("KSHARE_DATA", "./data"),
		OIDCIssuer:   strings.TrimSpace(strings.TrimRight(os.Getenv("KSHARE_OIDC_ISSUER"), "/")),
		OIDCAudience: strings.TrimSpace(os.Getenv("KSHARE_OIDC_AUDIENCE")),
	}
	if cfg.OIDCIssuer == "" {
		return nil, errors.New("KSHARE_OIDC_ISSUER is required")
	}
	if cfg.OIDCAudience == "" {
		return nil, errors.New("KSHARE_OIDC_AUDIENCE is required")
	}

	var err error
	if cfg.MaxUploadSize, err = envInt64("KSHARE_MAX_UPLOAD_SIZE", 104857600); err != nil {
		return nil, err
	}
	if cfg.MaxUploadSize <= 0 {
		return nil, fmt.Errorf("KSHARE_MAX_UPLOAD_SIZE must be > 0; got %d", cfg.MaxUploadSize)
	}
	if cfg.DefaultTTL, err = envTTL("KSHARE_DEFAULT_TTL", 7*24*time.Hour); err != nil {
		return nil, err
	}
	if cfg.MinTTL, err = envTTL("KSHARE_MIN_TTL", 10*time.Minute); err != nil {
		return nil, err
	}
	if cfg.MaxTTL, err = envTTL("KSHARE_MAX_TTL", 365*24*time.Hour); err != nil {
		return nil, err
	}
	if cfg.MinTTL > cfg.MaxTTL {
		return nil, fmt.Errorf("KSHARE_MIN_TTL (%s) cannot exceed MAX_TTL (%s)", cfg.MinTTL, cfg.MaxTTL)
	}
	if cfg.DefaultTTL < cfg.MinTTL || cfg.DefaultTTL > cfg.MaxTTL {
		return nil, fmt.Errorf("KSHARE_DEFAULT_TTL (%s) must be within [MIN_TTL=%s, MAX_TTL=%s]",
			cfg.DefaultTTL, cfg.MinTTL, cfg.MaxTTL)
	}
	return cfg, nil
}

// newRootLogger configures the process-wide zerolog root: stderr sink, snake_case keys, RFC3339Nano stamps.
// JSON unless KSHARE_LOG_FORMAT=console.
func newRootLogger() zerolog.Logger {
	zerolog.TimestampFieldName = "ts"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "msg"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	level := zerolog.InfoLevel
	if v := os.Getenv("KSHARE_LOG_LEVEL"); v != "" {
		if parsed, err := zerolog.ParseLevel(strings.ToLower(v)); err == nil {
			level = parsed
		}
	}

	if os.Getenv("KSHARE_LOG_FORMAT") == "console" {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			Level(level).With().Timestamp().Str("svc", "kshared").Logger()
	}
	return zerolog.New(os.Stderr).
		Level(level).With().Timestamp().Str("svc", "kshared").Logger()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envInt64 errors on a malformed value rather than degrading: bad config should fail boot.
func envInt64(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse int64: %w", key, err)
	}
	return n, nil
}

// envTTL parses a duration from $key via api.ParseTTL, which adds `d`/`w` suffixes to the stdlib's h/m/s, so
// an operator can write `KSHARE_MAX_TTL=365d` instead of `8760h`.
func envTTL(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	d, err := api.ParseTTL(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// verdictd — the HTTP service wrapper over the pipeline (v3): submit
// evidence-assessment runs over HTTP, watch status, fetch artifacts.
// The CLI stays the primary interface; both share the client assembly
// (cmd/internal/app). One run executes at a time — LLM concurrency lives
// inside the pipeline stages. Jobs are kept in runs/verdict.db, next to the
// run index.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/db"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/server"
)

const (
	// readHeaderTimeout and idleTimeout bound slow or abandoned connections;
	// there is deliberately no write timeout — artifact responses can be large.
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	// shutdownTimeout is how long in-flight HTTP requests get to finish.
	shutdownTimeout = 30 * time.Second

	runsDir = "runs"
)

// version is stamped by the Makefile (-ldflags "-X main.version=...").
var version = app.DevVersion

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "localhost:8080", "listen address")

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A .env in the working directory fills variables that are not set yet.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loading .env: %w", err)
	}

	var cfg config.Config
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("parsing the environment: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	log := app.NewLogger(os.Stderr, &cfg)
	log.Info("verdictd starting", "version", app.Version(version))

	store, err := db.Open(ctx, filepath.Join(runsDir, "verdict.db"))
	if err != nil {
		return err
	}

	defer func() {
		if err := store.Close(); err != nil {
			log.Warn("closing the database failed", "err", err)
		}
	}()

	runner := func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		p, err := app.BuildPipeline(&cfg, log, opts)
		if err != nil {
			return nil, "", err
		}

		return p.Run(ctx, opts)
	}

	svc, err := server.New(ctx, runner, store.Jobs(time.Now), runsDir, log)
	if err != nil {
		return err
	}

	return serve(ctx, log, svc, *addr)
}

// serve runs the worker and the HTTP server until ctx is canceled or one of
// them fails, then shuts the listener down and lets the worker interrupt the
// running job.
func serve(ctx context.Context, log *slog.Logger, svc *server.Server, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           svc.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	log.Info("verdictd listening", "addr", addr,
		"endpoints", "POST /runs, GET /runs, GET /runs/{id}, GET /runs/{id}/{artifact}")

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		svc.Work(gctx)
		return nil
	})
	g.Go(func() error {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving HTTP: %w", err)
		}

		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down: stopping the listener, interrupting the running job")

		// gctx is already canceled; shutdown gets its own deadline.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(gctx), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(sctx); err != nil {
			return fmt.Errorf("shutting down the HTTP server: %w", err)
		}

		return nil
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("running the daemon: %w", err)
	}

	return nil
}

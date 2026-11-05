// verdict — an orchestrator for evidence assessment of scientific claims:
// discovery (Consensus API / paste, OpenAlex) → smart citations and
// retraction checks (Scite/OpenAlex) → LLM verdict with an evidence matrix.
//
// main.go parses the environment and wires the commands; each command lives
// in its own file and delegates its logic to internal/.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"

	"github.com/roman-redl/verdict/cmd/internal/app"
	"github.com/roman-redl/verdict/internal/config"
)

// errReported marks a failure cobra has already printed (error and usage).
var errReported = errors.New("command failed")

// version is stamped by the Makefile (-ldflags "-X main.version=...").
var version = app.DevVersion

func main() {
	if err := run(); err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(os.Stderr, err)
		}

		os.Exit(1)
	}
}

func run() error {
	// A .env in the working directory fills variables that are not set yet,
	// so `bin/verdict ...` works without a make wrapper.
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
	log.Info("verdict starting", "version", app.Version(version))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		// the first signal cancels the work; a second one gets the default
		// behavior back and kills a stuck process
		<-ctx.Done()
		stop()
	}()

	c := &cli{cfg: &cfg, log: log, now: time.Now}

	if err := c.root().ExecuteContext(ctx); err != nil {
		return errReported
	}

	return nil
}

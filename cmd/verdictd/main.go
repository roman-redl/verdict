// verdictd — the HTTP service wrapper over the pipeline (v3): submit
// evidence-assessment runs over HTTP, watch status, fetch artifacts.
// The CLI stays the primary interface; both share the client assembly
// (internal/app). One run executes at a time — LLM concurrency lives
// inside the pipeline stages.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/roman-redl/verdict/internal/app"
	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/server"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	runner := func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		cfg := config.Load()
		clients, err := app.BuildClients(cfg, log, opts)
		if err != nil {
			return nil, "", err
		}
		return pipeline.New(cfg, clients, log).Run(ctx, opts)
	}

	handler, drain := server.New(runner)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Info("shutting down: draining the running job")
		drain()
		os.Exit(0)
	}()

	log.Info("verdictd listening", "addr", *addr,
		"endpoints", "POST /runs, GET /runs, GET /runs/{id}, GET /runs/{id}/{artifact}")
	if err := http.ListenAndServe(*addr, handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

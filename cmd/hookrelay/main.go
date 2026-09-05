// Command hookrelay is the single binary: HTTP API, dispatcher, and worker pool.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akshar27/hookrelay/internal/api"
	"github.com/akshar27/hookrelay/internal/breaker"
	"github.com/akshar27/hookrelay/internal/config"
	"github.com/akshar27/hookrelay/internal/dispatch"
	"github.com/akshar27/hookrelay/internal/obs"
	"github.com/akshar27/hookrelay/internal/secretbox"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/worker"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	if err := run(); err != nil {
		obs.NewLogger("development").Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := obs.NewLogger(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied")

	keyB64 := cfg.SecretKey
	if keyB64 == "" {
		keyB64 = secretbox.GenerateKey()
		log.Warn("HOOKRELAY_SECRET_KEY not set — using an ephemeral key; sealed secrets won't survive a restart")
	}
	box, err := secretbox.New(keyB64)
	if err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	metrics := obs.NewMetrics(reg)

	dispatcher := dispatch.New(st, log)
	go dispatcher.Run(ctx)

	breakers := breaker.NewRegistry()
	pool := worker.New(st, box, dispatcher, log, worker.Options{Breakers: breakers, Metrics: metrics})
	go pool.Run(ctx)

	apiServer, err := api.New(cfg, api.Deps{
		Store: st, Secrets: box, Breakers: breakers, Notifier: dispatcher,
		Metrics: metrics, Registry: reg, Log: log,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

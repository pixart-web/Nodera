// Nodera Core API entrypoint. This file only wires dependencies together —
// business logic lives in internal/<domain> packages (ADR-002).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/platform/db"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.Env)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("connected to database")

	migs, err := migrations.Load()
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx, pool, migs); err != nil {
		return err
	}
	log.Info("migrations applied", "count", len(migs))

	rbacSvc := rbac.New(pool)
	auditSvc := audit.New(pool)
	identitySvc := identity.New(pool, rbacSvc, cfg.Auth.SessionTTL)
	tenancySvc := tenancy.New(pool)
	infraSvc := infrastructure.New(pool, auditSvc)

	deps := apiDeps{
		log:      log,
		identity: identitySvc,
		tenancy:  tenancySvc,
		audit:    auditSvc,
		infra:    infraSvc,
		pool:     pool,
	}

	handler := newRouter(deps)

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

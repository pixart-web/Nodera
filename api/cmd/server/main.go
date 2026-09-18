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

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/ai/providers/ollama"
	"github.com/nodera/nodera/internal/applications"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/platform/db"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platform/ratelimit"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/secrets"
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
	appsSvc := applications.New(pool, auditSvc)
	jobsSvc := jobs.New(pool)

	registeredProviders := []providers.Provider{localecho.New()}
	if cfg.Ollama.BaseURL != "" {
		registeredProviders = append(registeredProviders, ollama.New("ollama", cfg.Ollama.BaseURL))
	}
	aiSvc := ai.New(pool, auditSvc, registeredProviders...)

	if cfg.Ollama.BaseURL != "" {
		// Auto-register the provider row so it shows up in the registry
		// without a manual POST /api/v1/ai/providers call — the Go adapter
		// above is what actually makes it callable; this just makes it
		// discoverable. System actor bypasses the per-org permission check
		// (rbac.Require) since this isn't derived from a client request.
		if _, err := aiSvc.UpsertProvider(ctx, authctx.System(uuid.Nil), ai.UpsertProviderInput{
			Key: "ollama", Kind: "local", DisplayName: "Ollama (local)", Status: "active",
		}); err != nil {
			log.Error("failed to auto-register ollama provider", "error", err)
		} else {
			log.Info("ollama provider registered", "base_url", cfg.Ollama.BaseURL)
		}
	}

	// The secrets module is optional at the config level (rule 36: report
	// "not configured" rather than fabricate or crash) — a fresh local
	// clone can run the whole rest of the API with no encryption key set.
	var secretsSvc *secrets.Service
	if cfg.Secrets.EncryptionKeyBase64 == "" {
		log.Warn("secrets module disabled: NODERA_SECRETS_ENCRYPTION_KEY is not set")
	} else {
		secretsSvc, err = secrets.New(pool, auditSvc, cfg.Secrets.EncryptionKeyBase64)
		if err != nil {
			return err
		}
	}

	worker := jobs.NewWorker(pool)
	// No handlers are registered yet (docs/ROADMAP.md: "jobs worker" ships
	// the dispatcher itself in this pass; concrete job types like
	// backup.create arrive with the domains that need them). An enqueued
	// job with no matching handler fails visibly rather than hanging.
	go worker.Run(ctx)
	log.Info("job worker started")

	deps := apiDeps{
		log:         log,
		identity:    identitySvc,
		tenancy:     tenancySvc,
		audit:       auditSvc,
		infra:       infraSvc,
		apps:        appsSvc,
		jobs:        jobsSvc,
		ai:          aiSvc,
		secrets:     secretsSvc,
		pool:        pool,
		loginRate:   ratelimit.New(5, 5*time.Minute),
		corsOrigins: cfg.HTTP.CORSOrigins,
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

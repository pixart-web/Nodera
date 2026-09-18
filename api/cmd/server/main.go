// Nodera Core API entrypoint. This file only wires dependencies together —
// business logic lives in internal/<domain> packages (ADR-002).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/agents"
	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/anthropic"
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
	"github.com/nodera/nodera/internal/tools"
	"github.com/nodera/nodera/internal/tools/handlers"
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
	if cfg.Anthropic.APIKey != "" {
		registeredProviders = append(registeredProviders, anthropic.New("anthropic", cfg.Anthropic.APIKey))
	}
	aiSvc := ai.New(pool, auditSvc, registeredProviders...)

	// Auto-register each configured provider's row so it shows up in the
	// registry without a manual POST /api/v1/ai/providers call — the Go
	// adapter above is what actually makes it callable; this just makes it
	// discoverable. System actor bypasses the per-org permission check
	// (rbac.Require) since this isn't derived from a client request.
	if cfg.Ollama.BaseURL != "" {
		autoRegisterProvider(ctx, log, aiSvc, ai.UpsertProviderInput{
			Key: "ollama", Kind: "local", DisplayName: "Ollama (local)", Status: "active",
		})
	}
	if cfg.Anthropic.APIKey != "" {
		autoRegisterProvider(ctx, log, aiSvc, ai.UpsertProviderInput{
			Key: "anthropic", Kind: "cloud", DisplayName: "Anthropic", Status: "active",
		})
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

	toolsSvc := tools.New(pool, auditSvc)
	toolsSvc.RegisterHandler("get_server_metrics", newGetServerMetricsHandler(infraSvc))
	toolsSvc.RegisterHandler("check_ssl", handlers.CheckSSL)
	go runApprovalExpirySweep(ctx, log, toolsSvc)

	agentsSvc := agents.New(pool, auditSvc, aiSvc, toolsSvc)

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
		tools:       toolsSvc,
		agents:      agentsSvc,
		pool:        pool,
		loginRate:   ratelimit.New(5, 5*time.Minute),
		signupRate:  ratelimit.New(3, time.Hour),
		aiChatRate:  ratelimit.New(60, time.Minute),
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

// autoRegisterProvider upserts a provider's registry row for a provider
// whose Go adapter was just registered with aiSvc. A failure here doesn't
// stop startup — the adapter still works for Chat calls that reference it
// by key directly; only its discoverability via GET /api/v1/ai/providers
// is affected, which is why this logs and continues rather than returning
// an error.
func autoRegisterProvider(ctx context.Context, log *slog.Logger, aiSvc *ai.Service, in ai.UpsertProviderInput) {
	if _, err := aiSvc.UpsertProvider(ctx, authctx.System(uuid.Nil), in); err != nil {
		log.Error("failed to auto-register AI provider", "provider", in.Key, "error", err)
	} else {
		log.Info("AI provider registered", "provider", in.Key, "kind", in.Kind)
	}
}

// approvalExpirySweepInterval is how often RunExpirySweep runs
// organization-independently. Not yet configurable — a deliberate
// phase-1 simplification (docs/AGENTS.md).
const approvalExpirySweepInterval = 5 * time.Minute

// runApprovalExpirySweep periodically expires stale pending approvals
// across every organization, so an idle org's approvals still flip to
// 'expired' on schedule rather than staying 'pending' forever if nobody
// happens to call ListApprovals/DecideApproval for it (those also sweep,
// but only for the org making that particular call — see
// tools.Registry.expirePending). Runs until ctx is cancelled.
func runApprovalExpirySweep(ctx context.Context, log *slog.Logger, toolsSvc *tools.Registry) {
	ticker := time.NewTicker(approvalExpirySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := toolsSvc.RunExpirySweep(ctx)
			if err != nil {
				log.Error("approval expiry sweep failed", "error", err)
				continue
			}
			if n > 0 {
				log.Info("approval expiry sweep", "expired", n)
			}
		}
	}
}

// newGetServerMetricsHandler wraps infrastructure.Service.Get rather than
// querying the nodes table directly (ADR-002: compose existing domain
// services from cmd/server, don't duplicate their logic). It returns the
// node's last-known inventory record — cpu_cores/memory_mb/storage_gb,
// status, last_seen_at — not live-sampled metrics: no Node Agent exists yet
// to report those (docs/INFRASTRUCTURE.md), and reporting fabricated live
// numbers here would violate rule 36. The field names make that plain to
// any caller.
func newGetServerMetricsHandler(infraSvc *infrastructure.Service) tools.Handler {
	return func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		nodeID, err := uuid.Parse(resourceID)
		if err != nil {
			return nil, fmt.Errorf("get_server_metrics: resource_id must be a node UUID: %w", err)
		}
		node, err := infraSvc.Get(ctx, ac, nodeID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"source":       "inventory", // not live telemetry — see doc comment above
			"hostname":     node.Hostname,
			"status":       node.Status,
			"cpu_cores":    node.CPUCores,
			"memory_mb":    node.MemoryMB,
			"storage_gb":   node.StorageGB,
			"last_seen_at": node.LastSeenAt,
		}, nil
	}
}

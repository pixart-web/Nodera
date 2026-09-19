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
	"github.com/redis/go-redis/v9"

	"github.com/nodera/nodera/internal/agents"
	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/anthropic"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/ai/providers/ollama"
	"github.com/nodera/nodera/internal/ai/providers/openai"
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
	"github.com/nodera/nodera/internal/platformauth"
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

	// auditSvc and platformSvc need each other (platformSvc writes its own
	// grant/revoke audit entries; auditSvc.QueryPlatform checks a platform
	// permission before returning platform-scope rows) — SetPlatformAuthorizer
	// closes that cycle after both are constructed, since Go doesn't allow
	// them to take each other as constructor arguments. See
	// audit.PlatformAuthorizer's doc comment.
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	platformSvc := platformauth.New(pool, auditSvc)
	auditSvc.SetPlatformAuthorizer(platformSvc)
	identitySvc := identity.New(pool, rbacSvc, cfg.Auth.SessionTTL, auditSvc)
	tenancySvc := tenancy.New(pool, identitySvc, auditSvc)
	infraSvc := infrastructure.New(pool, auditSvc)
	appsSvc := applications.New(pool, auditSvc)
	jobsSvc := jobs.New(pool, auditSvc)

	if cfg.Platform.BootstrapAdminEmail != "" {
		if err := platformSvc.BootstrapAdmin(ctx, cfg.Platform.BootstrapAdminEmail); err != nil {
			return fmt.Errorf("failed to bootstrap platform administrator: %w", err)
		}
	}

	registeredProviders := []providers.Provider{localecho.New()}
	if cfg.Ollama.BaseURL != "" {
		registeredProviders = append(registeredProviders, ollama.New("ollama", cfg.Ollama.BaseURL))
	}
	if cfg.Anthropic.APIKey != "" {
		registeredProviders = append(registeredProviders, anthropic.New("anthropic", cfg.Anthropic.APIKey))
	}
	if cfg.OpenAI.APIKey != "" {
		registeredProviders = append(registeredProviders, openai.New("openai", cfg.OpenAI.APIKey))
	}
	aiSvc := ai.New(pool, auditSvc, platformSvc, registeredProviders...)

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
	if cfg.OpenAI.APIKey != "" {
		autoRegisterProvider(ctx, log, aiSvc, ai.UpsertProviderInput{
			Key: "openai", Kind: "cloud", DisplayName: "OpenAI", Status: "active",
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

	loginRate, signupRate, aiChatRate, createOrgRate, changePasswordRate, closeRateLimiting, err := newRateLimiters(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeRateLimiting()

	worker := jobs.NewWorker(pool)
	// No handlers are registered yet (docs/ROADMAP.md: "jobs worker" ships
	// the dispatcher itself in this pass; concrete job types like
	// backup.create arrive with the domains that need them). An enqueued
	// job with no matching handler fails visibly rather than hanging.
	go worker.Run(ctx)
	log.Info("job worker started")

	deps := apiDeps{
		log:                log,
		identity:           identitySvc,
		tenancy:            tenancySvc,
		audit:              auditSvc,
		infra:              infraSvc,
		apps:               appsSvc,
		jobs:               jobsSvc,
		ai:                 aiSvc,
		secrets:            secretsSvc,
		tools:              toolsSvc,
		agents:             agentsSvc,
		rbac:               rbacSvc,
		platform:           platformSvc,
		pool:               pool,
		loginRate:          loginRate,
		signupRate:         signupRate,
		aiChatRate:         aiChatRate,
		createOrgRate:      createOrgRate,
		changePasswordRate: changePasswordRate,
		corsOrigins:        cfg.HTTP.CORSOrigins,
		sessionTTL:         cfg.Auth.SessionTTL,
		secureCookies:      cfg.Env == "production",
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

// newRateLimiters builds the login/signup/AI-chat/change-password rate
// limiters. When NODERA_REDIS_URL is configured it connects to Redis
// (verified with a PING so a misconfigured URL fails startup loudly
// rather than silently falling back — rule 36: don't quietly degrade a
// limiter the operator explicitly asked to be shared) and returns
// Redis-backed limiters, safe across multiple API process instances.
// Otherwise it falls back to the in-process limiter, correct for a single
// instance but not shared across one (docs/ROADMAP.md). The returned
// close func closes the Redis client, if one was opened; it is always
// safe to call.
func newRateLimiters(ctx context.Context, cfg config.Config, log *slog.Logger) (login, signup, aiChat, createOrg, changePassword ratelimit.Allower, closeFn func(), err error) {
	if cfg.Redis.URL == "" {
		log.Warn("rate limiting is in-process only: NODERA_REDIS_URL is not set — limits are per-instance, not shared across a multi-instance deployment")
		return ratelimit.New(5, 5*time.Minute),
			ratelimit.New(3, time.Hour),
			ratelimit.New(60, time.Minute),
			ratelimit.New(10, time.Hour),
			ratelimit.New(5, 5*time.Minute),
			func() {}, nil
	}

	opts, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("config: NODERA_REDIS_URL is not a valid redis URL: %w", err)
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("redis: could not connect using NODERA_REDIS_URL: %w", err)
	}
	log.Info("rate limiting is Redis-backed, shared across instances")

	return ratelimit.NewRedis(client, "ratelimit:login", 5, 5*time.Minute, log),
		ratelimit.NewRedis(client, "ratelimit:signup", 3, time.Hour, log),
		ratelimit.NewRedis(client, "ratelimit:ai_chat", 60, time.Minute, log),
		ratelimit.NewRedis(client, "ratelimit:create_org", 10, time.Hour, log),
		ratelimit.NewRedis(client, "ratelimit:change_password", 5, 5*time.Minute, log),
		func() { _ = client.Close() }, nil
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

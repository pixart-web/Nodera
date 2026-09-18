// Package agents implements the agent identity and scoped-execution
// foundation (section 17, docs/AGENTS.md). It deliberately does NOT
// implement an autonomous loop where an LLM decides which tool to call —
// rule 38 explicitly defers "unrestricted autonomous infrastructure
// agents". What exists instead: an agent has its own bounded identity (a
// permission_scope no broader than its creator's own permissions, an
// allowed_tool_keys list), a chat capability via its configured AI profile
// and system_instructions, and a way for a caller to invoke one of its
// allowed tools *on the agent's behalf* — the caller decides which tool to
// invoke and when; the agent supplies the scoped identity that call runs
// under. This is real, tested infrastructure for a future autonomous loop
// to be built on top of, not the loop itself.
package agents

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tools"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
	ai    *ai.Service
	tools *tools.Registry
}

func New(pool *pgxpool.Pool, audit AuditRecorder, aiSvc *ai.Service, toolsSvc *tools.Registry) *Service {
	return &Service{pool: pool, audit: audit, ai: aiSvc, tools: toolsSvc}
}

const (
	permManage  = "agents.manage"
	permExecute = "agents.execute"
)

type Agent struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	SystemInstructions string    `json:"system_instructions"`
	AIProfileKey       string    `json:"ai_profile_key"`
	AllowedToolKeys    []string  `json:"allowed_tool_keys"`
	PermissionScope    []string  `json:"permission_scope"`
	Status             string    `json:"status"`
	TimeoutSeconds     int       `json:"timeout_seconds"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateAgentInput struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	SystemInstructions string   `json:"system_instructions"`
	AIProfileKey       string   `json:"ai_profile_key"`
	AllowedToolKeys    []string `json:"allowed_tool_keys"`
	PermissionScope    []string `json:"permission_scope"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
}

// CreateAgent requires agents.manage, and — same no-privilege-escalation
// rule as API token scopes (internal/identity/apitoken.go) — the agent's
// permission_scope can never exceed the creator's own permissions. A
// newly created agent starts 'disabled' (the schema default); see
// SetStatus.
func (s *Service) CreateAgent(ctx context.Context, ac authctx.AuthContext, in CreateAgentInput) (Agent, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Agent{}, err
	}
	if in.Name == "" {
		return Agent{}, apierr.Validation("agent name is required")
	}
	if in.AIProfileKey == "" {
		return Agent{}, apierr.Validation("ai_profile_key is required")
	}
	for _, perm := range in.PermissionScope {
		if !ac.HasPermission(perm) {
			return Agent{}, apierr.Forbidden("cannot grant an agent a permission you do not hold: " + perm)
		}
	}
	if err := s.validateToolKeys(ctx, in.AllowedToolKeys); err != nil {
		return Agent{}, err
	}
	if in.AllowedToolKeys == nil {
		in.AllowedToolKeys = []string{}
	}
	if in.PermissionScope == nil {
		in.PermissionScope = []string{}
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 120
	}

	var a Agent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents (organization_id, name, description, system_instructions, ai_profile_key,
		                     allowed_tool_keys, permission_scope, timeout_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, name, description, system_instructions, ai_profile_key,
		          allowed_tool_keys, permission_scope, status, timeout_seconds, created_at, updated_at
	`, ac.OrganizationID, in.Name, in.Description, in.SystemInstructions, in.AIProfileKey,
		in.AllowedToolKeys, in.PermissionScope, in.TimeoutSeconds).Scan(
		&a.ID, &a.Name, &a.Description, &a.SystemInstructions, &a.AIProfileKey,
		&a.AllowedToolKeys, &a.PermissionScope, &a.Status, &a.TimeoutSeconds, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Agent{}, apierr.Conflict("an agent with this name already exists in this organization")
		}
		return Agent{}, apierr.Wrap(apierr.CodeInternal, "failed to create agent", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "agents.agent.created", ResourceType: "agent", ResourceID: a.ID.String(),
		Success: true, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

func (s *Service) validateToolKeys(ctx context.Context, keys []string) error {
	for _, key := range keys {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tools WHERE key = $1)`, key).Scan(&exists); err != nil {
			return apierr.Wrap(apierr.CodeInternal, "failed to validate tool key", err)
		}
		if !exists {
			return apierr.Validation("unknown tool key: " + key)
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext) ([]Agent, error) {
	if err := rbac.Require(ac, permExecute); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, system_instructions, ai_profile_key,
		       allowed_tool_keys, permission_scope, status, timeout_seconds, created_at, updated_at
		FROM agents WHERE organization_id = $1 ORDER BY name ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list agents", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.SystemInstructions, &a.AIProfileKey,
			&a.AllowedToolKeys, &a.PermissionScope, &a.Status, &a.TimeoutSeconds, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan agent", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, orgID, id uuid.UUID) (Agent, error) {
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, system_instructions, ai_profile_key,
		       allowed_tool_keys, permission_scope, status, timeout_seconds, created_at, updated_at
		FROM agents WHERE id = $1 AND organization_id = $2
	`, id, orgID).Scan(&a.ID, &a.Name, &a.Description, &a.SystemInstructions, &a.AIProfileKey,
		&a.AllowedToolKeys, &a.PermissionScope, &a.Status, &a.TimeoutSeconds, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, apierr.NotFound("agent")
	}
	if err != nil {
		return Agent{}, apierr.Wrap(apierr.CodeInternal, "failed to load agent", err)
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Agent, error) {
	if err := rbac.Require(ac, permExecute); err != nil {
		return Agent{}, err
	}
	return s.get(ctx, ac.OrganizationID, id)
}

// SetStatus enables or disables an agent. A disabled agent cannot be Run
// or have a tool executed on its behalf — Run/ExecuteTool both check this.
func (s *Service) SetStatus(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, active bool) (Agent, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Agent{}, err
	}
	status := "disabled"
	if active {
		status = "active"
	}
	var a Agent
	err := s.pool.QueryRow(ctx, `
		UPDATE agents SET status = $3, updated_at = now() WHERE id = $1 AND organization_id = $2
		RETURNING id, name, description, system_instructions, ai_profile_key,
		          allowed_tool_keys, permission_scope, status, timeout_seconds, created_at, updated_at
	`, id, ac.OrganizationID, status).Scan(
		&a.ID, &a.Name, &a.Description, &a.SystemInstructions, &a.AIProfileKey,
		&a.AllowedToolKeys, &a.PermissionScope, &a.Status, &a.TimeoutSeconds, &a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, apierr.NotFound("agent")
	}
	if err != nil {
		return Agent{}, apierr.Wrap(apierr.CodeInternal, "failed to update agent status", err)
	}
	return a, nil
}

// agentAuthContext derives the scoped AuthContext an agent acts under —
// its own permission_scope, never the invoking caller's broader
// permissions (that would defeat the point of scoping an agent at all).
func agentAuthContext(a Agent, orgID uuid.UUID, correlationID string) authctx.AuthContext {
	perms := make(map[string]struct{}, len(a.PermissionScope))
	for _, p := range a.PermissionScope {
		perms[p] = struct{}{}
	}
	return authctx.AuthContext{
		ActorType:      authctx.ActorAgent,
		ActorID:        a.ID,
		ActorLabel:     a.Name,
		OrganizationID: orgID,
		Permissions:    perms,
		CorrelationID:  correlationID,
	}
}

// Run has the agent chat via its configured AI profile, with
// system_instructions supplied as a leading system message ahead of the
// caller-supplied userMessage. The caller must hold agents.execute; the
// AI call itself runs under the agent's own scoped AuthContext, so
// ai.manage-gated actions (there are none in Chat today, but this stays
// correct if that changes) can't be triggered via an agent that lacks them.
func (s *Service) Run(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, userMessage string) (ai.ChatResult, error) {
	if err := rbac.Require(ac, permExecute); err != nil {
		return ai.ChatResult{}, err
	}
	a, err := s.get(ctx, ac.OrganizationID, id)
	if err != nil {
		return ai.ChatResult{}, err
	}
	if a.Status != "active" {
		return ai.ChatResult{}, apierr.Conflict("agent is disabled")
	}

	agentAC := agentAuthContext(a, ac.OrganizationID, ac.CorrelationID)
	// agents.execute alone doesn't imply ai.use — an agent must be
	// explicitly scoped with it, same as any other capability, or Run
	// correctly refuses rather than silently granting AI access.
	if !agentAC.HasPermission("ai.use") {
		return ai.ChatResult{}, apierr.Forbidden("agent's permission_scope does not include ai.use")
	}

	messages := []providers.Message{{Role: "user", Content: userMessage}}
	if a.SystemInstructions != "" {
		messages = append([]providers.Message{{Role: "system", Content: a.SystemInstructions}}, messages...)
	}

	result, err := s.ai.Chat(ctx, agentAC, a.AIProfileKey, messages)

	if auditErr := s.audit.Record(ctx, ac, audit.Entry{
		Action: "agents.agent.run", ResourceType: "agent", ResourceID: a.ID.String(),
		Success: err == nil, ResultingState: map[string]any{"profile_key": a.AIProfileKey},
	}); auditErr != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", auditErr)
	}

	return result, err
}

// ExecuteTool invokes toolKey on the agent's behalf: the caller (who must
// hold agents.execute) decides which allowed tool to invoke and when — the
// agent itself never autonomously selects a tool (rule 38). The call runs
// under the agent's own scoped AuthContext, going through the same
// tools.Registry.Execute pipeline (permission check, risk tier, approval
// gate for privileged/critical) as a human-initiated call.
func (s *Service) ExecuteTool(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, toolKey string, in tools.ExecuteInput) (tools.ExecuteResult, error) {
	if err := rbac.Require(ac, permExecute); err != nil {
		return tools.ExecuteResult{}, err
	}
	a, err := s.get(ctx, ac.OrganizationID, id)
	if err != nil {
		return tools.ExecuteResult{}, err
	}
	if a.Status != "active" {
		return tools.ExecuteResult{}, apierr.Conflict("agent is disabled")
	}

	allowed := false
	for _, k := range a.AllowedToolKeys {
		if k == toolKey {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ExecuteResult{}, apierr.Forbidden("tool '" + toolKey + "' is not in this agent's allowed_tool_keys")
	}

	agentAC := agentAuthContext(a, ac.OrganizationID, ac.CorrelationID)
	return s.tools.Execute(ctx, agentAC, toolKey, in)
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

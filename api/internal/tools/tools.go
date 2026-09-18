// Package tools implements the Tool Gateway (section 18): a single choke
// point every tool call passes through — permission check, risk-tier gate,
// human approval for privileged/critical tools, audit — before (if ever) a
// handler actually runs. No tool is ever unrestricted; a tool with no
// registered handler reports NOT_IMPLEMENTED, never a fabricated result
// (rule 36).
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// Handler executes one tool call and returns a JSON-serializable result.
// Handlers are plain Go closures registered at process wiring time
// (cmd/server/main.go), typically composing an existing domain service
// (e.g. get_server_metrics wraps infrastructure.Service.Get) rather than
// duplicating its logic — see ADR-002.
type Handler func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error)

type Registry struct {
	pool     *pgxpool.Pool
	audit    AuditRecorder
	handlers map[string]Handler
}

func New(pool *pgxpool.Pool, audit AuditRecorder) *Registry {
	return &Registry{pool: pool, audit: audit, handlers: make(map[string]Handler)}
}

// RegisterHandler wires a Go implementation to a tool key already present
// in the `tools` table (seeded by migration 0007_agents.sql). Registering a
// handler for a key that doesn't exist in the table is a wiring bug, not a
// runtime condition — it's caught at first use, not here, to keep this
// method a simple map write.
func (r *Registry) RegisterHandler(toolKey string, h Handler) {
	r.handlers[toolKey] = h
}

type Tool struct {
	Key                string `json:"key"`
	Description        string `json:"description"`
	RiskLevel          string `json:"risk_level"`
	RequiredPermission string `json:"required_permission"`
	Implemented        bool   `json:"implemented"`
}

func (r *Registry) List(ctx context.Context, ac authctx.AuthContext) ([]Tool, error) {
	if err := rbac.Require(ac, "tools.read"); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT key, description, risk_level, required_permission, implemented
		FROM tools ORDER BY key ASC
	`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list tools", err)
	}
	defer rows.Close()

	var out []Tool
	for rows.Next() {
		var t Tool
		if err := rows.Scan(&t.Key, &t.Description, &t.RiskLevel, &t.RequiredPermission, &t.Implemented); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan tool", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Registry) getTool(ctx context.Context, key string) (Tool, error) {
	var t Tool
	err := r.pool.QueryRow(ctx, `
		SELECT key, description, risk_level, required_permission, implemented
		FROM tools WHERE key = $1
	`, key).Scan(&t.Key, &t.Description, &t.RiskLevel, &t.RequiredPermission, &t.Implemented)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tool{}, apierr.NotFound("tool")
	}
	if err != nil {
		return Tool{}, apierr.Wrap(apierr.CodeInternal, "failed to load tool", err)
	}
	return t, nil
}

// riskTierPermission is the additional permission a risk tier requires
// beyond the tool's own required_permission (docs/AGENTS.md) — 'read' has
// no separate tier permission since the tool's own (typically *.read)
// required_permission already gates it.
var riskTierPermission = map[string]string{
	"read":       "",
	"safe":       "tools.safe",
	"privileged": "tools.privileged",
	"critical":   "tools.critical",
}

func requiresApproval(riskLevel string) bool {
	return riskLevel == "privileged" || riskLevel == "critical"
}

type ExecuteInput struct {
	ResourceType string
	ResourceID   string
	Parameters   map[string]any
}

// ExecuteResult is either an immediate result (read/safe tools) or a
// pending approval (privileged/critical tools) — never both, and the
// caller can tell which by checking ApprovalID.
type ExecuteResult struct {
	Status     string     `json:"status"` // "executed" | "approval_required"
	Result     any        `json:"result,omitempty"`
	ApprovalID *uuid.UUID `json:"approval_id,omitempty"`
}

// Execute is the Tool Gateway's single entry point (section 18):
//
//	permission check -> risk-tier check -> [approval gate] -> handler -> audit
//
// A privileged/critical tool is never executed synchronously here,
// regardless of whether a handler is registered for it — see DecideApproval.
func (r *Registry) Execute(ctx context.Context, ac authctx.AuthContext, toolKey string, in ExecuteInput) (ExecuteResult, error) {
	tool, err := r.getTool(ctx, toolKey)
	if err != nil {
		return ExecuteResult{}, err
	}

	if err := rbac.Require(ac, tool.RequiredPermission); err != nil {
		return ExecuteResult{}, err
	}
	if tier := riskTierPermission[tool.RiskLevel]; tier != "" {
		if err := rbac.Require(ac, tier); err != nil {
			return ExecuteResult{}, err
		}
	}

	if requiresApproval(tool.RiskLevel) {
		approval, err := r.createApproval(ctx, ac, tool, in)
		if err != nil {
			return ExecuteResult{}, err
		}
		return ExecuteResult{Status: "approval_required", ApprovalID: &approval.ID}, nil
	}

	result, execErr := r.runHandler(ctx, ac, tool, in)
	success := execErr == nil

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action:         "tools." + tool.Key + ".executed",
		ResourceType:   in.ResourceType,
		ResourceID:     in.ResourceID,
		Success:        success,
		ResultingState: map[string]any{"tool": tool.Key, "risk_level": tool.RiskLevel},
	}); err != nil {
		// See audit.Service.Record's documented tradeoff: a logging failure
		// must not mask the real result of the tool call.
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	if execErr != nil {
		return ExecuteResult{}, execErr
	}
	return ExecuteResult{Status: "executed", Result: result}, nil
}

func (r *Registry) runHandler(ctx context.Context, ac authctx.AuthContext, tool Tool, in ExecuteInput) (any, error) {
	if !tool.Implemented {
		return nil, apierr.NotImplemented("tool '" + tool.Key + "' has no execution backend yet")
	}
	h, ok := r.handlers[tool.Key]
	if !ok {
		// Implemented=true in the registry but nothing was wired at
		// startup is a deployment/config bug, not a normal NOT_IMPLEMENTED
		// case — still fails safely rather than panicking.
		return nil, apierr.New(apierr.CodeInternal, "tool '"+tool.Key+"' is marked implemented but has no registered handler")
	}
	return h(ctx, ac, in.ResourceType, in.ResourceID, in.Parameters)
}

// --- Approvals ---

// defaultApprovalTTL is how long a pending approval stays actionable
// before it's treated as expired. Not yet configurable per-tool or
// per-organization — a deliberate phase-1 simplification (docs/AGENTS.md).
const defaultApprovalTTL = 24 * time.Hour

type Approval struct {
	ID              uuid.UUID       `json:"id"`
	RequestedAction string          `json:"requested_action"`
	RiskLevel       string          `json:"risk_level"`
	ResourceType    string          `json:"resource_type"`
	ResourceID      string          `json:"resource_id"`
	Parameters      json.RawMessage `json:"parameters"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
	DecidedAt       *time.Time      `json:"decided_at,omitempty"`
	DecisionReason  string          `json:"decision_reason,omitempty"`
	ExecutionResult json.RawMessage `json:"execution_result,omitempty"`
}

func (r *Registry) createApproval(ctx context.Context, ac authctx.AuthContext, tool Tool, in ExecuteInput) (Approval, error) {
	paramsJSON, err := json.Marshal(in.Parameters)
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to encode tool parameters", err)
	}

	var userID *uuid.UUID
	if ac.ActorType == authctx.ActorUser {
		id := ac.ActorID
		userID = &id
	}

	expiresAt := time.Now().Add(defaultApprovalTTL)

	var a Approval
	err = r.pool.QueryRow(ctx, `
		INSERT INTO approvals (organization_id, requested_action, requesting_user_id, risk_level,
		                        resource_type, resource_id, parameters, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, requested_action, risk_level, COALESCE(resource_type, ''), COALESCE(resource_id, ''),
		          parameters, status, created_at, expires_at
	`, ac.OrganizationID, tool.Key, userID, tool.RiskLevel, in.ResourceType, in.ResourceID, paramsJSON, expiresAt).Scan(
		&a.ID, &a.RequestedAction, &a.RiskLevel, &a.ResourceType, &a.ResourceID, &a.Parameters, &a.Status, &a.CreatedAt, &a.ExpiresAt,
	)
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to create approval request", err)
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "approvals.approval.requested", ResourceType: "approval", ResourceID: a.ID.String(),
		Success: true, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

// expirePending marks any pending approval past its expires_at as
// 'expired', scoped to one organization. It runs lazily at the start of
// ListApprovals and DecideApproval, the two request-driven places that
// actually need an up-to-date status for the org making the call. See
// RunExpirySweep for the organization-independent background version.
func (r *Registry) expirePending(ctx context.Context, orgID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE approvals SET status = 'expired'
		WHERE organization_id = $1 AND status = 'pending' AND expires_at IS NOT NULL AND expires_at < now()
	`, orgID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to expire stale approvals", err)
	}
	return nil
}

// RunExpirySweep expires every organization's stale pending approvals in
// one statement, independent of any request — so an idle organization's
// approvals still flip to 'expired' on schedule rather than staying
// (incorrectly) 'pending' forever if nobody happens to call
// ListApprovals/DecideApproval for it. Call it periodically (see
// cmd/server/main.go); it is idempotent and safe to run concurrently with
// itself or with the per-request expirePending calls.
func (r *Registry) RunExpirySweep(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE approvals SET status = 'expired'
		WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < now()
	`)
	if err != nil {
		return 0, apierr.Wrap(apierr.CodeInternal, "failed to run approval expiry sweep", err)
	}
	return tag.RowsAffected(), nil
}

func (r *Registry) ListApprovals(ctx context.Context, ac authctx.AuthContext, status string) ([]Approval, error) {
	if err := rbac.Require(ac, "approvals.decide"); err != nil {
		return nil, err
	}
	if err := r.expirePending(ctx, ac.OrganizationID); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, requested_action, risk_level, COALESCE(resource_type, ''), COALESCE(resource_id, ''),
		       parameters, status, created_at, expires_at, decided_at, COALESCE(decision_reason, ''),
		       COALESCE(execution_result, 'null')
		FROM approvals
		WHERE organization_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
	`, ac.OrganizationID, status)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list approvals", err)
	}
	defer rows.Close()

	var out []Approval
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.ID, &a.RequestedAction, &a.RiskLevel, &a.ResourceType, &a.ResourceID,
			&a.Parameters, &a.Status, &a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecisionReason, &a.ExecutionResult); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan approval", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DecideApproval records a human decision on a pending approval. If
// approved, it then attempts execution immediately (using the parameters
// captured when the approval was requested) and records the outcome —
// including a NOT_IMPLEMENTED outcome for a tool with no execution backend
// yet, which is a legitimate, honestly-reported result, not an error in the
// approval process itself (rule 36: the approval succeeded; the tool call
// it authorized may still not exist).
func (r *Registry) DecideApproval(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, approve bool, reason string) (Approval, error) {
	if err := rbac.Require(ac, "approvals.decide"); err != nil {
		return Approval{}, err
	}
	if err := r.expirePending(ctx, ac.OrganizationID); err != nil {
		return Approval{}, err
	}

	var a Approval
	var resourceType, resourceID string
	var paramsJSON json.RawMessage
	err := r.pool.QueryRow(ctx, `
		SELECT id, requested_action, risk_level, COALESCE(resource_type, ''), COALESCE(resource_id, ''), parameters, status
		FROM approvals WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(&a.ID, &a.RequestedAction, &a.RiskLevel, &resourceType, &resourceID, &paramsJSON, &a.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, apierr.NotFound("approval")
	}
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to load approval", err)
	}
	if a.Status != "pending" {
		return Approval{}, apierr.Conflict("approval is no longer pending (status: " + a.Status + ")")
	}

	newStatus := "rejected"
	if approve {
		newStatus = "approved"
	}

	var decidedByUserID *uuid.UUID
	if ac.ActorType == authctx.ActorUser {
		id := ac.ActorID
		decidedByUserID = &id
	}

	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		params = map[string]any{}
	}

	var executionResult json.RawMessage
	if approve {
		tool, err := r.getTool(ctx, a.RequestedAction)
		if err != nil {
			executionResult, _ = json.Marshal(map[string]string{"error": "tool no longer exists in the registry"})
		} else {
			result, err := r.runHandler(ctx, ac, tool, ExecuteInput{ResourceType: resourceType, ResourceID: resourceID, Parameters: params})
			if err != nil {
				var ae *apierr.Error
				if errors.As(err, &ae) {
					executionResult, _ = json.Marshal(map[string]string{"error": string(ae.Code), "message": ae.Message})
				} else {
					executionResult, _ = json.Marshal(map[string]string{"error": "INTERNAL_ERROR"})
				}
			} else {
				executionResult, _ = json.Marshal(result)
			}
		}
	}

	err = r.pool.QueryRow(ctx, `
		UPDATE approvals SET status = $2, decided_by_user_id = $3, decided_at = now(),
		                      decision_reason = $4, execution_result = $5
		WHERE id = $1
		RETURNING id, requested_action, risk_level, COALESCE(resource_type, ''), COALESCE(resource_id, ''),
		          parameters, status, created_at, expires_at, decided_at, COALESCE(decision_reason, ''),
		          COALESCE(execution_result, 'null')
	`, id, newStatus, decidedByUserID, reason, executionResult).Scan(
		&a.ID, &a.RequestedAction, &a.RiskLevel, &a.ResourceType, &a.ResourceID,
		&a.Parameters, &a.Status, &a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecisionReason, &a.ExecutionResult,
	)
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to record approval decision", err)
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "approvals.approval." + newStatus, ResourceType: "approval", ResourceID: a.ID.String(),
		Success: true, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

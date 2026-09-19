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
// before it's treated as expired, when the organization hasn't set its own
// override for that tool (organization_tool_settings, migration 0013).
const defaultApprovalTTL = 24 * time.Hour

// minApprovalTTL / maxApprovalTTL bound what an organization can configure
// a tool's approval TTL to. Below the minimum, a human realistically can't
// react in time and every privileged/critical call becomes de facto
// unusable; above the maximum, a stale pending approval sits actionable
// long enough that the context behind the original request (who asked,
// why, whether it's still needed) has likely gone stale too.
const (
	minApprovalTTL = 5 * time.Minute
	maxApprovalTTL = 30 * 24 * time.Hour
)

const permToolsManage = "tools.manage"

// OrganizationToolSetting is one organization's approval-TTL override for
// one tool. Absence of a row for a (organization, tool) pair means "use
// defaultApprovalTTL", not "TTL is zero" — see resolveApprovalTTL.
type OrganizationToolSetting struct {
	ToolKey            string    `json:"tool_key"`
	ApprovalTTLSeconds int       `json:"approval_ttl_seconds"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// SetApprovalTTL creates or updates the calling organization's approval-TTL
// override for toolKey. Takes effect for approvals created after this
// call; it never retroactively changes an already-pending approval's
// expires_at.
func (r *Registry) SetApprovalTTL(ctx context.Context, ac authctx.AuthContext, toolKey string, ttl time.Duration) (OrganizationToolSetting, error) {
	if err := rbac.Require(ac, permToolsManage); err != nil {
		return OrganizationToolSetting{}, err
	}
	if _, err := r.getTool(ctx, toolKey); err != nil {
		return OrganizationToolSetting{}, err
	}
	if ttl < minApprovalTTL || ttl > maxApprovalTTL {
		return OrganizationToolSetting{}, apierr.Validation(
			"approval_ttl_seconds must be between " + minApprovalTTL.String() + " and " + maxApprovalTTL.String())
	}

	var updatedByUserID *uuid.UUID
	if ac.ActorType == authctx.ActorUser {
		id := ac.ActorID
		updatedByUserID = &id
	}

	var s OrganizationToolSetting
	err := r.pool.QueryRow(ctx, `
		INSERT INTO organization_tool_settings (organization_id, tool_key, approval_ttl_seconds, updated_by_user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, tool_key)
		DO UPDATE SET approval_ttl_seconds = $3, updated_by_user_id = $4, updated_at = now()
		RETURNING tool_key, approval_ttl_seconds, updated_at
	`, ac.OrganizationID, toolKey, int(ttl.Seconds()), updatedByUserID).Scan(&s.ToolKey, &s.ApprovalTTLSeconds, &s.UpdatedAt)
	if err != nil {
		return OrganizationToolSetting{}, apierr.Wrap(apierr.CodeInternal, "failed to set approval TTL override", err)
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "tools.approval_ttl.set", ResourceType: "tool", ResourceID: toolKey,
		Success: true, ResultingState: s,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return s, nil
}

// ClearApprovalTTL removes the calling organization's override for toolKey,
// reverting it to defaultApprovalTTL. Not an error if no override existed.
func (r *Registry) ClearApprovalTTL(ctx context.Context, ac authctx.AuthContext, toolKey string) error {
	if err := rbac.Require(ac, permToolsManage); err != nil {
		return err
	}
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM organization_tool_settings WHERE organization_id = $1 AND tool_key = $2
	`, ac.OrganizationID, toolKey); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to clear approval TTL override", err)
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "tools.approval_ttl.cleared", ResourceType: "tool", ResourceID: toolKey,
		Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return nil
}

// ListApprovalTTLOverrides returns every approval-TTL override the calling
// organization has set. A tool with no row in the result uses
// defaultApprovalTTL.
func (r *Registry) ListApprovalTTLOverrides(ctx context.Context, ac authctx.AuthContext) ([]OrganizationToolSetting, error) {
	if err := rbac.Require(ac, permToolsManage); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT tool_key, approval_ttl_seconds, updated_at
		FROM organization_tool_settings WHERE organization_id = $1 ORDER BY tool_key ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list approval TTL overrides", err)
	}
	defer rows.Close()

	var out []OrganizationToolSetting
	for rows.Next() {
		var s OrganizationToolSetting
		if err := rows.Scan(&s.ToolKey, &s.ApprovalTTLSeconds, &s.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan approval TTL override", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// resolveApprovalTTL returns the organization's configured TTL for toolKey,
// falling back to defaultApprovalTTL if no override row exists.
func (r *Registry) resolveApprovalTTL(ctx context.Context, orgID uuid.UUID, toolKey string) (time.Duration, error) {
	var seconds int
	err := r.pool.QueryRow(ctx, `
		SELECT approval_ttl_seconds FROM organization_tool_settings WHERE organization_id = $1 AND tool_key = $2
	`, orgID, toolKey).Scan(&seconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultApprovalTTL, nil
	}
	if err != nil {
		return 0, apierr.Wrap(apierr.CodeInternal, "failed to resolve approval TTL", err)
	}
	return time.Duration(seconds) * time.Second, nil
}

// Approval carries enough immutable information to reconstruct the full
// trust chain behind a privileged/critical tool call: who requested it
// (RequestedByUserID xor RequestedByAgentID — a human caller or an agent
// acting under its own scoped identity, never both), what/where
// (RequestedAction/ResourceType/ResourceID/Parameters), who decided it
// (DecidedByUserID) and when, and what executing it actually produced
// (ExecutionResult). The requester and the approver/executor are
// deliberately distinct fields — an agent-requested, human-approved action
// must never collapse into looking like the human originated the request
// (see docs/AGENTS.md "Requester/approver/execution identity").
type Approval struct {
	ID                 uuid.UUID       `json:"id"`
	RequestedAction    string          `json:"requested_action"`
	RiskLevel          string          `json:"risk_level"`
	ResourceType       string          `json:"resource_type"`
	ResourceID         string          `json:"resource_id"`
	Parameters         json.RawMessage `json:"parameters"`
	Status             string          `json:"status"`
	RequestedByUserID  *uuid.UUID      `json:"requested_by_user_id,omitempty"`
	RequestedByAgentID *uuid.UUID      `json:"requested_by_agent_id,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty"`
	DecidedByUserID    *uuid.UUID      `json:"decided_by_user_id,omitempty"`
	DecidedAt          *time.Time      `json:"decided_at,omitempty"`
	DecisionReason     string          `json:"decision_reason,omitempty"`
	ExecutionResult    json.RawMessage `json:"execution_result,omitempty"`
}

// approvalColumns is the column list shared by every SELECT/RETURNING that
// produces a full Approval row — kept in one place so the state-machine
// transitions below (which each need their own RETURNING) can't drift from
// each other or from scanApproval.
const approvalColumns = `id, requested_action, risk_level, COALESCE(resource_type, ''), COALESCE(resource_id, ''),
	          parameters, status, requesting_user_id, requesting_agent_id, created_at, expires_at,
	          decided_by_user_id, decided_at, COALESCE(decision_reason, ''), COALESCE(execution_result, 'null')`

func scanApproval(row pgx.Row) (Approval, error) {
	var a Approval
	err := row.Scan(&a.ID, &a.RequestedAction, &a.RiskLevel, &a.ResourceType, &a.ResourceID,
		&a.Parameters, &a.Status, &a.RequestedByUserID, &a.RequestedByAgentID, &a.CreatedAt, &a.ExpiresAt,
		&a.DecidedByUserID, &a.DecidedAt, &a.DecisionReason, &a.ExecutionResult)
	return a, err
}

func (r *Registry) createApproval(ctx context.Context, ac authctx.AuthContext, tool Tool, in ExecuteInput) (Approval, error) {
	paramsJSON, err := json.Marshal(in.Parameters)
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to encode tool parameters", err)
	}

	var userID, agentID *uuid.UUID
	switch ac.ActorType {
	case authctx.ActorUser:
		id := ac.ActorID
		userID = &id
	case authctx.ActorAgent:
		id := ac.ActorID
		agentID = &id
	}

	ttl, err := r.resolveApprovalTTL(ctx, ac.OrganizationID, tool.Key)
	if err != nil {
		return Approval{}, err
	}
	expiresAt := time.Now().Add(ttl)

	a, err := scanApproval(r.pool.QueryRow(ctx, `
		INSERT INTO approvals (organization_id, requested_action, requesting_user_id, requesting_agent_id,
		                        risk_level, resource_type, resource_id, parameters, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+approvalColumns+`
	`, ac.OrganizationID, tool.Key, userID, agentID, tool.RiskLevel, in.ResourceType, in.ResourceID, paramsJSON, expiresAt))
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
		SELECT `+approvalColumns+`
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
		a, err := scanApproval(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan approval", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CancelApproval lets the original requester withdraw their own pending
// approval — the self-service counterpart to DecideApproval, which
// requires approvals.decide and can act on anyone's request. A requester
// who realizes a call was a mistake shouldn't have to wait for an
// approver to reject it or for it to expire; this needs no permission
// beyond being the original requester, the same "act on your own
// resource" pattern RevokeSession/RevokeAPIToken/LeaveOrganization
// already follow. Only reaches approvals requested by a human user
// (requesting_user_id) — an agent- or service-account-originated request
// has no self-service cancel path here, since ac.ActorID wouldn't match
// either.
func (r *Registry) CancelApproval(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Approval, error) {
	if ac.ActorType != authctx.ActorUser {
		return Approval{}, apierr.Forbidden("only the requesting user can cancel an approval")
	}
	if err := r.expirePending(ctx, ac.OrganizationID); err != nil {
		return Approval{}, err
	}

	var existingStatus string
	var requestingUserID *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT status, requesting_user_id FROM approvals WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(&existingStatus, &requestingUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, apierr.NotFound("approval")
	}
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to load approval", err)
	}
	if requestingUserID == nil || *requestingUserID != ac.ActorID {
		return Approval{}, apierr.Forbidden("only the requesting user can cancel an approval")
	}
	// This pre-check is purely a fast, friendly error path (distinguishing
	// "not yours" from "already decided" for the caller) — it is NOT what
	// makes cancellation safe. The UPDATE below is the atomic guard: its
	// own `WHERE status = 'pending'` is what actually prevents a cancel
	// from racing a concurrent DecideApproval/CancelApproval past this
	// point (see the identical pattern and comment in DecideApproval).
	if existingStatus != "pending" {
		return Approval{}, apierr.Conflict("approval is no longer pending (status: " + existingStatus + ")")
	}

	a, err := scanApproval(r.pool.QueryRow(ctx, `
		UPDATE approvals SET status = 'cancelled', decided_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'pending'
		RETURNING `+approvalColumns+`
	`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, apierr.Conflict("approval is no longer pending")
	}
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to cancel approval", err)
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "approvals.approval.cancelled", ResourceType: "approval", ResourceID: a.ID.String(),
		Success: true, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

// DecideApproval records a human decision on a pending approval. If
// approved, it then attempts execution immediately (using the parameters
// captured when the approval was requested) and records the outcome —
// including a NOT_IMPLEMENTED outcome for a tool with no execution backend
// yet, which is a legitimate, honestly-reported result, not an error in the
// approval process itself (rule 36: the approval succeeded; the tool call
// it authorized may still not exist).
//
// Concurrency: the whole "decide" step (pending -> rejected, or
// pending -> executing) is a single conditional UPDATE ... WHERE status =
// 'pending' RETURNING. PostgreSQL serializes concurrent UPDATEs against the
// same row, so exactly one concurrent caller's UPDATE can match that WHERE
// clause and return a row; every other concurrent caller (a second
// DecideApproval, or a concurrent CancelApproval, which uses the identical
// pattern) matches zero rows and gets a deterministic "already decided"
// conflict — before either of them has touched the handler. Only the
// caller that wins this UPDATE ever calls runHandler, so a tool can be
// executed at most once per approval. This is what actually prevents
// double execution; the SELECT below (before the UPDATE) is read-only and
// exists purely to distinguish "not found" from "not pending" in the
// error message — it grants no ownership and nothing unsafe depends on it.
//
// Crash semantics: if the process crashes after the pending -> executing
// transition but before the handler finishes and the final executing ->
// executed/execution_failed UPDATE runs, the approval is left stuck in
// 'executing' forever. This is intentional: automatically resuming or
// retrying an unknown-outcome privileged/critical operation (was the
// container actually restarted or not?) risks a second, uncoordinated
// execution, which is strictly worse than requiring an operator to
// manually inspect and resolve the stuck row. There is deliberately no
// code path that reclaims an 'executing' approval.
func (r *Registry) DecideApproval(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, approve bool, reason string) (Approval, error) {
	if err := rbac.Require(ac, "approvals.decide"); err != nil {
		return Approval{}, err
	}
	if err := r.expirePending(ctx, ac.OrganizationID); err != nil {
		return Approval{}, err
	}

	var existingStatus string
	if err := r.pool.QueryRow(ctx, `
		SELECT status FROM approvals WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(&existingStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Approval{}, apierr.NotFound("approval")
		}
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to load approval", err)
	}
	if existingStatus != "pending" {
		return Approval{}, apierr.Conflict("approval is no longer pending (status: " + existingStatus + ")")
	}

	var decidedByUserID *uuid.UUID
	if ac.ActorType == authctx.ActorUser {
		uid := ac.ActorID
		decidedByUserID = &uid
	}

	if !approve {
		a, err := scanApproval(r.pool.QueryRow(ctx, `
			UPDATE approvals SET status = 'rejected', decided_by_user_id = $2, decided_at = now(), decision_reason = $3
			WHERE id = $1 AND status = 'pending'
			RETURNING `+approvalColumns+`
		`, id, decidedByUserID, reason))
		if errors.Is(err, pgx.ErrNoRows) {
			return Approval{}, apierr.Conflict("approval is no longer pending")
		}
		if err != nil {
			return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to record approval decision", err)
		}
		if err := r.audit.Record(ctx, ac, audit.Entry{
			Action: "approvals.approval.rejected", ResourceType: "approval", ResourceID: a.ID.String(),
			Success: true, ResultingState: a,
		}); err != nil {
			logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
		}
		return a, nil
	}

	// Atomically claim sole execution ownership. Only the caller whose
	// UPDATE matches a row reaches runHandler below.
	a, err := scanApproval(r.pool.QueryRow(ctx, `
		UPDATE approvals SET status = 'executing', decided_by_user_id = $2, decided_at = now(), decision_reason = $3
		WHERE id = $1 AND status = 'pending'
		RETURNING `+approvalColumns+`
	`, id, decidedByUserID, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, apierr.Conflict("approval is no longer pending")
	}
	if err != nil {
		return Approval{}, apierr.Wrap(apierr.CodeInternal, "failed to record approval decision", err)
	}
	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "approvals.approval.executing", ResourceType: "approval", ResourceID: a.ID.String(),
		Success: true, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	var params map[string]any
	if err := json.Unmarshal(a.Parameters, &params); err != nil {
		params = map[string]any{}
	}

	var executionResult json.RawMessage
	var handlerErr error
	tool, toolErr := r.getTool(ctx, a.RequestedAction)
	if toolErr != nil {
		executionResult, _ = json.Marshal(map[string]string{"error": "tool no longer exists in the registry"})
		handlerErr = toolErr
	} else {
		result, execErr := r.runHandler(ctx, ac, tool, ExecuteInput{ResourceType: a.ResourceType, ResourceID: a.ResourceID, Parameters: params})
		handlerErr = execErr
		if execErr != nil {
			var ae *apierr.Error
			if errors.As(execErr, &ae) {
				executionResult, _ = json.Marshal(map[string]string{"error": string(ae.Code), "message": ae.Message})
			} else {
				executionResult, _ = json.Marshal(map[string]string{"error": "INTERNAL_ERROR"})
			}
		} else {
			executionResult, _ = json.Marshal(result)
		}
	}

	finalStatus := "executed"
	if handlerErr != nil {
		finalStatus = "execution_failed"
	}

	// This final transition is owned exclusively by this caller (it holds
	// the only 'executing' row for this approval), so WHERE status =
	// 'executing' can never lose a race here — it is a safety net against
	// this same code path somehow running twice, not a contended path.
	finalApproval, err := scanApproval(r.pool.QueryRow(ctx, `
		UPDATE approvals SET status = $2, execution_result = $3
		WHERE id = $1 AND status = 'executing'
		RETURNING `+approvalColumns+`
	`, id, finalStatus, executionResult))
	if err != nil {
		// The handler already ran — this is a bookkeeping failure, not an
		// execution failure. Surface what we know rather than losing the
		// execution result entirely; the row is left in 'executing' for
		// operator follow-up per the crash-semantics note above.
		logger.FromContext(ctx).Error("failed to record approval execution outcome", "error", err, "approval_id", id)
		a.Status = finalStatus
		a.ExecutionResult = executionResult
		if err := r.audit.Record(ctx, ac, audit.Entry{
			Action: "approvals.approval." + finalStatus, ResourceType: "approval", ResourceID: a.ID.String(),
			Success: handlerErr == nil, ResultingState: a,
		}); err != nil {
			logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
		}
		return a, nil
	}

	if err := r.audit.Record(ctx, ac, audit.Entry{
		Action: "approvals.approval." + finalStatus, ResourceType: "approval", ResourceID: finalApproval.ID.String(),
		Success: handlerErr == nil, ResultingState: finalApproval,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return finalApproval, nil
}

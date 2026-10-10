package ops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

const permRead = "operations.read"

type OperationView struct {
	ID              uuid.UUID       `json:"id"`
	Operation       string          `json:"operation"`
	Status          string          `json:"status"`
	Progress        float64         `json:"progress"`
	ProjectID       *uuid.UUID      `json:"project_id,omitempty"`
	CreatedByUserID *uuid.UUID      `json:"created_by_user_id,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	Error           string          `json:"error,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	RolledBack      bool            `json:"rolled_back"`
	CancelRequested bool            `json:"cancel_requested"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type StepView struct {
	Seq        int        `json:"seq"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Progress   float64    `json:"progress"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type LogView struct {
	ID       int64           `json:"id"`
	StepSeq  *int            `json:"step_seq,omitempty"`
	TS       time.Time       `json:"timestamp"`
	Level    string          `json:"level"`
	Message  string          `json:"message"`
	Metadata json.RawMessage `json:"metadata"`
}

const viewCols = `id, COALESCE(operation, type), status, progress, project_id, created_by_user_id, COALESCE(correlation_id, ''),
	COALESCE(error, ''), COALESCE(result, 'null'), rolled_back, cancel_requested, created_at, started_at, finished_at`

func scanView(row pgx.Row) (OperationView, error) {
	var v OperationView
	err := row.Scan(&v.ID, &v.Operation, &v.Status, &v.Progress, &v.ProjectID, &v.CreatedByUserID, &v.CorrelationID,
		&v.Error, &v.Result, &v.RolledBack, &v.CancelRequested, &v.CreatedAt, &v.StartedAt, &v.FinishedAt)
	return v, err
}

// Get returns one operation, scoped to the caller's organization.
func (e *Engine) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (OperationView, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return OperationView{}, err
	}
	v, err := scanView(e.pool.QueryRow(ctx, `SELECT `+viewCols+` FROM jobs WHERE id = $1 AND organization_id = $2 AND operation IS NOT NULL`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationView{}, apierr.NotFound("operation")
	}
	if err != nil {
		return OperationView{}, apierr.Wrap(apierr.CodeInternal, "failed to load operation", err)
	}
	return v, nil
}

// List returns operations for the organization, optionally for one project.
func (e *Engine) List(ctx context.Context, ac authctx.AuthContext, projectID uuid.UUID, limit, offset int) ([]OperationView, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := e.pool.Query(ctx, `
		SELECT `+viewCols+` FROM jobs
		WHERE organization_id = $1 AND operation IS NOT NULL AND ($2::uuid IS NULL OR project_id = $2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4
	`, ac.OrganizationID, nullUUID(projectID), limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list operations", err)
	}
	defer rows.Close()
	var out []OperationView
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan operation", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (e *Engine) Steps(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) ([]StepView, error) {
	if _, err := e.Get(ctx, ac, id); err != nil {
		return nil, err
	}
	rows, err := e.pool.Query(ctx, `
		SELECT seq, name, status, progress, COALESCE(error, ''), started_at, finished_at
		FROM job_steps WHERE job_id = $1 AND organization_id = $2 ORDER BY seq`, id, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list steps", err)
	}
	defer rows.Close()
	var out []StepView
	for rows.Next() {
		var s StepView
		if err := rows.Scan(&s.Seq, &s.Name, &s.Status, &s.Progress, &s.Error, &s.StartedAt, &s.FinishedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan step", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Logs returns log lines with id > afterID (cursor-style pagination, which is
// also what the SSE stream uses).
func (e *Engine) Logs(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, afterID int64, limit int) ([]LogView, error) {
	if _, err := e.Get(ctx, ac, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := e.pool.Query(ctx, `
		SELECT id, step_seq, ts, level, message, metadata FROM job_logs
		WHERE job_id = $1 AND organization_id = $2 AND id > $3 ORDER BY id LIMIT $4`, id, ac.OrganizationID, afterID, limit)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list logs", err)
	}
	defer rows.Close()
	var out []LogView
	for rows.Next() {
		var l LogView
		if err := rows.Scan(&l.ID, &l.StepSeq, &l.TS, &l.Level, &l.Message, &l.Metadata); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan log", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RequestCancel asks a queued or running operation to stop; running
// operations stop at the next step boundary and roll back. A queued job is
// cancelled immediately. Requires the operation's own permission or the
// caller being its creator.
func (e *Engine) RequestCancel(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	v, err := e.Get(ctx, ac, id)
	if err != nil {
		return err
	}
	d, known := e.defs[v.Operation]
	isCreator := v.CreatedByUserID != nil && *v.CreatedByUserID == ac.ActorID
	if !isCreator && (!known || !ac.HasPermission(d.Permission)) && ac.ActorType != authctx.ActorSystem {
		return apierr.Forbidden("only the operation's creator or a holder of its permission can cancel it")
	}
	switch v.Status {
	case "succeeded", "failed", "cancelled":
		return apierr.Conflict("operation already finished (" + v.Status + ")")
	}
	tag, err := e.pool.Exec(ctx, `
		UPDATE jobs SET cancel_requested = true,
		       status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
		       finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END,
		       updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status NOT IN ('succeeded', 'failed', 'cancelled')`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to request cancellation", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.Conflict("operation already finished")
	}
	if err := e.audit.Record(ctx, ac, audit.Entry{Action: "operations.operation.cancel_requested", ResourceType: "job", ResourceID: id.String(), Success: true}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// Package ops is Nodera's Operation Framework. Every important infrastructure
// action (create a project, deploy, back up, restore, migrate, issue a
// certificate, ...) is an Operation: validate -> execute (as persisted,
// individually-tracked steps) -> automatic rollback of completed steps on
// failure -> audit -> notification. Operations run as jobs on the existing
// Postgres-backed worker, so a request can always be traced
// Request -> Job -> Operation -> Audit via the shared correlation id.
//
// Dangerous operations are additionally routed through the Tool Gateway
// (internal/tools) so they inherit permission -> risk tier -> human approval
// -> atomic single execution -> audit.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

// Operation is the unit of infrastructure work.
type Operation interface {
	Name() string
	// Validate checks inputs and preconditions with no side effects. It runs
	// synchronously at submission (fast, user-facing errors) and again just
	// before execution (state may have changed while queued).
	Validate(ctx context.Context) error
	// Execute performs the work. Operations that implement Stepper are driven
	// step by step by the engine instead (preferred).
	Execute(ctx context.Context, r *Run) error
	// Rollback undoes what Execute did, best effort and idempotent. Only used
	// for non-Stepper operations; Stepper operations roll back per step.
	Rollback(ctx context.Context, r *Run) error
}

// Step is one persisted, individually reported unit of an operation. Undo must
// be idempotent and safe to call even if Run only partially completed.
type Step struct {
	Name string
	Run  func(ctx context.Context, r *Run) error
	Undo func(ctx context.Context, r *Run) error
}

// Stepper is implemented by operations expressed as a list of steps.
type Stepper interface{ Steps() []Step }

// Env is what factories receive to build operations.
type Env struct {
	Pool *pgxpool.Pool
}

// Definition registers an operation type with the engine.
type Definition struct {
	Name string
	// Permission is required to submit the operation (rbac key).
	Permission string
	// ToolKey, when set, routes submission through the Tool Gateway for
	// approval (the tool must exist in the tools table).
	ToolKey string
	// NotifyKind is the notification kind emitted on success (e.g.
	// "deployment.completed"); failures always emit "job.failed".
	NotifyKind string
	// TimeoutSeconds bounds a single execution (default 900).
	TimeoutSeconds int
	Factory        func(env *Env, orgID, projectID uuid.UUID, payload json.RawMessage) (Operation, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// Notifier receives operation outcomes (implemented by internal/notifications).
type Notifier interface {
	Notify(ctx context.Context, orgID uuid.UUID, kind, title, body, resourceType, resourceID string)
}

type Engine struct {
	pool     *pgxpool.Pool
	audit    AuditRecorder
	env      *Env
	defs     map[string]Definition
	notifier Notifier
}

func New(pool *pgxpool.Pool, auditRecorder AuditRecorder) *Engine {
	return &Engine{pool: pool, audit: auditRecorder, env: &Env{Pool: pool}, defs: map[string]Definition{}}
}

func (e *Engine) SetNotifier(n Notifier) { e.notifier = n }

// Register adds an operation type.
func (e *Engine) Register(d Definition) {
	if d.TimeoutSeconds == 0 {
		d.TimeoutSeconds = 900
	}
	e.defs[d.Name] = d
}

// Definitions lists registered operation names (for diagnostics/tests).
func (e *Engine) Definitions() []Definition {
	out := make([]Definition, 0, len(e.defs))
	for _, d := range e.defs {
		out = append(out, d)
	}
	return out
}

// RegisterWorker wires every registered operation into the job worker.
func (e *Engine) RegisterWorker(w *jobs.Worker) {
	for name, d := range e.defs {
		d := d
		w.Register(name, func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
			return e.execute(ctx, d, j)
		})
	}
}

// SubmitInput describes an operation request.
type SubmitInput struct {
	Operation      string
	ProjectID      uuid.UUID // uuid.Nil when not project-scoped
	Payload        any
	IdempotencyKey string
}

// JobRef is what Submit returns.
type JobRef struct {
	JobID   uuid.UUID `json:"job_id"`
	Created bool      `json:"created"` // false when an idempotent duplicate returned the existing job
}

// Submit authorises (permission), validates, and enqueues an operation.
func (e *Engine) Submit(ctx context.Context, ac authctx.AuthContext, in SubmitInput) (JobRef, error) {
	d, ok := e.defs[in.Operation]
	if !ok {
		return JobRef{}, apierr.Validation("unknown operation: " + in.Operation)
	}
	if err := rbac.Require(ac, d.Permission); err != nil {
		return JobRef{}, err
	}
	if d.ToolKey != "" {
		return JobRef{}, apierr.Validation("operation " + d.Name + " is dangerous and must be requested through the tool gateway (" + d.ToolKey + ") so it goes through approval")
	}
	return e.submit(ctx, ac, d, in)
}

// SubmitTrusted enqueues without a permission check. It exists solely for the
// Tool Gateway handler path, where the gateway already authorised the
// requester and a human approver decided the request.
func (e *Engine) SubmitTrusted(ctx context.Context, ac authctx.AuthContext, in SubmitInput) (JobRef, error) {
	d, ok := e.defs[in.Operation]
	if !ok {
		return JobRef{}, apierr.Validation("unknown operation: " + in.Operation)
	}
	return e.submit(ctx, ac, d, in)
}

func (e *Engine) submit(ctx context.Context, ac authctx.AuthContext, d Definition, in SubmitInput) (JobRef, error) {
	payload, err := json.Marshal(in.Payload)
	if err != nil {
		return JobRef{}, apierr.Validation("payload is not valid JSON")
	}
	if string(payload) == "null" {
		payload = []byte(`{}`)
	}
	op, err := d.Factory(e.env, ac.OrganizationID, in.ProjectID, payload)
	if err != nil {
		return JobRef{}, err
	}
	if err := op.Validate(ctx); err != nil {
		return JobRef{}, err
	}

	var projectID, createdBy any
	if in.ProjectID != uuid.Nil {
		projectID = in.ProjectID
	}
	if ac.ActorType == authctx.ActorUser {
		createdBy = ac.ActorID
	}
	var id uuid.UUID
	err = e.pool.QueryRow(ctx, `
		INSERT INTO jobs (organization_id, type, operation, project_id, created_by_user_id, payload, max_attempts,
		                  timeout_seconds, correlation_id, idempotency_key)
		VALUES ($1, $2, $2, $3, $4, $5, 1, $6, NULLIF($7, ''), NULLIF($8, ''))
		ON CONFLICT (organization_id, idempotency_key) DO NOTHING
		RETURNING id
	`, ac.OrganizationID, d.Name, projectID, createdBy, payload, d.TimeoutSeconds, ac.CorrelationID, in.IdempotencyKey).Scan(&id)
	created := true
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		err = e.pool.QueryRow(ctx, `SELECT id FROM jobs WHERE organization_id = $1 AND idempotency_key = $2`,
			ac.OrganizationID, in.IdempotencyKey).Scan(&id)
	}
	if err != nil {
		return JobRef{}, apierr.Wrap(apierr.CodeInternal, "failed to enqueue operation", err)
	}

	if created {
		if err := e.audit.Record(ctx, ac, audit.Entry{
			Action: "operations.operation.submitted", ResourceType: "job", ResourceID: id.String(), Success: true,
			ResultingState: map[string]any{"operation": d.Name, "project_id": in.ProjectID},
		}); err != nil {
			logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
		}
	}
	return JobRef{JobID: id, Created: created}, nil
}

// systemContext is the AuthContext operations run under: the platform itself,
// labelled with the job so audit rows are attributable and correlated.
func systemContext(j jobs.Job, creator *uuid.UUID) authctx.AuthContext {
	ac := authctx.System(j.OrganizationID)
	ac.ActorLabel = "operation:" + j.Type
	ac.CorrelationID = j.CorrelationID
	_ = creator
	return ac
}

func (e *Engine) recordAudit(ctx context.Context, ac authctx.AuthContext, action string, j jobs.Job, success bool, meta map[string]any) {
	if err := e.audit.Record(ctx, ac, audit.Entry{
		Action: action, ResourceType: "job", ResourceID: j.ID.String(), Success: success, Source: "system", Metadata: meta,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

func (e *Engine) execute(ctx context.Context, d Definition, j jobs.Job) (json.RawMessage, error) {
	log := logger.FromContext(ctx).With("job_id", j.ID, "operation", d.Name)
	var projectID uuid.UUID
	var creator *uuid.UUID
	var pid *uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT project_id, created_by_user_id FROM jobs WHERE id = $1`, j.ID).Scan(&pid, &creator); err != nil {
		return nil, fmt.Errorf("load job context: %w", err)
	}
	if pid != nil {
		projectID = *pid
	}
	ac := systemContext(j, creator)
	meta := map[string]any{"operation": d.Name}
	if creator != nil {
		meta["initiated_by_user_id"] = creator.String()
	}

	run := &Run{e: e, JobID: j.ID, OrgID: j.OrganizationID, ProjectID: projectID, Payload: j.Payload, state: map[string]any{}, opName: d.Name}

	op, err := d.Factory(e.env, j.OrganizationID, projectID, j.Payload)
	if err != nil {
		e.fail(ctx, ac, d, j, run, err)
		return nil, err
	}
	run.Log("info", "operation started", nil)
	e.recordAudit(ctx, ac, "operations.operation.started", j, true, meta)
	if err := op.Validate(ctx); err != nil {
		run.Log("error", "validation failed: "+err.Error(), nil)
		e.fail(ctx, ac, d, j, run, err)
		return nil, err
	}

	steps := stepsOf(op)
	if err := run.initSteps(ctx, steps); err != nil {
		return nil, err
	}

	var failed error
	done := 0
	for i, st := range steps {
		if run.cancelRequested(ctx) {
			failed = jobs.ErrCancelled
			break
		}
		run.beginStep(ctx, i, st.Name)
		if err := safeRun(ctx, st.Run, run); err != nil {
			run.endStep(ctx, i, "failed", err)
			run.Log("error", fmt.Sprintf("step %q failed: %v", st.Name, err), nil)
			failed = err
			if errors.Is(err, errCancelRequested) {
				failed = jobs.ErrCancelled
			}
			done = i + 1 // include the failed step in rollback (its Undo is idempotent)
			break
		}
		run.endStep(ctx, i, "succeeded", nil)
		done = i + 1
		run.setProgress(ctx, float64(done)*100/float64(len(steps)))
	}

	if failed == nil {
		result := run.resultJSON()
		run.Log("success", "operation completed", nil)
		e.recordAudit(ctx, ac, "operations.operation.succeeded", j, true, meta)
		if d.NotifyKind != "" && e.notifier != nil {
			e.notifier.Notify(ctx, j.OrganizationID, d.NotifyKind, humanName(d.Name)+" concluído", "", "job", j.ID.String())
		}
		return result, nil
	}

	// ---- failure: roll back completed steps in reverse order ----
	cancelled := errors.Is(failed, jobs.ErrCancelled)
	run.setJobStatus(ctx, "rolling_back")
	run.Log("warning", "rolling back: "+failed.Error(), nil)
	rolledBackAll := true
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if _, isStepper := op.(Stepper); isStepper {
		for i := done - 1; i >= 0; i-- {
			if steps[i].Undo == nil {
				continue
			}
			if err := safeRun(rbCtx, steps[i].Undo, run); err != nil {
				run.endStep(rbCtx, i, "rollback_failed", err)
				run.Log("error", fmt.Sprintf("rollback of %q failed: %v", steps[i].Name, err), nil)
				rolledBackAll = false
			} else {
				run.endStep(rbCtx, i, "rolled_back", nil)
			}
		}
	} else if err := safeRun(rbCtx, op.Rollback, run); err != nil {
		run.Log("error", "rollback failed: "+err.Error(), nil)
		rolledBackAll = false
	}
	run.markRolledBack(rbCtx, rolledBackAll)

	meta["rolled_back"] = rolledBackAll
	if cancelled {
		e.recordAudit(ctx, ac, "operations.operation.cancelled", j, true, meta)
		return nil, fmt.Errorf("%w: rolled back", jobs.ErrCancelled)
	}
	meta["error"] = failed.Error()
	e.recordAudit(ctx, ac, "operations.operation.failed", j, false, meta)
	if e.notifier != nil {
		e.notifier.Notify(ctx, j.OrganizationID, "job.failed", humanName(d.Name)+" falhou", failed.Error(), "job", j.ID.String())
	}
	log.Warn("operation failed", "error", failed, "rolled_back", rolledBackAll)
	return nil, failed
}

func (e *Engine) fail(ctx context.Context, ac authctx.AuthContext, d Definition, j jobs.Job, run *Run, err error) {
	e.recordAudit(ctx, ac, "operations.operation.failed", j, false, map[string]any{"operation": d.Name, "error": err.Error()})
	if e.notifier != nil {
		e.notifier.Notify(ctx, j.OrganizationID, "job.failed", humanName(d.Name)+" falhou", err.Error(), "job", j.ID.String())
	}
}

func stepsOf(op Operation) []Step {
	if s, ok := op.(Stepper); ok {
		return s.Steps()
	}
	return []Step{{Name: "execute", Run: func(ctx context.Context, r *Run) error { return op.Execute(ctx, r) }}}
}

// safeRun converts a panicking step into an error so rollback still happens.
func safeRun(ctx context.Context, f func(context.Context, *Run) error, r *Run) (err error) {
	if f == nil {
		return nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("step panicked: %v", rec)
		}
	}()
	return f(ctx, r)
}

func humanName(op string) string { return op }

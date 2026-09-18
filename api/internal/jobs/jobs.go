// Package jobs implements the generic asynchronous job system (section 16).
// PostgreSQL is the durable source of truth for job state (ADR-003); this
// phase-1 worker polls the jobs table directly with `FOR UPDATE SKIP LOCKED`
// rather than depending on Redis, so job processing works even before a
// Redis-backed queue is introduced. See docs/ROADMAP.md for the Redis
// delivery-mechanism upgrade path.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
)

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusRetrying  Status = "retrying"
)

type Job struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	Type           string          `json:"type"`
	Status         Status          `json:"status"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	Progress       float64         `json:"progress"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

type EnqueueInput struct {
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int             `json:"priority"`
	MaxAttempts    int             `json:"max_attempts"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	// IdempotencyKey, when set, makes Enqueue safe to call more than once
	// for "the same" logical job — a second call with the same key returns
	// the original job instead of creating a duplicate (rule 42).
	IdempotencyKey string `json:"idempotency_key"`
}

const permManage = "jobs.manage"
const permRead = "jobs.read"

func (s *Service) Enqueue(ctx context.Context, ac authctx.AuthContext, in EnqueueInput) (Job, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Job{}, err
	}
	if in.Type == "" {
		return Job{}, apierr.Validation("job type is required")
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 1
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 300
	}
	if in.Payload == nil {
		in.Payload = json.RawMessage(`{}`)
	}

	if in.IdempotencyKey != "" {
		if existing, ok, err := s.findByIdempotencyKey(ctx, ac.OrganizationID, in.IdempotencyKey); err != nil {
			return Job{}, err
		} else if ok {
			return existing, nil
		}
	}

	var j Job
	err := s.pool.QueryRow(ctx, `
		INSERT INTO jobs (organization_id, type, priority, payload, max_attempts, timeout_seconds, correlation_id, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))
		RETURNING id, organization_id, type, status, priority, payload, COALESCE(result, 'null'),
		          COALESCE(error, ''), progress, attempts, max_attempts, COALESCE(correlation_id, ''),
		          created_at, updated_at, started_at, finished_at
	`, ac.OrganizationID, in.Type, in.Priority, in.Payload, in.MaxAttempts, in.TimeoutSeconds, ac.CorrelationID, in.IdempotencyKey).Scan(
		&j.ID, &j.OrganizationID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result,
		&j.Error, &j.Progress, &j.Attempts, &j.MaxAttempts, &j.CorrelationID,
		&j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt,
	)
	if err != nil {
		return Job{}, apierr.Wrap(apierr.CodeInternal, "failed to enqueue job", err)
	}
	return j, nil
}

func (s *Service) findByIdempotencyKey(ctx context.Context, orgID uuid.UUID, key string) (Job, bool, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, type, status, priority, payload, COALESCE(result, 'null'),
		       COALESCE(error, ''), progress, attempts, max_attempts, COALESCE(correlation_id, ''),
		       created_at, updated_at, started_at, finished_at
		FROM jobs WHERE organization_id = $1 AND idempotency_key = $2
	`, orgID, key).Scan(
		&j.ID, &j.OrganizationID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result,
		&j.Error, &j.Progress, &j.Attempts, &j.MaxAttempts, &j.CorrelationID,
		&j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, apierr.Wrap(apierr.CodeInternal, "failed to check idempotency key", err)
	}
	return j, true, nil
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Job, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return Job{}, err
	}

	var j Job
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, type, status, priority, payload, COALESCE(result, 'null'),
		       COALESCE(error, ''), progress, attempts, max_attempts, COALESCE(correlation_id, ''),
		       created_at, updated_at, started_at, finished_at
		FROM jobs WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(
		&j.ID, &j.OrganizationID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result,
		&j.Error, &j.Progress, &j.Attempts, &j.MaxAttempts, &j.CorrelationID,
		&j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, apierr.NotFound("job")
	}
	if err != nil {
		return Job{}, apierr.Wrap(apierr.CodeInternal, "failed to load job", err)
	}
	return j, nil
}

// List returns up to limit jobs in the caller's organization, starting at
// offset, most recent first. See infrastructure.Service.List's doc comment
// for why this method enforces its own limit ceiling independent of the
// HTTP layer's.
func (s *Service) List(ctx context.Context, ac authctx.AuthContext, status Status, limit, offset int) ([]Job, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return nil, err
	}
	limit = normalizeLimit(limit)

	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, type, status, priority, payload, COALESCE(result, 'null'),
		       COALESCE(error, ''), progress, attempts, max_attempts, COALESCE(correlation_id, ''),
		       created_at, updated_at, started_at, finished_at
		FROM jobs
		WHERE organization_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`, ac.OrganizationID, string(status), limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list jobs", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.OrganizationID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result,
			&j.Error, &j.Progress, &j.Attempts, &j.MaxAttempts, &j.CorrelationID,
			&j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan job", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Cancel marks a queued job cancelled. It cannot cancel a job that is
// already running (no preemption mechanism exists) or finished.
func (s *Service) Cancel(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, permManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status = 'cancelled', updated_at = now(), finished_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'queued'
	`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to cancel job", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.Conflict("job is not in a cancellable (queued) state, or does not exist")
	}
	return nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

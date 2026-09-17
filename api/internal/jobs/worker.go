package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/logger"
)

// Handler executes one job and returns its result payload, or an error if
// the job failed. Handlers must respect ctx cancellation (the worker derives
// a per-job context from the job's timeout_seconds).
type Handler func(ctx context.Context, j Job) (result json.RawMessage, err error)

// Worker polls the jobs table directly with `FOR UPDATE SKIP LOCKED`
// (ADR-003: Postgres is the durable source of truth; this works with zero
// Redis dependency). Multiple Worker instances (e.g. one per process) can
// run concurrently against the same database safely — SKIP LOCKED ensures
// they never claim the same row.
type Worker struct {
	pool         *pgxpool.Pool
	handlers     map[string]Handler
	pollInterval time.Duration
}

func NewWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{
		pool:         pool,
		handlers:     make(map[string]Handler),
		pollInterval: 2 * time.Second,
	}
}

// Register associates a job type with the handler that executes it. Calling
// Enqueue for a type with no registered handler is not an error — the job
// simply fails once claimed, with a clear "no handler registered" error, so
// it's visible in job history rather than silently stuck (rule 36: no
// fabricated completion).
func (w *Worker) Register(jobType string, h Handler) {
	w.handlers[jobType] = h
}

// Run polls for queued jobs until ctx is cancelled. It never returns an
// error on its own — polling errors are logged and retried on the next
// tick, since a transient DB hiccup should not crash the worker process.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for w.claimAndRunOne(ctx) {
				// Drain the queue: keep claiming jobs back-to-back until
				// none are left, rather than waiting a full poll interval
				// between each one.
			}
		}
	}
}

// claimAndRunOne claims and executes at most one job. It returns true if a
// job was claimed (so the caller should immediately try again), false if the
// queue was empty.
func (w *Worker) claimAndRunOne(ctx context.Context) bool {
	log := logger.FromContext(ctx)

	job, ok, err := w.claim(ctx)
	if err != nil {
		log.Error("jobs: failed to claim job", "error", err)
		return false
	}
	if !ok {
		return false
	}

	w.execute(ctx, job)
	return true
}

func (w *Worker) claim(ctx context.Context) (Job, bool, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM jobs
		WHERE status = 'queued'
		ORDER BY priority DESC, created_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}

	var j Job
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET status = 'running', started_at = now(), attempts = attempts + 1, updated_at = now()
		WHERE id = $1
		RETURNING id, organization_id, type, status, priority, payload, COALESCE(result, 'null'),
		          COALESCE(error, ''), progress, attempts, max_attempts, timeout_seconds,
		          COALESCE(correlation_id, ''), created_at, updated_at, started_at, finished_at
	`, id).Scan(
		&j.ID, &j.OrganizationID, &j.Type, &j.Status, &j.Priority, &j.Payload, &j.Result,
		&j.Error, &j.Progress, &j.Attempts, &j.MaxAttempts, &j.TimeoutSeconds, &j.CorrelationID,
		&j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt,
	)
	if err != nil {
		return Job{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

func (w *Worker) execute(ctx context.Context, j Job) {
	log := logger.FromContext(ctx).With("job_id", j.ID, "job_type", j.Type)

	handler, ok := w.handlers[j.Type]
	if !ok {
		w.finish(ctx, j, nil, "no handler registered for job type: "+j.Type)
		return
	}

	timeout := time.Duration(j.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := w.runWithRecover(jobCtx, handler, j)
	if err != nil {
		log.Warn("job failed", "error", err, "attempt", j.Attempts, "max_attempts", j.MaxAttempts)
		if j.Attempts < j.MaxAttempts {
			w.retry(ctx, j, err.Error())
			return
		}
		w.finish(ctx, j, nil, err.Error())
		return
	}

	w.finish(ctx, j, result, "")
}

// runWithRecover ensures a panicking handler is reported as a failed job
// rather than crashing the worker process.
func (w *Worker) runWithRecover(ctx context.Context, h Handler, j Job) (result json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errPanic(r)
		}
	}()
	return h(ctx, j)
}

func (w *Worker) finish(ctx context.Context, j Job, result json.RawMessage, errMsg string) {
	status := StatusSucceeded
	if errMsg != "" {
		status = StatusFailed
	}
	if result == nil {
		result = json.RawMessage(`null`)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE jobs SET status = $2, result = $3, error = NULLIF($4, ''), progress = 100,
		                finished_at = now(), updated_at = now()
		WHERE id = $1
	`, j.ID, status, result, errMsg); err != nil {
		logger.FromContext(ctx).Error("jobs: failed to record job completion", "job_id", j.ID, "error", err)
	}
}

func (w *Worker) retry(ctx context.Context, j Job, errMsg string) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE jobs SET status = 'queued', error = $2, updated_at = now()
		WHERE id = $1
	`, j.ID, errMsg); err != nil {
		logger.FromContext(ctx).Error("jobs: failed to requeue job for retry", "job_id", j.ID, "error", err)
	}
}

type panicError struct{ v any }

func (e panicError) Error() string { return "job handler panicked" }

func errPanic(v any) error { return panicError{v: v} }

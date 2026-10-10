package ops

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platform/redact"
)

// Run is the handle an operation uses while executing: logging, progress,
// cancellation checks, and a small key/value state shared between steps (so a
// later step's Undo can see what an earlier step created).
type Run struct {
	e         *Engine
	JobID     uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	Payload   json.RawMessage
	opName    string

	mu     sync.Mutex
	state  map[string]any
	result any
	seq    int // current step sequence (for log association)
}

func (r *Run) Set(key string, v any) { r.mu.Lock(); r.state[key] = v; r.mu.Unlock() }
func (r *Run) Get(key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.state[key]
	return v, ok
}
func (r *Run) GetString(key string) string {
	if v, ok := r.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SetResult records the job's result payload.
func (r *Run) SetResult(v any) { r.mu.Lock(); r.result = v; r.mu.Unlock() }

func (r *Run) resultJSON() json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.result == nil {
		return json.RawMessage(`null`)
	}
	b, err := json.Marshal(r.result)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}

// Log appends a job log line. Secrets must never be passed here.
func (r *Run) Log(level, msg string, meta map[string]any) {
	switch level {
	case "warn":
		level = "warning"
	case "debug", "info", "success", "warning", "error":
	default:
		level = "info"
	}
	if meta == nil {
		meta = map[string]any{}
	}
	mj, _ := json.Marshal(meta)
	mj = []byte(redact.String(string(mj)))
	msg = redact.String(msg)
	ctx := context.Background()
	r.mu.Lock()
	seq := r.seq
	r.mu.Unlock()
	if _, err := r.e.pool.Exec(ctx, `
		INSERT INTO job_logs (job_id, organization_id, step_seq, level, message, metadata) VALUES ($1, $2, $3, $4, $5, $6)
	`, r.JobID, r.OrgID, seq, level, msg, mj); err != nil {
		logger.FromContext(ctx).Error("failed to write job log", "error", err)
	}
	if r.ProjectID != uuid.Nil {
		source := "system"
		if len(r.opName) >= 10 && r.opName[:10] == "deployment" {
			source = "deployment"
		} else if len(r.opName) >= 9 && r.opName[:9] == "migration" {
			source = "migration"
		}
		_, _ = r.e.pool.Exec(ctx, `
			INSERT INTO log_entries (organization_id, project_id, source, service, level, message, metadata) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, r.OrgID, r.ProjectID, source, r.opName, level, msg, mj)
	}
}

func (r *Run) cancelRequested(ctx context.Context) bool {
	var c bool
	if err := r.e.pool.QueryRow(ctx, `SELECT cancel_requested FROM jobs WHERE id = $1`, r.JobID).Scan(&c); err != nil {
		return false
	}
	return c
}

// CheckCancel lets long steps abort cooperatively.
func (r *Run) CheckCancel(ctx context.Context) error {
	if r.cancelRequested(ctx) {
		return errCancelRequested
	}
	return nil
}

type cancelErr struct{}

func (cancelErr) Error() string { return "cancel requested" }

var errCancelRequested error = cancelErr{}

func (r *Run) initSteps(ctx context.Context, steps []Step) error {
	for i, s := range steps {
		if _, err := r.e.pool.Exec(ctx, `
			INSERT INTO job_steps (job_id, organization_id, seq, name) VALUES ($1, $2, $3, $4)
			ON CONFLICT (job_id, seq) DO UPDATE SET name = EXCLUDED.name, status = 'pending', error = NULL, started_at = NULL, finished_at = NULL
		`, r.JobID, r.OrgID, i, s.Name); err != nil {
			return err
		}
	}
	return nil
}

func (r *Run) beginStep(ctx context.Context, seq int, name string) {
	r.mu.Lock()
	r.seq = seq
	r.mu.Unlock()
	_, _ = r.e.pool.Exec(ctx, `UPDATE job_steps SET status = 'running', started_at = now() WHERE job_id = $1 AND seq = $2`, r.JobID, seq)
	r.Log("info", "step started: "+name, nil)
}

func (r *Run) endStep(ctx context.Context, seq int, status string, err error) {
	var msg *string
	if err != nil {
		s := err.Error()
		msg = &s
	}
	progress := 0.0
	if status == "succeeded" {
		progress = 100
	}
	_, _ = r.e.pool.Exec(ctx, `
		UPDATE job_steps SET status = $3, error = $4, progress = $5, finished_at = now() WHERE job_id = $1 AND seq = $2
	`, r.JobID, seq, status, msg, progress)
}

func (r *Run) setProgress(ctx context.Context, p float64) {
	_, _ = r.e.pool.Exec(ctx, `UPDATE jobs SET progress = $2, updated_at = now() WHERE id = $1`, r.JobID, p)
}

func (r *Run) setJobStatus(ctx context.Context, status string) {
	_, _ = r.e.pool.Exec(ctx, `UPDATE jobs SET status = $2, updated_at = now() WHERE id = $1`, r.JobID, status)
}

func (r *Run) markRolledBack(ctx context.Context, all bool) {
	_, _ = r.e.pool.Exec(ctx, `UPDATE jobs SET rolled_back = $2, updated_at = now() WHERE id = $1`, r.JobID, all)
}

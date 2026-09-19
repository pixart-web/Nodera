package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestJobsEnqueueWorkerProcessesAndReportsResult(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "jobs-owner@nodera.dev")

	jobsSvc := jobs.New(pool, h.audit)

	j, err := jobsSvc.Enqueue(ctx, ac, jobs.EnqueueInput{
		Type:    "test.echo",
		Payload: json.RawMessage(`{"message":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if j.Status != jobs.StatusQueued {
		t.Fatalf("expected a freshly enqueued job to be queued, got %q", j.Status)
	}

	worker := jobs.NewWorker(pool)
	worker.Register("test.echo", func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
		return j.Payload, nil
	})

	workerCtx, stopWorker := context.WithCancel(ctx)
	go worker.Run(workerCtx)
	defer stopWorker()

	deadline := time.After(5 * time.Second)
	for {
		got, err := jobsSvc.Get(ctx, ac, j.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == jobs.StatusSucceeded {
			var result struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(got.Result, &result); err != nil {
				t.Fatalf("failed to unmarshal job result %s: %v", got.Result, err)
			}
			if result.Message != "hello" {
				t.Fatalf("unexpected job result: %s", got.Result)
			}
			break
		}
		if got.Status == jobs.StatusFailed {
			t.Fatalf("job unexpectedly failed: %s", got.Error)
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for job to complete, last status: %s", got.Status)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// A job with no registered handler fails visibly instead of hanging
// forever (rule 36: no fabricated completion).
func TestJobsUnregisteredHandlerFailsVisibly(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "jobs-owner-2@nodera.dev")

	jobsSvc := jobs.New(pool, h.audit)
	j, err := jobsSvc.Enqueue(ctx, ac, jobs.EnqueueInput{Type: "no.such.handler"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	worker := jobs.NewWorker(pool) // no handlers registered
	workerCtx, stopWorker := context.WithCancel(ctx)
	go worker.Run(workerCtx)
	defer stopWorker()

	deadline := time.After(5 * time.Second)
	for {
		got, err := jobsSvc.Get(ctx, ac, j.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == jobs.StatusFailed {
			if got.Error == "" {
				t.Fatal("expected a non-empty error message for an unhandled job type")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for job to fail, last status: %s", got.Status)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Retry re-queues a genuinely failed job (not a mock) for another full
// attempt cycle, and a second pass with a now-registered handler lets it
// actually succeed — proving Retry doesn't just flip a status flag but
// puts the job back somewhere the real worker will pick it up again.
func TestJobsRetryRequeuesAFailedJobForAnotherAttempt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "jobs-retry-owner@nodera.dev")

	jobsSvc := jobs.New(pool, h.audit)
	j, err := jobsSvc.Enqueue(ctx, ac, jobs.EnqueueInput{Type: "test.retry-me"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// First pass: no handler registered, so it genuinely fails (same
	// mechanism as TestJobsUnregisteredHandlerFailsVisibly).
	worker := jobs.NewWorker(pool)
	workerCtx, stopWorker := context.WithCancel(ctx)
	go worker.Run(workerCtx)

	waitForStatus(t, ctx, jobsSvc, ac, j.ID, jobs.StatusFailed)
	stopWorker()

	// Retrying anything but a failed job is refused.
	if err := jobsSvc.Cancel(ctx, ac, j.ID); err == nil {
		t.Fatal("expected cancelling an already-failed job to be refused (only queued jobs are cancellable)")
	}

	retried, err := jobsSvc.Retry(ctx, ac, j.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if retried.Status != jobs.StatusQueued {
		t.Fatalf("expected status 'queued' after retry, got %q", retried.Status)
	}
	if retried.Attempts != 0 || retried.Error != "" {
		t.Fatalf("expected attempts and error to be reset, got attempts=%d error=%q", retried.Attempts, retried.Error)
	}

	// Retrying a job that isn't in the failed state is refused.
	if _, err := jobsSvc.Retry(ctx, ac, j.ID); err == nil {
		t.Fatal("expected retrying a queued (not failed) job to be refused")
	}

	// Second pass: this time with the handler registered, the same job
	// (same ID) actually succeeds.
	worker2 := jobs.NewWorker(pool)
	worker2.Register("test.retry-me", func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	})
	worker2Ctx, stopWorker2 := context.WithCancel(ctx)
	defer stopWorker2()
	go worker2.Run(worker2Ctx)

	waitForStatus(t, ctx, jobsSvc, ac, j.ID, jobs.StatusSucceeded)
}

// Enqueue/Cancel/Retry now write real audit entries — verifies they're
// actually queryable, not just written.
func TestJobs_WritesAuditEntries(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "jobs-audit-owner@nodera.dev")

	jobsSvc := jobs.New(pool, h.audit)

	j, err := jobsSvc.Enqueue(ctx, ac, jobs.EnqueueInput{Type: "test.audited", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := jobsSvc.Cancel(ctx, ac, j.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	records, err := h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID, ResourceType: "job", ResourceID: j.ID.String(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var sawEnqueued, sawCancelled bool
	for _, r := range records {
		switch r.Action {
		case "jobs.job.enqueued":
			sawEnqueued = true
		case "jobs.job.cancelled":
			sawCancelled = true
		}
	}
	if !sawEnqueued || !sawCancelled {
		t.Fatalf("expected both enqueued and cancelled audit actions, got %+v", records)
	}
}

// waitForStatus polls Get until the job reaches want or the context is
// done, failing the test on timeout — shared by the retry test above and
// could replace the duplicated polling loops in the two tests above it in
// a future cleanup pass, but that's out of scope for this change.
func waitForStatus(t *testing.T, ctx context.Context, jobsSvc *jobs.Service, ac authctx.AuthContext, id uuid.UUID, want jobs.Status) jobs.Job {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		got, err := jobsSvc.Get(ctx, ac, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == want {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for job status %q, last status: %s", want, got.Status)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

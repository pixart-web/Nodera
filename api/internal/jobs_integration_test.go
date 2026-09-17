package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestJobsEnqueueWorkerProcessesAndReportsResult(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "jobs-owner@nodera.dev")

	jobsSvc := jobs.New(pool)

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

	jobsSvc := jobs.New(pool)
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

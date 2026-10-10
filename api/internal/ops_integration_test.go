package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

type recNotifier struct {
	mu   sync.Mutex
	kind []string
}

func (n *recNotifier) Notify(_ context.Context, _ uuid.UUID, kind, _, _, _, _ string) {
	n.mu.Lock()
	n.kind = append(n.kind, kind)
	n.mu.Unlock()
}

// testOp is a configurable step operation that records execution order.
type testOp struct {
	log      *[]string
	failAt   string
	invalid  bool
	panicAt  string
	cancelAt string
	runRef   **ops.Run
}

func (o *testOp) Name() string { return "test.op" }
func (o *testOp) Validate(context.Context) error {
	if o.invalid {
		return apierr.Validation("nope")
	}
	return nil
}
func (o *testOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *testOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *testOp) Steps() []ops.Step {
	mk := func(name string) ops.Step {
		return ops.Step{
			Name: name,
			Run: func(_ context.Context, r *ops.Run) error {
				*o.log = append(*o.log, "run:"+name)
				if o.panicAt == name {
					panic("kaboom")
				}
				if o.failAt == name {
					return errors.New("step " + name + " exploded")
				}
				r.SetResult(map[string]string{"last": name})
				return nil
			},
			Undo: func(_ context.Context, _ *ops.Run) error {
				*o.log = append(*o.log, "undo:"+name)
				return nil
			},
		}
	}
	return []ops.Step{mk("a"), mk("b"), mk("c")}
}

func newOpsHarness(t *testing.T, o *testOp) (*testHarness, *ops.Engine, *jobs.Worker, *recNotifier, context.Context) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	eng := ops.New(pool, h.audit)
	n := &recNotifier{}
	eng.SetNotifier(n)
	eng.Register(ops.Definition{
		Name: "test.op", Permission: "projects.create", NotifyKind: "test.done",
		Factory: func(_ *ops.Env, _, _ uuid.UUID, _ json.RawMessage) (ops.Operation, error) { return o, nil },
	})
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return h, eng, w, n, context.Background()
}

func TestOps_SuccessPersistsStepsLogsProgressAndNotifies(t *testing.T) {
	var log []string
	h, eng, w, n, ctx := newOpsHarness(t, &testOp{log: &log})
	ac, _ := h.newOwnerContext(t, ctx, "ops-ok@nodera.dev")

	ref, err := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op", Payload: map[string]string{"x": "y"}})
	if err != nil {
		t.Fatal(err)
	}
	if !w.RunOnce(ctx) {
		t.Fatal("expected a queued job")
	}
	v, err := eng.Get(ctx, ac, ref.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "succeeded" || v.Progress != 100 || v.RolledBack {
		t.Fatalf("unexpected view %+v", v)
	}
	steps, _ := eng.Steps(ctx, ac, ref.JobID)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	for _, s := range steps {
		if s.Status != "succeeded" || s.StartedAt == nil || s.FinishedAt == nil {
			t.Fatalf("step not fully recorded: %+v", s)
		}
	}
	logs, _ := eng.Logs(ctx, ac, ref.JobID, 0, 100)
	if len(logs) < 5 {
		t.Fatalf("expected step logs, got %d", len(logs))
	}
	if strings.Join(log, ",") != "run:a,run:b,run:c" {
		t.Fatalf("execution order %v", log)
	}
	if len(n.kind) != 1 || n.kind[0] != "test.done" {
		t.Fatalf("notifications %v", n.kind)
	}
	// Audit trail covers submit/start/succeed with the shared correlation chain.
	recs, _ := h.audit.Query(ctx, ac, auditFilter(ac))
	got := map[string]bool{}
	for _, r := range recs {
		got[r.Action] = true
	}
	for _, a := range []string{"operations.operation.submitted", "operations.operation.started", "operations.operation.succeeded"} {
		if !got[a] {
			t.Errorf("missing audit action %s", a)
		}
	}
}

func TestOps_FailureRollsBackCompletedStepsInReverse(t *testing.T) {
	var log []string
	h, eng, w, n, ctx := newOpsHarness(t, &testOp{log: &log, failAt: "c"})
	ac, _ := h.newOwnerContext(t, ctx, "ops-fail@nodera.dev")
	ref, _ := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op"})
	w.RunOnce(ctx)

	v, _ := eng.Get(ctx, ac, ref.JobID)
	if v.Status != "failed" || !v.RolledBack || !strings.Contains(v.Error, "exploded") {
		t.Fatalf("unexpected view %+v", v)
	}
	// The failed step is undone too (it may have partially applied), then b, then a.
	if strings.Join(log, ",") != "run:a,run:b,run:c,undo:c,undo:b,undo:a" {
		t.Fatalf("rollback order wrong: %v", log)
	}
	steps, _ := eng.Steps(ctx, ac, ref.JobID)
	want := []string{"rolled_back", "rolled_back", "rolled_back"}
	for i, s := range steps {
		if s.Status != want[i] {
			t.Errorf("step %d status %q", i, s.Status)
		}
	}
	if len(n.kind) != 1 || n.kind[0] != "job.failed" {
		t.Fatalf("expected a job.failed notification, got %v", n.kind)
	}
}

func TestOps_PanicInStepIsContainedAndRolledBack(t *testing.T) {
	var log []string
	h, eng, w, _, ctx := newOpsHarness(t, &testOp{log: &log, panicAt: "b"})
	ac, _ := h.newOwnerContext(t, ctx, "ops-panic@nodera.dev")
	ref, _ := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op"})
	w.RunOnce(ctx)
	v, _ := eng.Get(ctx, ac, ref.JobID)
	if v.Status != "failed" || !v.RolledBack {
		t.Fatalf("unexpected view %+v", v)
	}
}

func TestOps_CancelBeforeStartRollsBackNothingAndMarksCancelled(t *testing.T) {
	var log []string
	h, eng, w, _, ctx := newOpsHarness(t, &testOp{log: &log})
	ac, _ := h.newOwnerContext(t, ctx, "ops-cancel@nodera.dev")
	ref, _ := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op"})
	if err := eng.RequestCancel(ctx, ac, ref.JobID); err != nil {
		t.Fatal(err)
	}
	if w.RunOnce(ctx) {
		t.Fatal("a cancelled queued job must not be claimed")
	}
	v, _ := eng.Get(ctx, ac, ref.JobID)
	if v.Status != "cancelled" || len(log) != 0 {
		t.Fatalf("unexpected: %+v log=%v", v, log)
	}
	if err := eng.RequestCancel(ctx, ac, ref.JobID); err == nil {
		t.Fatal("cancelling a finished operation must conflict")
	}
}

func TestOps_SubmitIsIdempotentUnderConcurrency(t *testing.T) {
	var log []string
	h, eng, _, _, ctx := newOpsHarness(t, &testOp{log: &log})
	ac, _ := h.newOwnerContext(t, ctx, "ops-idem@nodera.dev")
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 12)
	created := make(chan bool, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op", IdempotencyKey: "same-key"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- r.JobID
			created <- r.Created
		}()
	}
	wg.Wait()
	close(ids)
	close(created)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one job for one idempotency key, got %d", len(seen))
	}
	n := 0
	for c := range created {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exactly one submit must report created=true, got %d", n)
	}
}

func TestOps_PermissionValidationAndTenantIsolation(t *testing.T) {
	var log []string
	h, eng, _, _, ctx := newOpsHarness(t, &testOp{log: &log})
	owner, _ := h.newOwnerContext(t, ctx, "ops-owner@nodera.dev")
	member := h.newMemberContext(t, ctx, owner.OrganizationID, "ops-member@nodera.dev")
	other, _ := h.newOwnerContext(t, ctx, "ops-other@nodera.dev")

	if _, err := eng.Submit(ctx, member, ops.SubmitInput{Operation: "test.op"}); err == nil {
		t.Fatal("a member without projects.create must be forbidden")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
	if _, err := eng.Submit(ctx, owner, ops.SubmitInput{Operation: "nope"}); err == nil {
		t.Fatal("unknown operation must be rejected")
	}
	ref, _ := eng.Submit(ctx, owner, ops.SubmitInput{Operation: "test.op"})
	// Cross-tenant: another organization's owner must not see, list, step, log or cancel it.
	if _, err := eng.Get(ctx, other, ref.JobID); err == nil {
		t.Fatal("cross-tenant Get must fail")
	}
	if _, err := eng.Steps(ctx, other, ref.JobID); err == nil {
		t.Fatal("cross-tenant Steps must fail")
	}
	if _, err := eng.Logs(ctx, other, ref.JobID, 0, 10); err == nil {
		t.Fatal("cross-tenant Logs must fail")
	}
	if err := eng.RequestCancel(ctx, other, ref.JobID); err == nil {
		t.Fatal("cross-tenant cancel must fail")
	}
	if l, _ := eng.List(ctx, other, uuid.Nil, 50, 0); len(l) != 0 {
		t.Fatal("cross-tenant List must be empty")
	}
}

func TestOps_ValidationFailsAtSubmitWithoutCreatingAJob(t *testing.T) {
	var log []string
	h, eng, _, _, ctx := newOpsHarness(t, &testOp{log: &log, invalid: true})
	ac, _ := h.newOwnerContext(t, ctx, "ops-invalid@nodera.dev")
	if _, err := eng.Submit(ctx, ac, ops.SubmitInput{Operation: "test.op"}); err == nil {
		t.Fatal("expected a validation error")
	}
	if l, _ := eng.List(ctx, ac, uuid.Nil, 50, 0); len(l) != 0 {
		t.Fatal("no job may be created for an invalid operation")
	}
}

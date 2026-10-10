// Package aiplans is the AI operations layer: the model may analyse a
// project, suggest actions and draft a PLAN, but it never executes anything.
//
//	goal -> AI proposes steps -> every step is vetted against the real
//	operation registry (exists, caller holds the permission, payload valid,
//	preconditions hold) -> a human approves the plan -> a human runs each step
//	in order under their own permissions (dangerous steps still need the
//	Tool Gateway approval) -> everything is audited.
//
// The model's output is untrusted input: it is parsed as data, size-limited,
// and only operation names that exist in the registry are accepted. There is
// no code path from model text to a shell or arbitrary provider call.
package aiplans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platform/redact"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tools"
)

const maxSteps = 8

// Chatter is satisfied by *ai.Service.
type Chatter interface {
	Chat(ctx context.Context, ac authctx.AuthContext, profileKey string, messages []providers.Message) (ai.ChatResult, error)
}

type Gateway interface {
	Execute(ctx context.Context, ac authctx.AuthContext, toolKey string, in tools.ExecuteInput) (tools.ExecuteResult, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
	chat  Chatter
	eng   *ops.Engine
	gate  Gateway
}

func New(pool *pgxpool.Pool, a AuditRecorder, chat Chatter, eng *ops.Engine, gate Gateway) *Service {
	return &Service{pool: pool, audit: a, chat: chat, eng: eng, gate: gate}
}

type Step struct {
	Index      int             `json:"index"`
	Operation  string          `json:"operation"`
	Payload    json.RawMessage `json:"payload"`
	Rationale  string          `json:"rationale"`
	Dangerous  bool            `json:"dangerous"`
	Valid      bool            `json:"valid"`
	Issue      string          `json:"issue,omitempty"`
	Status     string          `json:"status"` // pending | submitted | awaiting_approval
	JobID      *uuid.UUID      `json:"job_id,omitempty"`
	ApprovalID *uuid.UUID      `json:"approval_id,omitempty"`
}

type Plan struct {
	ID        uuid.UUID  `json:"id"`
	ProjectID *uuid.UUID `json:"project_id"`
	Goal      string     `json:"goal"`
	Summary   string     `json:"summary"`
	Status    string     `json:"status"`
	Steps     []Step     `json:"steps"`
	Model     string     `json:"model"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at"`
}

func (s *Service) catalog(ac authctx.AuthContext) string {
	defs := s.eng.Definitions()
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	var b strings.Builder
	for _, d := range defs {
		if rbac.Require(ac, d.Permission) != nil {
			continue // never even mention operations the user cannot run
		}
		danger := ""
		if d.ToolKey != "" {
			danger = " (dangerous: needs human approval)"
		}
		fmt.Fprintf(&b, "- %s%s\n", d.Name, danger)
	}
	return b.String()
}

const systemPrompt = `You are Nodera's infrastructure planning assistant. You may ONLY propose plans; you never execute anything.
Reply with a single JSON object and nothing else:
{"summary":"one sentence","steps":[{"operation":"<name from the list>","payload":{...},"rationale":"why"}]}
Rules: use only operation names from the list below; at most 8 steps; never include shell commands, URLs to fetch, credentials or secrets;
if the goal cannot be met with the listed operations, return {"summary":"explain why","steps":[]}.
Allowed operations:
`

// Propose asks the model for a plan and vets every step before storing it.
func (s *Service) Propose(ctx context.Context, ac authctx.AuthContext, profileKey string, projectID *uuid.UUID, goal string) (Plan, error) {
	if err := rbac.Require(ac, "ai.use"); err != nil {
		return Plan{}, err
	}
	goal = strings.TrimSpace(goal)
	if goal == "" || len(goal) > 2000 {
		return Plan{}, apierr.Validation("goal is required (max 2000 characters)")
	}
	facts := "No project selected."
	if projectID != nil {
		var name, kind, status string
		if err := s.pool.QueryRow(ctx, `SELECT name, kind, status FROM projects WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, *projectID, ac.OrganizationID).Scan(&name, &kind, &status); err != nil {
			return Plan{}, apierr.NotFound("project")
		}
		facts = fmt.Sprintf("Project %q is a %s project with status %s.", name, kind, status) // metadata only: no config, no secrets
	}
	res, err := s.chat.Chat(ctx, ac, profileKey, []providers.Message{
		{Role: "system", Content: systemPrompt + s.catalog(ac)},
		{Role: "user", Content: facts + "\nGoal: " + redact.String(goal)},
	})
	if err != nil {
		return Plan{}, err
	}
	summary, steps := s.parse(ctx, ac, projectID, res.Content)
	if len(steps) == 0 && summary == "" {
		summary = "The model did not return an actionable plan."
	}
	stepsJSON, _ := json.Marshal(steps)
	var uid any
	if ac.ActorType == authctx.ActorUser {
		uid = ac.ActorID
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO ai_plans (organization_id, project_id, created_by_user_id, goal, summary, steps, model)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, ac.OrganizationID, projectID, uid, goal, truncate(summary, 500), stepsJSON, res.ProviderKey+"/"+res.Model).Scan(&id)
	if err != nil {
		return Plan{}, apierr.Wrap(apierr.CodeInternal, "failed to store plan", err)
	}
	s.rec(ctx, ac, "aiplans.plan.proposed", id, map[string]any{"steps": len(steps), "model": res.Model})
	return s.Get(ctx, ac, id)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// parse extracts the JSON object from model output (tolerating code fences)
// and vets each step. Unknown operations and over-limit input are dropped.
func (s *Service) parse(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, content string) (string, []Step) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end <= start || end-start > 32<<10 {
		return "", nil
	}
	var raw struct {
		Summary string `json:"summary"`
		Steps   []struct {
			Operation string          `json:"operation"`
			Payload   json.RawMessage `json:"payload"`
			Rationale string          `json:"rationale"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &raw); err != nil {
		return "", nil
	}
	var steps []Step
	for _, rs := range raw.Steps {
		if len(steps) >= maxSteps {
			break
		}
		def, known := s.eng.Describe(rs.Operation)
		if !known {
			continue // the model invented an operation: ignore it entirely
		}
		if len(rs.Payload) == 0 || len(rs.Payload) > 8<<10 {
			rs.Payload = json.RawMessage(`{}`)
		}
		st := Step{Index: len(steps), Operation: rs.Operation, Payload: rs.Payload, Rationale: truncate(rs.Rationale, 400), Dangerous: def.ToolKey != "", Status: "pending", Valid: true}
		var pid uuid.UUID
		if projectID != nil {
			pid = *projectID
		}
		var payload any
		_ = json.Unmarshal(rs.Payload, &payload)
		if err := s.eng.Check(ctx, ac, ops.SubmitInput{Operation: rs.Operation, ProjectID: pid, Payload: payload}); err != nil {
			// A dangerous op cannot be Submit-checked the normal way only for the
			// direct-submit guard; Check bypasses that, so any error here is real.
			st.Valid, st.Issue = false, truncate(err.Error(), 300)
		}
		steps = append(steps, st)
	}
	return raw.Summary, steps
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action string, id uuid.UUID, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: "ai_plan", ResourceID: id.String(), Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

const planCols = `id, project_id, goal, summary, status, steps, model, created_at, decided_at`

func scanPlan(row pgx.Row) (Plan, error) {
	var p Plan
	var steps []byte
	err := row.Scan(&p.ID, &p.ProjectID, &p.Goal, &p.Summary, &p.Status, &steps, &p.Model, &p.CreatedAt, &p.DecidedAt)
	if err == nil {
		_ = json.Unmarshal(steps, &p.Steps)
		if p.Steps == nil {
			p.Steps = []Step{}
		}
	}
	return p, err
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Plan, error) {
	if err := rbac.Require(ac, "ai.use"); err != nil {
		return Plan{}, err
	}
	p, err := scanPlan(s.pool.QueryRow(ctx, `SELECT `+planCols+` FROM ai_plans WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, apierr.NotFound("plan")
	}
	return p, err
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext, limit, offset int) ([]Plan, error) {
	if err := rbac.Require(ac, "ai.use"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `SELECT `+planCols+` FROM ai_plans WHERE organization_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, ac.OrganizationID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list plans", err)
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Decide approves or rejects a plan. Approving does not run anything.
func (s *Service) Decide(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, approve bool) (Plan, error) {
	if err := rbac.Require(ac, "approvals.decide"); err != nil {
		return Plan{}, err
	}
	next := "rejected"
	if approve {
		next = "approved"
	}
	var uid any
	if ac.ActorType == authctx.ActorUser {
		uid = ac.ActorID
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ai_plans SET status=$3, decided_by_user_id=$4, decided_at=now() WHERE id=$1 AND organization_id=$2 AND status='proposed'`, id, ac.OrganizationID, next, uid)
	if err != nil {
		return Plan{}, apierr.Wrap(apierr.CodeInternal, "failed to decide plan", err)
	}
	if tag.RowsAffected() == 0 {
		return Plan{}, apierr.Conflict("plan is not awaiting a decision")
	}
	s.rec(ctx, ac, "aiplans.plan."+next, id, nil)
	return s.Get(ctx, ac, id)
}

// RunStep executes the next pending step of an approved plan under the
// CALLER's identity and permissions. Steps run strictly in order.
func (s *Service) RunStep(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, index int) (Plan, error) {
	if _, err := s.Get(ctx, ac, id); err != nil {
		return Plan{}, err
	}
	// Lock the plan row so two concurrent "run" clicks cannot both start a step.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Plan{}, apierr.Wrap(apierr.CodeInternal, "failed to start", err)
	}
	defer tx.Rollback(ctx)
	p, err := scanPlan(tx.QueryRow(ctx, `SELECT `+planCols+` FROM ai_plans WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, ac.OrganizationID))
	if err != nil {
		return Plan{}, apierr.NotFound("plan")
	}
	if p.Status != "approved" {
		return Plan{}, apierr.Conflict("plan must be approved before any step runs")
	}
	if index < 0 || index >= len(p.Steps) {
		return Plan{}, apierr.NotFound("step")
	}
	for i := 0; i < index; i++ {
		if p.Steps[i].Status == "pending" {
			return Plan{}, apierr.Conflict(fmt.Sprintf("step %d must run first", i))
		}
	}
	st := p.Steps[index]
	if st.Status != "pending" {
		return Plan{}, apierr.Conflict("this step has already been run")
	}
	if !st.Valid {
		return Plan{}, apierr.Conflict("this step failed validation and cannot run: " + st.Issue)
	}
	var pid uuid.UUID
	if p.ProjectID != nil {
		pid = *p.ProjectID
	}
	var payload map[string]any
	_ = json.Unmarshal(st.Payload, &payload)
	def, _ := s.eng.Describe(st.Operation)
	if def.ToolKey != "" {
		res, err := s.gate.Execute(ctx, ac, def.ToolKey, tools.ExecuteInput{ResourceType: "project", ResourceID: pid.String(), Parameters: payload})
		if err != nil {
			return Plan{}, err
		}
		st.Status, st.ApprovalID = "awaiting_approval", res.ApprovalID
	} else {
		ref, err := s.eng.Submit(ctx, ac, ops.SubmitInput{Operation: st.Operation, ProjectID: pid, Payload: payload, IdempotencyKey: fmt.Sprintf("aiplan:%s:%d", id, index)})
		if err != nil {
			return Plan{}, err
		}
		st.Status, st.JobID = "submitted", &ref.JobID
	}
	p.Steps[index] = st
	done := true
	for _, x := range p.Steps {
		if x.Status == "pending" {
			done = false
		}
	}
	b, _ := json.Marshal(p.Steps)
	status := p.Status
	if done {
		status = "completed"
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_plans SET steps=$3, status=$4 WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID, b, status); err != nil {
		return Plan{}, apierr.Wrap(apierr.CodeInternal, "failed to record step", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Plan{}, apierr.Wrap(apierr.CodeInternal, "failed to record step", err)
	}
	s.rec(ctx, ac, "aiplans.step.run", id, map[string]any{"index": index, "operation": st.Operation})
	return s.Get(ctx, ac, id)
}

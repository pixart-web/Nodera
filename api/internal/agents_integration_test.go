package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/agents"
	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

// newAgentsServices builds the ai/tools/agents services a test needs,
// wired the same way cmd/server/main.go wires them (minus provider
// adapters beyond local-echo, which needs no external dependency — rule 39).
func newAgentsServices(h *testHarness) (*agents.Service, *ai.Service, *tools.Registry) {
	aiSvc := ai.New(h.pool, h.audit, localecho.New())
	toolsSvc := tools.New(h.pool, h.audit)
	agentsSvc := agents.New(h.pool, h.audit, aiSvc, toolsSvc)
	return agentsSvc, aiSvc, toolsSvc
}

// A caller cannot create an agent scoped with a permission they don't hold
// themselves — same no-privilege-escalation rule as API token scopes.
func TestAgents_CannotExceedCreatorPermissions(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-scope-owner@nodera.dev")
	agentsSvc, _, _ := newAgentsServices(h)

	// The owner genuinely holds every permission, so to exercise the
	// rejection we need a caller who doesn't — a 'member'.
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "agent-scope-member@nodera.dev")

	_, err := agentsSvc.CreateAgent(ctx, memberAC, agents.CreateAgentInput{
		Name:            "should-fail",
		AIProfileKey:    "test.profile",
		PermissionScope: []string{"infrastructure.manage"}, // member doesn't hold this
	})
	if err == nil {
		t.Fatal("expected creating an agent scoped beyond the caller's own permissions to fail")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestAgents_RejectsUnknownToolKey(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-tool-owner@nodera.dev")
	agentsSvc, _, _ := newAgentsServices(h)

	_, err := agentsSvc.CreateAgent(ctx, ac, agents.CreateAgentInput{
		Name:            "bad-tool-agent",
		AIProfileKey:    "test.profile",
		AllowedToolKeys: []string{"not_a_real_tool"},
	})
	if err == nil {
		t.Fatal("expected creating an agent with an unknown tool key to fail")
	}
}

// A newly created agent starts disabled and cannot be run until explicitly
// enabled — verified end to end via SetStatus.
func TestAgents_CreateEnableRun(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-run-owner@nodera.dev")
	agentsSvc, aiSvc, _ := newAgentsServices(h)

	if _, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "agent.echo",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	a, err := agentsSvc.CreateAgent(ctx, ac, agents.CreateAgentInput{
		Name:               "helper-bot",
		SystemInstructions: "You are a terse assistant.",
		AIProfileKey:       "agent.echo",
		PermissionScope:    []string{"ai.use"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.Status != "disabled" {
		t.Fatalf("expected a freshly created agent to start disabled, got %q", a.Status)
	}

	if _, err := agentsSvc.Run(ctx, ac, a.ID, "hello"); err == nil {
		t.Fatal("expected running a disabled agent to fail")
	}

	enabled, err := agentsSvc.SetStatus(ctx, ac, a.ID, true)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if enabled.Status != "active" {
		t.Fatalf("expected status 'active' after enabling, got %q", enabled.Status)
	}

	result, err := agentsSvc.Run(ctx, ac, a.ID, "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Content != "echo: hello" {
		t.Fatalf("unexpected chat content: %q", result.Content)
	}
	if result.ProviderKey != "local-echo" {
		t.Fatalf("expected the local-echo provider, got %q", result.ProviderKey)
	}
}

// An agent whose permission_scope doesn't include ai.use cannot Run, even
// though agents.execute (the caller's own permission) is satisfied — the
// agent's own scope is what gates the AI call, not the caller's.
func TestAgents_RunRequiresAIUseInAgentScope(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-noaiuse-owner@nodera.dev")
	agentsSvc, aiSvc, _ := newAgentsServices(h)

	if _, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "agent.echo2",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	a, err := agentsSvc.CreateAgent(ctx, ac, agents.CreateAgentInput{
		Name:            "no-ai-scope-agent",
		AIProfileKey:    "agent.echo2",
		PermissionScope: []string{"infrastructure.read"}, // deliberately no ai.use
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := agentsSvc.SetStatus(ctx, ac, a.ID, true); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if _, err := agentsSvc.Run(ctx, ac, a.ID, "hello"); err == nil {
		t.Fatal("expected Run to fail when the agent's own permission_scope lacks ai.use")
	}
}

// ExecuteTool respects both allowed_tool_keys (agent-level, checked first)
// and the underlying Tool Gateway pipeline (agent's permission_scope,
// risk tier, handler existence) — a tool not on the allowlist never even
// reaches the Tool Gateway.
func TestAgents_ExecuteToolRespectsAllowlistAndScope(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-tool-exec-owner@nodera.dev")
	agentsSvc, _, _ := newAgentsServices(h)

	// get_container_logs is read-risk, required_permission
	// infrastructure.read, and — unlike check_ssl — has no execution
	// backend at all yet (implemented=false in the registry itself, not
	// just "no handler registered in this test process"), so it's the
	// right tool to prove a clean NOT_IMPLEMENTED path with.
	a, err := agentsSvc.CreateAgent(ctx, ac, agents.CreateAgentInput{
		Name:            "log-reader-agent",
		AIProfileKey:    "unused.profile",
		AllowedToolKeys: []string{"get_container_logs"},
		PermissionScope: []string{"infrastructure.read"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := agentsSvc.SetStatus(ctx, ac, a.ID, true); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// Not on the allowlist at all.
	if _, err := agentsSvc.ExecuteTool(ctx, ac, a.ID, "restart_container", tools.ExecuteInput{}); err == nil {
		t.Fatal("expected a tool not in allowed_tool_keys to be refused")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN for a disallowed tool, got %v", err)
	}

	// On the allowlist, and the registry itself has no handler for it —
	// correctly reports NOT_IMPLEMENTED rather than a fabricated result,
	// proving the call actually reached the real Tool Gateway pipeline
	// (permission check passed, risk tier passed; only the handler
	// lookup legitimately failed).
	_, err = agentsSvc.ExecuteTool(ctx, ac, a.ID, "get_container_logs", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "web-1",
	})
	if err == nil {
		t.Fatal("expected NOT_IMPLEMENTED since get_container_logs has no execution backend")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotImplemented {
		t.Fatalf("expected NOT_IMPLEMENTED, got %v", err)
	}
}

// A disabled agent refuses ExecuteTool too, not just Run.
func TestAgents_DisabledAgentCannotExecuteTool(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "agent-disabled-owner@nodera.dev")
	agentsSvc, _, _ := newAgentsServices(h)

	a, err := agentsSvc.CreateAgent(ctx, ac, agents.CreateAgentInput{
		Name:            "still-disabled-agent",
		AIProfileKey:    "unused.profile",
		AllowedToolKeys: []string{"check_ssl"},
		PermissionScope: []string{"infrastructure.read"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := agentsSvc.ExecuteTool(ctx, ac, a.ID, "check_ssl", tools.ExecuteInput{}); err == nil {
		t.Fatal("expected ExecuteTool to fail for a disabled agent")
	}
}

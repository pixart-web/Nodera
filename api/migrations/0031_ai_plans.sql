-- AI-proposed operation plans. The AI only ever PROPOSES; a human approves the
-- plan and then runs each step under their own permissions (dangerous steps
-- additionally go through the Tool Gateway approval flow).
CREATE TABLE ai_plans (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    goal            TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'rejected', 'completed')),
    steps           JSONB NOT NULL DEFAULT '[]',
    model           TEXT NOT NULL DEFAULT '',
    decided_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_plans_org ON ai_plans(organization_id, created_at DESC);

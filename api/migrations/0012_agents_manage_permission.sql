-- Agent definitions (the `agents` table, seeded schema-only since migration
-- 0007) now have a real Go domain (internal/agents) that can create/list/
-- disable them. Defining what an agent is allowed to do is a meaningfully
-- more sensitive action than invoking an already-defined agent (which
-- 'agents.execute' already covers, seeded in 0002_rbac.sql) — reusing
-- 'agents.execute' for both would let anyone who can run an agent also
-- redefine its permission_scope and allowed_tool_keys, which is exactly
-- the privilege-escalation shape every other module in this codebase goes
-- out of its way to prevent. So this adds a distinct permission rather
-- than overloading an existing one, unlike a few earlier modules
-- (applications.deploy, organization.manage) that reused the closest
-- available key because no better option existed — here one does.

INSERT INTO permissions (key, description) VALUES
    ('agents.manage', 'Create, update, and disable agent definitions');

INSERT INTO role_permissions (role_id, permission_key) VALUES
    ('00000000-0000-0000-0000-000000000001', 'agents.manage'), -- owner
    ('00000000-0000-0000-0000-000000000002', 'agents.manage'); -- admin
-- 'member' does not get agents.manage by default, matching its existing
-- read-and-safe-use-only posture from 0002_rbac.sql.

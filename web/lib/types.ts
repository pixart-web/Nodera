// Mirrors the JSON shapes returned by the Nodera Core API (see
// docs/API.md and the individual internal/<domain> Go packages). Kept
// hand-written for the moment even though api-types.generated.ts (run
// `npm run gen:types`) now exists as a generated alternative — swapping
// every page over is tracked in docs/ROADMAP.md, not done yet.

// The envelope every paginated list endpoint returns
// (internal/platform/httpserver.Page) — GET /infrastructure/nodes,
// /applications, /jobs, and /audit as of this writing.
export interface Page<T> {
  items: T[];
  limit: number;
  offset: number;
  has_more: boolean;
}

export interface User {
  id: string;
  email: string;
  display_name: string;
}

export interface Session {
  id: string;
  created_at: string;
  expires_at: string;
  ip_address?: string;
  user_agent?: string;
  is_current: boolean;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  created_at: string;
}

export interface Role {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  permissions: string[];
}

export interface MemberRole {
  role_id: string;
  name: string;
}

export interface Member {
  user_id: string;
  email: string;
  display_name: string;
  roles: MemberRole[];
}

export interface Node {
  id: string;
  organization_id: string;
  hostname: string;
  provider: string;
  provider_resource_id: string;
  role: string;
  environment: string;
  status: string;
  operating_system: string;
  cpu_cores: number;
  memory_mb: number;
  storage_gb: number;
  capabilities: string[];
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface Application {
  id: string;
  organization_id: string;
  name: string;
  kind: string;
  node_id: string | null;
  environment: string;
  status: string;
  repository_url: string;
  created_at: string;
  updated_at: string;
}

export interface Job {
  id: string;
  organization_id: string;
  type: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled" | "retrying";
  priority: number;
  payload: unknown;
  result?: unknown;
  error?: string;
  progress: number;
  attempts: number;
  max_attempts: number;
  correlation_id?: string;
  created_at: string;
  updated_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface SecretMeta {
  id: string;
  key: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface AuditRecord {
  id: string;
  organization_id: string | null;
  actor_label: string;
  action: string;
  resource_type: string;
  resource_id: string;
  source: string;
  correlation_id: string;
  success: boolean;
  created_at: string;
}

export interface APIToken {
  id: string;
  name: string;
  token_prefix: string;
  scopes: string[];
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
}

export interface AdminAPIToken extends APIToken {
  owner_type: "user" | "service_account";
  owner_label: string;
}

export interface CreatedAPIToken {
  token: string; // shown exactly once
  info: APIToken;
}

export interface ServiceAccount {
  id: string;
  name: string;
  description: string;
  status: "active" | "disabled";
  created_at: string;
}

export interface Tool {
  key: string;
  description: string;
  risk_level: "read" | "safe" | "privileged" | "critical";
  required_permission: string;
  implemented: boolean;
}

export interface ExecuteResult {
  status: "executed" | "approval_required";
  result?: unknown;
  approval_id?: string;
}

export interface Approval {
  id: string;
  requested_action: string;
  risk_level: "privileged" | "critical";
  resource_type: string;
  resource_id: string;
  parameters: unknown;
  status: "pending" | "approved" | "rejected" | "expired";
  created_at: string;
  expires_at?: string | null;
  decided_at?: string | null;
  decision_reason?: string;
  execution_result?: unknown;
}

export interface OrganizationToolSetting {
  tool_key: string;
  approval_ttl_seconds: number;
  updated_at: string;
}

export interface Agent {
  id: string;
  name: string;
  description: string;
  system_instructions: string;
  ai_profile_key: string;
  allowed_tool_keys: string[];
  permission_scope: string[];
  status: "active" | "disabled";
  timeout_seconds: number;
  created_at: string;
  updated_at: string;
}

export interface ChatResult {
  content: string;
  profile_key: string;
  provider_key: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
}

export interface AIProfile {
  id: string;
  key: string;
  description: string;
  required_capabilities: string[];
  privacy_level: string;
  preferred_model_ids: string[];
  fallback_model_ids: string[];
  temperature: number;
  max_tokens: number;
  timeout_seconds: number;
  created_at: string;
}

// One row per Chat call (internal/ai.recordUsage) — operational metrics
// only, never prompt/response content (docs/AI_ARCHITECTURE.md section 15).
export interface AIUsageRecord {
  id: string;
  profile_key: string;
  provider_key: string;
  model_identifier: string;
  classification: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  latency_ms: number | null;
  status: string;
  correlation_id: string;
  created_at: string;
}

// Platform-wide, not org-scoped (docs/AI_ARCHITECTURE.md) — the registry
// row makes a provider/model discoverable; whether it's actually callable
// depends on a matching Go adapter being registered at server boot.
export interface AIProvider {
  id: string;
  key: string;
  kind: "cloud" | "local";
  display_name: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface AIModel {
  id: string;
  provider_key: string;
  model_identifier: string;
  display_name: string;
  capabilities: string[];
  context_window: number;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    request_id?: string;
  };
}

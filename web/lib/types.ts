// Mirrors the JSON shapes returned by the Nodera Core API (see
// docs/API.md and the individual internal/<domain> Go packages). Kept
// hand-written rather than generated for now — see docs/ROADMAP.md
// (OpenAPI/Swagger generation is not yet implemented).

export interface User {
  id: string;
  email: string;
  display_name: string;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  created_at: string;
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

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    request_id?: string;
  };
}

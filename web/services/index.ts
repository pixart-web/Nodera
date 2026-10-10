// Data-access layer: every function here calls the real Nodera API (lib/api.ts).
// There is no mock data anywhere in the frontend; when the backend has nothing
// to show, pages render an honest empty state.
import { api } from "@/lib/api";
import { colorFor } from "@/lib/format";
import { toStatus } from "@/lib/status";
import type { Project } from "@/lib/domain";
import type {
  Page, ApiAIPlan, ApiAlertRule, ApiBackup, ApiBackupPolicy, ApiCertificate, ApiClient, ApiDashboardSummary, ApiDeployment, ApiDNSRecord,
  ApiDomain, ApiFeatureFlag, ApiIncident, ApiLogEntry, ApiMetricSample, ApiMigration, ApiMonitor, ApiNotification, ApiNotificationChannel,
  ApiOperation, ApiOperationLog, ApiOperationStep, ApiProject, ApiProjectApplication, ApiProjectContainers, ApiProjectDatabase,
  ApiProjectOverview, ApiPropagationResult, ApiSearchHit, ApiWordPressHealth, ApiRetentionPolicy, ApiNodeAgent,
} from "@/lib/types";

const V1 = "/api/v1";
const qs = (o: Record<string, string | number | undefined | null | boolean>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== null && v !== "") p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : "";
};

export function toProject(p: ApiProject): Project {
  const st = toStatus(p.status);
  const domain = typeof p.config?.primary_domain === "string" ? (p.config.primary_domain as string) : "—";
  return {
    id: p.id, name: p.name, domain, type: p.kind === "wordpress" ? "WORDPRESS" : "APPLICATION",
    status: st.status, statusLabel: st.label, clientId: p.client_id ?? undefined, clientName: p.client_name || undefined,
    createdAt: p.created_at, initial: (p.name[0] ?? "?").toUpperCase(), color: colorFor(p.name),
  };
}

export const projectsService = {
  list: async (f: { q?: string; status?: string; kind?: string; client_id?: string; limit?: number; offset?: number } = {}) =>
    (await api.get<Page<ApiProject>>(`${V1}/projects${qs({ limit: 100, ...f })}`)).items.map(toProject),
  listRaw: (f: { q?: string; limit?: number } = {}) => api.get<Page<ApiProject>>(`${V1}/projects${qs({ limit: 100, ...f })}`).then((r) => r.items),
  get: (id: string) => api.get<ApiProject>(`${V1}/projects/${id}`),
  overview: (id: string) => api.get<ApiProjectOverview>(`${V1}/projects/${id}/overview`),
  create: (b: { name: string; kind: "wordpress" | "application"; client_id?: string | null; description?: string; config?: Record<string, unknown> }) => api.post<ApiProject>(`${V1}/projects`, b),
  update: (id: string, b: { name?: string; description?: string; client_id?: string; config?: Record<string, unknown> }) => api.put<ApiProject>(`${V1}/projects/${id}`, b),
  provision: (id: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/projects/${id}/provision`),
  remove: (id: string) => api.del<{ status: string; approval_id?: string }>(`${V1}/projects/${id}`),
  databases: (id: string) => api.get<ApiProjectDatabase[]>(`${V1}/projects/${id}/databases`),
  applications: (id: string) => api.get<ApiProjectApplication[]>(`${V1}/projects/${id}/applications`),
  containers: (id: string) => api.get<ApiProjectContainers>(`${V1}/projects/${id}/containers`),
  operations: (id: string) => api.get<Page<ApiOperation>>(`${V1}/projects/${id}/operations?limit=30`).then((r) => r.items),
  containerLogs: (id: string, tail = 200) => api.get<{ lines: string[] }>(`${V1}/projects/${id}/container-logs${qs({ tail })}`).then((r) => r.lines),
  wpHealth: (id: string) => api.get<ApiWordPressHealth>(`${V1}/projects/${id}/wordpress/health`),
  wpClone: (id: string, b: { name: string; domain?: string }) => api.post<{ job_id: string; created: boolean }>(`${V1}/projects/${id}/wordpress/clone`, b),
  wpUpdate: (id: string, image_tag: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/projects/${id}/wordpress/update`, { image_tag }),
};

export const clientsService = {
  list: (q?: string) => api.get<Page<ApiClient>>(`${V1}/clients${qs({ limit: 200, q })}`).then((r) => r.items),
  create: (b: { name: string; contact_email?: string; notes?: string }) => api.post<ApiClient>(`${V1}/clients`, b),
  update: (id: string, b: { name: string; contact_email?: string; notes?: string }) => api.put<ApiClient>(`${V1}/clients/${id}`, b),
  remove: (id: string) => api.del<void>(`${V1}/clients/${id}`),
};

export const domainsService = {
  list: (project_id?: string) => api.get<Page<ApiDomain>>(`${V1}/domains${qs({ limit: 200, project_id })}`).then((r) => r.items),
  add: (name: string, project_id?: string) => api.post<ApiDomain>(`${V1}/domains`, { name, project_id: project_id || null }),
  remove: (id: string) => api.del<{ status: string; approval_id?: string }>(`${V1}/domains/${id}`),
  records: (id: string) => api.get<ApiDNSRecord[]>(`${V1}/domains/${id}/records`),
  upsertRecord: (id: string, b: { type: string; name: string; value: string; ttl?: number; priority?: number }) => api.post<ApiDNSRecord>(`${V1}/domains/${id}/records`, b),
  deleteRecord: (id: string, rid: string) => api.del<void>(`${V1}/domains/${id}/records/${rid}`),
  checkPropagation: (id: string) => api.post<ApiPropagationResult[]>(`${V1}/domains/${id}/check-propagation`),
  sync: (id: string) => api.post<{ synced: number }>(`${V1}/domains/${id}/sync`),
};

export const certificatesService = {
  list: (project_id?: string) => api.get<Page<ApiCertificate>>(`${V1}/certificates${qs({ limit: 200, project_id })}`).then((r) => r.items),
  issue: (domainId: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/domains/${domainId}/certificate`),
  renew: (domainId: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/domains/${domainId}/certificate/renew`),
  revoke: (domainId: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/domains/${domainId}/certificate/revoke`),
};

export const backupsService = {
  list: (project_id?: string) => api.get<Page<ApiBackup>>(`${V1}/backups${qs({ limit: 200, project_id })}`).then((r) => r.items),
  create: (projectId: string, b: { type: string; retention_days?: number }) => api.post<{ job_id: string; created: boolean }>(`${V1}/projects/${projectId}/backups`, b),
  verify: (id: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/backups/${id}/verify`),
  restore: (id: string) => api.post<{ status: string; approval_id?: string }>(`${V1}/backups/${id}/restore`),
  remove: (id: string) => api.del<{ status: string; approval_id?: string }>(`${V1}/backups/${id}`),
  policies: (projectId: string) => api.get<ApiBackupPolicy[]>(`${V1}/projects/${projectId}/backup-policies`),
  savePolicy: (projectId: string, b: { schedule: string; type: string; retention_days?: number; enabled?: boolean }) => api.put<ApiBackupPolicy>(`${V1}/projects/${projectId}/backup-policies`, b),
  deletePolicy: (id: string) => api.del<void>(`${V1}/backup-policies/${id}`),
};

export const deploymentsService = {
  list: (project_id?: string) => api.get<Page<ApiDeployment>>(`${V1}/deployments${qs({ limit: 100, project_id })}`).then((r) => r.items),
  create: (projectId: string, b: { source: string; repository?: string; ref?: string; environment?: string; files?: Record<string, string> }) =>
    api.post<{ job_id?: string; created?: boolean; status?: string; approval_id?: string }>(`${V1}/projects/${projectId}/deployments`, b),
  rollback: (projectId: string, deploymentId: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/projects/${projectId}/deployments/${deploymentId}/rollback`),
  repositories: () => api.get<Array<{ full_name: string; default_branch: string; private: boolean }>>(`${V1}/git/repositories`),
};

export const migrationsService = {
  list: (project_id?: string) => api.get<Page<ApiMigration>>(`${V1}/migrations${qs({ limit: 100, project_id })}`).then((r) => r.items),
  get: (id: string) => api.get<ApiMigration>(`${V1}/migrations/${id}`),
  create: (b: { project_id?: string | null; source_kind: string; target_domain: string; source_url?: string; source_config?: Record<string, unknown>; credentials?: Record<string, string> }) => api.post<ApiMigration>(`${V1}/migrations`, b),
  uploadSource: async (id: string, file: File): Promise<ApiMigration> => {
    const { getCSRFToken, getCurrentOrgId } = await import("@/lib/session");
    const base = process.env.NEXT_PUBLIC_NODERA_API_URL ?? "http://localhost:8080";
    const headers: Record<string, string> = { "Content-Type": "application/zip" };
    const org = getCurrentOrgId(); if (org) headers["X-Nodera-Org"] = org;
    const csrf = getCSRFToken(); if (csrf) headers["X-CSRF-Token"] = csrf;
    const res = await fetch(`${base}${V1}/migrations/${id}/source`, { method: "PUT", headers, credentials: "include", body: file });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body?.error?.message ?? `upload failed (${res.status})`);
    return body as ApiMigration;
  },
  plan: (id: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/migrations/${id}/plan`),
  run: (id: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/migrations/${id}/run`),
  cutover: (id: string) => api.post<{ status: string; approval_id?: string }>(`${V1}/migrations/${id}/cutover`),
  rollback: (id: string) => api.post<{ job_id: string; created: boolean }>(`${V1}/migrations/${id}/rollback`),
};

export const operationsService = {
  list: (project_id?: string, limit = 50) => api.get<Page<ApiOperation>>(`${V1}/operations${qs({ limit, project_id })}`).then((r) => r.items),
  get: (id: string) => api.get<ApiOperation>(`${V1}/operations/${id}`),
  steps: (id: string) => api.get<ApiOperationStep[]>(`${V1}/operations/${id}/steps`),
  logs: (id: string) => api.get<ApiOperationLog[]>(`${V1}/operations/${id}/logs?limit=500`),
};

export const monitoringService = {
  monitors: () => api.get<Page<ApiMonitor>>(`${V1}/monitors?limit=200`).then((r) => r.items),
  createMonitor: (b: { kind: string; name: string; target: string; interval_seconds?: number; project_id?: string | null }) => api.post<ApiMonitor>(`${V1}/monitors`, b),
  toggleMonitor: (id: string, enabled: boolean) => api.patch<ApiMonitor>(`${V1}/monitors/${id}`, { enabled }),
  runMonitor: (id: string) => api.post<ApiMonitor>(`${V1}/monitors/${id}/run`),
  deleteMonitor: (id: string) => api.del<void>(`${V1}/monitors/${id}`),
  metric: (metric: string, f: { node_id?: string; project_id?: string; since?: string } = {}) => api.get<ApiMetricSample[]>(`${V1}/metrics/${metric}${qs({ limit: 120, ...f })}`),
  rules: () => api.get<ApiAlertRule[]>(`${V1}/alert-rules`),
  createRule: (b: { name: string; condition: string; threshold?: number; severity?: string; channels?: string[] }) => api.post<ApiAlertRule>(`${V1}/alert-rules`, b),
  deleteRule: (id: string) => api.del<void>(`${V1}/alert-rules/${id}`),
  incidents: (status?: string) => api.get<Page<ApiIncident>>(`${V1}/incidents${qs({ limit: 100, status })}`).then((r) => r.items),
  incident: (id: string) => api.get<ApiIncident>(`${V1}/incidents/${id}`),
  incidentAction: (id: string, action: string, note?: string) => api.post<ApiIncident>(`${V1}/incidents/${id}/${action}`, { note }),
  channels: () => api.get<ApiNotificationChannel[]>(`${V1}/notification-channels`),
  createChannel: (b: { kind: string; name: string; target?: string }) => api.post<ApiNotificationChannel>(`${V1}/notification-channels`, b),
  deleteChannel: (id: string) => api.del<void>(`${V1}/notification-channels/${id}`),
};

export const logsService = {
  query: (f: { project_id?: string; source?: string; level?: string; q?: string; limit?: number }) => api.get<Page<ApiLogEntry>>(`${V1}/logs${qs({ limit: 200, ...f })}`).then((r) => r.items),
};

export const notificationsService = {
  list: (unread = false) => api.get<Page<ApiNotification>>(`${V1}/notifications${qs({ limit: 30, unread: unread ? "true" : undefined })}`).then((r) => r.items),
  unread: () => api.get<{ unread: number }>(`${V1}/notifications/unread-count`).then((r) => r.unread),
  markRead: (ids: string[]) => api.post<void>(`${V1}/notifications/read`, { ids }),
  markAll: () => api.post<void>(`${V1}/notifications/read`, { all: true }),
};

export const dashboardService = { get: () => api.get<ApiDashboardSummary>(`${V1}/dashboard`) };
export const searchService = { search: (q: string) => api.get<{ hits: ApiSearchHit[] }>(`${V1}/search${qs({ q })}`).then((r) => r.hits) };
export const flagsService = {
  list: () => api.get<ApiFeatureFlag[]>(`${V1}/feature-flags`),
  set: (key: string, enabled: boolean | null) => api.put<void>(`${V1}/feature-flags/${key}`, { enabled }),
};
export const retentionService = {
  get: () => api.get<ApiRetentionPolicy[]>(`${V1}/retention`),
  set: (resource: string, retention_days: number) => api.put<void>(`${V1}/retention/${resource}`, { retention_days }),
};
export const agentsAdminService = {
  list: () => api.get<ApiNodeAgent[]>(`${V1}/infrastructure/agents`),
  register: (nodeId: string) => api.post<{ token: string; expires_at: string; node_id: string }>(`${V1}/infrastructure/nodes/${nodeId}/agent-registrations`),
  revoke: (id: string) => api.post<void>(`${V1}/infrastructure/agents/${id}/revoke`),
};
export const aiPlansService = {
  list: () => api.get<Page<ApiAIPlan>>(`${V1}/ai/plans?limit=30`).then((r) => r.items),
  propose: (goal: string, project_id?: string, profile_key?: string) => api.post<ApiAIPlan>(`${V1}/ai/plans`, { goal, project_id: project_id || null, profile_key }),
  decide: (id: string, approve: boolean) => api.post<ApiAIPlan>(`${V1}/ai/plans/${id}/${approve ? "approve" : "reject"}`),
  runStep: (id: string, n: number) => api.post<ApiAIPlan>(`${V1}/ai/plans/${id}/steps/${n}/run`),
};

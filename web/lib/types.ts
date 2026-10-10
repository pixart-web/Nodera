// Backed by api-types.generated.ts (run `npm run gen:types`), which is
// generated straight from api/openapi/openapi.json — the same
// hand-maintained-but-consistently-updated spec every phase's backend
// work keeps in sync. Every type below is a thin alias onto a generated
// schema rather than a hand-copied shape, so a field this file used to
// get wrong (or forget to update) is now a real `tsc` error the moment
// the spec changes, not a runtime surprise caught only by live
// verification. See docs/ROADMAP.md Phase 50 for the migration and why
// Page<T> stays hand-written below (it's a structural generic, not a
// single named schema — every paginated endpoint has its own concrete
// *Page schema, e.g. NodePage, generated from the same shape).
//
// Import sites are unchanged (`import type { Node } from "@/lib/types"`
// still works) — only this file's own definitions moved.
import type { components } from "./api-types.generated";

// The envelope every paginated list endpoint returns
// (internal/platform/httpserver.Page) — GET /infrastructure/nodes,
// /applications, /jobs, /audit, and /ai/usage as of this writing.
export interface Page<T> {
  items: T[];
  limit: number;
  offset: number;
  has_more: boolean;
}

export type User = components["schemas"]["User"];
export type Session = components["schemas"]["Session"];
export type Organization = components["schemas"]["Organization"];
export type Role = components["schemas"]["Role"];
export type MemberRole = components["schemas"]["MemberRole"];
export type Member = components["schemas"]["Member"];
export type Node = components["schemas"]["Node"];
export type Application = components["schemas"]["Application"];
export type Job = components["schemas"]["Job"];
export type SecretMeta = components["schemas"]["SecretMeta"];
export type AuditRecord = components["schemas"]["AuditRecord"];
export type APIToken = components["schemas"]["APIToken"];
export type AdminAPIToken = components["schemas"]["AdminAPIToken"];
export type CreatedAPIToken = components["schemas"]["CreatedAPIToken"];
export type ServiceAccount = components["schemas"]["ServiceAccount"];
export type Tool = components["schemas"]["Tool"];
export type ExecuteResult = components["schemas"]["ExecuteResult"];
export type Approval = components["schemas"]["Approval"];
export type OrganizationToolSetting = components["schemas"]["OrganizationToolSetting"];
export type Agent = components["schemas"]["Agent"];
export type ChatResult = components["schemas"]["ChatResult"];
export type AIProfile = components["schemas"]["AIProfile"];
export type AIUsageRecord = components["schemas"]["AIUsageRecord"];
export type AIProvider = components["schemas"]["AIProvider"];
export type AIModel = components["schemas"]["AIModel"];
export type ChatMessage = components["schemas"]["ChatMessage"];

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    request_id?: string;
  };
}

// ---- Control plane (operations engine) schemas ----
export type ApiClient = components["schemas"]["Client"];
export type ApiClientInput = components["schemas"]["ClientInput"];
export type ApiProject = components["schemas"]["Project"];
export type ApiProjectInput = components["schemas"]["ProjectInput"];
export type ApiProjectUpdate = components["schemas"]["ProjectUpdate"];
export type ApiProjectOverview = components["schemas"]["ProjectOverview"];
export type ApiJobRef = components["schemas"]["JobRef"];
export type ApiGatewayResult = components["schemas"]["GatewayResult"];
export type ApiOperation = components["schemas"]["Operation"];
export type ApiOperationStep = components["schemas"]["OperationStep"];
export type ApiOperationLog = components["schemas"]["OperationLog"];
export type ApiBackup = components["schemas"]["Backup"];
export type ApiBackupRequest = components["schemas"]["BackupRequest"];
export type ApiBackupPolicy = components["schemas"]["BackupPolicy"];
export type ApiBackupPolicyInput = components["schemas"]["BackupPolicyInput"];
export type ApiDomain = components["schemas"]["Domain"];
export type ApiDNSRecord = components["schemas"]["DNSRecord"];
export type ApiDNSRecordInput = components["schemas"]["DNSRecordInput"];
export type ApiPropagationResult = components["schemas"]["PropagationResult"];
export type ApiCertificate = components["schemas"]["Certificate"];
export type ApiDeployment = components["schemas"]["Deployment"];
export type ApiDeploymentInput = components["schemas"]["DeploymentInput"];
export type ApiMigrationCreate = components["schemas"]["MigrationCreate"];
export type ApiMigration = components["schemas"]["Migration"];
export type ApiMonitor = components["schemas"]["Monitor"];
export type ApiMonitorInput = components["schemas"]["MonitorInput"];
export type ApiMetricSample = components["schemas"]["MetricSample"];
export type ApiAlertRule = components["schemas"]["AlertRule"];
export type ApiAlertRuleInput = components["schemas"]["AlertRuleInput"];
export type ApiIncident = components["schemas"]["Incident"];
export type ApiNotification = components["schemas"]["Notification"];
export type ApiNotificationChannel = components["schemas"]["NotificationChannel"];
export type ApiNotificationChannelInput = components["schemas"]["NotificationChannelInput"];
export type ApiLogEntry = components["schemas"]["LogEntry"];
export type ApiRetentionPolicy = components["schemas"]["RetentionPolicy"];
export type ApiFeatureFlag = components["schemas"]["FeatureFlag"];
export type ApiDashboardSummary = components["schemas"]["DashboardSummary"];
export type ApiSearchHit = components["schemas"]["SearchHit"];
export type ApiSystemInfo = components["schemas"]["SystemInfo"];
export type ApiWordPressHealth = components["schemas"]["WordPressHealth"];
export type ApiAIPlan = components["schemas"]["AIPlan"];
export type ApiNodeAgent = components["schemas"]["NodeAgent"];
export type ApiAgentRegistration = components["schemas"]["AgentRegistration"];
export type ApiQueuedCommand = components["schemas"]["QueuedCommand"];
export type ApiProjectDatabase = components["schemas"]["ProjectDatabase"];
export type ApiProjectApplication = components["schemas"]["ProjectApplication"];
export type ApiProjectContainers = components["schemas"]["ProjectContainers"];

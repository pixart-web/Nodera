// Domain types for the Nodera control-plane UI. These are the contracts the
// services/ layer returns; mock/ implements them today and a real API client
// replaces that without touching any component.

export type ResourceStatus =
  | "ONLINE" | "WARNING" | "ERROR" | "OFFLINE" | "DEPLOYING"
  | "MIGRATING" | "BACKUP" | "MAINTENANCE" | "PENDING" | "UNKNOWN";

export type ProjectType = "WORDPRESS" | "APPLICATION";

export interface Project {
  id: string;
  name: string;
  domain: string; // primary domain from the project config, or "—"
  type: ProjectType;
  status: ResourceStatus;
  statusLabel?: string;
  clientId?: string;
  clientName?: string;
  createdAt: string;
  initial: string;
  color: string;
}
export interface Client { id: string; name: string; projects: number }
export interface Domain {
  id: string; name: string; project: string;
  dns: "OK" | "PENDING" | "MISCONFIGURED"; ssl: "OK" | "PENDING" | "NONE";
  status: ResourceStatus; expires?: string;
}
export interface Certificate {
  id: string; domain: string; issuer: string; validFrom: string; expires: string;
  daysRemaining: number; status: ResourceStatus; autoRenewal: boolean;
}
export interface Backup {
  id: string; project: string; type: "FULL" | "DATABASE" | "FILES"; size: string;
  created: string; status: ResourceStatus; retention: string;
}
export interface ServiceHealth { id: string; name: string; status: ResourceStatus; latencyMs: number }
export interface Container { id: string; name: string; image: string; status: ResourceStatus; uptime: string }
export interface Deployment { id: string; project: string; ref: string; status: ResourceStatus; at: string }
export interface Migration { id: string; source: string; destination: string; status: ResourceStatus; at: string }
export interface MetricSeries { id: "cpu" | "ram" | "disk" | "network"; label: string; points: number[]; secondary?: number[] }
export interface MetricSnapshot {
  cpu: { pct: number };
  ram: { usedGb: number; totalGb: number };
  disk: { usedGb: number; totalGb: number };
  network: { upMbps: number; downMbps: number };
  series: MetricSeries[];
}
export type MetricRange = "1h" | "6h" | "24h" | "7d";
export interface Activity {
  id: string; subject: string; event: string; at: string;
  status: ResourceStatus; kind: "wordpress" | "container" | "ssl" | "backup" | "git" | "app";
}
export interface WordPressSite {
  id: string; name: string; domain: string; wpVersion: string; php: string;
  woocommerce: boolean; ssl: "OK" | "PENDING" | "NONE"; lastBackup?: string; status: ResourceStatus;
}
export interface User { id: string; name: string; email: string; role: string; lastSeen: string; status: ResourceStatus }
export type LogLevel = "INFO" | "SUCCESS" | "WARNING" | "ERROR" | "DEBUG";
export interface LogLine { id: string; ts: string; level: LogLevel; source: string; project: string; message: string }

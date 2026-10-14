// DEMONSTRATION DATA for the marketing site. Every number, hostname and
// event below is fictional and is labelled as demo wherever it is shown.
// Terminology mirrors the real product (pipeline stages from
// api/internal/deployments, audit/approval vocabulary from the Core API).

export const demoMetrics = {
  health: 98.7,
  servers: 24,
  healthy: 22,
  degraded: 2,
  deployments: 184,
  successful: 181,
  failed: 3,
  sites: 147,
  containers: 86,
  backupSuccess: 99.98,
  incidents: 3,
  resolved: 2,
};

/* ---------- Control plane topology ---------- */

export type TopoStatus = "ok" | "degraded" | "idle";
export interface TopoNode { id: string; label: string; tier: number; status?: TopoStatus }

export const topoTiers = ["Servers", "Containers", "Applications", "Databases", "Websites", "Backups", "Monitoring"];

export const topoNodes: TopoNode[] = [
  { id: "s1", label: "web-prod-01", tier: 0 },
  { id: "s2", label: "web-prod-02", tier: 0 },
  { id: "s3", label: "web-prod-03", tier: 0 },
  { id: "s4", label: "web-prod-04", tier: 0, status: "degraded" },
  { id: "s5", label: "db-prod-01", tier: 0 },

  { id: "c1", label: "wp-shop", tier: 1 },
  { id: "c2", label: "wp-agency", tier: 1 },
  { id: "c3", label: "api-runtime", tier: 1, status: "degraded" },
  { id: "c4", label: "redis-cache", tier: 1 },

  { id: "a1", label: "storefront", tier: 2 },
  { id: "a2", label: "client-portal", tier: 2 },
  { id: "a3", label: "public-api", tier: 2, status: "degraded" },

  { id: "d1", label: "mariadb-shop", tier: 3 },
  { id: "d2", label: "postgres-prod", tier: 3 },
  { id: "d3", label: "mariadb-agency", tier: 3 },

  { id: "w1", label: "shop.example.com", tier: 4 },
  { id: "w2", label: "agency.example.com", tier: 4 },
  { id: "w3", label: "api.example.com", tier: 4, status: "degraded" },

  { id: "b1", label: "nightly-snapshots", tier: 5 },
  { id: "b2", label: "offsite-storage", tier: 5 },

  { id: "m1", label: "uptime-checks", tier: 6 },
  { id: "m2", label: "tls-expiry", tier: 6 },
  { id: "m3", label: "incident-router", tier: 6 },
];

export const topoEdges: [string, string][] = [
  ["s1", "c1"], ["s2", "c2"], ["s3", "c1"], ["s4", "c3"], ["s4", "c4"], ["s5", "d1"], ["s5", "d2"], ["s5", "d3"],
  ["c1", "a1"], ["c2", "a2"], ["c3", "a3"], ["c4", "a3"], ["c4", "a1"],
  ["a1", "d1"], ["a2", "d3"], ["a3", "d2"],
  ["d1", "w1"], ["d3", "w2"], ["d2", "w3"],
  ["d1", "b1"], ["d2", "b1"], ["d3", "b1"], ["b1", "b2"],
  ["w1", "m1"], ["w2", "m1"], ["w3", "m1"], ["w1", "m2"], ["w2", "m2"], ["w3", "m2"], ["m1", "m3"],
];

/* ---------- AI root-cause sequence ---------- */

export const aiQuestion = "Why is web-prod-04 degraded?";

export const aiSignals = [
  { label: "CPU saturation", value: "94% · +41% / 17m", tone: "warn" },
  { label: "PHP workers", value: "48/48 busy", tone: "warn" },
  { label: "Deployment #1842", value: "applied 02:01 UTC", tone: "info" },
  { label: "Redis latency", value: "1.2 ms · normal", tone: "ok" },
] as const;

export const aiAffected = ["web-prod-04", "shop.example.com", "api.example.com"];

/* ---------- Incident timeline ---------- */

export const incidentSteps = ["Detect", "Understand", "Decide", "Act", "Verify"];

export const incidentEvents = [
  { t: "02:14:03", step: 0, text: "CPU anomaly detected", meta: "web-prod-04 · cpu 94%" },
  { t: "02:14:06", step: 1, text: "Nodera identifies affected service", meta: "api.example.com · p95 2.4s" },
  { t: "02:14:12", step: 1, text: "Root cause analysis complete", meta: "deployment #1842" },
  { t: "02:14:18", step: 2, text: "Remediation approved", meta: "rollback · approved by on-call" },
  { t: "02:14:31", step: 3, text: "Service restored", meta: "release #1841 active" },
  { t: "02:14:35", step: 4, text: "Health verified", meta: "HEALTH_CHECK passed" },
];

/* ---------- Security ---------- */

export const auditEvents = [
  { action: "approvals.approval.requested", user: "deploy-bot", target: "web-prod-04", detail: "deployment.production · release #1842", status: "PENDING" },
  { action: "deployment.approved", user: "infrastructure-admin", target: "web-prod-04", detail: "release #1842 · approval required", status: "VERIFIED" },
  { action: "secrets.secret.set", user: "platform-ops", target: "shop.example.com", detail: "SMTP_PASSWORD · value never logged", status: "VERIFIED" },
  { action: "backup.verified", user: "system", target: "mariadb-shop", detail: "sha256 match · off-site copy", status: "VERIFIED" },
  { action: "identity.api_token.revoked", user: "infrastructure-admin", target: "ci-runner-02", detail: "scope: deployments.write", status: "VERIFIED" },
  { action: "agent.signature.rejected", user: "—", target: "203.0.113.47", detail: "unknown key · request dropped", status: "REJECTED" },
  { action: "deployment.rolled_back", user: "on-call-sre", target: "web-prod-04", detail: "#1842 → #1841", status: "VERIFIED" },
];

/* ---------- Product showcase ---------- */

export const demoServers = [
  { name: "web-prod-01", ip: "10.0.1.11", role: "PRODUCTION", status: "HEALTHY", cpu: 38, mem: 61 },
  { name: "web-prod-02", ip: "10.0.1.12", role: "PRODUCTION", status: "HEALTHY", cpu: 42, mem: 57 },
  { name: "web-prod-03", ip: "10.0.1.13", role: "PRODUCTION", status: "HEALTHY", cpu: 29, mem: 49 },
  { name: "web-prod-04", ip: "10.0.1.14", role: "PRODUCTION", status: "DEGRADED", cpu: 94, mem: 82 },
  { name: "postgres-prod", ip: "10.0.2.21", role: "DATABASE", status: "HEALTHY", cpu: 33, mem: 71 },
];

export const demoDeploys = [
  { id: "#1842", target: "web-prod-04", source: "git · main@4be91c2", status: "ROLLED BACK", by: "deploy-bot" },
  { id: "#1841", target: "web-prod-04", source: "git · main@a03f7d1", status: "ACTIVE", by: "on-call-sre" },
  { id: "#1840", target: "shop.example.com", source: "upload · theme.zip", status: "COMPLETE", by: "agency-dev" },
  { id: "#1839", target: "client-portal", source: "git · release/2.3", status: "COMPLETE", by: "deploy-bot" },
];

export const deployStages = ["PRECHECK", "FETCH", "BUILD", "TEST", "DEPLOY", "HEALTH_CHECK", "COMPLETE"];

export const demoBackups = [
  { target: "mariadb-shop", kind: "database", size: "2.4 GB", when: "02:00", verify: "VERIFIED", offsite: true },
  { target: "shop.example.com", kind: "files", size: "11.8 GB", when: "02:05", verify: "VERIFIED", offsite: true },
  { target: "postgres-prod", kind: "database", size: "6.1 GB", when: "02:10", verify: "VERIFIED", offsite: true },
  { target: "agency.example.com", kind: "files", size: "3.2 GB", when: "02:14", verify: "RUNNING", offsite: false },
];

export const demoSites = [
  { domain: "shop.example.com", stack: "WordPress 6.6", health: 96, ssl: "61 days", backup: "02:05" },
  { domain: "agency.example.com", stack: "WordPress 6.6", health: 99, ssl: "83 days", backup: "02:14" },
  { domain: "studio.example.org", stack: "WordPress 6.5", health: 88, ssl: "12 days", backup: "01:40" },
  { domain: "careers.example.net", stack: "WordPress 6.6", health: 100, ssl: "74 days", backup: "01:52" },
];

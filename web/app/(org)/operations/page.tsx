"use client";

import Link from "next/link";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import type { Application, AuditRecord, Job, Node as NoderaNode, Organization, Page } from "@/lib/types";

function StatCard({ label, value, href }: { label: string; value: string | number; href: string }) {
  return (
    <Link href={href} className="card block p-4 transition-colors hover:border-accent-600/50">
      <div className="text-xs uppercase tracking-wide text-base-400">{label}</div>
      <div className="mt-1 font-mono text-2xl text-base-100">{value}</div>
    </Link>
  );
}

// A page's own item count understates the true total once has_more is
// true — "50+" is the honest thing to show rather than a number that
// looks precise but isn't (rule 36).
function countLabel(page: Page<unknown> | null, loading: boolean): string {
  if (loading || !page) return "…";
  return page.has_more ? `${page.items.length}+` : String(page.items.length);
}

export default function DashboardPage() {
  const org = useApi(() => api.get<Organization>("/api/v1/organization"), []);
  const nodes = useApi(() => api.get<Page<NoderaNode>>("/api/v1/infrastructure/nodes"), []);
  const apps = useApi(() => api.get<Page<Application>>("/api/v1/applications"), []);
  const jobs = useApi(() => api.get<Page<Job>>("/api/v1/jobs"), []);
  const audit = useApi(() => api.get<Page<AuditRecord>>("/api/v1/audit"), []);

  const failedJobs = (jobs.data?.items ?? []).filter((j) => j.status === "failed").length;
  const recentAudit = (audit.data?.items ?? []).slice(0, 8);

  return (
    <div>
      <PageHeader
        title={org.data ? org.data.name : "Dashboard"}
        description={org.data ? `/${org.data.slug}` : undefined}
      />

      {(nodes.error || apps.error || jobs.error || audit.error) && (
        <ErrorBanner message="Some data could not be loaded — check that the Nodera API is running and reachable." />
      )}

      <div className="mb-8 grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Nodes" value={countLabel(nodes.data, nodes.loading)} href="/infrastructure" />
        <StatCard label="Applications" value={countLabel(apps.data, apps.loading)} href="/applications" />
        <StatCard label="Jobs" value={countLabel(jobs.data, jobs.loading)} href="/jobs" />
        <StatCard label="Failed jobs" value={jobs.loading ? "…" : failedJobs} href="/jobs?status=failed" />
      </div>

      <div className="card p-4">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Recent activity</h2>
          <Link href="/audit" className="text-xs text-accent-400 hover:text-accent-300">
            View all →
          </Link>
        </div>
        {audit.loading ? (
          <div className="text-sm text-base-400">Loading…</div>
        ) : recentAudit.length === 0 ? (
          <div className="text-sm text-base-400">No activity recorded yet.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Resource</th>
                <th>Actor</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {recentAudit.map((a) => (
                <tr key={a.id}>
                  <td className="font-mono text-xs">{a.action}</td>
                  <td className="text-xs text-base-300">{a.resource_type}</td>
                  <td className="text-xs text-base-300">{a.actor_label}</td>
                  <td className="text-xs text-base-400">{new Date(a.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
